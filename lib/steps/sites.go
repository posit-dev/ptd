package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"dario.cat/mergo"
	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/types"
	pulumiaws "github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/rds"
	kubernetes "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	yaml "gopkg.in/yaml.v3"
)

const (
	positTeamNamespace = "posit-team"
	// Matches the old Python module name to avoid label diffs on existing Site resources.
	siteManagedByLabel = "ptd.pulumi_resources.team_site"
)

type SitesStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *SitesStep) Name() string {
	return "sites"
}

func (s *SitesStep) ProxyRequired() bool {
	return true
}

func (s *SitesStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *SitesStep) Run(ctx context.Context) error {
	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}
	envVars, err := prepareEnvVarsForPulumi(ctx, s.DstTarget, creds)
	if err != nil {
		return err
	}

	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		return s.runAWSInlineGo(ctx, creds, envVars)
	case types.Azure:
		return s.runAzureInlineGo(ctx, creds, envVars)
	default:
		return fmt.Errorf("unsupported cloud provider for sites: %s", s.DstTarget.CloudProvider())
	}
}

// --- AWS ---

type awsSiteParams struct {
	compoundName         string
	accountID            string
	region               string
	chronicleBucket      string
	packageManagerBucket string
	fsDnsName            string
	mainDBInstanceID     string
	networkTrust         int
	vpcCidr              string
	tailscaleEnabled     bool
	kubeconfigsByRelease map[string]string
	clusters             map[string]types.AWSWorkloadClusterConfig
	sites                map[string]types.SiteConfig
	resourceTags         map[string]string
}

func (s *SitesStep) runAWSInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("sites: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSWorkloadConfig)
	if !ok {
		return fmt.Errorf("sites: expected AWSWorkloadConfig")
	}

	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}

	// Fetch workload secrets from Secrets Manager.
	secretName := s.DstTarget.Name() + ".posit.team"
	secretJSON, err := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, secretName)
	if err != nil {
		return fmt.Errorf("sites: failed to get workload secret %q: %w", secretName, err)
	}
	var secrets map[string]string
	if err := json.Unmarshal([]byte(secretJSON), &secrets); err != nil {
		return fmt.Errorf("sites: failed to parse workload secret: %w", err)
	}

	// Build kubeconfig string per release.
	kubeconfigsByRelease := make(map[string]string, len(cfg.Clusters))
	for release := range cfg.Clusters {
		clusterName := s.DstTarget.Name() + "-" + release
		endpoint, caCert, err := aws.GetClusterInfo(ctx, awsCreds, s.DstTarget.Region(), clusterName)
		if err != nil {
			return fmt.Errorf("sites: failed to get cluster info for %s: %w", clusterName, err)
		}
		token, err := aws.GetEKSToken(ctx, awsCreds, s.DstTarget.Region(), clusterName)
		if err != nil {
			return fmt.Errorf("sites: failed to get EKS token for %s: %w", clusterName, err)
		}
		config := kube.BuildEKSKubeConfig(endpoint, caCert, token, clusterName)
		if !cfg.TailscaleEnabled {
			config.Clusters[0].Cluster.ProxyURL = "socks5://localhost:1080"
		}
		data, err := yaml.Marshal(config)
		if err != nil {
			return fmt.Errorf("sites: failed to marshal kubeconfig for %s: %w", clusterName, err)
		}
		kubeconfigsByRelease[release] = string(data)
	}

	params := awsSiteParams{
		compoundName:         s.DstTarget.Name(),
		accountID:            awsCreds.AccountID(),
		region:               s.DstTarget.Region(),
		chronicleBucket:      secrets["chronicle-bucket"],
		packageManagerBucket: secrets["packagemanager-bucket"],
		fsDnsName:            secrets["fs-dns-name"],
		mainDBInstanceID:     secrets["main-database-id"],
		networkTrust:         types.NetworkTrustValue(cfg.NetworkTrust),
		vpcCidr:              cfg.VpcCidr,
		tailscaleEnabled:     cfg.TailscaleEnabled,
		kubeconfigsByRelease: kubeconfigsByRelease,
		clusters:             cfg.Clusters,
		sites:                cfg.Sites,
		resourceTags:         cfg.ResourceTags,
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return awsSitesDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return runPulumi(ctx, stack, s.Options)
}

