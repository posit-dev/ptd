package steps

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/kube"
	ptdpulumi "github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/types"
	awscloudwatch "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	awseks "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/eks"
	awsiam "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	awssqs "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/sqs"
	kubernetes "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	apiextensions "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	helmv3 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	kustomize "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/kustomize"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	rbacv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/rbac/v1"
	k8syaml "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/yaml"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v3"
)

// --- AWS ---

// awsClustersParams bundles pre-fetched data for the AWS clusters deploy function.
type awsClustersParams struct {
	compoundName              string
	accountID                 string
	region                    string
	iamPermissionsBoundaryARN string
	teamOperatorPolicyName    string
	chronicleBucketName       string
	ppmBucketName             string
	oidcURLTails              []string
	// oidcIssuerURLsByCluster holds the OIDC issuer URL fetched from the live EKS cluster, keyed by cluster.
	// Used by Karpenter to determine if the controller IAM role should be created.
	oidcIssuerURLsByCluster map[string]string
	kubeconfigsByCluster    map[string]string
	clusters                map[string]types.AWSWorkloadClusterConfig
	sites                   map[string]types.SiteConfig
	resourceTags            map[string]string
	networkTrust            string
	keycloakEnabled         bool
	externalDNSEnabled      bool
	autoscalingEnabled      bool
	tailscaleEnabled        bool
	// grafanaDBAddress and grafanaDBPW are fetched from persistent + postgres_config stack outputs
	grafanaDBAddress string
	grafanaDBPW      string
}

func (s *ClustersStep) runAWSInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("clusters: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSWorkloadConfig)
	if !ok {
		return fmt.Errorf("clusters: expected AWSWorkloadConfig")
	}

	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}

	// Fetch workload secrets (chronicle-bucket, packagemanager-bucket, etc.)
	secretName := s.DstTarget.Name() + ".posit.team"
	secretJSON, err := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, secretName)
	if err != nil {
		return fmt.Errorf("clusters: failed to get workload secret %q: %w", secretName, err)
	}
	var secrets map[string]string
	if err := json.Unmarshal([]byte(secretJSON), &secrets); err != nil {
		return fmt.Errorf("clusters: failed to parse workload secret: %w", err)
	}

	// Build per-cluster kubeconfigs and collect OIDC URL tails
	var oidcURLTails []string
	kubeconfigsByCluster := make(map[string]string, len(cfg.Clusters))
	oidcIssuerURLsByCluster := make(map[string]string, len(cfg.Clusters))
	for release, clusterCfg := range cfg.Clusters {
		clusterName := s.DstTarget.Name() + "-" + release
		endpoint, caCert, liveOIDCIssuerURL, clusterErr := aws.GetClusterInfo(ctx, awsCreds, s.DstTarget.Region(), clusterName)
		if clusterErr != nil {
			return fmt.Errorf("clusters: failed to get cluster info for %s: %w", clusterName, clusterErr)
		}
		token, clusterErr := aws.GetEKSToken(ctx, awsCreds, s.DstTarget.Region(), clusterName)
		if clusterErr != nil {
			return fmt.Errorf("clusters: failed to get EKS token for %s: %w", clusterName, clusterErr)
		}
		config := kube.BuildEKSKubeConfig(endpoint, caCert, token, clusterName)
		if !cfg.TailscaleEnabled {
			config.Clusters[0].Cluster.ProxyURL = "socks5://localhost:1080"
		}
		data, marshalErr := yaml.Marshal(config)
		if marshalErr != nil {
			return fmt.Errorf("clusters: failed to marshal kubeconfig for %s: %w", clusterName, marshalErr)
		}
		kubeconfigsByCluster[release] = string(data)

		// Store the live OIDC issuer URL for this cluster (used by Karpenter controller role creation).
		// Prefer the live URL from the cluster over the one in the config.
		oidcURL := liveOIDCIssuerURL
		if oidcURL == "" {
			oidcURL = clusterCfg.Spec.ClusterOIDCIssuerURL
		}
		if oidcURL != "" {
			oidcIssuerURLsByCluster[release] = oidcURL
			// Collect OIDC URL tail for IRSA trust policies.
			tail := strings.TrimPrefix(oidcURL, "https://")
			tail = strings.TrimPrefix(tail, "http://")
			oidcURLTails = append(oidcURLTails, tail)
		}
	}

	// Extra OIDC URLs from top-level config
	for _, u := range cfg.ExtraClusterOidcUrls {
		tail := strings.TrimPrefix(u, "https://")
		tail = strings.TrimPrefix(tail, "http://")
		oidcURLTails = append(oidcURLTails, tail)
	}
	sort.Strings(oidcURLTails)

	// Fetch DB address from persistent stack
	grafanaDBAddress := ""
	persistentOutputs, err := getPersistentStackOutputs(ctx, s.DstTarget)
	if err != nil {
		return fmt.Errorf("clusters: failed to get persistent stack outputs: %w", err)
	}
	if dbAddr, ok := persistentOutputs["db_address"]; ok {
		grafanaDBAddress = fmt.Sprintf("%v", dbAddr.Value)
	}

	// Fetch grafana password from postgres_config stack
	grafanaDBPW := ""
	pgConfigOutputs, err := getPostgresConfigStackOutputs(ctx, s.DstTarget, envVars)
	if err != nil {
		return fmt.Errorf("clusters: failed to get postgres_config stack outputs: %w", err)
	}
	if pw, ok := pgConfigOutputs["db_grafana_pw"]; ok {
		grafanaDBPW = fmt.Sprintf("%v", pw.Value)
	}

	params := awsClustersParams{
		compoundName:              s.DstTarget.Name(),
		accountID:                 awsCreds.AccountID(),
		region:                    s.DstTarget.Region(),
		iamPermissionsBoundaryARN: fmt.Sprintf("arn:aws:iam::%s:policy/PositTeamDedicatedAdmin", awsCreds.AccountID()),
		teamOperatorPolicyName:    fmt.Sprintf("team-operator.%s.posit.team", s.DstTarget.Name()),
		chronicleBucketName:       secrets["chronicle-bucket"],
		ppmBucketName:             secrets["packagemanager-bucket"],
		oidcURLTails:              oidcURLTails,
		oidcIssuerURLsByCluster:   oidcIssuerURLsByCluster,
		kubeconfigsByCluster:      kubeconfigsByCluster,
		clusters:                  cfg.Clusters,
		sites:                     cfg.Sites,
		resourceTags:              cfg.ResourceTags,
		networkTrust:              cfg.NetworkTrust,
		keycloakEnabled:           cfg.KeycloakEnabled,
		externalDNSEnabled:        cfg.ExternalDNSEnabled == nil || *cfg.ExternalDNSEnabled,
		autoscalingEnabled:        cfg.AutoscalingEnabled,
		tailscaleEnabled:          cfg.TailscaleEnabled,
		grafanaDBAddress:          grafanaDBAddress,
		grafanaDBPW:               grafanaDBPW,
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return awsClustersDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return runPulumi(ctx, stack, s.Options)
}

// getPostgresConfigStackOutputs reads outputs from the postgres_config stack for the given target.
func getPostgresConfigStackOutputs(ctx context.Context, target types.Target, envVars map[string]string) (auto.OutputMap, error) {
	pgStack, err := ptdpulumi.NewPythonPulumiStack(
		ctx,
		string(target.CloudProvider()),
		string(target.Type()),
		"postgres_config",
		target.Name(),
		target.Region(),
		target.PulumiBackendUrl(),
		target.PulumiSecretsProviderKey(),
		envVars,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres_config stack handle: %w", err)
	}

	outputs, err := pgStack.Outputs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get postgres_config outputs: %w", err)
	}

	return outputs, nil
}

