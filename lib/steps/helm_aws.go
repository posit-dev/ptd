package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/types"
	kubernetes "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	apiextensions "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	schedulingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/scheduling/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	yamlv2 "gopkg.in/yaml.v2"
	yaml "gopkg.in/yaml.v3"
)

// --- AWS ---

// karpenterVPCConfig holds the dynamically fetched VPC networking configuration
// for a Karpenter-enabled cluster.
type karpenterVPCConfig struct {
	SubnetIDs        []string
	SecurityGroupIDs []string
}

// awsHelmParams bundles pre-fetched data for the AWS helm deploy function.
type awsHelmParams struct {
	compoundName                string
	trueName                    string // derived: second-to-last segment of compoundName split by "-"
	environment                 string // derived: last segment of compoundName split by "-"
	accountID                   string
	region                      string
	kubeconfigsByCluster        map[string]string
	certARNs                    []string
	cfg                         types.AWSWorkloadConfig
	mimirPassword               string // for alloy mimir-auth secret
	nodeGroupNamesByCluster     map[string][]string
	karpenterVPCConfigByCluster map[string]karpenterVPCConfig
	workloadDir                 string
}

func (s *HelmStep) runAWSInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("helm: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSWorkloadConfig)
	if !ok {
		return fmt.Errorf("helm: expected AWSWorkloadConfig")
	}

	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}

	// Build per-cluster kubeconfigs
	kubeconfigsByCluster := make(map[string]string, len(cfg.Clusters))
	for release := range cfg.Clusters {
		clusterName := s.DstTarget.Name() + "-" + release
		endpoint, caCert, _, clusterErr := aws.GetClusterInfo(ctx, awsCreds, s.DstTarget.Region(), clusterName)
		if clusterErr != nil {
			return fmt.Errorf("helm: failed to get cluster info for %s: %w", clusterName, clusterErr)
		}
		token, clusterErr := aws.GetEKSToken(ctx, awsCreds, s.DstTarget.Region(), clusterName)
		if clusterErr != nil {
			return fmt.Errorf("helm: failed to get EKS token for %s: %w", clusterName, clusterErr)
		}
		config := kube.BuildEKSKubeConfig(endpoint, caCert, token, clusterName)
		if !cfg.TailscaleEnabled {
			config.Clusters[0].Cluster.ProxyURL = "socks5://localhost:1080"
		}
		data, marshalErr := yaml.Marshal(config)
		if marshalErr != nil {
			return fmt.Errorf("helm: failed to marshal kubeconfig for %s: %w", clusterName, marshalErr)
		}
		kubeconfigsByCluster[release] = string(data)
	}

	// Fetch cert_arns from persistent stack outputs
	var certARNs []string
	persistentOutputs, err := getPersistentStackOutputs(ctx, s.DstTarget)
	if err != nil {
		return fmt.Errorf("helm: failed to get persistent stack outputs: %w", err)
	}
	if v, ok := persistentOutputs["cert_arns"]; ok {
		if arr, ok := v.Value.([]interface{}); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					certARNs = append(certARNs, s)
				}
			}
		}
	}

	// Fetch mimir password from workload secrets. Alloy is always deployed (its version always
	// resolves to a non-empty default), so we always need this secret.
	mimirPassword := ""
	if len(cfg.Clusters) > 0 {
		secretName := s.DstTarget.Name() + ".posit.team"
		secretJSON, err := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, secretName)
		if err != nil {
			// Non-fatal: log warning and continue without mimir password
			fmt.Printf("helm: warning: failed to get workload secret %q (alloy mimir-auth will be empty): %v\n", secretName, err)
		} else {
			var secrets map[string]string
			if err := json.Unmarshal([]byte(secretJSON), &secrets); err == nil {
				mimirPassword = secrets["mimir-password"]
			}
		}
	}

	// Fetch nodegroup names for clusters with Karpenter
	nodeGroupNamesByCluster := make(map[string][]string)
	for release, clusterCfg := range cfg.Clusters {
		if clusterCfg.Spec.KarpenterConfig == nil || len(clusterCfg.Spec.KarpenterConfig.NodePools) == 0 {
			continue
		}
		clusterName := s.DstTarget.Name() + "-" + release
		names, err := aws.GetNodeGroupNames(ctx, awsCreds, s.DstTarget.Region(), clusterName)
		if err != nil {
			// Fall back to common architecture names
			names = []string{"amd64", "arm64"}
		}
		nodeGroupNamesByCluster[release] = names
	}

	// Fetch VPC config for clusters with Karpenter
	karpenterVPCConfigByCluster := make(map[string]karpenterVPCConfig)
	for release, clusterCfg := range cfg.Clusters {
		if clusterCfg.Spec.KarpenterConfig == nil || len(clusterCfg.Spec.KarpenterConfig.NodePools) == 0 {
			continue
		}
		clusterName := s.DstTarget.Name() + "-" + release
		vpcCfg, err := aws.GetClusterVPCConfig(ctx, awsCreds, s.DstTarget.Region(), clusterName)
		if err != nil {
			return fmt.Errorf("helm: failed to get VPC config for cluster %s: %w", clusterName, err)
		}

		sgIDs := vpcCfg.SecurityGroupIDs

		// Look up FSX NFS security group
		fsxSGID, fsxFound, err := aws.GetNFSSecurityGroupID(ctx, awsCreds, s.DstTarget.Region(), vpcCfg.VpcID, "eks-nodes-fsx-nfs.posit.team")
		if err != nil {
			return fmt.Errorf("helm: failed to look up FSX NFS security group for cluster %s: %w", clusterName, err)
		}
		if fsxFound {
			sgIDs = append(sgIDs, fsxSGID)
		}

		// Look up EFS NFS security group if EFS is enabled
		if clusterCfg.Spec.EnableEfsCsiDriver || clusterCfg.Spec.EfsConfig != nil {
			efsSGID, efsFound, err := aws.GetNFSSecurityGroupID(ctx, awsCreds, s.DstTarget.Region(), vpcCfg.VpcID, "eks-nodes-efs-nfs.posit.team")
			if err != nil {
				return fmt.Errorf("helm: failed to look up EFS NFS security group for cluster %s: %w", clusterName, err)
			}
			if efsFound {
				sgIDs = append(sgIDs, efsSGID)
			}
		}

		karpenterVPCConfigByCluster[release] = karpenterVPCConfig{
			SubnetIDs:        vpcCfg.SubnetIDs,
			SecurityGroupIDs: sgIDs,
		}
	}

	// Derive trueName and environment from compoundName
	trueName, environment := splitCompoundName(s.DstTarget.Name())

	params := awsHelmParams{
		compoundName:                s.DstTarget.Name(),
		trueName:                    trueName,
		environment:                 environment,
		accountID:                   awsCreds.AccountID(),
		region:                      s.DstTarget.Region(),
		kubeconfigsByCluster:        kubeconfigsByCluster,
		certARNs:                    certARNs,
		cfg:                         cfg,
		mimirPassword:               mimirPassword,
		nodeGroupNamesByCluster:     nodeGroupNamesByCluster,
		karpenterVPCConfigByCluster: karpenterVPCConfigByCluster,
		workloadDir:                 filepath.Join(helpers.GetTargetsConfigPath(), helpers.WorkDir, s.DstTarget.Name()),
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, _ types.Target) error {
		return awsHelmDeploy(pctx, params)
	}, envVars)
	if err != nil {
		return err
	}
	return runPulumi(ctx, stack, s.Options)
}

// splitCompoundName returns (trueName, environment) from a compound name like "myorg-staging".
// The last "-xxx" segment is the environment; everything before is the trueName.
func splitCompoundName(compoundName string) (trueName, environment string) {
	idx := strings.LastIndex(compoundName, "-")
	if idx < 0 {
		return compoundName, ""
	}
	return compoundName[:idx], compoundName[idx+1:]
}