func awsSitesDeploy(ctx *pulumi.Context, _ types.Target, params awsSiteParams) error {
	// Look up the RDS master user secret ARN via Pulumi data source.
	mainDBSecretARN := ""
	if params.mainDBInstanceID != "" {
		awsProvider, err := pulumiaws.NewProvider(ctx, "aws", &pulumiaws.ProviderArgs{
			Region: pulumi.String(params.region),
		})
		if err != nil {
			return err
		}

		dbInstance, err := rds.LookupInstance(ctx, &rds.LookupInstanceArgs{
			DbInstanceIdentifier: &params.mainDBInstanceID,
		}, pulumi.Provider(awsProvider))
		if err != nil {
			return fmt.Errorf("sites: failed to look up DB instance %s: %w", params.mainDBInstanceID, err)
		}
		if len(dbInstance.MasterUserSecrets) > 0 {
			mainDBSecretARN = dbInstance.MasterUserSecrets[0].SecretArn
		}
	}

	for _, release := range helpers.SortedKeys(params.kubeconfigsByRelease) {
		kubeconfig := params.kubeconfigsByRelease[release]
		providerName := params.compoundName + "-" + release + "-k8s"

		provider, err := kubernetes.NewProvider(ctx, providerName, &kubernetes.ProviderArgs{
			Kubeconfig: pulumi.String(kubeconfig),
		})
		if err != nil {
			return err
		}

		clusterCfg := params.clusters[release].Spec

		for _, siteName := range helpers.SortedKeys(params.sites) {
			siteConfig := params.sites[siteName].Spec

			spec := buildAWSSiteSpec(params, mainDBSecretARN, release, siteName, siteConfig, clusterCfg)

			spec, err = applySiteOverrides(spec, params.compoundName, siteName)
			if err != nil {
				return err
			}

			labels := buildSiteLabels(params.resourceTags, siteName, params.compoundName)

			oldURN := fmt.Sprintf(
				"urn:pulumi:%s::ptd-aws-workload-sites::ptd:AWSWorkloadSites$ptd:TeamSite$kubernetes:yaml:ConfigFile$kubernetes:core.posit.team/v1beta1:Site::%s-%s-%s/%s",
				ctx.Stack(), params.compoundName, release, positTeamNamespace, siteName,
			)

			resourceName := release + "-" + siteName
			_, err = apiextensions.NewCustomResource(ctx, resourceName, &apiextensions.CustomResourceArgs{
				ApiVersion: pulumi.String("core.posit.team/v1beta1"),
				Kind:       pulumi.String("Site"),
				Metadata: &metav1.ObjectMetaArgs{
					Name:      pulumi.String(siteName),
					Namespace: pulumi.String(positTeamNamespace),
					Labels:    pulumi.StringMap(labels),
				},
				OtherFields: kubernetes.UntypedArgs{
					"spec": spec,
				},
			}, pulumi.Provider(provider), pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}}))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func buildAWSSiteSpec(
	params awsSiteParams,
	mainDBSecretARN string,
	release, siteName string,
	siteConfig types.SiteConfigSpec,
	clusterCfg types.AWSWorkloadClusterSpec,
) map[string]interface{} {
	siteSecretName := params.compoundName + "-" + siteName + ".posit.team"
	workloadSecretName := params.compoundName + ".posit.team"

	spec := map[string]interface{}{
		"awsAccountId": params.accountID,
		"chronicle": map[string]interface{}{
			"s3Bucket": params.chronicleBucket,
		},
		"clusterDate": release,
		"domain":      siteConfig.Domain,
		"mainDatabaseCredentialSecret": map[string]interface{}{
			"type":      "aws",
			"vaultName": mainDBSecretARN,
		},
		"networkTrust": params.networkTrust,
		"packageManager": map[string]interface{}{
			"s3Bucket": params.packageManagerBucket,
		},
		"secret": map[string]interface{}{
			"type":      "aws",
			"vaultName": siteSecretName,
		},
		"secretType": "aws",
		"volumeSource": map[string]interface{}{
			"dnsName": params.fsDnsName,
			"type":    "nfs",
		},
		"workloadCompoundName": params.compoundName,
		"workloadSecret": map[string]interface{}{
			"type":      "aws",
			"vaultName": workloadSecretName,
		},
	}

	// EFS configuration.
	if clusterCfg.EnableEfsCsiDriver || clusterCfg.EfsConfig != nil {
		spec["efsEnabled"] = true
		if params.vpcCidr != "" {
			spec["vpcCIDR"] = params.vpcCidr
		}
	}

	// Karpenter session tolerations.
	if tolerations := sessionTolerations(clusterCfg.KarpenterConfig); len(tolerations) > 0 {
		spec["workbench"] = map[string]interface{}{
			"sessionTolerations": tolerations,
		}
	}

	return spec
}