// awsClustersDeploy is the package-level AWS deploy function, callable from tests.
func awsClustersDeploy(ctx *pulumi.Context, _ types.Target, params awsClustersParams) error {
	name := params.compoundName

	// Python component type for alias resolution.
	// All resources were direct children of AWSWorkloadClusters in Python.
	outerComponentType := "ptd:AWSWorkloadClusters"

	// componentURN is the old Python AWSWorkloadClusters component URN.
	componentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s",
		ctx.Stack(), ctx.Project(), outerComponentType, name)

	// withAlias returns an alias pointing to the old Python component parent URN.
	// Resources that were direct children of AWSWorkloadClusters use this.
	withAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(componentURN)}})
	}

	// withSubComponentAlias returns an alias for resources that were children of a
	// nested Python ComponentResource (e.g., TeamOperator, ExternalDNS, etc.).
	withSubComponentAlias := func(subType, subName string) pulumi.ResourceOption {
		parentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$%s::%s",
			ctx.Stack(), ctx.Project(), outerComponentType, subType, subName)
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(parentURN)}})
	}

	// withRoleChildAlias returns an alias for resources that were children of an IAM role
	// that was itself a child of AWSWorkloadClusters. In state the role's URN type chain is
	// ptd:AWSWorkloadClusters$aws:iam/role:Role, so the parent URN we provide is:
	// urn:pulumi:{stack}::{project}::ptd:AWSWorkloadClusters$aws:iam/role:Role::{roleName}
	withRoleChildAlias := func(roleName string) pulumi.ResourceOption {
		roleURN := fmt.Sprintf("urn:pulumi:%s::%s::ptd:AWSWorkloadClusters$aws:iam/role:Role::%s",
			ctx.Stack(), ctx.Project(), roleName)
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(roleURN)}})
	}

	// withBucketChildAlias returns an alias for resources that were children of an S3 bucket
	// that was a top-level stack resource (type: aws:s3/bucket:Bucket::{bucketLogicalName}).
	// Python used aws.s3.Bucket.get() without a parent, producing a root-level bucket.
	withBucketChildAlias := func(bucketLogicalName string) pulumi.ResourceOption {
		bucketURN := fmt.Sprintf("urn:pulumi:%s::%s::aws:s3/bucket:Bucket::%s",
			ctx.Stack(), ctx.Project(), bucketLogicalName)
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(bucketURN)}})
	}

	releases := helpers.SortedKeys(params.clusters)
	sortedSites := helpers.SortedKeys(params.sites)
	networkTrustInt := types.NetworkTrustValue(params.networkTrust)

	// IAM policy documents
	readSecretsPolicyDoc := buildReadSecretsPolicy()
	bedrockPolicyDoc := buildBedrockPolicy()

	// Chronicle and PPM bucket ARNs (derived from names; managed by persistent step)
	chronicleBucketARN := fmt.Sprintf("arn:aws:s3:::%s", params.chronicleBucketName)
	ppmBucketARN := fmt.Sprintf("arn:aws:s3:::%s", params.ppmBucketName)

	for _, release := range releases {
		clusterCfg := params.clusters[release].Spec
		efsEnabled := clusterCfg.EnableEfsCsiDriver || clusterCfg.EfsConfig != nil

		// ── K8s provider ──────────────────────────────────────────────────────
		k8sProviderName := name + "-" + release + "-k8s"
		k8sProvider, err := kubernetes.NewProvider(ctx, k8sProviderName, &kubernetes.ProviderArgs{
			Kubeconfig: pulumi.String(params.kubeconfigsByCluster[release]),
		}, withAlias(), pulumi.IgnoreChanges([]string{"kubeconfig"}))
		if err != nil {
			return fmt.Errorf("clusters: failed to create K8s provider for %s: %w", release, err)
		}
		k8sProviderOpt := pulumi.Provider(k8sProvider)

		// ── Home IAM role ──────────────────────────────────────────────────────
		homeRoleName := fmt.Sprintf("home.%s.%s.posit.team", release, name)
		homeSAs := make([]string, 0, len(sortedSites))
		for _, siteName := range sortedSites {
			homeSAs = append(homeSAs, siteName+"-home")
		}
		if err := createAWSIAMRole(ctx, homeRoleName, homeRoleName, clustersPositTeamNamespace, homeSAs,
			[]inlinePolicy{{name: homeRoleName + "-role-policy-0", doc: readSecretsPolicyDoc}},
			"", release, params, withAlias()); err != nil {
			return err
		}

		// ── Chronicle IAM ─────────────────────────────────────────────────────
		// Python used aws.s3.Bucket.get("{name}-chronicle-bucket", ...) without a parent,
		// creating the bucket as a root-level stack resource. Policies were children of that bucket.
		chronicleBucketLogicalName := fmt.Sprintf("%s-chronicle-bucket", name)
		for _, siteName := range sortedSites {
			// Chronicle (read-write) policy
			// In Python this policy had parent=bucket (the root-level Bucket.get() resource).
			// Python did NOT pass required_tags to define_bucket_policy here (only Name tag).
			chroniclePolicyName := fmt.Sprintf("chronicle-s3-bucket.%s.%s.%s.posit.team", release, siteName, name)
			chroniclePolicy, err := awsiam.NewPolicy(ctx, chroniclePolicyName, &awsiam.PolicyArgs{
				Name:        pulumi.String(chroniclePolicyName),
				Description: pulumi.String(fmt.Sprintf("Posit Team Dedicated policy for %s to read/write the Chronicle S3 bucket at the %s/ and below paths", name, siteName)),
				Policy:      pulumi.String(buildS3ReadWritePolicy(chronicleBucketARN, siteName)),
				Tags:        pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-%s-s3-bucket-policy", name, chroniclePolicyName))},
			}, withBucketChildAlias(chronicleBucketLogicalName))
			if err != nil {
				return fmt.Errorf("clusters: failed to create chronicle policy for %s/%s: %w", release, siteName, err)
			}

			// Chronicle (read-write) role
			chronicleRoleName := fmt.Sprintf("chr.%s.%s.%s.posit.team", release, siteName, name)
			if err := createAWSIAMRole(ctx, chronicleRoleName, chronicleRoleName, clustersPositTeamNamespace,
				[]string{siteName + "-chronicle"},
				[]inlinePolicy{{name: chronicleRoleName + "-role-policy-0", doc: readSecretsPolicyDoc}},
				"", release, params, withAlias()); err != nil {
				return err
			}
			// In Python, attachment had parent=role (role was child of AWSWorkloadClusters component).
			if _, err := awsiam.NewRolePolicyAttachment(ctx, fmt.Sprintf("%s-%s-att", chroniclePolicyName, release), &awsiam.RolePolicyAttachmentArgs{
				Role:      pulumi.String(chronicleRoleName),
				PolicyArn: chroniclePolicy.Arn,
			}, withRoleChildAlias(chronicleRoleName), pulumi.DeleteBeforeReplace(true)); err != nil {
				return fmt.Errorf("clusters: failed to attach chronicle policy for %s/%s: %w", release, siteName, err)
			}

			// Chronicle read-only policy (also child of chronicle bucket in Python)
			// Python did NOT pass required_tags here either (only Name tag).
			chronicleROPolicyName := fmt.Sprintf("chronicle-s3-bucket-read-only.%s.%s.%s.posit.team", release, siteName, name)
			chronicleROPolicy, err := awsiam.NewPolicy(ctx, chronicleROPolicyName, &awsiam.PolicyArgs{
				Name:        pulumi.String(chronicleROPolicyName),
				Description: pulumi.String(fmt.Sprintf("Posit Team Dedicated policy for %s to read the Chronicle S3 bucket at the %s/ and below paths", name, siteName)),
				Policy:      pulumi.String(buildS3ReadPolicy(chronicleBucketARN, siteName)),
				Tags:        pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-%s-s3-bucket-read-only-policy", name, chronicleROPolicyName))},
			}, withBucketChildAlias(chronicleBucketLogicalName))
			if err != nil {
				return fmt.Errorf("clusters: failed to create chronicle RO policy for %s/%s: %w", release, siteName, err)
			}

			// Chronicle read-only role
			chronicleRORoleName := fmt.Sprintf("chr-ro.%s.%s.%s.posit.team", release, siteName, name)
			if err := createAWSIAMRole(ctx, chronicleRORoleName, chronicleRORoleName, clustersPositTeamNamespace,
				[]string{siteName + "-chronicle"},
				[]inlinePolicy{{name: chronicleRORoleName + "-role-policy-0", doc: readSecretsPolicyDoc}},
				"", release, params, withAlias()); err != nil {
				return err
			}
			// Attachment parent=role in Python.
			if _, err := awsiam.NewRolePolicyAttachment(ctx, fmt.Sprintf("%s-%s-att", chronicleROPolicyName, release), &awsiam.RolePolicyAttachmentArgs{
				Role:      pulumi.String(chronicleRORoleName),
				PolicyArn: chronicleROPolicy.Arn,
			}, withRoleChildAlias(chronicleRORoleName), pulumi.DeleteBeforeReplace(true)); err != nil {
				return fmt.Errorf("clusters: failed to attach chronicle RO policy for %s/%s: %w", release, siteName, err)
			}
		}

		// ── Connect IAM ───────────────────────────────────────────────────────
		connectRoleName := fmt.Sprintf("pub.%s.%s.posit.team", release, name)
		connectSAs := make([]string, 0, len(sortedSites))
		for _, siteName := range sortedSites {
			connectSAs = append(connectSAs, siteName+"-connect")
		}
		if err := createAWSIAMRole(ctx, connectRoleName, connectRoleName, clustersPositTeamNamespace, connectSAs,
			[]inlinePolicy{{name: connectRoleName + "-role-policy-0", doc: readSecretsPolicyDoc}},
			"", release, params, withAlias()); err != nil {
			return err
		}

		for _, siteName := range sortedSites {
			connectSessionRoleName := fmt.Sprintf("pub-ses.%s.%s.%s.posit.team", release, siteName, name)
			chronicleROPolicyName := fmt.Sprintf("chronicle-s3-bucket-read-only.%s.%s.%s.posit.team", release, siteName, name)
			chronicleROPolicyARN := fmt.Sprintf("arn:aws:iam::%s:policy/%s", params.accountID, chronicleROPolicyName)
			if err := createAWSIAMRole(ctx, connectSessionRoleName, connectSessionRoleName, clustersPositTeamNamespace,
				[]string{siteName + "-connect-session"},
				[]inlinePolicy{{name: connectSessionRoleName + "-role-policy-0", doc: bedrockPolicyDoc}},
				chronicleROPolicyARN, release, params, withAlias()); err != nil {
				return err
			}
		}

		// ── Workbench IAM ─────────────────────────────────────────────────────
		workbenchRoleName := fmt.Sprintf("dev.%s.%s.posit.team", release, name)
		workbenchSAs := make([]string, 0, len(sortedSites))
		for _, siteName := range sortedSites {
			workbenchSAs = append(workbenchSAs, siteName+"-workbench")
		}
		workbenchRolePolicies := []inlinePolicy{{name: workbenchRoleName + "-role-policy-0", doc: readSecretsPolicyDoc}}
		if efsEnabled && clusterCfg.EfsConfig != nil {
			workbenchRolePolicies = append(workbenchRolePolicies, inlinePolicy{
				name: workbenchRoleName + "-role-policy-1",
				doc:  buildEFSPolicy(clusterCfg.EfsConfig.FileSystemID, clusterCfg.EfsConfig.AccessPointID, params.accountID, params.region),
			})
		}
		if err := createAWSIAMRole(ctx, workbenchRoleName, workbenchRoleName, clustersPositTeamNamespace, workbenchSAs,
			workbenchRolePolicies, "", release, params, withAlias()); err != nil {
			return err
		}

		for _, siteName := range sortedSites {
			workbenchSessionRoleName := fmt.Sprintf("dev-ses.%s.%s.%s.posit.team", release, siteName, name)
			chronicleROPolicyName := fmt.Sprintf("chronicle-s3-bucket-read-only.%s.%s.%s.posit.team", release, siteName, name)
			chronicleROPolicyARN := fmt.Sprintf("arn:aws:iam::%s:policy/%s", params.accountID, chronicleROPolicyName)
			wbSessionPolicies := []inlinePolicy{{name: workbenchSessionRoleName + "-role-policy-0", doc: bedrockPolicyDoc}}
			if efsEnabled && clusterCfg.EfsConfig != nil {
				wbSessionPolicies = append(wbSessionPolicies, inlinePolicy{
					name: workbenchSessionRoleName + "-role-policy-1",
					doc:  buildEFSPolicy(clusterCfg.EfsConfig.FileSystemID, clusterCfg.EfsConfig.AccessPointID, params.accountID, params.region),
				})
			}
			if err := createAWSIAMRole(ctx, workbenchSessionRoleName, workbenchSessionRoleName, clustersPositTeamNamespace,
				[]string{siteName + "-workbench-session"},
				wbSessionPolicies, chronicleROPolicyARN, release, params, withAlias()); err != nil {
				return err
			}
		}

		// ── PackageManager IAM ────────────────────────────────────────────────
		// Python used aws.s3.Bucket.get("{name}-ppm-bucket", ...) without a parent (root-level).
		// PPM policies were children of that root-level bucket.
		ppmBucketLogicalName := fmt.Sprintf("%s-ppm-bucket", name)
		for _, siteName := range sortedSites {
			ppmPolicyName := fmt.Sprintf("ppm-s3-bucket.%s.%s.%s.posit.team", release, siteName, name)
			// Policy parent was the root-level PPM S3 bucket in Python.
			// Python passed required_tags here (unlike chronicle), so posit.team/* + Name.
			ppmPolicyTags := buildAWSClustersResourceTags(params.compoundName, params.resourceTags)
			ppmPolicyTags["Name"] = pulumi.String(fmt.Sprintf("%s-ppm-%s-%s-s3-bucket-policy", name, siteName, release))
			ppmPolicy, err := awsiam.NewPolicy(ctx, ppmPolicyName, &awsiam.PolicyArgs{
				Name:        pulumi.String(ppmPolicyName),
				Description: pulumi.String(fmt.Sprintf("Posit Team Dedicated policy for %s to read the PPM S3 bucket at the %s/ and below paths", name, siteName)),
				Policy:      pulumi.String(buildS3ReadWritePolicy(ppmBucketARN, siteName)),
				Tags:        ppmPolicyTags,
			}, withBucketChildAlias(ppmBucketLogicalName))
			if err != nil {
				return fmt.Errorf("clusters: failed to create PPM policy for %s/%s: %w", release, siteName, err)
			}

			ppmRoleName := fmt.Sprintf("pkg.%s.%s.%s.posit.team", release, siteName, name)
			if err := createAWSIAMRole(ctx, ppmRoleName, ppmRoleName, clustersPositTeamNamespace,
				[]string{siteName + "-packagemanager"},
				[]inlinePolicy{{name: ppmRoleName + "-role-policy-0", doc: readSecretsPolicyDoc}},
				"", release, params, withAlias()); err != nil {
				return err
			}
			// Attachment parent was the PPM role (child of AWSWorkloadClusters) in Python.
			if _, err := awsiam.NewRolePolicyAttachment(ctx, fmt.Sprintf("%s-%s-att", ppmPolicyName, release), &awsiam.RolePolicyAttachmentArgs{
				Role:      pulumi.String(ppmRoleName),
				PolicyArn: ppmPolicy.Arn,
			}, withRoleChildAlias(ppmRoleName), pulumi.DeleteBeforeReplace(true)); err != nil {
				return fmt.Errorf("clusters: failed to attach PPM policy for %s/%s: %w", release, siteName, err)
			}
		}

		// ── Team Operator IAM ─────────────────────────────────────────────────
		teamOperatorRoleName := fmt.Sprintf("team-operator.%s.%s.posit.team", release, name)
		teamOperatorPolicyARN := fmt.Sprintf("arn:aws:iam::%s:policy/%s", params.accountID, params.teamOperatorPolicyName)
		teamOperatorRole, err := awsiam.NewRole(ctx, teamOperatorRoleName, &awsiam.RoleArgs{
			Name:                pulumi.String(teamOperatorRoleName),
			AssumeRolePolicy:    pulumi.String(buildIRSATrustPolicy(clustersPositTeamSystemNamespace, []string{clustersTeamOperatorServiceAccount}, params.accountID, params.oidcURLTails, params.region)),
			PermissionsBoundary: pulumi.String(params.iamPermissionsBoundaryARN),
			Tags:                buildAWSClustersResourceTags(params.compoundName, params.resourceTags),
		}, withAlias(), pulumi.DeleteBeforeReplace(true))
		if err != nil {
			return fmt.Errorf("clusters: failed to create team operator role for %s: %w", release, err)
		}
		// In Python: logical name = "{policy_name}-{release}-att" where policy_name = params.teamOperatorPolicyName.
		// Parent was the team operator role (child of AWSWorkloadClusters component).
		teamOpAttachName := fmt.Sprintf("%s-%s-att", params.teamOperatorPolicyName, release)
		if _, err := awsiam.NewRolePolicyAttachment(ctx, teamOpAttachName, &awsiam.RolePolicyAttachmentArgs{
			Role:      teamOperatorRole.Name,
			PolicyArn: pulumi.String(teamOperatorPolicyARN),
		}, withRoleChildAlias(teamOperatorRoleName), pulumi.DeleteBeforeReplace(true)); err != nil {
			return fmt.Errorf("clusters: failed to attach team operator policy for %s: %w", release, err)
		}

		// ── Keycloak IAM (optional) ────────────────────────────────────────────
		if params.keycloakEnabled {
			keycloakRoleName := fmt.Sprintf("keycloak.%s.%s.posit.team", release, name)
			keycloakSAs := make([]string, 0, len(sortedSites))
			for _, siteName := range sortedSites {
				keycloakSAs = append(keycloakSAs, siteName+"-keycloak")
			}
			if err := createAWSIAMRole(ctx, keycloakRoleName, keycloakRoleName, clustersPositTeamNamespace, keycloakSAs,
				[]inlinePolicy{{name: keycloakRoleName + "-role-policy-0", doc: readSecretsPolicyDoc}},
				"", release, params, withAlias()); err != nil {
				return err
			}
		}

		// ── Grafana namespace + secret ─────────────────────────────────────────
		grafanaNsLogical := fmt.Sprintf("%s-%s-grafana-ns", name, release)
		_, err = corev1.NewNamespace(ctx, grafanaNsLogical, &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("grafana"),
			},
		}, k8sProviderOpt, withAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create grafana namespace for %s: %w", release, err)
		}

		grafanaSecretLogical := fmt.Sprintf("%s-%s-grafana-db-url", name, release)
		grafanaDBURL := buildGrafanaDBURL(name, params.grafanaDBPW, params.grafanaDBAddress)
		_, err = corev1.NewSecret(ctx, grafanaSecretLogical, &corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String("grafana-db-url"),
				Namespace: pulumi.String("grafana"),
			},
			Data: pulumi.StringMap{
				"PTD_DATABASE_URL": pulumi.String(grafanaDBURL),
			},
		}, k8sProviderOpt, withAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create grafana secret for %s: %w", release, err)
		}

		// ── Team Operator Helm release ─────────────────────────────────────────
		teamOpSubName := fmt.Sprintf("%s-%s", name, release)
		// Python: TeamOperator is instantiated with compound_name-release as its name
		teamOpParentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:TeamOperator::%s",
			ctx.Stack(), ctx.Project(), outerComponentType, teamOpSubName)
		withTeamOpAlias := func() pulumi.ResourceOption {
			return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(teamOpParentURN)}})
		}

		// posit-team namespace (created inside TeamOperator in Python)
		_, err = corev1.NewNamespace(ctx, fmt.Sprintf("%s-%s-%s", name, release, clustersPositTeamNamespace), &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(clustersPositTeamNamespace),
			},
		}, k8sProviderOpt, withTeamOpAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create posit-team namespace for %s: %w", release, err)
		}

		// Build team-operator tolerations from config (matches Python team_operator_tolerations).
		teamOpTolerations := pulumi.Array{}
		for _, t := range clusterCfg.TeamOperatorTolerations {
			tMap := pulumi.Map{
				"key":      pulumi.String(t.Key),
				"operator": pulumi.String(t.Operator),
				"effect":   pulumi.String(t.Effect),
			}
			if t.Value != "" {
				tMap["value"] = pulumi.String(t.Value)
			}
			teamOpTolerations = append(teamOpTolerations, tMap)
		}

		// Team operator Helm release
		_, err = helmv3.NewRelease(ctx, fmt.Sprintf("%s-%s-team-operator", name, release), &helmv3.ReleaseArgs{
			Name:            pulumi.String("team-operator"),
			Chart:           pulumi.String("oci://ghcr.io/posit-dev/charts/team-operator"),
			Version:         pulumi.String(clustersDefaultTeamOperatorChartVersion),
			Namespace:       pulumi.String(clustersPositTeamSystemNamespace),
			CreateNamespace: pulumi.Bool(true),
			Values: pulumi.Map{
				"controllerManager": pulumi.Map{
					"replicas": pulumi.Int(1),
					"container": pulumi.Map{
						"env": pulumi.Map{
							"WATCH_NAMESPACES": pulumi.String(clustersPositTeamNamespace),
							"AWS_REGION":       pulumi.String(params.region),
						},
					},
					"serviceAccount": pulumi.Map{
						"annotations": pulumi.Map{
							"eks.amazonaws.com/role-arn": teamOperatorRole.Arn,
						},
					},
					"tolerations": teamOpTolerations,
				},
				"crd": pulumi.Map{
					"enable": pulumi.Bool(true),
					"keep":   pulumi.Bool(true),
				},
			},
		}, k8sProviderOpt, withTeamOpAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create team operator helm release for %s: %w", release, err)
		}

		// ── HelmController ─────────────────────────────────────────────────────
		helmCtrlSubName := fmt.Sprintf("%s-%s-helm-controller", name, release)
		helmCtrlParentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:HelmController::%s",
			ctx.Stack(), ctx.Project(), outerComponentType, helmCtrlSubName)
		withHelmCtrlAlias := func() pulumi.ResourceOption {
			return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(helmCtrlParentURN)}})
		}

		helmCtrlNs, err := corev1.NewNamespace(ctx, fmt.Sprintf("%s-%s-helm-controller-namespace", name, release), &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(clustersHelmControllerNamespace),
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helm-controller namespace for %s: %w", release, err)
		}

		// HelmController CRDs — use untyped resource to include full openAPIV3Schema,
		// which the Kubernetes API server requires (typed structs omitted the schema).
		_, err = apiextensions.NewCustomResource(ctx, fmt.Sprintf("%s-%s-helmcharts-crd", name, release), &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("apiextensions.k8s.io/v1"),
			Kind:       pulumi.String("CustomResourceDefinition"),
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("helmcharts.helm.cattle.io"),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"group":                 "helm.cattle.io",
					"preserveUnknownFields": false,
					"scope":                 "Namespaced",
					"names": map[string]interface{}{
						"kind":     "HelmChart",
						"plural":   "helmcharts",
						"singular": "helmchart",
					},
					"versions": []interface{}{
						map[string]interface{}{
							"name":    "v1",
							"served":  true,
							"storage": true,
							"subresources": map[string]interface{}{
								"status": map[string]interface{}{},
							},
							"additionalPrinterColumns": []interface{}{
								map[string]interface{}{"jsonPath": ".status.jobName", "name": "Job", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.chart", "name": "Chart", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.targetNamespace", "name": "TargetNamespace", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.version", "name": "Version", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.repo", "name": "Repo", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.helmVersion", "name": "HelmVersion", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.bootstrap", "name": "Bootstrap", "type": "string"},
							},
							"schema": map[string]interface{}{
								"openAPIV3Schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"spec": map[string]interface{}{
											"type": "object",
											"properties": map[string]interface{}{
												"authPassCredentials":   map[string]interface{}{"type": "boolean"},
												"authSecret":            map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
												"backOffLimit":          map[string]interface{}{"nullable": true, "type": "integer"},
												"bootstrap":             map[string]interface{}{"type": "boolean"},
												"chart":                 map[string]interface{}{"nullable": true, "type": "string"},
												"chartContent":          map[string]interface{}{"nullable": true, "type": "string"},
												"createNamespace":       map[string]interface{}{"type": "boolean"},
												"dockerRegistrySecret":  map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
												"failurePolicy":         map[string]interface{}{"nullable": true, "type": "string"},
												"helmVersion":           map[string]interface{}{"nullable": true, "type": "string"},
												"insecureSkipTLSVerify": map[string]interface{}{"type": "boolean"},
												"jobImage":              map[string]interface{}{"nullable": true, "type": "string"},
												"plainHTTP":             map[string]interface{}{"type": "boolean"},
												"podSecurityContext": map[string]interface{}{
													"nullable": true,
													"type":     "object",
													"properties": map[string]interface{}{
														"fsGroup":             map[string]interface{}{"nullable": true, "type": "integer"},
														"fsGroupChangePolicy": map[string]interface{}{"nullable": true, "type": "string"},
														"runAsGroup":          map[string]interface{}{"nullable": true, "type": "integer"},
														"runAsNonRoot":        map[string]interface{}{"nullable": true, "type": "boolean"},
														"runAsUser":           map[string]interface{}{"nullable": true, "type": "integer"},
														"seLinuxOptions": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"level": map[string]interface{}{"nullable": true, "type": "string"},
																"role":  map[string]interface{}{"nullable": true, "type": "string"},
																"type":  map[string]interface{}{"nullable": true, "type": "string"},
																"user":  map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
														"seccompProfile": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"localhostProfile": map[string]interface{}{"nullable": true, "type": "string"},
																"type":             map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
														"supplementalGroups": map[string]interface{}{
															"nullable": true, "type": "array",
															"items": map[string]interface{}{"type": "integer"},
														},
														"sysctls": map[string]interface{}{
															"nullable": true, "type": "array",
															"items": map[string]interface{}{
																"type": "object",
																"properties": map[string]interface{}{
																	"name":  map[string]interface{}{"nullable": true, "type": "string"},
																	"value": map[string]interface{}{"nullable": true, "type": "string"},
																},
															},
														},
														"windowsOptions": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"gmsaCredentialSpec":     map[string]interface{}{"nullable": true, "type": "string"},
																"gmsaCredentialSpecName": map[string]interface{}{"nullable": true, "type": "string"},
																"hostProcess":            map[string]interface{}{"nullable": true, "type": "boolean"},
																"runAsUserName":          map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
													},
												},
												"repo":            map[string]interface{}{"nullable": true, "type": "string"},
												"repoCA":          map[string]interface{}{"nullable": true, "type": "string"},
												"repoCAConfigMap": map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
												"securityContext": map[string]interface{}{
													"nullable": true,
													"type":     "object",
													"properties": map[string]interface{}{
														"allowPrivilegeEscalation": map[string]interface{}{"nullable": true, "type": "boolean"},
														"capabilities": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"add":  map[string]interface{}{"nullable": true, "type": "array", "items": map[string]interface{}{"nullable": true, "type": "string"}},
																"drop": map[string]interface{}{"nullable": true, "type": "array", "items": map[string]interface{}{"nullable": true, "type": "string"}},
															},
														},
														"privileged":             map[string]interface{}{"nullable": true, "type": "boolean"},
														"procMount":              map[string]interface{}{"nullable": true, "type": "string"},
														"readOnlyRootFilesystem": map[string]interface{}{"nullable": true, "type": "boolean"},
														"runAsGroup":             map[string]interface{}{"nullable": true, "type": "integer"},
														"runAsNonRoot":           map[string]interface{}{"nullable": true, "type": "boolean"},
														"runAsUser":              map[string]interface{}{"nullable": true, "type": "integer"},
														"seLinuxOptions": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"level": map[string]interface{}{"nullable": true, "type": "string"},
																"role":  map[string]interface{}{"nullable": true, "type": "string"},
																"type":  map[string]interface{}{"nullable": true, "type": "string"},
																"user":  map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
														"seccompProfile": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"localhostProfile": map[string]interface{}{"nullable": true, "type": "string"},
																"type":             map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
														"windowsOptions": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"gmsaCredentialSpec":     map[string]interface{}{"nullable": true, "type": "string"},
																"gmsaCredentialSpecName": map[string]interface{}{"nullable": true, "type": "string"},
																"hostProcess":            map[string]interface{}{"nullable": true, "type": "boolean"},
																"runAsUserName":          map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
													},
												},
												"set":             map[string]interface{}{"nullable": true, "type": "object", "additionalProperties": map[string]interface{}{"x-kubernetes-int-or-string": true}},
												"targetNamespace": map[string]interface{}{"nullable": true, "type": "string"},
												"timeout":         map[string]interface{}{"nullable": true, "type": "string"},
												"valuesContent":   map[string]interface{}{"nullable": true, "type": "string"},
												"version":         map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
										"status": map[string]interface{}{
											"type": "object",
											"properties": map[string]interface{}{
												"conditions": map[string]interface{}{
													"nullable": true, "type": "array",
													"items": map[string]interface{}{
														"type": "object",
														"properties": map[string]interface{}{
															"message": map[string]interface{}{"nullable": true, "type": "string"},
															"reason":  map[string]interface{}{"nullable": true, "type": "string"},
															"status":  map[string]interface{}{"nullable": true, "type": "string"},
															"type":    map[string]interface{}{"nullable": true, "type": "string"},
														},
													},
												},
												"jobName": map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helmcharts CRD for %s: %w", release, err)
		}

		_, err = apiextensions.NewCustomResource(ctx, fmt.Sprintf("%s-%s-helmchartconfigs", name, release), &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("apiextensions.k8s.io/v1"),
			Kind:       pulumi.String("CustomResourceDefinition"),
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("helmchartconfigs.helm.cattle.io"),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"group":                 "helm.cattle.io",
					"preserveUnknownFields": false,
					"scope":                 "Namespaced",
					"names": map[string]interface{}{
						"kind":     "HelmChartConfig",
						"plural":   "helmchartconfigs",
						"singular": "helmchartconfig",
					},
					"versions": []interface{}{
						map[string]interface{}{
							"name":    "v1",
							"served":  true,
							"storage": true,
							"schema": map[string]interface{}{
								"openAPIV3Schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"spec": map[string]interface{}{
											"type": "object",
											"properties": map[string]interface{}{
												"failurePolicy": map[string]interface{}{"nullable": true, "type": "string"},
												"valuesContent": map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helmchartconfigs CRD for %s: %w", release, err)
		}

		// HelmController ClusterRole + ClusterRoleBinding
		_, err = rbacv1.NewClusterRole(ctx, fmt.Sprintf("%s-%s-helm-controller-cluster-role", name, release), &rbacv1.ClusterRoleArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("helm-controller"),
			},
			Rules: rbacv1.PolicyRuleArray{
				&rbacv1.PolicyRuleArgs{
					ApiGroups: pulumi.StringArray{pulumi.String("*")},
					Resources: pulumi.StringArray{pulumi.String("*")},
					Verbs:     pulumi.StringArray{pulumi.String("*")},
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helm-controller ClusterRole for %s: %w", release, err)
		}

		_, err = rbacv1.NewClusterRoleBinding(ctx, fmt.Sprintf("%s-%s-helm-controller-cluster-role-binding", name, release), &rbacv1.ClusterRoleBindingArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("helm-controller"),
			},
			RoleRef: &rbacv1.RoleRefArgs{
				ApiGroup: pulumi.String("rbac.authorization.k8s.io"),
				Kind:     pulumi.String("ClusterRole"),
				Name:     pulumi.String("helm-controller"),
			},
			Subjects: rbacv1.SubjectArray{
				&rbacv1.SubjectArgs{
					Kind:      pulumi.String("ServiceAccount"),
					Name:      pulumi.String("default"),
					Namespace: pulumi.String(clustersHelmControllerNamespace),
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helm-controller ClusterRoleBinding for %s: %w", release, err)
		}

		// HelmController Deployment
		_, err = appsv1.NewDeployment(ctx, fmt.Sprintf("%s-%s-helm-controller-deployment", name, release), &appsv1.DeploymentArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Namespace: pulumi.String(clustersHelmControllerNamespace),
				Name:      pulumi.String("helm-controller"),
				Labels: pulumi.StringMap{
					"app": pulumi.String("helm-controller"),
				},
			},
			Spec: &appsv1.DeploymentSpecArgs{
				Replicas: pulumi.Int(1),
				Selector: &metav1.LabelSelectorArgs{
					MatchLabels: pulumi.StringMap{
						"app": pulumi.String("helm-controller"),
					},
				},
				Template: &corev1.PodTemplateSpecArgs{
					Metadata: &metav1.ObjectMetaArgs{
						Labels: pulumi.StringMap{
							"app": pulumi.String("helm-controller"),
						},
					},
					Spec: &corev1.PodSpecArgs{
						Containers: corev1.ContainerArray{
							&corev1.ContainerArgs{
								Name:    pulumi.String("helm-controller"),
								Image:   pulumi.String("ghcr.io/k3s-io/helm-controller:v0.16.10"),
								Command: pulumi.StringArray{pulumi.String("helm-controller")},
								Args: pulumi.StringArray{
									pulumi.String("--namespace"),
									pulumi.String(clustersHelmControllerNamespace),
									pulumi.String("--default-job-image"),
									pulumi.String("ghcr.io/k3s-io/klipper-helm:latest"),
								},
							},
						},
					},
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias(), pulumi.DependsOn([]pulumi.Resource{helmCtrlNs}))
		if err != nil {
			return fmt.Errorf("clusters: failed to create helm-controller Deployment for %s: %w", release, err)
		}

		// ── NetworkPolicies ─────────────────────────────────────────────────────
		networkPolSubName := fmt.Sprintf("%s-%s-network-policies", name, release)
		networkPolParentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:NetworkPolicies::%s",
			ctx.Stack(), ctx.Project(), outerComponentType, networkPolSubName)
		withNetPolAlias := func() pulumi.ResourceOption {
			return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(networkPolParentURN)}})
		}

		// The NetworkPolicies component uses kubernetes.yaml.ConfigGroup for Calico resources.
		// ConfigGroup creates a group of K8s resources from inline YAML.
		// Resources inside ConfigGroup follow deep URN nesting.
		// For simplicity, we create the Calico policies directly as custom resources.
		if err := createCalicoNetworkPolicies(ctx, name, release, networkTrustInt, k8sProviderOpt, withNetPolAlias); err != nil {
			return err
		}

		// ── ExternalDNS (optional) ──────────────────────────────────────────────
		if params.externalDNSEnabled {
			extDNSSubName := fmt.Sprintf("%s-%s-external-dns", name, release)
			extDNSParentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:ExternalDNS::%s",
				ctx.Stack(), ctx.Project(), outerComponentType, extDNSSubName)
			withExtDNSAlias := func() pulumi.ResourceOption {
				return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(extDNSParentURN)}})
			}

			extDNSRoleName := fmt.Sprintf("external-dns.%s.posit.team", name)
			extDNSRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", params.accountID, extDNSRoleName)
			eksClusterName := fmt.Sprintf("default_%s-%s-control-plane", name, release)

			domainFilters := make([]string, 0, len(sortedSites))
			for _, siteName := range sortedSites {
				if site, ok := params.sites[siteName]; ok {
					domainFilters = append(domainFilters, site.Spec.Domain)
				}
			}
			// Python sorts domain filters by domain name string (not site name).
			sort.Strings(domainFilters)
			domainFiltersI := make([]interface{}, len(domainFilters))
			for i, d := range domainFilters {
				domainFiltersI[i] = d
			}

			// External DNS version: use the per-cluster config value if set,
			// otherwise fall back to Python's default of "1.14.4".
			extDNSVersion := "1.14.4"
			if clusterCfg.Components != nil && clusterCfg.Components.ExternalDNSVersion != nil {
				extDNSVersion = *clusterCfg.Components.ExternalDNSVersion
			}
			// serviceAccount.name: Python uses Roles.EXTERNAL_DNS = "external-dns.posit.team"
			// env: Python always sets AWS_DEFAULT_REGION and AWS_REGION from workload region
			// extraArgs: --aws-zone-match-parent added for versions >= 1.14.0 (always in practice)
			_, err = helmv3.NewRelease(ctx, fmt.Sprintf("%s-%s-external-dns", name, release), &helmv3.ReleaseArgs{
				Name:      pulumi.String("external-dns"),
				Chart:     pulumi.String("external-dns"),
				Version:   pulumi.String(extDNSVersion),
				Namespace: pulumi.String("kube-system"),
				RepositoryOpts: &helmv3.RepositoryOptsArgs{
					Repo: pulumi.String("https://kubernetes-sigs.github.io/external-dns/"),
				},
				Atomic: pulumi.Bool(true),
				Values: pulumi.Map{
					"provider": pulumi.String("aws"),
					"serviceAccount": pulumi.Map{
						"create": pulumi.Bool(true),
						"name":   pulumi.String("external-dns.posit.team"),
						"annotations": pulumi.Map{
							"eks.amazonaws.com/role-arn": pulumi.String(extDNSRoleARN),
						},
					},
					"domainFilters": pulumi.ToArray(domainFiltersI),
					"env": pulumi.Array{
						pulumi.Map{"name": pulumi.String("AWS_DEFAULT_REGION"), "value": pulumi.String(params.region)},
						pulumi.Map{"name": pulumi.String("AWS_REGION"), "value": pulumi.String(params.region)},
					},
					"policy":     pulumi.String("sync"),
					"txtOwnerId": pulumi.String(eksClusterName),
					"txtPrefix":  pulumi.String("_d"),
					"extraArgs":  pulumi.ToArray([]interface{}{"--aws-zone-match-parent"}),
				},
			}, k8sProviderOpt, withExtDNSAlias())
			if err != nil {
				return fmt.Errorf("clusters: failed to create external-dns for %s: %w", release, err)
			}
		}

		// ── TraefikForwardAuth (optional, per-cluster version) ─────────────────
		// Only deployed when traefik_forward_auth_version is set in the cluster's components.
		if clusterCfg.Components != nil && clusterCfg.Components.TraefikForwardAuthVersion != nil {
			tfaSubName := fmt.Sprintf("%s-%s-traefik-forward-auth", name, release)
			tfaParentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:TraefikForwardAuthAWS::%s",
				ctx.Stack(), ctx.Project(), outerComponentType, tfaSubName)
			withTFAAlias := func() pulumi.ResourceOption {
				return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(tfaParentURN)}})
			}
			if err := createTraefikForwardAuth(
				ctx, name, release,
				params.accountID,
				params.sites, sortedSites,
				*clusterCfg.Components.TraefikForwardAuthVersion,
				k8sProviderOpt, withTFAAlias,
			); err != nil {
				return err
			}
		}

		// ── KeycloakOperator (optional) ─────────────────────────────────────────
		if params.keycloakEnabled {
			keycloakSubName := fmt.Sprintf("%s-%s", name, release)
			keycloakParentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:KeycloakOperator::%s",
				ctx.Stack(), ctx.Project(), outerComponentType, keycloakSubName)
			withKeycloakAlias := func() pulumi.ResourceOption {
				return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(keycloakParentURN)}})
			}
			if err := createKeycloakOperator(
				ctx, name, release, params.accountID,
				params.resourceTags,
				k8sProviderOpt, withKeycloakAlias,
			); err != nil {
				return err
			}
		}

		_ = withSubComponentAlias // used above via closure
		_ = networkTrustInt       // used in createCalicoNetworkPolicies
	}

	// ── Karpenter (optional, spans all clusters) ─────────────────────────────
	// AWSKarpenter is instantiated once across all clusters, not per-cluster.
	if params.autoscalingEnabled {
		if err := createKarpenter(ctx, name, releases, params.clusters, params, withAlias); err != nil {
			return err
		}
	}

	return nil
}