// awsHelmDeploy is the package-level AWS deploy function, callable from tests.
func awsHelmDeploy(ctx *pulumi.Context, params awsHelmParams) error {
	name := params.compoundName

	// Alias helpers: resources were children of ptd:AWSWorkloadHelm in Python.
	outerProject := "ptd-aws-workload-helm"

	// withAlias returns an alias from the old Python project URN.
	withAlias := func(resourceType, resourceName string) pulumi.ResourceOption {
		oldURN := fmt.Sprintf("urn:pulumi:%s::%s::ptd:AWSWorkloadHelm$%s::%s",
			ctx.Stack(), outerProject, resourceType, resourceName)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}})
	}

	// withNestedAlias returns an alias for resources nested under AlloyConfig.
	withNestedAlias := func(parentType, resourceType, resourceName string) pulumi.ResourceOption {
		oldURN := fmt.Sprintf("urn:pulumi:%s::%s::ptd:AWSWorkloadHelm$%s$%s::%s",
			ctx.Stack(), outerProject, parentType, resourceType, resourceName)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}})
	}

	releases := helpers.SortedKeys(params.cfg.Clusters)

	for _, release := range releases {
		clusterCfg := params.cfg.Clusters[release].Spec

		var components types.AWSWorkloadClusterComponents
		if clusterCfg.Components != nil {
			components = *clusterCfg.Components
		}
		resolved := components.ResolveAWSComponents()

		routingWeight := "100"
		if clusterCfg.RoutingWeight != nil {
			routingWeight = *clusterCfg.RoutingWeight
		}

		k8sProviderName := name + "-" + release
		k8sProvider, err := kubernetes.NewProvider(ctx, k8sProviderName, &kubernetes.ProviderArgs{
			Kubeconfig:            pulumi.String(params.kubeconfigsByCluster[release]),
			EnableServerSideApply: pulumi.BoolPtr(true),
		}, withAlias("pulumi:providers:kubernetes", k8sProviderName),
			// Also alias for old Python naming: top-level resource with -k8s suffix.
			pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(fmt.Sprintf(
				"urn:pulumi:%s::%s::pulumi:providers:kubernetes::%s-k8s",
				ctx.Stack(), outerProject, k8sProviderName))}}),
			pulumi.IgnoreChanges([]string{"kubeconfig"}))
		if err != nil {
			return fmt.Errorf("helm aws: failed to create k8s provider for %s: %w", release, err)
		}
		k8sOpt := pulumi.Provider(k8sProvider)

		// 1. AWS Load Balancer Controller
		if err := awsHelmLBC(ctx, k8sOpt, name, release, params.accountID, resolved.AwsLoadBalancerControllerVersion, withAlias); err != nil {
			return err
		}

		// 2. FSx OpenZFS CSI driver
		fsxRoleName := "aws-fsx-openzfs-csi-driver." + name + ".posit.team"
		if err := awsHelmFsxOpenzfsCsi(ctx, k8sOpt, name, release, params.accountID, fsxRoleName, resolved.AwsFsxOpenzfsCsiDriverVersion, withAlias); err != nil {
			return err
		}

		// 3 & 4. Secret Store CSI (if not using addon)
		if !params.cfg.SecretsStoreAddonEnabled {
			if err := awsHelmSecretStoreCsi(ctx, k8sOpt, name, release, resolved.SecretStoreCsiDriverVersion, withAlias); err != nil {
				return err
			}
			if err := awsHelmSecretStoreCsiAws(ctx, k8sOpt, name, release, resolved.SecretStoreCsiDriverAwsProviderVersion, withAlias); err != nil {
				return err
			}
		}

		// 5. Traefik (namespace + helmchart + ingress)
		if err := awsHelmTraefik(ctx, k8sOpt, name, release, params, routingWeight, resolved.TraefikVersion, withAlias); err != nil {
			return err
		}

		// 6. Metrics Server
		if err := awsHelmMetricsServer(ctx, k8sOpt, name, release, resolved.MetricsServerVersion, withAlias); err != nil {
			return err
		}

		// 7. Loki
		if err := awsHelmLoki(ctx, k8sOpt, name, release, params, resolved, withAlias); err != nil {
			return err
		}

		// 8. Grafana
		if err := awsHelmGrafana(ctx, k8sOpt, name, release, params, resolved.GrafanaVersion, withAlias); err != nil {
			return err
		}

		// 9. Mimir
		if err := awsHelmMimir(ctx, k8sOpt, name, release, params, resolved, withAlias); err != nil {
			return err
		}

		// 10. Kube State Metrics
		if err := awsHelmKubeStateMetrics(ctx, k8sOpt, name, release, resolved.KubeStateMetricsVersion, withAlias); err != nil {
			return err
		}

		// 11. Alloy (always deployed since alloy version always resolves)
		if err := awsHelmAlloy(ctx, k8sOpt, name, release, params, resolved, clusterCfg, withAlias, withNestedAlias); err != nil {
			return err
		}

		// 12. Nvidia Device Plugin (optional)
		if params.cfg.NvidiaGpuEnabled {
			if err := awsHelmNvidiaDevicePlugin(ctx, k8sOpt, name, release, resolved.NvidiaDevicePluginVersion, withAlias); err != nil {
				return err
			}
		}

		// 13. Karpenter (optional)
		if params.cfg.AutoscalingEnabled && clusterCfg.KarpenterConfig != nil {
			nodeGroupNames := params.nodeGroupNamesByCluster[release]
			if len(nodeGroupNames) == 0 {
				nodeGroupNames = []string{"amd64", "arm64"}
			}
			if err := awsHelmKarpenter(ctx, k8sOpt, name, release, params, resolved.KarpenterVersion, clusterCfg, nodeGroupNames, withAlias); err != nil {
				return err
			}
		}
	}

	return nil
}

// marshalYAML encodes v as YAML matching Python's yaml.dump default (yaml.v2 sequence behavior,
// single-quoted boolean strings to avoid yaml.v2 quoting them as "true"/"false").
// The boolean replacement only matches `: "keyword"` (colon-space-quoted value position), so it
// cannot misfire on substrings inside longer string values or on YAML keys.
func marshalYAML(v interface{}) (string, error) {
	data, err := yamlv2.Marshal(v)
	if err != nil {
		return "", err
	}
	result := string(data)
	for _, kw := range []string{"true", "false", "yes", "no", "on", "off"} {
		result = strings.ReplaceAll(result, fmt.Sprintf(`: "%s"`, kw), fmt.Sprintf(`: '%s'`, kw))
	}
	return result, nil
}

func helmChartCR(ctx *pulumi.Context, resourceName, metaName, namespace, repo, chart, targetNamespace, version string, valuesContent map[string]interface{}, k8sOpt pulumi.ResourceOption, aliases ...pulumi.ResourceOption) error {
	valuesYAML, encErr := marshalYAML(valuesContent)
	if encErr != nil {
		return fmt.Errorf("failed to marshal values for %s: %w", resourceName, encErr)
	}

	opts := []pulumi.ResourceOption{k8sOpt}
	opts = append(opts, aliases...)

	spec := pulumi.Map{
		"chart":           pulumi.String(chart),
		"targetNamespace": pulumi.String(targetNamespace),
		"version":         pulumi.String(version),
		"valuesContent":   pulumi.String(valuesYAML),
	}
	if repo != "" {
		spec["repo"] = pulumi.String(repo)
	}

	_, err := apiextensions.NewCustomResource(ctx, resourceName, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("helm.cattle.io/v1"),
		Kind:       pulumi.String("HelmChart"),
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String(metaName),
			Namespace: pulumi.String(namespace),
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": spec,
		},
	}, opts...)
	return err
}