// sessionTolerations returns a slice of session toleration objects for the first
// node pool that has session_taints=true. Multiple pools with different taint keys
// are not supported — this matches the existing Python behavior.
func sessionTolerations(kc *types.KarpenterConfig) []map[string]interface{} {
	if kc == nil {
		return nil
	}
	for _, pool := range kc.NodePools {
		if pool.SessionTaints {
			return []map[string]interface{}{
				{
					"key":      "workload-type",
					"operator": "Equal",
					"value":    "session",
					"effect":   "NoSchedule",
				},
			}
		}
	}
	return nil
}

// --- Azure ---

type azureSiteParams struct {
	compoundName         string
	networkTrust         int
	ppmFileShareSizeGib  int
	kubeconfigsByRelease map[string]string
	clusters             map[string]types.AzureWorkloadClusterConfig
	sites                map[string]types.SiteConfig
	resourceTags         map[string]string
}

func (s *SitesStep) runAzureInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("sites: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AzureWorkloadConfig)
	if !ok {
		return fmt.Errorf("sites: expected AzureWorkloadConfig")
	}

	azTarget, ok := s.DstTarget.(azure.Target)
	if !ok {
		return fmt.Errorf("sites: expected Azure target")
	}

	azCreds, err := azure.OnlyAzureCredentials(creds)
	if err != nil {
		return err
	}

	// Build kubeconfig per release. Azure always uses proxy.
	kubeconfigsByRelease := make(map[string]string, len(cfg.Clusters))
	for release := range cfg.Clusters {
		clusterName := s.DstTarget.Name() + "-" + release
		kubeconfigBytes, err := azure.GetKubeCredentials(
			ctx, azCreds, azTarget.SubscriptionID(), azTarget.ResourceGroupName(), clusterName,
		)
		if err != nil {
			return fmt.Errorf("sites: failed to get AKS kubeconfig for %s: %w", clusterName, err)
		}

		kubeconfigBytes, err = kube.AddProxyToKubeConfigBytes(kubeconfigBytes, "socks5://localhost:1080")
		if err != nil {
			return fmt.Errorf("sites: failed to add proxy to kubeconfig for %s: %w", clusterName, err)
		}
		kubeconfigsByRelease[release] = string(kubeconfigBytes)
	}

	ppmSize := cfg.PpmFileShareSizeGib
	if ppmSize == 0 {
		ppmSize = 100 // default from Python config
	}

	params := azureSiteParams{
		compoundName:         s.DstTarget.Name(),
		networkTrust:         types.NetworkTrustValue(cfg.NetworkTrust),
		ppmFileShareSizeGib:  ppmSize,
		kubeconfigsByRelease: kubeconfigsByRelease,
		clusters:             cfg.Clusters,
		sites:                cfg.Sites,
		resourceTags:         cfg.ResourceTags,
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return azureSitesDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return runPulumi(ctx, stack, s.Options)
}

func azureSitesDeploy(ctx *pulumi.Context, _ types.Target, params azureSiteParams) error {
	for _, release := range helpers.SortedKeys(params.kubeconfigsByRelease) {
		kubeconfig := params.kubeconfigsByRelease[release]
		providerName := params.compoundName + "-" + release + "-k8s"

		provider, err := kubernetes.NewProvider(ctx, providerName, &kubernetes.ProviderArgs{
			Kubeconfig: pulumi.String(kubeconfig),
		})
		if err != nil {
			return err
		}

		for _, siteName := range helpers.SortedKeys(params.sites) {
			siteConfig := params.sites[siteName].Spec

			spec := buildAzureSiteSpec(params, release, siteName, siteConfig)

			spec, err = applySiteOverrides(spec, params.compoundName, siteName)
			if err != nil {
				return err
			}

			labels := buildSiteLabels(params.resourceTags, siteName, params.compoundName)

			oldURN := fmt.Sprintf(
				"urn:pulumi:%s::ptd-azure-workload-sites::ptd:AzureWorkloadSites$ptd:TeamSite$kubernetes:yaml:ConfigFile$kubernetes:core.posit.team/v1beta1:Site::%s-%s-%s/%s",
				ctx.Stack(), params.compoundName, release, positTeamNamespace, siteName,
			)

			resourceName := release + "-" + siteName
			_, err = apiextensions.NewCustomResource(ctx, resourceName, &apiextensions.CustomResourceArgs{
				ApiVersion: pulumi.String("core.posit.team/v1beta1"),
				Kind:       pulumi.String("Site"),
				Metadata: &metav1.ObjectMetaArgs{
					Name:      pulumi.String(siteName),
					Namespace: pulumi.String(positTeamNamespace),
					Labels:    pulumi.StringMap(labels),
				},
				OtherFields: kubernetes.UntypedArgs{
					"spec": spec,
				},
			}, pulumi.Provider(provider), pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}}))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func buildAzureSiteSpec(
	params azureSiteParams,
	release, siteName string,
	siteConfig types.SiteConfigSpec,
) map[string]interface{} {
	siteSecretName := params.compoundName + "-" + siteName + ".posit.team"
	workloadSecretName := params.compoundName + ".posit.team"

	return map[string]interface{}{
		"clusterDate":  release,
		"domain":       siteConfig.Domain,
		"networkTrust": params.networkTrust,
		"packageManager": map[string]interface{}{
			"azureFiles": map[string]interface{}{
				"storageClassName": params.compoundName + "-azure-files-csi",
				"shareSizeGiB":     params.ppmFileShareSizeGib,
			},
		},
		"secret": map[string]interface{}{
			"type":      "kubernetes",
			"vaultName": siteSecretName,
		},
		"secretType": "kubernetes",
		"volumeSource": map[string]interface{}{
			"type": "azure-netapp",
		},
		"workloadCompoundName": params.compoundName,
		"workloadSecret": map[string]interface{}{
			"type":      "kubernetes",
			"vaultName": workloadSecretName,
		},
	}
}

// --- Shared helpers ---

func buildSiteLabels(resourceTags map[string]string, siteName, compoundName string) pulumi.StringMap {
	labels := pulumi.StringMap{
		"posit.team/managed-by":      pulumi.String(siteManagedByLabel),
		"posit.team/site-name":       pulumi.String(siteName),
		"app.kubernetes.io/instance": pulumi.String(siteName),
		// Retained from the old Python YAML template for consistency with existing resources.
		// The "managed-by: kustomize" value is incorrect (PTD manages these), but changing it
		// would cause unnecessary resource diffs during migration.
		"app.kubernetes.io/name":       pulumi.String("site"),
		"app.kubernetes.io/part-of":    pulumi.String("team-operator"),
		"app.kubernetes.io/managed-by": pulumi.String("kustomize"),
		"app.kubernetes.io/created-by": pulumi.String("team-operator"),
	}

	// Derive true-name and environment from the compound name (e.g. "acme01-staging").
	if idx := strings.LastIndex(compoundName, "-"); idx > 0 {
		labels["posit.team/true-name"] = pulumi.String(compoundName[:idx])
		labels["posit.team/environment"] = pulumi.String(compoundName[idx+1:])
	}

	// Include resource tags that are valid K8s label keys (no colon character).
	for k, v := range resourceTags {
		if !strings.Contains(k, ":") {
			labels[k] = pulumi.String(v)
		}
	}
	return labels
}

// applySiteOverrides reads site_<siteName>/site.yaml from the workload directory
// and deep-merges its "spec" on top of the base spec. If the file does not exist,
// the base spec is returned unchanged.
func applySiteOverrides(spec map[string]interface{}, workloadName, siteName string) (map[string]interface{}, error) {
	siteYamlPath := filepath.Join(
		helpers.GetTargetsConfigPath(),
		helpers.WorkDir,
		workloadName,
		fmt.Sprintf("site_%s", siteName),
		"site.yaml",
	)

	data, err := os.ReadFile(siteYamlPath)
	if errors.Is(err, fs.ErrNotExist) {
		return spec, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sites: failed to read site override %s: %w", siteYamlPath, err)
	}

	// yaml.v3 unmarshals nested mappings as map[string]interface{} natively,
	// so no type conversion is needed before passing to mergo.
	var overrides map[string]interface{}
	if err := yaml.Unmarshal(data, &overrides); err != nil {
		return nil, fmt.Errorf("sites: failed to parse site override %s: %w", siteYamlPath, err)
	}

	overrideSpec, ok := overrides["spec"].(map[string]interface{})
	if !ok {
		return spec, nil
	}

	if err := mergo.Merge(&spec, overrideSpec, mergo.WithOverride); err != nil {
		return nil, fmt.Errorf("sites: failed to merge site overrides for %s: %w", siteName, err)
	}

	return spec, nil
}