// inlinePolicy holds an IAM inline policy name and document.
type inlinePolicy struct {
	name string
	doc  string
}

// createAWSIAMRole creates an IAM role with IRSA trust, optional inline policies, and optional managed policy attachment.
// release is used to construct the Python-compatible attachment logical name "{roleName}-{release}-att".
// aliasOpt should point to the old Python parent URN (the AWSWorkloadClusters component).
func createAWSIAMRole(
	ctx *pulumi.Context,
	logicalName, roleName, namespace string,
	serviceAccounts []string,
	inlinePolicies []inlinePolicy,
	attachPolicyARN string,
	release string,
	params awsClustersParams,
	aliasOpt pulumi.ResourceOption,
) error {
	trustPolicy := buildIRSATrustPolicy(namespace, serviceAccounts, params.accountID, params.oidcURLTails, params.region)

	role, err := awsiam.NewRole(ctx, logicalName, &awsiam.RoleArgs{
		Name:                pulumi.String(roleName),
		AssumeRolePolicy:    pulumi.String(trustPolicy),
		PermissionsBoundary: pulumi.String(params.iamPermissionsBoundaryARN),
		Tags:                buildAWSClustersResourceTags(params.compoundName, params.resourceTags),
	}, aliasOpt, pulumi.DeleteBeforeReplace(true))
	if err != nil {
		return fmt.Errorf("clusters: failed to create IAM role %s: %w", roleName, err)
	}

	for idx, pol := range inlinePolicies {
		pName := pol.name
		if pName == "" {
			pName = fmt.Sprintf("%s-role-policy-%d", roleName, idx)
		}
		// Python RolePolicy had no explicit parent, so it was a root-level stack resource.
		// Use the same logical name as Python ("{roleName}-role-policy-{idx}") with no alias.
		if _, err := awsiam.NewRolePolicy(ctx, fmt.Sprintf("%s-role-policy-%d", logicalName, idx), &awsiam.RolePolicyArgs{
			Name:   pulumi.String(pName),
			Role:   role.ID(),
			Policy: pulumi.String(pol.doc),
		}); err != nil {
			return fmt.Errorf("clusters: failed to create inline policy for %s: %w", roleName, err)
		}
	}

	if attachPolicyARN != "" {
		// Python used logical name "{roleName}-{release}-att" with parent=role.
		// Role was a child of AWSWorkloadClusters, so attachment URN type chain is:
		// ptd:AWSWorkloadClusters$aws:iam/role:Role$aws:iam/rolePolicyAttachment.
		// We alias via withRoleChildAlias (which is not available here as a closure).
		// Instead we build the role parent URN inline.
		//
		// Note: aliasOpt here is withAlias() (component as parent). We override it to use
		// the role as parent (matching Python's parent=role).
		_ = aliasOpt // intentionally unused for the attachment — the role-parent alias is built below
		attachLogicalName := fmt.Sprintf("%s-%s-att", roleName, release)
		// Role's old URN has type chain ptd:AWSWorkloadClusters$aws:iam/role:Role.
		roleURN := fmt.Sprintf("urn:pulumi:%s::%s::ptd:AWSWorkloadClusters$aws:iam/role:Role::%s",
			ctx.Stack(), ctx.Project(), roleName)
		roleParentAlias := pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(roleURN)}})
		if _, err := awsiam.NewRolePolicyAttachment(ctx, attachLogicalName, &awsiam.RolePolicyAttachmentArgs{
			Role:      role.Name,
			PolicyArn: pulumi.String(attachPolicyARN),
		}, roleParentAlias, pulumi.DeleteBeforeReplace(true)); err != nil {
			return fmt.Errorf("clusters: failed to attach policy for %s: %w", roleName, err)
		}
	}

	return nil
}