// awsHelmChartCR creates a HelmChart custom resource with the posit.team/managed-by label
// added to every resource. Use this (not helmChartCR) for all AWS workload helm resources.
func awsHelmChartCR(ctx *pulumi.Context, resourceName, metaName, namespace, repo, chart, targetNamespace, version string, valuesContent map[string]interface{}, k8sOpt pulumi.ResourceOption, aliases ...pulumi.ResourceOption) error {
	valuesYAML, encErr := marshalYAML(valuesContent)
	if encErr != nil {
		return fmt.Errorf("failed to marshal values for %s: %w", resourceName, encErr)
	}

	opts := []pulumi.ResourceOption{k8sOpt}
	opts = append(opts, aliases...)

	spec := pulumi.Map{
		"chart":           pulumi.String(chart),
		"targetNamespace": pulumi.String(targetNamespace),
		"version":         pulumi.String(version),
		"valuesContent":   pulumi.String(valuesYAML),
	}
	if repo != "" {
		spec["repo"] = pulumi.String(repo)
	}

	_, err := apiextensions.NewCustomResource(ctx, resourceName, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("helm.cattle.io/v1"),
		Kind:       pulumi.String("HelmChart"),
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String(metaName),
			Namespace: pulumi.String(namespace),
			Labels:    pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": spec,
		},
	}, opts...)
	return err
}