// buildIRSATrustPolicy creates an IAM assume-role policy for IRSA (IAM Roles for Service Accounts).
// This matches Python's build_hybrid_irsa_role_assume_role_policy: one statement per (oidcTail, sa)
// pair, using StringEquals for both the :aud and :sub conditions.
func buildIRSATrustPolicy(namespace string, serviceAccounts []string, accountID string, oidcURLTails []string, _ string) string {
	if len(oidcURLTails) == 0 {
		// Fallback when no OIDC providers exist
		doc := map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{
				{
					"Action": "sts:AssumeRole",
					"Effect": "Allow",
					"Principal": map[string]interface{}{
						"AWS": fmt.Sprintf("arn:aws:iam::%s:root", accountID),
					},
				},
			},
		}
		b, _ := json.Marshal(doc)
		return string(b)
	}

	var statements []map[string]interface{}

	for _, oidcTail := range oidcURLTails {
		providerARN := fmt.Sprintf("arn:aws:iam::%s:oidc-provider/%s", accountID, oidcTail)
		subs := make([]string, len(serviceAccounts))
		for i, sa := range serviceAccounts {
			subs[i] = fmt.Sprintf("system:serviceaccount:%s:%s", namespace, sa)
		}
		statements = append(statements, map[string]interface{}{
			"Effect": "Allow",
			"Principal": map[string]interface{}{
				"Federated": providerARN,
			},
			"Action": "sts:AssumeRoleWithWebIdentity",
			"Condition": map[string]interface{}{
				"StringEquals": map[string]interface{}{
					fmt.Sprintf("%s:aud", oidcTail): "sts.amazonaws.com",
					fmt.Sprintf("%s:sub", oidcTail): subs,
				},
			},
		})
	}

	doc := map[string]interface{}{
		"Version":   "2012-10-17",
		"Statement": statements,
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func buildReadSecretsPolicy() string {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":   "Allow",
				"Action":   []string{"secretsmanager:Get*", "secretsmanager:Describe*", "secretsmanager:ListSecrets"},
				"Resource": "*",
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func buildBedrockPolicy() string {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{
					"bedrock:Get*", "bedrock:List*", "bedrock:Retrieve",
					"bedrock:RetrieveAndGenerate", "bedrock:ApplyGuardrail", "bedrock:Invoke*",
				},
				"Resource": "*",
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func buildEFSPolicy(fileSystemID, accessPointID, accountID, region string) string {
	fsARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", region, accountID, fileSystemID)
	stmt := map[string]interface{}{
		"Effect":   "Allow",
		"Action":   []string{"elasticfilesystem:ClientMount", "elasticfilesystem:ClientWrite"},
		"Resource": fsARN,
	}
	if accessPointID != "" {
		apARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:access-point/%s", region, accountID, accessPointID)
		stmt["Condition"] = map[string]interface{}{
			"StringEquals": map[string]interface{}{
				"elasticfilesystem:AccessPointArn": apARN,
			},
		}
	}
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			stmt,
			{
				"Effect":   "Allow",
				"Action":   []string{"elasticfilesystem:DescribeFileSystems", "elasticfilesystem:DescribeMountTargets"},
				"Resource": "*",
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func buildS3ReadWritePolicy(bucketARN, prefix string) string {
	resources := s3Resources(bucketARN, prefix)
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{
					"s3:AbortMultipartUpload", "s3:DeleteObject", "s3:GetBucketLocation",
					"s3:GetObject", "s3:GetObjectTagging", "s3:HeadObject",
					"s3:ListBucket", "s3:ListObjects", "s3:PutObject", "s3:PutObjectTagging",
				},
				"Resource": resources,
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func buildS3ReadPolicy(bucketARN, prefix string) string {
	resources := s3Resources(bucketARN, prefix)
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":   "Allow",
				"Action":   []string{"s3:ListBucket", "s3:ListObjects", "s3:GetObject", "s3:GetObjectTagging", "s3:HeadObject"},
				"Resource": resources,
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func s3Resources(bucketARN, prefix string) []string {
	if prefix == "" || prefix == "/" {
		return []string{bucketARN, bucketARN + "/*"}
	}
	p := strings.TrimPrefix(strings.TrimSuffix(prefix, "/"), "/")
	return []string{bucketARN, bucketARN + "/" + p, bucketARN + "/" + p + "/*"}
}

// buildGrafanaDBURL builds the base64-encoded Grafana database URL matching the Python format.
func buildGrafanaDBURL(compoundName, pw, dbAddress string) string {
	role := fmt.Sprintf("grafana-%s", compoundName)
	database := fmt.Sprintf("grafana-%s", compoundName)
	s := fmt.Sprintf("postgres://%s:%s@%s/%s", role, pw, dbAddress, database)
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// createCalicoNetworkPolicies creates Calico network policies using custom K8s resources.
// The Python NetworkPolicies component used kubernetes.yaml.ConfigGroup for each manifest.
// ConfigGroup registers as type "kubernetes:yaml:ConfigGroup" with children typed
// "kubernetes:projectcalico.org/v3:NetworkPolicy" etc., and the child logical name is
// "namespace/k8s-resource-name".
// We reproduce that by aliasing each resource with a parent URN pointing to the intermediate
// ConfigGroup logical name, matching the Python state.
func createCalicoNetworkPolicies(
	ctx *pulumi.Context,
	name, release string,
	networkTrust int,
	k8sProviderOpt pulumi.ResourceOption,
	withNetPolAlias func() pulumi.ResourceOption,
) error {
	// NetworkTrust constants match Python: FULL=100, SAMESITE=50, ZERO=0.
	const networkTrustFull = 100
	const networkTrustSamesite = 50
	const networkTrustZero = 0

	// Helper: alias pointing to the intermediate ConfigGroup parent in the Python state.
	// Python: kubernetes.yaml.ConfigGroup(f"{name}-{release}-{suffix}", ..., opts=ResourceOptions(parent=NetworkPolicies))
	// ConfigGroup URN type chain: ptd:AWSWorkloadClusters$ptd:NetworkPolicies$kubernetes:yaml:ConfigGroup
	// The logical name of the ConfigGroup is "{name}-{release}-{suffix}".
	configGroupAlias := func(suffix string) pulumi.ResourceOption {
		cgLogicalName := fmt.Sprintf("%s-%s-%s", name, release, suffix)
		cgParentURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::ptd:AWSWorkloadClusters$ptd:NetworkPolicies$kubernetes:yaml:ConfigGroup::%s",
			ctx.Stack(), ctx.Project(), cgLogicalName,
		)
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(cgParentURN)}})
	}

	// Python networkTrust > SAMESITE (i.e. FULL=100) → allow-external
	if networkTrust > networkTrustSamesite {
		_, err := apiextensions.NewCustomResource(ctx,
			fmt.Sprintf("%s/%s", clustersPositTeamNamespace, fmt.Sprintf("allow-external-%s", release)),
			&apiextensions.CustomResourceArgs{
				ApiVersion: pulumi.String("projectcalico.org/v3"),
				Kind:       pulumi.String("NetworkPolicy"),
				Metadata: &metav1.ObjectMetaArgs{
					Name:      pulumi.String(fmt.Sprintf("allow-external-%s", release)),
					Namespace: pulumi.String(clustersPositTeamNamespace),
				},
				OtherFields: kubernetes.UntypedArgs{
					"spec": map[string]interface{}{
						"ingress": []interface{}{
							map[string]interface{}{
								"action":      "Allow",
								"destination": map[string]interface{}{"nets": []interface{}{"0.0.0.0/0"}},
							},
						},
						"egress": []interface{}{
							map[string]interface{}{
								"action":      "Allow",
								"destination": map[string]interface{}{"nets": []interface{}{"0.0.0.0/0"}},
							},
						},
					},
				},
			},
			k8sProviderOpt, configGroupAlias("calico-policy-allow-external"),
		)
		if err != nil {
			return fmt.Errorf("clusters: failed to create allow-external network policy for %s: %w", release, err)
		}
	}

	// Python networkTrust <= SAMESITE → default-deny
	if networkTrust <= networkTrustSamesite {
		_, err := apiextensions.NewCustomResource(ctx,
			fmt.Sprintf("%s/%s", clustersPositTeamNamespace, fmt.Sprintf("default-deny-%s", release)),
			&apiextensions.CustomResourceArgs{
				ApiVersion: pulumi.String("projectcalico.org/v3"),
				Kind:       pulumi.String("NetworkPolicy"),
				Metadata: &metav1.ObjectMetaArgs{
					Name:      pulumi.String(fmt.Sprintf("default-deny-%s", release)),
					Namespace: pulumi.String(clustersPositTeamNamespace),
				},
				OtherFields: kubernetes.UntypedArgs{
					"spec": map[string]interface{}{
						"selector": "all()",
						"types":    []interface{}{"Ingress", "Egress"},
					},
				},
			},
			k8sProviderOpt, configGroupAlias("calico-policy-default-deny"),
		)
		if err != nil {
			return fmt.Errorf("clusters: failed to create default-deny network policy for %s: %w", release, err)
		}
	}

	// Python networkTrust == ZERO → global-default-deny
	if networkTrust == networkTrustZero {
		_, err := apiextensions.NewCustomResource(ctx,
			fmt.Sprintf("default-deny-%s", release),
			&apiextensions.CustomResourceArgs{
				ApiVersion: pulumi.String("projectcalico.org/v3"),
				Kind:       pulumi.String("GlobalNetworkPolicy"),
				Metadata: &metav1.ObjectMetaArgs{
					Name: pulumi.String(fmt.Sprintf("default-deny-%s", release)),
				},
				OtherFields: kubernetes.UntypedArgs{
					"spec": map[string]interface{}{
						"selector": "projectcalico.org/namespace not in {'kube-system', 'calico-system', 'calico-apiserver'}",
						"types":    []interface{}{"Ingress", "Egress"},
					},
				},
			},
			k8sProviderOpt, configGroupAlias("calico-policy-global-default-deny"),
		)
		if err != nil {
			return fmt.Errorf("clusters: failed to create global-default-deny network policy for %s: %w", release, err)
		}
	}

	// Always: egress-allow-dns
	_, err := apiextensions.NewCustomResource(ctx,
		fmt.Sprintf("%s/%s", clustersPositTeamNamespace, fmt.Sprintf("egress-allow-dns-%s", release)),
		&apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("projectcalico.org/v3"),
			Kind:       pulumi.String("NetworkPolicy"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(fmt.Sprintf("egress-allow-dns-%s", release)),
				Namespace: pulumi.String(clustersPositTeamNamespace),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"order": 100,
					"egress": []interface{}{
						map[string]interface{}{
							"action":   "Allow",
							"protocol": "TCP",
							"destination": map[string]interface{}{
								"namespaceSelector": fmt.Sprintf("projectcalico.org/name == '%s'", clustersKubeSystemNamespace),
								"ports":             []interface{}{53},
							},
						},
						map[string]interface{}{
							"action":   "Allow",
							"protocol": "UDP",
							"destination": map[string]interface{}{
								"namespaceSelector": fmt.Sprintf("projectcalico.org/name == '%s'", clustersKubeSystemNamespace),
								"ports":             []interface{}{53},
							},
						},
					},
				},
			},
		},
		k8sProviderOpt, configGroupAlias("calico-policy-egress-allow-dns"),
	)
	if err != nil {
		return fmt.Errorf("clusters: failed to create egress-allow-dns network policy for %s: %w", release, err)
	}

	// Always: egress-explicit-deny
	_, err = apiextensions.NewCustomResource(ctx,
		fmt.Sprintf("%s/%s", clustersPositTeamNamespace, fmt.Sprintf("egress-explicit-deny-%s", release)),
		&apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("projectcalico.org/v3"),
			Kind:       pulumi.String("NetworkPolicy"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(fmt.Sprintf("egress-explicit-deny-%s", release)),
				Namespace: pulumi.String(clustersPositTeamNamespace),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"selector": "posit.team/component == 'workbench' || posit.team/component == 'workbench-session' || posit.team/component == 'connect-session'",
					"order":    160,
					"egress": []interface{}{
						map[string]interface{}{
							"action":      "Deny",
							"destination": map[string]interface{}{"selector": "posit.team/egress == 'deny'"},
						},
					},
				},
			},
		},
		k8sProviderOpt, configGroupAlias("calico-policy-egress-explicit-deny"),
	)
	if err != nil {
		return fmt.Errorf("clusters: failed to create egress-explicit-deny network policy for %s: %w", release, err)
	}

	// Always: ec2-imds NetworkSet
	_, err = apiextensions.NewCustomResource(ctx,
		fmt.Sprintf("%s/%s", clustersPositTeamNamespace, fmt.Sprintf("ec2-imds-%s", release)),
		&apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("projectcalico.org/v3"),
			Kind:       pulumi.String("NetworkSet"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(fmt.Sprintf("ec2-imds-%s", release)),
				Namespace: pulumi.String(clustersPositTeamNamespace),
				Labels: pulumi.StringMap{
					"posit.team/egress": pulumi.String("deny"),
				},
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"nets": []interface{}{"169.254.169.254/32"},
				},
			},
		},
		k8sProviderOpt, configGroupAlias("calico-network-set-ec2-imds"),
	)
	if err != nil {
		return fmt.Errorf("clusters: failed to create ec2-imds network set for %s: %w", release, err)
	}

	// Always: flightdeck-team-operator-allow
	_, err = apiextensions.NewCustomResource(ctx,
		fmt.Sprintf("%s/%s", clustersPositTeamNamespace, fmt.Sprintf("flightdeck-team-operator-policy-allow-%s", release)),
		&apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("projectcalico.org/v3"),
			Kind:       pulumi.String("NetworkPolicy"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(fmt.Sprintf("flightdeck-team-operator-policy-allow-%s", release)),
				Namespace: pulumi.String(clustersPositTeamNamespace),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"selector": "app.kubernetes.io/managed-by == 'team-operator' && app.kubernetes.io/name == 'flightdeck'",
					"types":    []interface{}{"Ingress", "Egress"},
					"ingress": []interface{}{
						map[string]interface{}{
							"action":   "Allow",
							"protocol": "TCP",
							"source": map[string]interface{}{
								"namespaceSelector": "projectcalico.org/name == 'traefik'",
							},
							"destination": map[string]interface{}{
								"ports": []interface{}{8080},
							},
						},
						map[string]interface{}{
							"action":   "Allow",
							"protocol": "TCP",
							"source": map[string]interface{}{
								"namespaceSelector": "projectcalico.org/name == 'alloy'",
							},
							"destination": map[string]interface{}{
								"ports": []interface{}{8080},
							},
						},
					},
					"egress": []interface{}{
						map[string]interface{}{
							"action":   "Allow",
							"protocol": "TCP",
							"destination": map[string]interface{}{
								"nets":  []interface{}{"10.0.0.0/8", "172.16.0.0/12"},
								"ports": []interface{}{443},
							},
						},
						map[string]interface{}{
							"action": "Allow",
							"destination": map[string]interface{}{
								"namespaceSelector": "projectcalico.org/name == 'kube-system'",
							},
						},
					},
				},
			},
		},
		k8sProviderOpt, configGroupAlias("calico-policy-flightdeck-team-operator-allow"),
	)
	if err != nil {
		return fmt.Errorf("clusters: failed to create flightdeck network policy for %s: %w", release, err)
	}

	return nil
}

// createTraefikForwardAuth creates the TraefikForwardAuth component resources for a single release.
// Matches the Python TraefikForwardAuthAWS class which extends TraefikForwardAuth.
// Component logical name in Python: "{compound_name}-{release}-traefik-forward-auth"
// Component type: ptd:TraefikForwardAuthAWS
//
// name is the compound workload name (e.g. "demo01-staging"), which equals "{trueName}-{environment}"
// in Python. The IAM role for the service account is traefik-forward-auth.{name}.posit.team.
func createTraefikForwardAuth(
	ctx *pulumi.Context,
	name, release string,
	accountID string,
	sites map[string]types.SiteConfig,
	sortedSites []string,
	chartVersion string,
	k8sProviderOpt pulumi.ResourceOption,
	withTFAAlias func() pulumi.ResourceOption,
) error {
	// Service account — Python: logical name "{compound_name}-{release}-traefik-forward-auth"
	saLogicalName := fmt.Sprintf("%s-%s-traefik-forward-auth", name, release)
	_, err := corev1.NewServiceAccount(ctx, saLogicalName, &corev1.ServiceAccountArgs{
		ApiVersion: pulumi.String("v1"),
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(clustersTraefikForwardAuthSA),
			Namespace: pulumi.String(clustersKubeSystemNamespace),
			Annotations: pulumi.StringMap{
				"eks.amazonaws.com/role-arn": pulumi.String(fmt.Sprintf(
					"arn:aws:iam::%s:role/traefik-forward-auth.%s.posit.team",
					accountID, name,
				)),
			},
		},
	}, k8sProviderOpt, withTFAAlias())
	if err != nil {
		return fmt.Errorf("clusters: failed to create traefik-forward-auth service account for %s: %w", release, err)
	}

	// Forward-headers middleware — Python: logical name "{compound_name}-{release}-traefik-forward-auth-headers-middleware"
	headersMiddlewareLogical := fmt.Sprintf("%s-%s-traefik-forward-auth-headers-middleware", name, release)
	_, err = apiextensions.NewCustomResource(ctx, headersMiddlewareLogical, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("traefik.io/v1alpha1"),
		Kind:       pulumi.String("Middleware"),
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("traefik-forward-auth-add-forwarded-headers"),
			Namespace: pulumi.String(clustersKubeSystemNamespace),
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": map[string]interface{}{
				"headers": map[string]interface{}{
					"customRequestHeaders": map[string]interface{}{
						"X-Forwarded-Proto": "https",
						"X-Forwarded-Port":  "443",
					},
				},
			},
		},
	}, k8sProviderOpt, withTFAAlias())
	if err != nil {
		return fmt.Errorf("clusters: failed to create traefik-forward-auth headers middleware for %s: %w", release, err)
	}

	// Per-site: auth middleware + Helm release
	for _, siteName := range sortedSites {
		site, ok := sites[siteName]
		if !ok || !site.Spec.UseTraefikForwardAuth {
			continue
		}
		domain := site.Spec.Domain

		// Auth middleware — Python: logical name "traefik-forward-auth-{release}-{site}"
		authMiddlewareLogical := fmt.Sprintf("traefik-forward-auth-%s-%s", release, siteName)
		_, err = apiextensions.NewCustomResource(ctx, authMiddlewareLogical, &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("traefik.io/v1alpha1"),
			Kind:       pulumi.String("Middleware"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(fmt.Sprintf("traefik-forward-auth-%s", siteName)),
				Namespace: pulumi.String(clustersKubeSystemNamespace),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"forwardAuth": map[string]interface{}{
						"address": fmt.Sprintf(
							"http://traefik-forward-auth-%s.%s.svc.cluster.local",
							siteName, clustersKubeSystemNamespace,
						),
						"trustForwardHeader":    true,
						"authResponseHeaders":   []interface{}{"X-Forwarded-User"},
						"preserveRequestMethod": true,
					},
				},
			},
		}, k8sProviderOpt, withTFAAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create traefik-forward-auth auth middleware for %s/%s: %w", release, siteName, err)
		}

		// Secret provider class for AWS secrets manager (passed as extraObjects to Helm)
		secretProviderClass := map[string]interface{}{
			"apiVersion": "secrets-store.csi.x-k8s.io/v1",
			"kind":       "SecretProviderClass",
			"metadata": map[string]interface{}{
				"name":      fmt.Sprintf("traefik-forward-auth-spc-%s", siteName),
				"namespace": clustersKubeSystemNamespace,
			},
			"spec": map[string]interface{}{
				"provider": "aws",
				"parameters": map[string]interface{}{
					"objects": mustMarshalJSON([]map[string]interface{}{
						{
							"jmesPath": []map[string]interface{}{
								{"objectAlias": "clientId", "path": "oidcClientId"},
								{"objectAlias": "clientSecret", "path": "oidcClientSecret"},
								{"objectAlias": "signingSecret", "path": "signingSecret"},
							},
							"objectName": fmt.Sprintf(
								"okta-oidc-client-creds.%s-%s.posit.team",
								name, siteName,
							),
							"objectType": "secretsmanager",
						},
					}),
				},
				"secretObjects": []map[string]interface{}{
					{
						"secretName": fmt.Sprintf("traefik-forward-auth-creds-%s", siteName),
						"type":       "Opaque",
						"data": []map[string]interface{}{
							{"key": "clientId", "objectName": "clientId"},
							{"key": "clientSecret", "objectName": "clientSecret"},
							{"key": "signingSecret", "objectName": "signingSecret"},
						},
					},
				},
			},
		}

		// Helm release — Python: logical name "{compound_name}-{release}-traefik-forward-auth-{site}"
		helmLogical := fmt.Sprintf("%s-%s-traefik-forward-auth-%s", name, release, siteName)
		_, err = helmv3.NewRelease(ctx, helmLogical, &helmv3.ReleaseArgs{
			Chart: pulumi.String(fmt.Sprintf(
				"https://github.com/colearendt/helm/releases/download/traefik-forward-auth-%s/traefik-forward-auth-%s.tgz",
				chartVersion, chartVersion,
			)),
			Namespace: pulumi.String(clustersKubeSystemNamespace),
			Name:      pulumi.String(fmt.Sprintf("traefik-forward-auth-%s", siteName)),
			Atomic:    pulumi.Bool(false),
			Values: pulumi.Map{
				"config": pulumi.ToMap(buildTraefikForwardAuthHelmConfig(domain)),
				"serviceAccount": pulumi.Map{
					"create": pulumi.Bool(false),
					"name":   pulumi.String(clustersTraefikForwardAuthSA),
				},
				"extraObjects": pulumi.ToArray([]interface{}{secretProviderClass}),
				"pod": pulumi.Map{
					"env": pulumi.ToArray([]interface{}{
						map[string]interface{}{
							"name": "PROVIDERS_OIDC_CLIENT_ID",
							"valueFrom": map[string]interface{}{
								"secretKeyRef": map[string]interface{}{
									"name": fmt.Sprintf("traefik-forward-auth-creds-%s", siteName),
									"key":  "clientId",
								},
							},
						},
						map[string]interface{}{
							"name": "PROVIDERS_OIDC_CLIENT_SECRET",
							"valueFrom": map[string]interface{}{
								"secretKeyRef": map[string]interface{}{
									"name": fmt.Sprintf("traefik-forward-auth-creds-%s", siteName),
									"key":  "clientSecret",
								},
							},
						},
						map[string]interface{}{
							"name": "SECRET",
							"valueFrom": map[string]interface{}{
								"secretKeyRef": map[string]interface{}{
									"name": fmt.Sprintf("traefik-forward-auth-creds-%s", siteName),
									"key":  "signingSecret",
								},
							},
						},
					}),
					"volumes": pulumi.ToArray([]interface{}{
						map[string]interface{}{
							"name": "oidc-client-creds",
							"csi": map[string]interface{}{
								"driver":   "secrets-store.csi.k8s.io",
								"readOnly": true,
								"volumeAttributes": map[string]interface{}{
									"secretProviderClass": fmt.Sprintf("traefik-forward-auth-spc-%s", siteName),
								},
							},
						},
					}),
					"volumeMounts": pulumi.ToArray([]interface{}{
						map[string]interface{}{
							"name":      "oidc-client-creds",
							"mountPath": "/mnt/secrets/oidc-client-creds",
							"readOnly":  true,
						},
					}),
				},
				"ingress": pulumi.Map{
					"enabled":   pulumi.Bool(true),
					"className": pulumi.String("traefik"),
					"annotations": pulumi.StringMap{
						"traefik.ingress.kubernetes.io/router.middlewares": pulumi.String(fmt.Sprintf(
							"%s-traefik-forward-auth-add-forwarded-headers@kubernetescrd,%s-traefik-forward-auth-%s@kubernetescrd",
							clustersKubeSystemNamespace, clustersKubeSystemNamespace, siteName,
						)),
					},
					"hosts": pulumi.ToArray([]interface{}{
						map[string]interface{}{
							"host":  fmt.Sprintf("sso.%s", domain),
							"paths": []interface{}{"/"},
						},
					}),
				},
			},
		}, k8sProviderOpt, withTFAAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create traefik-forward-auth helm release for %s/%s: %w", release, siteName, err)
		}
	}

	return nil
}