func awsHelmLBC(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, accountID, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	clusterName := compoundName + "-" + release
	lbcRoleName := "aws-load-balancer-controller." + compoundName + ".posit.team"

	values := map[string]interface{}{
		"clusterName": clusterName,
		"serviceAccount": map[string]interface{}{
			"create": true,
			"name":   "aws-load-balancer-controller.posit.team",
			"annotations": map[string]interface{}{
				"eks.amazonaws.com/role-arn": fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, lbcRoleName),
			},
		},
		"hostNetwork":  true,
		"enableWaf":    false,
		"enableWafv2":  false,
		"enableShield": false,
	}

	resourceName := compoundName + "-" + release + "-aws-lbc-helm-release"
	return awsHelmChartCR(ctx, resourceName, "aws-load-balancer-controller",
		clustersHelmControllerNamespace,
		"https://aws.github.io/eks-charts",
		"aws-load-balancer-controller",
		clustersKubeSystemNamespace,
		version,
		values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmFsxOpenzfsCsi(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, accountID, fsxRoleName, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	values := map[string]interface{}{
		"controller": map[string]interface{}{
			"serviceAccount": map[string]interface{}{
				"create": true,
				"name":   "controller.aws-fsx-openzfs-csi-driver.posit.team",
				"annotations": map[string]interface{}{
					"eks.amazonaws.com/role-arn": fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, fsxRoleName),
				},
			},
			"tolerations": []interface{}{
				map[string]interface{}{"key": "CriticalAddonsOnly", "operator": "Exists"},
				map[string]interface{}{"effect": "NoExecute", "operator": "Exists", "tolerationSeconds": 300},
				map[string]interface{}{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
			},
		},
		"node": map[string]interface{}{
			"serviceAccount": map[string]interface{}{
				"create": true,
				"name":   "nodes.aws-fsx-openzfs-csi-driver.posit.team",
				"annotations": map[string]interface{}{
					"eks.amazonaws.com/role-arn": fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, fsxRoleName),
				},
			},
			"tolerations": []interface{}{
				map[string]interface{}{"operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300},
				map[string]interface{}{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
			},
		},
	}

	resourceName := compoundName + "-" + release + "-aws-fsx-openzfs-csi-helm-release"
	return awsHelmChartCR(ctx, resourceName, "aws-fsx-openzfs-csi",
		clustersHelmControllerNamespace,
		"https://kubernetes-sigs.github.io/aws-fsx-openzfs-csi-driver",
		"aws-fsx-openzfs-csi-driver",
		clustersKubeSystemNamespace,
		version, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmSecretStoreCsi(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	values := map[string]interface{}{
		"rotationPollInterval": "15s",
		"enableSecretRotation": true,
		"syncSecret":           map[string]interface{}{"enabled": true},
	}
	resourceName := compoundName + "-" + release + "-secret-store-csi-helm-release"
	return awsHelmChartCR(ctx, resourceName, "secret-store-csi",
		clustersHelmControllerNamespace,
		"https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts",
		"secrets-store-csi-driver",
		clustersKubeSystemNamespace,
		version, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmSecretStoreCsiAws(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	values := map[string]interface{}{
		"tolerations": []interface{}{
			map[string]interface{}{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
		},
	}
	resourceName := compoundName + "-" + release + "-secret-store-csi-provider-aws-helm-release"
	return awsHelmChartCR(ctx, resourceName, "secrets-store-csi-driver-provider-aws",
		clustersHelmControllerNamespace,
		"https://aws.github.io/secrets-store-csi-driver-provider-aws",
		"secrets-store-csi-driver-provider-aws",
		clustersKubeSystemNamespace,
		version, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmTraefik(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params awsHelmParams, weight, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	nsName := compoundName + "-" + release + "-traefik-ns"
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:   pulumi.String(helmTraefikNamespace),
			Labels: pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return fmt.Errorf("helm: failed to create traefik namespace: %w", err)
	}

	traefikValues := map[string]interface{}{
		"image": map[string]interface{}{
			"registry": "ghcr.io/traefik",
		},
		"deployment": map[string]interface{}{
			"kind": "Deployment",
		},
		"logs": map[string]interface{}{
			"access":  map[string]interface{}{"enabled": true},
			"general": map[string]interface{}{"level": "DEBUG"},
		},
		"ingressClass": map[string]interface{}{
			"enabled":        true,
			"isDefaultClass": true,
		},
		"ingressRoute": map[string]interface{}{
			"dashboard": map[string]interface{}{"enabled": true},
		},
		"providers": map[string]interface{}{
			"kubernetesCRD": map[string]interface{}{
				"allowCrossNamespace": true,
				"enabled":             true,
			},
			"kubernetesIngress": map[string]interface{}{"enabled": true},
		},
		"ports": map[string]interface{}{
			"traefik": map[string]interface{}{
				"expose":   map[string]interface{}{"default": true},
				"nodePort": 32090,
			},
		},
		"service": map[string]interface{}{"type": "NodePort"},
	}
	if !isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled) {
		traefikValues["globalArguments"] = []interface{}{
			"--global.checknewversion=false",
			"--global.sendanonymoususage=false",
		}
	}

	chartResourceName := compoundName + "-" + release + "-traefik-helm-release"
	valuesYAML, err := marshalYAML(traefikValues)
	if err != nil {
		return err
	}

	chartSpec := pulumi.Map{
		"repo":            pulumi.String("https://traefik.github.io/charts"),
		"chart":           pulumi.String("traefik"),
		"helmVersion":     pulumi.String("v3"),
		"targetNamespace": pulumi.String(helmTraefikNamespace),
		"version":         pulumi.String(version),
		"valuesContent":   pulumi.String(string(valuesYAML)),
	}

	_, err = apiextensions.NewCustomResource(ctx, chartResourceName, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("helm.cattle.io/v1"),
		Kind:       pulumi.String("HelmChart"),
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("traefik"),
			Namespace: pulumi.String(clustersHelmControllerNamespace),
			Labels:    pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": chartSpec,
		},
	}, k8sOpt, withAlias("kubernetes:helm.cattle.io/v1:HelmChart", chartResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	if err != nil {
		return fmt.Errorf("helm: failed to create traefik chart: %w", err)
	}

	// Create ALB ingress(es)
	albTagString := fmt.Sprintf("posit.team/true-name=%s,posit.team/environment=%s,Name=%s",
		params.trueName, params.environment, compoundName)

	if params.cfg.LoadBalancerPerSite {
		sortedSiteNames := helpers.SortedKeys(params.cfg.Sites)
		for _, siteName := range sortedSiteNames {
			siteConfig := params.cfg.Sites[siteName]
			ingressName := compoundName + "-" + release + "-" + siteName + "-traefik-ingress"
			metaIngressName := "traefik-" + siteName

			annotations := buildALBAnnotations(params.cfg, params.certARNs, albTagString)
			// DNS for per-site
			domain := siteConfig.Spec.Domain
			annotations["external-dns.alpha.kubernetes.io/hostname"] = domain + ",*." + domain
			annotations["external-dns.alpha.kubernetes.io/set-identifier"] = compoundName + "-" + release + "-" + siteName
			annotations["external-dns.alpha.kubernetes.io/aws-weight"] = weight

			annPulumi := pulumi.StringMap{}
			for k, v := range annotations {
				annPulumi[k] = pulumi.String(v)
			}

			_, err = apiextensions.NewCustomResource(ctx, ingressName, &apiextensions.CustomResourceArgs{
				ApiVersion: pulumi.String("networking.k8s.io/v1"),
				Kind:       pulumi.String("Ingress"),
				Metadata: metav1.ObjectMetaArgs{
					Name:        pulumi.String(metaIngressName),
					Namespace:   pulumi.String(helmTraefikNamespace),
					Annotations: annPulumi,
					Labels:      pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm"), "app": pulumi.String("traefik")},
				},
				OtherFields: kubernetes.UntypedArgs{
					"spec": pulumi.Map{
						"ingressClassName": pulumi.String("alb"),
						"rules": pulumi.Array{
							pulumi.Map{
								"http": pulumi.Map{
									"paths": pulumi.Array{
										pulumi.Map{
											"path":     pulumi.String("/*"),
											"pathType": pulumi.String("ImplementationSpecific"),
											"backend": pulumi.Map{
												"service": pulumi.Map{
													"name": pulumi.String("traefik"),
													"port": pulumi.Map{"number": pulumi.Int(80)},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}, k8sOpt, withAlias("kubernetes:networking.k8s.io/v1:Ingress", ingressName))
			if err != nil {
				return fmt.Errorf("helm: failed to create traefik ingress for site %s: %w", siteName, err)
			}
		}
	} else {
		ingressName := compoundName + "-" + release + "-traefik-ingress"
		annotations := buildALBAnnotations(params.cfg, params.certARNs, albTagString)

		// Collect unique DNS hosts
		uniqueHosts := map[string]bool{}
		for _, site := range params.cfg.Sites {
			domain := site.Spec.Domain
			uniqueHosts[domain] = true
			uniqueHosts["*."+domain] = true
		}
		hostList := make([]string, 0, len(uniqueHosts))
		for h := range uniqueHosts {
			hostList = append(hostList, h)
		}
		sort.Strings(hostList)
		annotations["external-dns.alpha.kubernetes.io/hostname"] = strings.Join(hostList, ",")
		annotations["external-dns.alpha.kubernetes.io/set-identifier"] = compoundName + "-" + release
		annotations["external-dns.alpha.kubernetes.io/aws-weight"] = weight

		annPulumi := pulumi.StringMap{}
		for k, v := range annotations {
			annPulumi[k] = pulumi.String(v)
		}

		_, err = apiextensions.NewCustomResource(ctx, ingressName, &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("networking.k8s.io/v1"),
			Kind:       pulumi.String("Ingress"),
			Metadata: metav1.ObjectMetaArgs{
				Name:        pulumi.String("traefik"),
				Namespace:   pulumi.String(helmTraefikNamespace),
				Annotations: annPulumi,
				Labels:      pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm"), "app": pulumi.String("traefik")},
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": pulumi.Map{
					"ingressClassName": pulumi.String("alb"),
					"rules": pulumi.Array{
						pulumi.Map{
							"http": pulumi.Map{
								"paths": pulumi.Array{
									pulumi.Map{
										"path":     pulumi.String("/*"),
										"pathType": pulumi.String("ImplementationSpecific"),
										"backend": pulumi.Map{
											"service": pulumi.Map{
												"name": pulumi.String("traefik"),
												"port": pulumi.Map{"number": pulumi.Int(80)},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}, k8sOpt, withAlias("kubernetes:networking.k8s.io/v1:Ingress", ingressName))
		if err != nil {
			return fmt.Errorf("helm: failed to create traefik ingress: %w", err)
		}
	}

	return nil
}

func buildALBAnnotations(cfg types.AWSWorkloadConfig, certARNs []string, tagString string) map[string]string {
	annotations := map[string]string{
		"alb.ingress.kubernetes.io/ssl-redirect":             "443",
		"alb.ingress.kubernetes.io/listen-ports":             `[{"HTTP": 80}, {"HTTPS": 443}]`,
		"alb.ingress.kubernetes.io/backend-protocol":         "HTTP",
		"alb.ingress.kubernetes.io/certificate-arn":          strings.Join(certARNs, ","),
		"alb.ingress.kubernetes.io/healthcheck-protocol":     "HTTP",
		"alb.ingress.kubernetes.io/ssl-policy":               "ELBSecurityPolicy-FS-1-2-2019-08",
		"alb.ingress.kubernetes.io/healthcheck-path":         "/ping",
		"alb.ingress.kubernetes.io/healthcheck-port":         "32090",
		"alb.ingress.kubernetes.io/load-balancer-attributes": "routing.http.drop_invalid_header_fields.enabled=true,idle_timeout.timeout_seconds=300",
		"alb.ingress.kubernetes.io/tags":                     tagString,
	}

	if cfg.ProvisionedVpc != nil {
		annotations["alb.ingress.kubernetes.io/subnets"] = strings.Join(cfg.ProvisionedVpc.PrivateSubnets, ",")
	}

	if cfg.PublicLoadBalancer == nil || *cfg.PublicLoadBalancer {
		annotations["alb.ingress.kubernetes.io/scheme"] = "internet-facing"
	} else {
		annotations["alb.ingress.kubernetes.io/scheme"] = "internal"
	}

	return annotations
}

func awsHelmMetricsServer(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	resourceName := compoundName + "-" + release + "-metrics-server-helm-release"
	return awsHelmChartCR(ctx, resourceName, "metrics-server",
		clustersHelmControllerNamespace,
		"https://kubernetes-sigs.github.io/metrics-server/",
		"metrics-server",
		clustersKubeSystemNamespace,
		version, map[string]interface{}{}, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmLoki(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params awsHelmParams, resolved types.ResolvedAWSComponents,
	withAlias func(string, string) pulumi.ResourceOption) error {

	nsName := compoundName + "-" + release + "-loki-ns"
	_, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:   pulumi.String(helmLokiNamespace),
			Labels: pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return err
	}

	lokiRole := "loki." + compoundName + ".posit.team"
	lokiBucket := "ptd-" + compoundName + "-loki"
	thirdParty := isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled)

	lokiCfg := map[string]interface{}{
		"auth_enabled": false,
		"storage": map[string]interface{}{
			"bucketNames": map[string]interface{}{
				"chunks": lokiBucket,
				"ruler":  lokiBucket,
				"admin":  lokiBucket,
			},
			"type": "s3",
			"s3": map[string]interface{}{
				"region":           params.region,
				"insecure":         false,
				"s3ForcePathStyle": false,
				"backoff_config": map[string]interface{}{
					"min_period":  "100ms",
					"max_period":  "10s",
					"max_retries": 5,
				},
				"http_config": map[string]interface{}{
					"idle_conn_timeout":       "90s",
					"response_header_timeout": "30s",
					"insecure_skip_verify":    false,
				},
			},
		},
		"limits_config": map[string]interface{}{
			"max_cache_freshness_per_query": "10m",
			"query_timeout":                 "300s",
			"reject_old_samples":            true,
			"reject_old_samples_max_age":    "168h",
			"split_queries_by_interval":     "15m",
			"volume_enabled":                true,
		},
		"storage_config": map[string]interface{}{
			"hedging": map[string]interface{}{
				"at":             "250ms",
				"max_per_second": 20,
				"up_to":          3,
			},
		},
	}
	if !thirdParty {
		lokiCfg["analytics"] = map[string]interface{}{"reporting_enabled": false}
	}

	lreplicas := resolved.LokiReplicas
	values := map[string]interface{}{
		"gateway": map[string]interface{}{
			"image": map[string]interface{}{
				"registry":   "quay.io",
				"repository": "nginx/nginx-unprivileged",
			},
		},
		"loki": lokiCfg,
		"serviceAccount": map[string]interface{}{
			"create": true,
			"name":   "loki.posit.team",
			"annotations": map[string]interface{}{
				"eks.amazonaws.com/role-arn": fmt.Sprintf("arn:aws:iam::%s:role/%s", params.accountID, lokiRole),
			},
		},
		"sidecar": map[string]interface{}{
			"image": map[string]interface{}{"repository": "quay.io/kiwigrid/k8s-sidecar"},
		},
		"monitoring": map[string]interface{}{
			"dashboards":     map[string]interface{}{"enabled": false},
			"serviceMonitor": map[string]interface{}{"enabled": false},
			"selfMonitoring": map[string]interface{}{"enabled": false, "grafanaAgent": map[string]interface{}{"installOperator": false}},
		},
		"test":    map[string]interface{}{"enabled": false},
		"backend": map[string]interface{}{"replicas": lreplicas, "persistence": map[string]interface{}{"enableStatefulSetAutoDeletePVC": true}},
		"read":    map[string]interface{}{"replicas": lreplicas, "persistence": map[string]interface{}{"enableStatefulSetAutoDeletePVC": true}},
		"write":   map[string]interface{}{"replicas": lreplicas, "persistence": map[string]interface{}{"enableStatefulSetAutoDeletePVC": true}},
	}

	resourceName := compoundName + "-" + release + "-loki-helm-release"
	return awsHelmChartCR(ctx, resourceName, "loki",
		clustersHelmControllerNamespace,
		"https://grafana.github.io/helm-charts",
		"loki",
		helmLokiNamespace,
		resolved.LokiVersion, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmGrafana(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params awsHelmParams, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	domain := mainDomain(params.cfg.Sites)
	thirdParty := isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled)

	iniCfg := map[string]interface{}{
		"server": map[string]interface{}{
			"domain":              domain,
			"root_url":            fmt.Sprintf("https://grafana.%s", domain),
			"serve_from_sub_path": false,
		},
		"auth.proxy": map[string]interface{}{
			"enabled":         true,
			"header_name":     "X-Forwarded-User",
			"header_property": "username",
			"auto_sign_up":    true,
		},
		"auth": map[string]interface{}{
			"disable_signout_menu": true,
		},
		"database": map[string]interface{}{
			"url":      "${PTD_DATABASE_URL}",
			"ssl_mode": "require",
		},
		"users": map[string]interface{}{
			"auto_assign_org_role": "Editor",
		},
	}
	if !thirdParty {
		iniCfg["analytics"] = map[string]interface{}{
			"reporting_enabled": false,
			"check_for_updates": false,
		}
		iniCfg["plugins"] = map[string]interface{}{
			"check_for_plugin_updates": false,
		}
	}

	values := map[string]interface{}{
		"envFromSecret": "grafana-db-url",
		"grafana.ini":   iniCfg,
		"ingress": map[string]interface{}{
			"enabled": true,
			"annotations": map[string]interface{}{
				"traefik.ingress.kubernetes.io/router.middlewares": "kube-system-traefik-forward-auth-add-forwarded-headers@kubernetescrd,kube-system-traefik-forward-auth-main@kubernetescrd",
			},
			"hosts": []interface{}{fmt.Sprintf("grafana.%s", domain)},
			"path":  "/",
		},
		"datasources": map[string]interface{}{
			"datasources.yaml": map[string]interface{}{
				"apiVersion": 1,
				"datasources": []interface{}{
					map[string]interface{}{
						"name":      "Loki",
						"type":      "loki",
						"access":    "proxy",
						"editable":  false,
						"url":       "http://loki-gateway.loki.svc.cluster.local",
						"isDefault": true,
					},
					map[string]interface{}{
						"name":      "Mimir",
						"type":      "prometheus",
						"access":    "proxy",
						"editable":  false,
						"url":       "http://mimir-gateway.mimir.svc.cluster.local/prometheus",
						"isDefault": false,
					},
				},
			},
		},
	}

	resourceName := compoundName + "-" + release + "-grafana-helm-release"
	return awsHelmChartCR(ctx, resourceName, "grafana",
		clustersHelmControllerNamespace,
		"https://grafana.github.io/helm-charts",
		"grafana",
		helmGrafanaNamespace,
		version, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmMimir(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params awsHelmParams, resolved types.ResolvedAWSComponents,
	withAlias func(string, string) pulumi.ResourceOption) error {

	nsName := compoundName + "-" + release + "-mimir-ns"
	_, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:   pulumi.String(helmMimirNamespace),
			Labels: pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return err
	}

	mimirRole := "mimir." + compoundName + ".posit.team"
	mimirBucket := "ptd-" + compoundName + "-mimir"
	thirdParty := isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled)

	structuredConfig := map[string]interface{}{
		"blocks_storage": map[string]interface{}{
			"backend":        "s3",
			"storage_prefix": "blocks",
			"s3": map[string]interface{}{
				"bucket_name": mimirBucket,
				"endpoint":    fmt.Sprintf("s3.%s.amazonaws.com", params.region),
				"insecure":    false,
			},
		},
		"limits": map[string]interface{}{
			"max_global_series_per_user": 800000,
			"max_label_names_per_series": 45,
		},
	}
	if !thirdParty {
		structuredConfig["usage_stats"] = map[string]interface{}{"enabled": false}
	}

	affinityRule := map[string]interface{}{
		"nodeAffinity": map[string]interface{}{
			"requiredDuringSchedulingIgnoredDuringExecution": map[string]interface{}{
				"nodeSelectorTerms": []interface{}{
					map[string]interface{}{
						"matchExpressions": []interface{}{
							map[string]interface{}{"key": "karpenter.sh/nodepool", "operator": "DoesNotExist"},
						},
					},
				},
			},
		},
	}

	mreplicas := resolved.MimirReplicas
	values := map[string]interface{}{
		"serviceAccount": map[string]interface{}{
			"create": true,
			"name":   "mimir.posit.team",
			"annotations": map[string]interface{}{
				"eks.amazonaws.com/role-arn": fmt.Sprintf("arn:aws:iam::%s:role/%s", params.accountID, mimirRole),
			},
		},
		"minio":        map[string]interface{}{"enabled": false},
		"mimir":        map[string]interface{}{"structuredConfig": structuredConfig},
		"alertmanager": map[string]interface{}{"enabled": false},
		"ruler":        map[string]interface{}{"enabled": false},
		"ingester": map[string]interface{}{
			"persistentVolume":     map[string]interface{}{"size": "20Gi", "enableRetentionPolicy": true, "whenDeleted": "Delete", "whenScaled": "Delete"},
			"replicas":             mreplicas,
			"zoneAwareReplication": map[string]interface{}{"enabled": false},
			"affinity":             affinityRule,
		},
		"compactor": map[string]interface{}{
			"persistentVolume": map[string]interface{}{"size": "20Gi", "enableRetentionPolicy": true, "whenDeleted": "Delete", "whenScaled": "Delete"},
			"replicas":         mreplicas,
			"affinity":         affinityRule,
		},
		"store_gateway": map[string]interface{}{
			"persistentVolume":     map[string]interface{}{"size": "20Gi", "enableRetentionPolicy": true, "whenDeleted": "Delete", "whenScaled": "Delete"},
			"replicas":             mreplicas,
			"zoneAwareReplication": map[string]interface{}{"enabled": false},
			"affinity":             affinityRule,
		},
		"gateway": map[string]interface{}{
			"enabledNonEnterprise": true,
			"nginx": map[string]interface{}{
				"image": map[string]interface{}{
					"registry":   "quay.io",
					"repository": "nginx/nginx-unprivileged",
				},
			},
		},
		"nginx": map[string]interface{}{"enabled": false},
	}

	resourceName := compoundName + "-" + release + "-mimir-helm-release"
	return awsHelmChartCR(ctx, resourceName, "mimir",
		clustersHelmControllerNamespace,
		"https://grafana.github.io/helm-charts",
		"mimir-distributed",
		helmMimirNamespace,
		resolved.MimirVersion, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmKubeStateMetrics(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	values := map[string]interface{}{
		"metricLabelsAllowlist": []interface{}{"pods=[launcher-instance-id,user-group-*]"},
	}
	resourceName := compoundName + "-" + release + "-kube-state-metrics-helm-release"
	return awsHelmChartCR(ctx, resourceName, "kube-state-metrics",
		clustersHelmControllerNamespace,
		"https://prometheus-community.github.io/helm-charts",
		"kube-state-metrics",
		clustersKubeSystemNamespace,
		version, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmAlloy(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params awsHelmParams, resolved types.ResolvedAWSComponents,
	clusterSpec types.AWSWorkloadClusterSpec,
	withAlias func(string, string) pulumi.ResourceOption,
	withNestedAlias func(string, string, string) pulumi.ResourceOption) error {

	nsName := compoundName + "-" + release + "-alloy-ns"
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:   pulumi.String(helmAlloyNamespace),
			Labels: pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return err
	}

	// Build alloy config
	domain := mainDomain(params.cfg.Sites)
	// Python's eks_cluster_name() uses "default_{fqn}-control-plane" as the cluster label in
	// Alloy config (Pulumi logical name convention). Match this to avoid breaking dashboard queries.
	alloyClusterName := "default_" + compoundName + "-control-plane"
	trueName, _ := splitCompoundName(compoundName)

	alloyParams := alloyConfigParams{
		compoundName:               compoundName,
		trueName:                   trueName,
		domain:                     domain,
		controlRoomDomain:          params.cfg.ControlRoomDomain,
		thirdPartyTelemetryEnabled: isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled),
		release:                    release,
		region:                     params.region,
		clusterName:                alloyClusterName,
		accountIDOrTenantID:        params.accountID,
		cloudProvider:              "aws",
		shouldScrapeSystemLogs:     params.cfg.GrafanaScrapeSystemLogs,
		sites:                      params.cfg.Sites,
		workloadDir:                params.workloadDir,
	}
	alloyConfigStr := buildAlloyConfig(alloyParams)

	// ConfigMap
	configMapName := compoundName + "-" + release + "-alloy-config"
	cmResourceName := configMapName + "-configmap"
	_, err = corev1.NewConfigMap(ctx, cmResourceName, &corev1.ConfigMapArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String(configMapName),
			Namespace: pulumi.String(helmAlloyNamespace),
			Labels:    pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
		Data: pulumi.StringMap{
			"config.alloy": pulumi.String(alloyConfigStr),
		},
	}, k8sOpt,
		withNestedAlias("ptd:AlloyConfig", "kubernetes:core/v1:ConfigMap", cmResourceName),
		// Also alias without ptd:AWSWorkloadHelm$ prefix for workloads where AlloyConfig was top-level.
		pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(fmt.Sprintf(
			"urn:pulumi:%s::ptd-aws-workload-helm::ptd:AlloyConfig$kubernetes:core/v1:ConfigMap::%s",
			ctx.Stack(), cmResourceName))}}))
	if err != nil {
		return err
	}

	// Mimir auth Secret (parent was the namespace in Python)
	secretResourceName := compoundName + "-" + release + "-alloy-mimir-auth"
	_, err = corev1.NewSecret(ctx, secretResourceName, &corev1.SecretArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("mimir-auth"),
			Namespace: pulumi.String(helmAlloyNamespace),
			Labels:    pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
		StringData: pulumi.StringMap{
			"password": pulumi.String(params.mimirPassword),
		},
	}, k8sOpt,
		// In Python: opts=ResourceOptions(parent=namespace, ...) — so URN path includes Namespace.
		// Two variants: with and without ptd:AWSWorkloadHelm$ prefix depending on Python state vintage.
		pulumi.Aliases([]pulumi.Alias{
			{URN: pulumi.URN(fmt.Sprintf(
				"urn:pulumi:%s::ptd-aws-workload-helm::ptd:AWSWorkloadHelm$kubernetes:core/v1:Namespace$kubernetes:core/v1:Secret::%s",
				ctx.Stack(), secretResourceName))},
			{URN: pulumi.URN(fmt.Sprintf(
				"urn:pulumi:%s::ptd-aws-workload-helm::kubernetes:core/v1:Namespace$kubernetes:core/v1:Secret::%s",
				ctx.Stack(), secretResourceName))},
		}),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	if err != nil {
		return err
	}

	alloyRoleName := "alloy." + compoundName + ".posit.team"
	thirdParty := isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled)

	alloyValues := map[string]interface{}{
		"serviceAccount": map[string]interface{}{
			"create": true,
			"name":   "alloy.posit.team",
			"annotations": map[string]interface{}{
				"eks.amazonaws.com/role-arn": fmt.Sprintf("arn:aws:iam::%s:role/%s", params.accountID, alloyRoleName),
			},
		},
		"controller": map[string]interface{}{
			"volumes": map[string]interface{}{
				"extra": []interface{}{
					map[string]interface{}{
						"name": "mimir-auth",
						"secret": map[string]interface{}{
							"secretName": "mimir-auth",
							"items": []interface{}{
								map[string]interface{}{"key": "password", "path": "password"},
							},
						},
					},
				},
			},
		},
		"alloy": map[string]interface{}{
			"clustering": map[string]interface{}{"enabled": true},
			"extraPorts": []interface{}{
				map[string]interface{}{"name": "faro", "port": 12347, "targetPort": 12347, "protocol": "TCP"},
			},
			"mounts": map[string]interface{}{
				"extra": []interface{}{
					map[string]interface{}{"name": "mimir-auth", "mountPath": "/etc/mimir/", "readOnly": true},
				},
				"varlog": params.cfg.GrafanaScrapeSystemLogs,
			},
			"securityContext": map[string]interface{}{
				"privileged": params.cfg.GrafanaScrapeSystemLogs,
				"runAsUser":  nil,
			},
			"configMap": map[string]interface{}{
				"create": false,
				"name":   configMapName,
				"key":    "config.alloy",
			},
		},
		"ingress": map[string]interface{}{
			"enabled":  true,
			"faroPort": 12347,
			"hosts":    []interface{}{fmt.Sprintf("faro.%s", domain)},
		},
		"tolerations": []interface{}{
			map[string]interface{}{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
		},
	}
	if !thirdParty {
		alloyValues["alloy"].(map[string]interface{})["reporting"] = map[string]interface{}{"enabled": false}
	}
	if params.cfg.GrafanaScrapeSystemLogs {
		alloyValues["alloy"].(map[string]interface{})["securityContext"].(map[string]interface{})["runAsUser"] = 0
	}

	chartResourceName := compoundName + "-" + release + "-grafana-alloy-release"
	return awsHelmChartCR(ctx, chartResourceName, "alloy",
		clustersHelmControllerNamespace,
		"https://grafana.github.io/helm-charts",
		"alloy",
		helmAlloyNamespace,
		resolved.AlloyVersion, alloyValues, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", chartResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
}

func awsHelmNvidiaDevicePlugin(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	nsName := compoundName + "-" + release + "-nvidia-device-plugin-ns"
	_, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:   pulumi.String(helmNvidiaNamespace),
			Labels: pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return err
	}

	values := map[string]interface{}{
		"nfd": map[string]interface{}{
			"enabled": true,
			"worker": map[string]interface{}{
				"tolerations": []interface{}{
					map[string]interface{}{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
					map[string]interface{}{"key": "nvidia.com/gpu", "operator": "Equal", "value": "present", "effect": "NoSchedule"},
					map[string]interface{}{"key": "node-role.kubernetes.io/master", "operator": "Equal", "value": "", "effect": "NoSchedule"},
				},
			},
		},
		"migStrategy":      "none",
		"failOnInitError":  true,
		"nvidiaDriverRoot": "/",
		"plugin": map[string]interface{}{
			"passDeviceSpecs":    false,
			"deviceListStrategy": "envvar",
			"deviceIDStrategy":   "uuid",
		},
		"tolerations": []interface{}{
			map[string]interface{}{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
			map[string]interface{}{"key": "nvidia.com/gpu", "operator": "Exists", "effect": "NoSchedule"},
			map[string]interface{}{"key": "CriticalAddonsOnly", "operator": "Exists"},
		},
	}

	resourceName := compoundName + "-" + release + "-nvidia-device-plugin-helm-release"
	return awsHelmChartCR(ctx, resourceName, "nvidia-device-plugin",
		clustersHelmControllerNamespace,
		"https://nvidia.github.io/k8s-device-plugin",
		"nvidia-device-plugin",
		helmNvidiaNamespace,
		version, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func awsHelmKarpenter(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params awsHelmParams, version string,
	clusterSpec types.AWSWorkloadClusterSpec,
	nodeGroupNames []string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	clusterName := compoundName + "-" + release
	karpenterRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/KarpenterControllerRole-%s.posit.team", params.accountID, clusterName)

	values := map[string]interface{}{
		"controller": map[string]interface{}{
			"resources": map[string]interface{}{
				"limits":   map[string]interface{}{"cpu": "1", "memory": "1Gi"},
				"requests": map[string]interface{}{"cpu": "1", "memory": "1Gi"},
			},
			"env": []interface{}{
				map[string]interface{}{"name": "AWS_REGION", "value": params.region},
			},
		},
		"affinity": map[string]interface{}{
			"nodeAffinity": map[string]interface{}{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]interface{}{
					"nodeSelectorTerms": []interface{}{
						map[string]interface{}{
							"matchExpressions": []interface{}{
								map[string]interface{}{"key": "karpenter.sh/nodepool", "operator": "DoesNotExist"},
								map[string]interface{}{"key": "eks.amazonaws.com/nodegroup", "operator": "In", "values": nodeGroupNames},
							},
						},
					},
				},
			},
			"podAntiAffinity": map[string]interface{}{
				"requiredDuringSchedulingIgnoredDuringExecution": []interface{}{
					map[string]interface{}{"topologyKey": "kubernetes.io/hostname"},
				},
			},
		},
		"serviceAccount": map[string]interface{}{
			"annotations": map[string]interface{}{
				"eks.amazonaws.com/role-arn": karpenterRoleARN,
			},
		},
		"settings": map[string]interface{}{
			"clusterName":       clusterName,
			"interruptionQueue": clusterName,
		},
	}

	chartResourceName := compoundName + "-karpenter-helm-release"
	valuesYAML, err := marshalYAML(values)
	if err != nil {
		return err
	}

	_, err = apiextensions.NewCustomResource(ctx, chartResourceName, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("helm.cattle.io/v1"),
		Kind:       pulumi.String("HelmChart"),
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("karpenter"),
			Namespace: pulumi.String(clustersHelmControllerNamespace),
			Labels:    pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": pulumi.Map{
				"chart":           pulumi.String("oci://public.ecr.aws/karpenter/karpenter"),
				"targetNamespace": pulumi.String(clustersKubeSystemNamespace),
				"version":         pulumi.String(version),
				"valuesContent":   pulumi.String(string(valuesYAML)),
			},
		},
	}, k8sOpt, withAlias("kubernetes:helm.cattle.io/v1:HelmChart", chartResourceName))
	if err != nil {
		return err
	}

	// NodePools and EC2NodeClasses
	karpenterCfg := clusterSpec.KarpenterConfig
	if karpenterCfg == nil {
		return nil
	}

	vpcCfg := params.karpenterVPCConfigByCluster[release]

	for _, nodePool := range karpenterCfg.NodePools {
		// Build requirements
		requirements := make([]interface{}, 0, len(nodePool.Requirements))
		for _, req := range nodePool.Requirements {
			requirements = append(requirements, map[string]interface{}{
				"key":      req.Key,
				"operator": req.Operator,
				"values":   req.Values,
			})
		}

		// Apply disruption defaults
		consolidationPolicy := nodePool.ConsolidationPolicy
		if consolidationPolicy == "" {
			consolidationPolicy = "WhenEmptyOrUnderutilized"
		}
		consolidateAfter := nodePool.ConsolidateAfter
		if consolidateAfter == "" {
			consolidateAfter = "5m"
		}

		nodepoolSpec := map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"requirements": requirements,
					"nodeClassRef": map[string]interface{}{
						"group": "karpenter.k8s.aws",
						"kind":  "EC2NodeClass",
						"name":  nodePool.Name,
					},
				},
			},
			"disruption": map[string]interface{}{
				"consolidationPolicy": consolidationPolicy,
				"consolidateAfter":    consolidateAfter,
			},
			"weight": nodePool.Weight,
		}

		if nodePool.ExpireAfter != nil {
			nodepoolSpec["template"].(map[string]interface{})["spec"].(map[string]interface{})["expireAfter"] = *nodePool.ExpireAfter
		}

		// Build taints: start with explicitly listed taints, then add session taint if session_taints: true.
		allTaints := make([]types.KarpenterTaint, 0, len(nodePool.Taints)+1)
		allTaints = append(allTaints, nodePool.Taints...)
		if nodePool.SessionTaints {
			hasSessionTaint := false
			for _, t := range nodePool.Taints {
				if t.Key == "workload-type" && t.Effect == "NoSchedule" {
					hasSessionTaint = true
					break
				}
			}
			if !hasSessionTaint {
				allTaints = append(allTaints, types.KarpenterTaint{Key: "workload-type", Value: "session", Effect: "NoSchedule"})
			}
		}
		if len(allTaints) > 0 {
			taints := make([]interface{}, 0, len(allTaints))
			for _, t := range allTaints {
				taints = append(taints, map[string]interface{}{
					"key":    t.Key,
					"value":  t.Value,
					"effect": t.Effect,
				})
			}
			nodepoolSpec["template"].(map[string]interface{})["spec"].(map[string]interface{})["taints"] = taints
		}

		if nodePool.Limits != nil {
			limits := map[string]interface{}{}
			if nodePool.Limits.CPU != nil {
				limits["cpu"] = *nodePool.Limits.CPU
			}
			if nodePool.Limits.Memory != nil {
				limits["memory"] = *nodePool.Limits.Memory
			}
			if nodePool.Limits.NvidiaComGPU != nil {
				limits["nvidia.com/gpu"] = *nodePool.Limits.NvidiaComGPU
			}
			if len(limits) > 0 {
				nodepoolSpec["limits"] = limits
			}
		}

		npResourceName := compoundName + "-" + release + "-karpenter-nodepool-" + nodePool.Name
		_, err = apiextensions.NewCustomResource(ctx, npResourceName, &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("karpenter.sh/v1"),
			Kind:       pulumi.String("NodePool"),
			Metadata: metav1.ObjectMetaArgs{
				Name:   pulumi.String(nodePool.Name),
				Labels: pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": nodepoolSpec,
			},
		}, k8sOpt, withAlias("kubernetes:karpenter.sh/v1:NodePool", npResourceName))
		if err != nil {
			return err
		}

		// EC2NodeClass
		// Subnet and security group IDs are fetched dynamically from the live EKS cluster.
		subnetTerms := make([]interface{}, 0, len(vpcCfg.SubnetIDs))
		for _, id := range vpcCfg.SubnetIDs {
			subnetTerms = append(subnetTerms, map[string]interface{}{"id": id})
		}
		sgTerms := make([]interface{}, 0, len(vpcCfg.SecurityGroupIDs))
		for _, id := range vpcCfg.SecurityGroupIDs {
			sgTerms = append(sgTerms, map[string]interface{}{"id": id})
		}

		instanceProfile := fmt.Sprintf("KarpenterNodeInstanceProfile-%s.posit.team", clusterName)
		rootVolumeSize := nodePool.RootVolumeSize
		if rootVolumeSize == "" {
			rootVolumeSize = "100Gi"
		}

		ec2ResourceName := compoundName + "-" + release + "-karpenter-ec2nodeclass-" + nodePool.Name
		_, err = apiextensions.NewCustomResource(ctx, ec2ResourceName, &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("karpenter.k8s.aws/v1"),
			Kind:       pulumi.String("EC2NodeClass"),
			Metadata: metav1.ObjectMetaArgs{
				Name:   pulumi.String(nodePool.Name),
				Labels: pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"instanceProfile":            instanceProfile,
					"amiSelectorTerms":           []interface{}{map[string]interface{}{"alias": "al2023@latest"}},
					"subnetSelectorTerms":        subnetTerms,
					"securityGroupSelectorTerms": sgTerms,
					"blockDeviceMappings": []interface{}{
						map[string]interface{}{
							"deviceName": "/dev/xvda",
							"ebs": map[string]interface{}{
								"volumeSize": rootVolumeSize,
								"volumeType": "gp3",
							},
						},
					},
				},
			},
		}, k8sOpt, withAlias("kubernetes:karpenter.k8s.aws/v1:EC2NodeClass", ec2ResourceName))
		if err != nil {
			return err
		}
	}

	// Overprovisioning priority class and deployments
	if err := awsHelmKarpenterOverprovisioning(ctx, k8sOpt, compoundName, release, karpenterCfg, withAlias); err != nil {
		return err
	}

	return nil
}

func awsHelmKarpenterOverprovisioning(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	karpenterCfg *types.KarpenterConfig, withAlias func(string, string) pulumi.ResourceOption) error {

	clusterName := compoundName + "-" + release

	// PriorityClass
	pcResourceName := clusterName + "-karpenter-overprovisioning-pool-priority"
	_, err := schedulingv1.NewPriorityClass(ctx, pcResourceName, &schedulingv1.PriorityClassArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:   pulumi.String("karpenter-overprovisioning-pool-priority"),
			Labels: pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
		},
		Value:            pulumi.Int(-100),
		GlobalDefault:    pulumi.Bool(false),
		Description:      pulumi.StringPtr("Low priority for Karpenter overprovisioning pool pods that should be evicted first"),
		PreemptionPolicy: pulumi.StringPtr("PreemptLowerPriority"),
	}, k8sOpt, withAlias("kubernetes:scheduling.k8s.io/v1:PriorityClass", pcResourceName))
	if err != nil {
		return err
	}

	for _, nodePool := range karpenterCfg.NodePools {
		if nodePool.OverprovisioningReplicas <= 0 {
			continue
		}

		deploymentName := nodePool.Name + "-overprovisioning"
		appLabel := nodePool.Name + "-overprovisioning"

		requests := pulumi.StringMap{}
		limits := pulumi.StringMap{}
		if nodePool.OverprovisioningCPURequest != nil {
			requests["cpu"] = pulumi.String(*nodePool.OverprovisioningCPURequest)
			limits["cpu"] = pulumi.String(*nodePool.OverprovisioningCPURequest)
		}
		if nodePool.OverprovisioningMemoryRequest != nil {
			requests["memory"] = pulumi.String(*nodePool.OverprovisioningMemoryRequest)
			limits["memory"] = pulumi.String(*nodePool.OverprovisioningMemoryRequest)
		}
		if nodePool.OverprovisioningNvidiaGPU != nil {
			requests["nvidia.com/gpu"] = pulumi.String(*nodePool.OverprovisioningNvidiaGPU)
			limits["nvidia.com/gpu"] = pulumi.String(*nodePool.OverprovisioningNvidiaGPU)
		}

		tolerations := make(corev1.TolerationArray, 0, len(nodePool.Taints))
		for _, taint := range nodePool.Taints {
			tolerations = append(tolerations, corev1.TolerationArgs{
				Key:      pulumi.String(taint.Key),
				Operator: pulumi.StringPtr("Equal"),
				Value:    pulumi.StringPtr(taint.Value),
				Effect:   pulumi.StringPtr(taint.Effect),
			})
		}

		depResourceName := clusterName + "-" + deploymentName
		_, err = appsv1.NewDeployment(ctx, depResourceName, &appsv1.DeploymentArgs{
			Metadata: metav1.ObjectMetaArgs{
				Name:      pulumi.String(deploymentName),
				Namespace: pulumi.String(clustersKubeSystemNamespace),
				Labels:    pulumi.StringMap{"posit.team/managed-by": pulumi.String("ptd.pulumi_resources.aws_workload_helm")},
			},
			Spec: appsv1.DeploymentSpecArgs{
				Replicas: pulumi.Int(nodePool.OverprovisioningReplicas),
				Selector: metav1.LabelSelectorArgs{
					MatchLabels: pulumi.StringMap{"app": pulumi.String(appLabel)},
				},
				Template: corev1.PodTemplateSpecArgs{
					Metadata: metav1.ObjectMetaArgs{
						Labels: pulumi.StringMap{"app": pulumi.String(appLabel), "nodepool": pulumi.String(nodePool.Name)},
					},
					Spec: corev1.PodSpecArgs{
						PriorityClassName:             pulumi.StringPtr("karpenter-overprovisioning-pool-priority"),
						TerminationGracePeriodSeconds: pulumi.IntPtr(0),
						Containers: corev1.ContainerArray{
							corev1.ContainerArgs{
								Name:  pulumi.String("pause"),
								Image: pulumi.String("registry.k8s.io/pause:3.9"),
								Resources: corev1.ResourceRequirementsArgs{
									Requests: requests,
									Limits:   limits,
								},
							},
						},
						Tolerations: tolerations,
						Affinity: corev1.AffinityArgs{
							NodeAffinity: corev1.NodeAffinityArgs{
								RequiredDuringSchedulingIgnoredDuringExecution: corev1.NodeSelectorArgs{
									NodeSelectorTerms: corev1.NodeSelectorTermArray{
										corev1.NodeSelectorTermArgs{
											MatchExpressions: corev1.NodeSelectorRequirementArray{
												corev1.NodeSelectorRequirementArgs{
													Key:      pulumi.String("karpenter.sh/nodepool"),
													Operator: pulumi.String("In"),
													Values:   pulumi.StringArray{pulumi.String(nodePool.Name)},
												},
											},
										},
									},
								},
							},
							PodAntiAffinity: corev1.PodAntiAffinityArgs{
								PreferredDuringSchedulingIgnoredDuringExecution: corev1.WeightedPodAffinityTermArray{
									corev1.WeightedPodAffinityTermArgs{
										Weight: pulumi.Int(100),
										PodAffinityTerm: corev1.PodAffinityTermArgs{
											LabelSelector: metav1.LabelSelectorArgs{
												MatchLabels: pulumi.StringMap{"app": pulumi.String(appLabel)},
											},
											TopologyKey: pulumi.String("kubernetes.io/hostname"),
										},
									},
								},
							},
						},
					},
				},
			},
		}, k8sOpt, withAlias("kubernetes:apps/v1:Deployment", depResourceName))
		if err != nil {
			return err
		}
	}

	return nil
}

// isThirdPartyTelemetryEnabled returns true if third-party telemetry is enabled (nil defaults to true).
func isThirdPartyTelemetryEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

// mainDomain returns the domain of the "main" site, mirroring Python's
// WorkloadConfig.domain property (self.sites["main"].domain). Falls back to
// the first site alphabetically for workloads without a "main" site.
func mainDomain(sites map[string]types.SiteConfig) string {
	if s, ok := sites["main"]; ok {
		return s.Spec.Domain
	}
	names := helpers.SortedKeys(sites)
	if len(names) == 0 {
		return ""
	}
	return sites[names[0]].Spec.Domain
}