// buildTraefikForwardAuthHelmConfig builds the helm config map for traefik-forward-auth.
// Matches the Python helm_config() function in traefik_forward_auth.py.
func buildTraefikForwardAuthHelmConfig(domain string) map[string]interface{} {
	// joinLines mimics Python's `" ".join([s.strip() for s in "...".splitlines()])`.
	// Python uses triple-quoted strings that start/end with newlines, so splitlines() gives
	// ["", "content", ""] which when joined with " " produces " content " (leading+trailing space).
	// Go rule strings are single-line, so we add the spaces explicitly to match Python state.
	joinLines := func(s string) string {
		lines := strings.Split(s, "\n")
		var parts []string
		for _, l := range lines {
			if t := strings.TrimSpace(l); t != "" {
				parts = append(parts, t)
			}
		}
		return " " + strings.Join(parts, " ") + " "
	}

	return map[string]interface{}{
		"auth-host":                  fmt.Sprintf("sso.%s", domain),
		"cookie-domain":              domain,
		"cookie-name":                "ptd_auth",
		"csrf-cookie-name":           "csrf_ptd_auth",
		"default-provider":           "oidc",
		"log-level":                  "debug",
		"providers.oidc.issuer-url":  "https://posit.okta.com",
		"url-path":                   "/__oauth__",
		"rule.ptd-flightdeck.action": "allow",
		"rule.ptd-flightdeck.rule": joinLines(fmt.Sprintf(
			`Host(`+"`"+`%s`+"`"+`) && ( HeadersRegexp(`+"`"+`Authorization`+"`"+`, `+"`"+`^B(asic|earer) .*`+"`"+`) || PathPrefix(`+"`"+`/static`+"`"+`) || PathPrefix(`+"`"+`/dl`+"`"+`) || PathPrefix(`+"`"+`/api`+"`"+`) )`,
			domain,
		)),
		"rule.ptd-ide.action": "allow",
		"rule.ptd-ide.rule": joinLines(fmt.Sprintf(
			`( Host(`+"`"+`dev.%s`+"`"+`) || Host(`+"`"+`dev-%s`+"`"+`) ) && HeadersRegexp(`+"`"+`Authorization`+"`"+`, `+"`"+`^Bearer .*`+"`"+`) && ( PathPrefix(`+"`"+`/api`+"`"+`) || PathPrefix(`+"`"+`/scim/v2/`+"`"+`) )`,
			domain, domain,
		)),
		"rule.ptd-ide-client-heartbeat.action": "allow",
		"rule.ptd-ide-client-heartbeat.rule": joinLines(fmt.Sprintf(
			`( Host(`+"`"+`dev.%s`+"`"+`) || Host(`+"`"+`dev-%s`+"`"+`) ) && ( PathPrefix(`+"`"+`/heartbeat`+"`"+`) )`,
			domain, domain,
		)),
		"rule.ptd-pub-public.action": "allow",
		"rule.ptd-pub-public.rule": joinLines(fmt.Sprintf(
			`( Host(`+"`"+`pub.%s`+"`"+`) || Host(`+"`"+`pub-%s`+"`"+`) ) && ( PathPrefix(`+"`"+`/public`+"`"+`) || PathPrefix(`+"`"+`/connect/out/unauthorized/`+"`"+`) || Path(`+"`"+`/connect/__favicon__`+"`"+`) || Path(`+"`"+`/__api__/server_settings`+"`"+`) || Path(`+"`"+`/__api__/v1/user`+"`"+`) || Path(`+"`"+`/.well-known/openid-configuration`+"`"+`) || Path(`+"`"+`/openid/v1/jwks`+"`"+`) || Path(`+"`"+`/__api__/tokens`+"`"+`) )`,
			domain, domain,
		)),
		"rule.ptd-pub.action": "allow",
		"rule.ptd-pub.rule": joinLines(fmt.Sprintf(
			`( Host(`+"`"+`pub.%s`+"`"+`) || Host(`+"`"+`pub-%s`+"`"+`) ) && ( HeadersRegexp(`+"`"+`X-Auth-Token`+"`"+`, `+"`"+`.*`+"`"+`) || HeadersRegexp(`+"`"+`Authorization`+"`"+`, `+"`"+`^Key .*`+"`"+`) )`,
			domain, domain,
		)),
		"rule.ptd-pkg.action": "allow",
		"rule.ptd-pkg.rule": joinLines(fmt.Sprintf(
			`( Host(`+"`"+`pkg.%s`+"`"+`) || Host(`+"`"+`pkg-%s`+"`"+`) )`,
			domain, domain,
		)),
		"rule.ptd-dev-health.action": "allow",
		"rule.ptd-dev-health.rule": joinLines(fmt.Sprintf(
			`( Host(`+"`"+`dev.%s`+"`"+`) || Host(`+"`"+`dev-%s`+"`"+`) ) && ( Path(`+"`"+`/health-check`+"`"+`) && HeadersRegexp(`+"`"+`X-PTD-Health`+"`"+`, `+"`"+`.*`+"`"+`) )`,
			domain, domain,
		)),
	}
}

// mustMarshalJSON marshals v to a JSON string using Python json.dumps default separators
// (': ' and ', '), matching the Python-generated Pulumi state for consistent comparison.
func mustMarshalJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustMarshalJSON: %v", err))
	}
	return pythonJSONSeparators(b)
}

// pythonJSONSeparators expands compact JSON to use Python json.dumps default separators:
// space after ':' and ',' outside of string literals.
func pythonJSONSeparators(b []byte) string {
	var out strings.Builder
	inString := false
	escaped := false
	for _, ch := range b {
		if escaped {
			escaped = false
			out.WriteByte(ch)
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			out.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inString = !inString
		}
		out.WriteByte(ch)
		if !inString && (ch == ':' || ch == ',') {
			out.WriteByte(' ')
		}
	}
	return out.String()
}

// createKarpenter creates the AWSKarpenter component resources.
// Unlike most components, AWSKarpenter spans ALL clusters (not per-cluster).
// The component logical name is "{compound_name}-karpenter".
// IAM node/instance profile resources are direct children of ptd:AWSKarpenter.
// IAM controller roles are direct children of ptd:AWSWorkloadClusters (via withAlias).
func createKarpenter(
	ctx *pulumi.Context,
	name string,
	releases []string,
	clusters map[string]types.AWSWorkloadClusterConfig,
	params awsClustersParams,
	withAlias func() pulumi.ResourceOption,
) error {
	// AWSKarpenter parent URN: ptd:AWSWorkloadClusters$ptd:AWSKarpenter::{name}-karpenter
	karpenterSubName := fmt.Sprintf("%s-karpenter", name)
	karpenterParentURN := fmt.Sprintf(
		"urn:pulumi:%s::%s::ptd:AWSWorkloadClusters$ptd:AWSKarpenter::%s",
		ctx.Stack(), ctx.Project(), karpenterSubName,
	)

	// withKarpenterAlias aliases direct children of ptd:AWSKarpenter
	withKarpenterAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(karpenterParentURN)}})
	}

	for _, release := range releases {
		clusterName := fmt.Sprintf("%s-%s", name, release)
		nodeRoleName := fmt.Sprintf("KarpenterNodeRole-%s.posit.team", clusterName)
		nodeRoleLogicalName := fmt.Sprintf("%s-%s", nodeRoleName, release)

		// Node role assume policy for EC2
		nodeRolePolicy := mustMarshalJSON(map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{
				{
					"Effect": "Allow",
					"Action": "sts:AssumeRole",
					"Principal": map[string]interface{}{
						"Service": "ec2.amazonaws.com",
					},
				},
			},
		})

		nodeRole, err := awsiam.NewRole(ctx, nodeRoleLogicalName, &awsiam.RoleArgs{
			Name:                pulumi.String(nodeRoleName),
			AssumeRolePolicy:    pulumi.String(nodeRolePolicy),
			PermissionsBoundary: pulumi.String(params.iamPermissionsBoundaryARN),
			Tags:                buildAWSClustersResourceTags(params.compoundName, params.resourceTags),
		}, withKarpenterAlias(), pulumi.DeleteBeforeReplace(true))
		if err != nil {
			return fmt.Errorf("clusters: failed to create KarpenterNodeRole for %s: %w", release, err)
		}

		// Node role policy attachments
		nodePolicies := []string{
			"arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
			"arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
			"arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryPullOnly",
			"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
		}
		for idx, policyARN := range nodePolicies {
			attachLogical := fmt.Sprintf("%s-policy-%d", nodeRoleName, idx)
			// Parent in Python: parent=nodeRole (which is child of AWSKarpenter)
			// Type chain: ptd:AWSWorkloadClusters$ptd:AWSKarpenter$aws:iam/role:Role$aws:iam/rolePolicyAttachment
			nodeRoleURN := fmt.Sprintf(
				"urn:pulumi:%s::%s::ptd:AWSWorkloadClusters$ptd:AWSKarpenter$aws:iam/role:Role::%s",
				ctx.Stack(), ctx.Project(), nodeRoleLogicalName,
			)
			nodeRoleChildAlias := pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(nodeRoleURN)}})
			if _, err := awsiam.NewRolePolicyAttachment(ctx, attachLogical, &awsiam.RolePolicyAttachmentArgs{
				Role:      nodeRole.Name,
				PolicyArn: pulumi.String(policyARN),
			}, nodeRoleChildAlias, pulumi.DeleteBeforeReplace(true)); err != nil {
				return fmt.Errorf("clusters: failed to attach node policy %d for %s: %w", idx, release, err)
			}
		}

		// Instance profile — parent in Python: parent=nodeRole
		instanceProfileName := fmt.Sprintf("KarpenterNodeInstanceProfile-%s.posit.team", clusterName)
		instanceProfileLogical := fmt.Sprintf("%s-%s", instanceProfileName, release)
		nodeRoleURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::ptd:AWSWorkloadClusters$ptd:AWSKarpenter$aws:iam/role:Role::%s",
			ctx.Stack(), ctx.Project(), nodeRoleLogicalName,
		)
		nodeRoleChildAlias := pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(nodeRoleURN)}})
		// Python adds karpenter-specific tags to the instance profile in addition to required_tags.
		instanceProfileTags := buildAWSClustersResourceTags(params.compoundName, params.resourceTags)
		instanceProfileTags[fmt.Sprintf("kubernetes.io/cluster/%s", clusterName)] = pulumi.String("owned")
		instanceProfileTags["topology.kubernetes.io/region"] = pulumi.String(params.region)
		instanceProfileTags["karpenter.k8s.aws/ec2nodeclass"] = pulumi.String(clusterName)
		if _, err := awsiam.NewInstanceProfile(ctx, instanceProfileLogical, &awsiam.InstanceProfileArgs{
			Name: pulumi.String(instanceProfileName),
			Role: nodeRole.Name,
			Tags: instanceProfileTags,
		}, nodeRoleChildAlias, pulumi.DeleteBeforeReplace(true)); err != nil {
			return fmt.Errorf("clusters: failed to create KarpenterNodeInstanceProfile for %s: %w", release, err)
		}

		// SQS queue — direct child of ptd:AWSKarpenter
		queueName := clusterName
		queueLogical := fmt.Sprintf("%s-interruption-queue", queueName)
		queue, err := awssqs.NewQueue(ctx, queueLogical, &awssqs.QueueArgs{
			Name:                    pulumi.String(queueName),
			MessageRetentionSeconds: pulumi.Int(300),
			SqsManagedSseEnabled:    pulumi.Bool(true),
			Tags: func() pulumi.StringMap {
				t := buildAWSClustersResourceTags(params.compoundName, params.resourceTags)
				t["Name"] = pulumi.String(queueName)
				return t
			}(),
		}, withKarpenterAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create Karpenter SQS queue for %s: %w", release, err)
		}

		// SQS queue policy — parent=queue (child of AWSKarpenter)
		queueURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::ptd:AWSWorkloadClusters$ptd:AWSKarpenter$aws:sqs/queue:Queue::%s",
			ctx.Stack(), ctx.Project(), queueLogical,
		)
		queueChildAlias := pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(queueURN)}})
		queuePolicyDoc := queue.Arn.ApplyT(func(arn string) string {
			return mustMarshalJSON(map[string]interface{}{
				"Version": "2012-10-17",
				"Statement": []map[string]interface{}{
					{
						"Effect": "Allow",
						"Principal": map[string]interface{}{
							"Service": []string{"sqs.amazonaws.com", "events.amazonaws.com"},
						},
						"Action":   "sqs:SendMessage",
						"Resource": arn,
					},
					{
						"Sid":       "DenyHTTP",
						"Effect":    "Deny",
						"Principal": "*",
						"Action":    "sqs:*",
						"Resource":  arn,
						"Condition": map[string]interface{}{
							"Bool": map[string]interface{}{
								"aws:SecureTransport": "false",
							},
						},
					},
				},
			})
		}).(pulumi.StringOutput)
		if _, err := awssqs.NewQueuePolicy(ctx,
			fmt.Sprintf("%s-interruption-queue-policy", queueName),
			&awssqs.QueuePolicyArgs{
				QueueUrl: queue.Url,
				Policy:   queuePolicyDoc,
			},
			queueChildAlias,
		); err != nil {
			return fmt.Errorf("clusters: failed to create Karpenter SQS queue policy for %s: %w", release, err)
		}

		// EventBridge rules and targets — direct children of ptd:AWSKarpenter
		type ebRule struct {
			suffix  string
			pattern map[string]interface{}
		}
		rules := []ebRule{
			{
				suffix:  "scheduled-change-rule",
				pattern: map[string]interface{}{"source": []string{"aws.health"}, "detail-type": []string{"AWS Health Event"}},
			},
			{
				suffix:  "spot-interruption-rule",
				pattern: map[string]interface{}{"source": []string{"aws.ec2"}, "detail-type": []string{"EC2 Spot Instance Interruption Warning"}},
			},
			{
				suffix:  "rebalance-rule",
				pattern: map[string]interface{}{"source": []string{"aws.ec2"}, "detail-type": []string{"EC2 Instance Rebalance Recommendation"}},
			},
			{
				suffix:  "instance-state-change-rule",
				pattern: map[string]interface{}{"source": []string{"aws.ec2"}, "detail-type": []string{"EC2 Instance State-change Notification"}},
			},
		}

		for _, r := range rules {
			ruleLogical := fmt.Sprintf("%s-%s", clusterName, r.suffix)
			patternJSON := mustMarshalJSON(r.pattern)
			rule, ruleErr := awscloudwatch.NewEventRule(ctx, ruleLogical, &awscloudwatch.EventRuleArgs{
				EventPattern: pulumi.String(patternJSON),
				Tags:         buildAWSClustersResourceTags(params.compoundName, params.resourceTags),
			}, withKarpenterAlias())
			if ruleErr != nil {
				return fmt.Errorf("clusters: failed to create EventBridge rule %s for %s: %w", r.suffix, release, ruleErr)
			}

			// Target — parent=rule (child of AWSKarpenter)
			ruleURN := fmt.Sprintf(
				"urn:pulumi:%s::%s::ptd:AWSWorkloadClusters$ptd:AWSKarpenter$aws:cloudwatch/eventRule:EventRule::%s",
				ctx.Stack(), ctx.Project(), ruleLogical,
			)
			ruleChildAlias := pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(ruleURN)}})
			targetSuffix := strings.TrimSuffix(r.suffix, "-rule") + "-target"
			targetLogical := fmt.Sprintf("%s-%s", clusterName, targetSuffix)
			if _, ruleErr = awscloudwatch.NewEventTarget(ctx, targetLogical, &awscloudwatch.EventTargetArgs{
				Rule:     rule.Name,
				TargetId: pulumi.String("KarpenterInterruptionQueueTarget"),
				Arn:      queue.Arn,
			}, ruleChildAlias); ruleErr != nil {
				return fmt.Errorf("clusters: failed to create EventBridge target %s for %s: %w", targetSuffix, release, ruleErr)
			}
		}

		// Karpenter controller IAM role — created via standard createAWSIAMRole.
		// In Python this was called via _define_k8s_iam_role_func which places it as a
		// direct child of ptd:AWSWorkloadClusters (withAlias), not of ptd:AWSKarpenter.
		// Use the OIDC URL from the live cluster (stored in params.oidcIssuerURLsByCluster) to
		// determine if the controller role should be created — matching Python's ptd.get_oidc_url().
		clusterSpec := clusters[release].Spec
		if _, hasOIDC := params.oidcIssuerURLsByCluster[release]; hasOIDC {
			controllerRoleName := fmt.Sprintf("KarpenterControllerRole-%s.posit.team", clusterName)
			controllerPolicyJSON := buildKarpenterControllerPolicy(
				clusterName, params.accountID, params.region, queueName,
			)
			if err := createAWSIAMRole(ctx,
				controllerRoleName, controllerRoleName,
				clustersKarpenterNamespace, []string{"karpenter"},
				[]inlinePolicy{{name: controllerRoleName + "-role-policy-0", doc: controllerPolicyJSON}},
				"", release, params, withAlias(),
			); err != nil {
				return err
			}
		}

		// EKS Access Entry for Karpenter node role (if using access entries)
		eksAccessCfg := clusterSpec.EksAccessEntries
		if eksAccessCfg != nil {
			accessEntryLogical := fmt.Sprintf("%s-karpenter-node-access-entry", clusterName)
			karpenterNodeRoleARN := fmt.Sprintf(
				"arn:aws:iam::%s:role/KarpenterNodeRole-%s.posit.team",
				params.accountID, clusterName,
			)
			if _, err := awseks.NewAccessEntry(ctx, accessEntryLogical, &awseks.AccessEntryArgs{
				ClusterName:  pulumi.String(clusterName),
				PrincipalArn: pulumi.String(karpenterNodeRoleARN),
				Type:         pulumi.String("EC2_LINUX"),
			}, withKarpenterAlias()); err != nil {
				return fmt.Errorf("clusters: failed to create Karpenter EKS access entry for %s: %w", release, err)
			}
		}
	}

	return nil
}

// buildKarpenterControllerPolicy builds the IAM policy document for the Karpenter controller role.
func buildKarpenterControllerPolicy(clusterName, accountID, region, queueName string) string {
	queueARN := fmt.Sprintf("arn:aws:sqs:%s:%s:%s", region, accountID, queueName)
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Sid":    "AllowScopedEC2InstanceAccessActions",
				"Effect": "Allow",
				"Resource": []string{
					fmt.Sprintf("arn:aws:ec2:%s::image/*", region),
					fmt.Sprintf("arn:aws:ec2:%s::snapshot/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:security-group/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:subnet/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:capacity-reservation/*", region),
				},
				"Action": []string{"ec2:RunInstances", "ec2:CreateFleet"},
			},
			{
				"Sid":      "AllowScopedEC2LaunchTemplateAccessActions",
				"Effect":   "Allow",
				"Resource": []string{fmt.Sprintf("arn:aws:ec2:%s:*:launch-template/*", region)},
				"Action":   []string{"ec2:RunInstances", "ec2:CreateFleet"},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						fmt.Sprintf("aws:ResourceTag/kubernetes.io/cluster/%s", clusterName): "owned",
					},
					"StringLike": map[string]interface{}{
						"aws:ResourceTag/karpenter.sh/nodepool": "*",
					},
				},
			},
			{
				"Sid":    "AllowScopedEC2InstanceActionsWithTags",
				"Effect": "Allow",
				"Resource": []string{
					fmt.Sprintf("arn:aws:ec2:%s:*:fleet/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:instance/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:volume/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:network-interface/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:launch-template/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:spot-instances-request/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:capacity-reservation/*", region),
				},
				"Action": []string{"ec2:RunInstances", "ec2:CreateFleet", "ec2:CreateLaunchTemplate"},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						fmt.Sprintf("aws:RequestTag/kubernetes.io/cluster/%s", clusterName): "owned",
						"aws:RequestTag/eks:eks-cluster-name":                               clusterName,
					},
					"StringLike": map[string]interface{}{
						"aws:RequestTag/karpenter.sh/nodepool": "*",
					},
				},
			},
			{
				"Sid":    "AllowScopedResourceCreationTagging",
				"Effect": "Allow",
				"Resource": []string{
					fmt.Sprintf("arn:aws:ec2:%s:*:fleet/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:instance/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:volume/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:network-interface/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:launch-template/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:spot-instances-request/*", region),
				},
				"Action": []string{"ec2:CreateTags"},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						fmt.Sprintf("aws:RequestTag/kubernetes.io/cluster/%s", clusterName): "owned",
						"aws:RequestTag/eks:eks-cluster-name":                               clusterName,
						"ec2:CreateAction":                                                  []string{"RunInstances", "CreateFleet", "CreateLaunchTemplate"},
					},
					"StringLike": map[string]interface{}{
						"aws:RequestTag/karpenter.sh/nodepool": "*",
					},
				},
			},
			{
				"Sid":      "AllowScopedResourceTagging",
				"Effect":   "Allow",
				"Resource": []string{fmt.Sprintf("arn:aws:ec2:%s:*:instance/*", region)},
				"Action":   []string{"ec2:CreateTags"},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						fmt.Sprintf("aws:ResourceTag/kubernetes.io/cluster/%s", clusterName): "owned",
					},
					"StringLike": map[string]interface{}{
						"aws:ResourceTag/karpenter.sh/nodepool": "*",
					},
					"StringEqualsIfExists": map[string]interface{}{
						"aws:RequestTag/eks:eks-cluster-name": clusterName,
					},
					"ForAllValues:StringEquals": map[string]interface{}{
						"aws:TagKeys": []string{"eks:eks-cluster-name", "karpenter.sh/nodeclaim", "Name"},
					},
				},
			},
			{
				"Sid":    "AllowScopedDeletion",
				"Effect": "Allow",
				"Resource": []string{
					fmt.Sprintf("arn:aws:ec2:%s:*:instance/*", region),
					fmt.Sprintf("arn:aws:ec2:%s:*:launch-template/*", region),
				},
				"Action": []string{"ec2:TerminateInstances", "ec2:DeleteLaunchTemplate"},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						fmt.Sprintf("aws:ResourceTag/kubernetes.io/cluster/%s", clusterName): "owned",
					},
					"StringLike": map[string]interface{}{
						"aws:ResourceTag/karpenter.sh/nodepool": "*",
					},
				},
			},
			{
				"Sid":      "AllowRegionalReadActions",
				"Effect":   "Allow",
				"Resource": []string{"*"},
				"Action": []string{
					"ec2:DescribeCapacityReservations",
					"ec2:DescribeImages",
					"ec2:DescribeInstances",
					"ec2:DescribeInstanceTypeOfferings",
					"ec2:DescribeInstanceTypes",
					"ec2:DescribeLaunchTemplates",
					"ec2:DescribeSecurityGroups",
					"ec2:DescribeSpotPriceHistory",
					"ec2:DescribeSubnets",
				},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						"aws:RequestedRegion": region,
					},
				},
			},
			{
				"Sid":      "AllowSSMReadActions",
				"Effect":   "Allow",
				"Resource": []string{fmt.Sprintf("arn:aws:ssm:%s::parameter/aws/service/*", region)},
				"Action":   []string{"ssm:GetParameter"},
			},
			{
				"Sid":      "AllowPricingReadActions",
				"Effect":   "Allow",
				"Resource": []string{"*"},
				"Action":   []string{"pricing:GetProducts"},
			},
			{
				"Sid":      "AllowInterruptionQueueActions",
				"Effect":   "Allow",
				"Resource": []string{queueARN},
				"Action":   []string{"sqs:DeleteMessage", "sqs:GetQueueUrl", "sqs:ReceiveMessage"},
			},
			{
				"Sid":    "AllowPassingInstanceRole",
				"Effect": "Allow",
				"Resource": []string{
					fmt.Sprintf("arn:aws:iam::%s:role/KarpenterNodeRole-%s.posit.team", accountID, clusterName),
				},
				"Action": []string{"iam:PassRole"},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						"iam:PassedToService": []string{"ec2.amazonaws.com", "ec2.amazonaws.com.cn"},
					},
				},
			},
			{
				"Sid":      "AllowScopedInstanceProfileCreationActions",
				"Effect":   "Allow",
				"Resource": []string{fmt.Sprintf("arn:aws:iam::%s:instance-profile/*", accountID)},
				"Action":   []string{"iam:CreateInstanceProfile"},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						fmt.Sprintf("aws:RequestTag/kubernetes.io/cluster/%s", clusterName): "owned",
						"aws:RequestTag/eks:eks-cluster-name":                               clusterName,
						"aws:RequestTag/topology.kubernetes.io/region":                      region,
					},
					"StringLike": map[string]interface{}{
						"aws:RequestTag/karpenter.k8s.aws/ec2nodeclass": "*",
					},
				},
			},
			{
				"Sid":      "AllowScopedInstanceProfileTagActions",
				"Effect":   "Allow",
				"Resource": []string{fmt.Sprintf("arn:aws:iam::%s:instance-profile/*", accountID)},
				"Action":   []string{"iam:TagInstanceProfile"},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						fmt.Sprintf("aws:ResourceTag/kubernetes.io/cluster/%s", clusterName): "owned",
						"aws:ResourceTag/topology.kubernetes.io/region":                      region,
						fmt.Sprintf("aws:RequestTag/kubernetes.io/cluster/%s", clusterName):  "owned",
						"aws:RequestTag/eks:eks-cluster-name":                                clusterName,
						"aws:RequestTag/topology.kubernetes.io/region":                       region,
					},
					"StringLike": map[string]interface{}{
						"aws:ResourceTag/karpenter.k8s.aws/ec2nodeclass": "*",
						"aws:RequestTag/karpenter.k8s.aws/ec2nodeclass":  "*",
					},
				},
			},
			{
				"Sid":      "AllowScopedInstanceProfileActions",
				"Effect":   "Allow",
				"Resource": []string{fmt.Sprintf("arn:aws:iam::%s:instance-profile/*", accountID)},
				"Action": []string{
					"iam:AddRoleToInstanceProfile",
					"iam:RemoveRoleFromInstanceProfile",
					"iam:DeleteInstanceProfile",
				},
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						fmt.Sprintf("aws:ResourceTag/kubernetes.io/cluster/%s", clusterName): "owned",
						"aws:ResourceTag/topology.kubernetes.io/region":                      region,
					},
					"StringLike": map[string]interface{}{
						"aws:ResourceTag/karpenter.k8s.aws/ec2nodeclass": "*",
					},
				},
			},
			{
				"Sid":      "AllowInstanceProfileReadActions",
				"Effect":   "Allow",
				"Resource": []string{fmt.Sprintf("arn:aws:iam::%s:instance-profile/*", accountID)},
				"Action":   []string{"iam:GetInstanceProfile"},
			},
			{
				"Sid":    "AllowAPIServerEndpointDiscovery",
				"Effect": "Allow",
				"Resource": []string{
					fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", region, accountID, clusterName),
				},
				"Action": []string{"eks:DescribeCluster"},
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// createKeycloakOperator creates the KeycloakOperator component resources using kustomize.
// The component logical name in Python is "{compound_name}-{release}" (no "keycloak" suffix).
// Component type: ptd:KeycloakOperator.
func createKeycloakOperator(
	ctx *pulumi.Context,
	name, release, accountID string,
	resourceTags map[string]string,
	k8sProviderOpt pulumi.ResourceOption,
	withKeycloakAlias func() pulumi.ResourceOption,
) error {
	ptdTop := viper.GetString("TOP")
	kustomizationDir := filepath.Join(ptdTop, "keycloak", "kustomization")

	// resource_prefix in Python: f"{workload.compound_name}-{release}"
	resourcePrefix := fmt.Sprintf("%s-%s", name, release)

	// Compute posit.team labels and IRSA role ARN (match Python KeycloakOperator required_tags).
	trueName, environment := name, ""
	if idx := strings.LastIndex(name, "-"); idx >= 0 {
		trueName = name[:idx]
		environment = name[idx+1:]
	}
	keycloakRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/keycloak.%s.%s.posit.team", accountID, release, name)

	// Transformations matching the Python code:
	// - remove_cluster_roles: turns ClusterRole/ClusterRoleBinding into empty List
	// - update_operator_role: patches Role namespace and adds keycloak RBAC rules
	// - set_deployment_namespace: patches Deployment/ServiceAccount/Service namespace
	// - set_labels: add posit.team labels (matching Python required_tags)
	// - set_irsa_annotation: add eks.amazonaws.com/role-arn to keycloak-operator SA
	// The Go kustomize SDK supports yaml.Transformation = func(state map[string]interface{}, opts ...pulumi.ResourceOption)
	transformations := []k8syaml.Transformation{
		// remove_cluster_roles: convert to empty List
		func(state map[string]interface{}, _ ...pulumi.ResourceOption) {
			kind, _ := state["kind"].(string)
			if kind == "ClusterRole" || kind == "ClusterRoleBinding" {
				for k := range state {
					delete(state, k)
				}
				state["kind"] = "List"
				state["apiVersion"] = "v1"
				return
			}
			if kind == "RoleBinding" {
				roleRef, _ := state["roleRef"].(map[string]interface{})
				if roleRef != nil {
					if roleRef["kind"] == "ClusterRole" {
						for k := range state {
							delete(state, k)
						}
						state["kind"] = "List"
						state["apiVersion"] = "v1"
						return
					}
				}
			}
		},
		// update_operator_role: patch Role namespace and add keycloak RBAC
		func(state map[string]interface{}, _ ...pulumi.ResourceOption) {
			meta, _ := state["metadata"].(map[string]interface{})
			if meta == nil {
				return
			}
			kind, _ := state["kind"].(string)
			if kind == "Role" {
				if name, _ := meta["name"].(string); name == "keycloak-operator-role" {
					meta["namespace"] = clustersPositTeamNamespace
					rules, _ := state["rules"].([]interface{})
					state["rules"] = append(rules,
						map[string]interface{}{
							"apiGroups": []interface{}{"k8s.keycloak.org"},
							"resources": []interface{}{
								// Python order: keycloaks first, then keycloakrealmimports
								"keycloaks",
								"keycloaks/status",
								"keycloaks/finalizers",
							},
							"verbs": []interface{}{"get", "list", "watch", "patch", "update", "create", "delete"},
						},
						map[string]interface{}{
							"apiGroups": []interface{}{"k8s.keycloak.org"},
							"resources": []interface{}{
								"keycloakrealmimports",
								"keycloakrealmimports/status",
								"keycloakrealmimports/finalizers",
							},
							"verbs": []interface{}{"get", "list", "watch", "patch", "update", "create", "delete"},
						},
					)
				}
			}
			if kind == "RoleBinding" {
				if objName, _ := meta["name"].(string); objName == "keycloak-operator-role-binding" {
					meta["namespace"] = clustersPositTeamNamespace
					// Python also sets namespace on subjects to point to the posit-team-system SA.
					if subjects, ok := state["subjects"].([]interface{}); ok {
						for _, subj := range subjects {
							if sm, ok := subj.(map[string]interface{}); ok {
								if sm["kind"] == "ServiceAccount" && sm["name"] == "keycloak-operator" {
									sm["namespace"] = clustersPositTeamSystemNamespace
								}
							}
						}
					}
				}
			}
		},
		// set_deployment_namespace: patch Deployment/ServiceAccount/Service to posit-team-system.
		// Also replace KUBERNETES_NAMESPACE valueFrom with a hardcoded value (matching Python).
		func(state map[string]interface{}, _ ...pulumi.ResourceOption) {
			meta, _ := state["metadata"].(map[string]interface{})
			if meta == nil {
				return
			}
			kind, _ := state["kind"].(string)
			objName, _ := meta["name"].(string)
			if (kind == "Deployment" || kind == "ServiceAccount" || kind == "Service") &&
				objName == "keycloak-operator" {
				meta["namespace"] = clustersPositTeamSystemNamespace
			}
			// Python replaces the KUBERNETES_NAMESPACE env valueFrom with a hardcoded value.
			if kind == "Deployment" && objName == "keycloak-operator" {
				spec, _ := state["spec"].(map[string]interface{})
				if spec == nil {
					return
				}
				tmpl, _ := spec["template"].(map[string]interface{})
				if tmpl == nil {
					return
				}
				podSpec, _ := tmpl["spec"].(map[string]interface{})
				if podSpec == nil {
					return
				}
				containers, _ := podSpec["containers"].([]interface{})
				for _, c := range containers {
					cm, _ := c.(map[string]interface{})
					if cm == nil {
						continue
					}
					envs, _ := cm["env"].([]interface{})
					for _, e := range envs {
						em, _ := e.(map[string]interface{})
						if em == nil {
							continue
						}
						if em["name"] == "KUBERNETES_NAMESPACE" {
							delete(em, "valueFrom")
							em["value"] = clustersPositTeamNamespace
						}
					}
				}
			}
		},
		// set_labels: add posit.team labels and resource tags (matching Python required_tags).
		func(state map[string]interface{}, _ ...pulumi.ResourceOption) {
			meta, _ := state["metadata"].(map[string]interface{})
			if meta == nil {
				state["metadata"] = map[string]interface{}{}
				meta = state["metadata"].(map[string]interface{})
			}
			labels, _ := meta["labels"].(map[string]interface{})
			if labels == nil {
				labels = map[string]interface{}{}
				meta["labels"] = labels
			}
			for k, v := range resourceTags {
				if !strings.Contains(k, ":") {
					labels[k] = v
				}
			}
			labels["posit.team/true-name"] = trueName
			labels["posit.team/environment"] = environment
			labels["posit.team/managed-by"] = "ptd.pulumi_resources.keycloak_operator"
		},
		// set_irsa_annotation: add eks.amazonaws.com/role-arn to keycloak-operator ServiceAccount.
		// Python's KeycloakOperator sets this so the operator pod can use IRSA to access AWS resources.
		func(state map[string]interface{}, _ ...pulumi.ResourceOption) {
			kind, _ := state["kind"].(string)
			if kind != "ServiceAccount" {
				return
			}
			meta, _ := state["metadata"].(map[string]interface{})
			if meta == nil {
				return
			}
			if objName, _ := meta["name"].(string); objName != "keycloak-operator" {
				return
			}
			annotations, _ := meta["annotations"].(map[string]interface{})
			if annotations == nil {
				annotations = map[string]interface{}{}
				meta["annotations"] = annotations
			}
			annotations["eks.amazonaws.com/role-arn"] = keycloakRoleARN
		},
	}

	// Python: logical name = f"{workload.compound_name}-{release}-keycloak"
	// BUT Python's kustomize.Directory prepends resource_prefix to the name before calling super().__init__():
	//   if resource_prefix: name = f"{resource_prefix}-{name}"
	// So the registered logical name becomes: f"{resource_prefix}-{workload.compound_name}-{release}-keycloak"
	//   = f"{name}-{release}-{name}-{release}-keycloak"
	// Component registers as "kubernetes:kustomize:Directory"
	kustomizeLogical := fmt.Sprintf("%s-%s-%s-%s-keycloak", name, release, name, release)
	// The kustomize directory is a direct child of ptd:KeycloakOperator.
	// We alias it with a parent pointing to the Python KeycloakOperator component.
	if _, err := kustomize.NewDirectory(ctx, kustomizeLogical, kustomize.DirectoryArgs{
		Directory:       pulumi.String(kustomizationDir),
		ResourcePrefix:  resourcePrefix,
		Transformations: transformations,
	}, k8sProviderOpt, withKeycloakAlias()); err != nil {
		return fmt.Errorf("clusters: failed to create KeycloakOperator kustomize directory for %s: %w", release, err)
	}

	return nil
}
