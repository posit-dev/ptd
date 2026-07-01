package steps

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/proxy"
	"github.com/posit-dev/ptd/lib/types"
	awspulumi "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// eksOldProjectName is the OLD Python Pulumi project name for the workload eks
// step. Used verbatim in alias URNs (NEVER ctx.Project() — see the migration
// playbook). The project name is ptd-<cloud>-<target-type>-<step>.
const eksOldProjectName = "ptd-aws-workload-eks"

// eksWorkloadWrapperType is the OLD Python wrapper ComponentResource type token
// (AWSWorkloadEKS). The shared AWSEKSCluster was created as a child of this
// wrapper, so the parent type chain for AWSEKSCluster children is
// "ptd:AWSWorkloadEKS$ptd:AWSEKSCluster".
const eksWorkloadWrapperType = "ptd:AWSWorkloadEKS"

// eksManagedByValue is the posit.team/managed-by tag value Python set on every
// resource (the __name__ of aws_workload_eks.py).
const eksManagedByValue = "ptd.pulumi_resources.aws_workload_eks"

// eksClusterData bundles the per-cluster live-state lookups done in the
// pre-fetch layer (auth mode, VPC id, kubeconfig, NFS security group ids).
type eksClusterData struct {
	subnetIDs       []string
	clusterExists   bool
	currentAuthMode string
	kubeconfig      string
	// securityGroupIDs are the launch-template security groups assembled the way
	// Python _build_with_vpc_config does: FSx NFS SG, optionally EFS NFS SG, the
	// cluster SG, and any cluster-config SGs.
	securityGroupIDs []string
	// vpcID is the cluster's VPC id (used for SG-access lookups + the alias config).
	vpcID string
	// clusterSGID is the cluster's primary (EKS-managed) security group id, used
	// as the target of the bastion/tailscale ingress rule. Empty on greenfield.
	clusterSGID string
	// accessEntries is the pre-fetched EKS access-entry import state (only used on
	// the access-entries auth path).
	accessEntries aws.AccessEntryData
	// efsSGID is the EFS NFS security group id when EFS is enabled (used by
	// AttachEfsSecurityGroup).
	efsSGID string
	// oidcThumbprint is the OIDC provider CA thumbprint, pre-fetched via
	// aws.GetOIDCThumbprint (replicates Python ptd.oidc.get_thumbprint). Empty on a
	// greenfield cluster that does not exist yet.
	oidcThumbprint string
}

// awsEKSParams bundles all pre-fetched data for the AWS eks deploy function.
type awsEKSParams struct {
	compoundName string
	region       string
	// requiredTags mirrors Python AWSWorkloadEKS.required_tags (resource_tags +
	// posit.team/{true-name,environment} + posit.team/managed-by).
	requiredTags map[string]string
	// iamPermissionsBoundaryARN is the workload permissions boundary policy ARN.
	iamPermissionsBoundaryARN string
	// protectPersistentResources applies pulumi.Protect to durable resources.
	protectPersistentResources bool
	tailscaleEnabled           bool
	// accountID is the workload AWS account id (for IRSA + admin/poweruser ARNs).
	accountID string
	// callerARN is the deploying identity's ARN (aws.GetCallerIdentity().Arn). It
	// is the fallback Principal.AWS for the IRSA trust policy when no OIDC issuer
	// is available; in the eks step the cluster always exists, so this is only a
	// parity safeguard. Mirrors the persistent step's callerARN.
	callerARN string
	// workloadIRSA holds the data needed to build the 8 workload-scoped IRSA roles
	// (FSx, LBC, ExternalDNS, TraefikForwardAuth, Mimir, Loki, EBS-CSI, Alloy) in
	// phase 2 of the deploy, after every cluster's OIDC provider is created. These
	// roles moved here from the persistent step (see persistent_reprise removal).
	workloadIRSA workloadIRSAParams
	// region is duplicated above; credentials is needed for the EFS mount-target
	// SG attach side effect.
	credentials *aws.Credentials
	// customerManagedBastionID, when set, suppresses PTD bastion SG wiring.
	customerManagedBastionID string
	// adminRoleARN overrides the aws-auth/access-entry admin role ARN
	// (custom_role.role_arn); empty → admin.posit.team.
	adminRoleARN string
	// secretsStoreAddonEnabled installs the secrets-store CSI provider addon.
	secretsStoreAddonEnabled bool
	// thirdPartyTelemetry mirrors third_party_telemetry_enabled (Tigera felix
	// usage reporting). Python default True.
	thirdPartyTelemetry bool
	// clusters is the workload's cluster config keyed by release.
	clusters map[string]types.AWSWorkloadClusterConfig
	// perCluster holds the live-state lookups keyed by release.
	perCluster map[string]eksClusterData
}

func (s *EKSStep) runAWSInlineGo(ctx context.Context) error {
	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}
	envVars, err := prepareEnvVarsForPulumi(ctx, s.DstTarget, creds)
	if err != nil {
		return err
	}
	// eks needs proxy connectivity for the K8s provider (Tigera CNI etc.) just
	// like the clusters step.
	if !s.DstTarget.TailscaleEnabled() {
		envVars["ALL_PROXY"] = fmt.Sprintf("socks5://localhost:%d", proxy.WorkloadPort(s.DstTarget.Name()))
	}

	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("eks: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSWorkloadConfig)
	if !ok {
		return fmt.Errorf("eks: expected AWSWorkloadConfig")
	}

	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}

	compoundName := s.DstTarget.Name()
	region := s.DstTarget.Region()

	// Determine provisioned-VPC subnet Name tags (if any) for greenfield subnet
	// lookups (mirrors Python workload.subnets("private")).
	var provisionedSubnetNames []string
	provisionedVPCID := ""
	if cfg.ProvisionedVpc != nil {
		provisionedVPCID = cfg.ProvisionedVpc.VpcID
		provisionedSubnetNames = cfg.ProvisionedVpc.PrivateSubnets
	}

	perCluster := make(map[string]eksClusterData, len(cfg.Clusters))
	for release, clusterCfg := range cfg.Clusters {
		clusterName := compoundName + "-" + release

		// Auth-mode preservation: probe the live cluster so we don't flip its
		// authentication mode (which would force a replace). Mirrors the boto3
		// describe_cluster probe in aws_eks_cluster.py __init__.
		exists, authMode, aerr := aws.GetClusterAuthMode(ctx, awsCreds, region, clusterName)
		if aerr != nil {
			return fmt.Errorf("eks: failed to probe cluster %s auth mode: %w", clusterName, aerr)
		}

		// VPC id + control-plane subnets + cluster security groups. When the
		// cluster exists the live VPC config is authoritative (byte-identical to
		// state). Otherwise fall back to the tag-based VPC/subnet lookup that
		// Python used at greenfield create time.
		var subnetIDs, clusterSGIDs []string
		var vpcID, clusterSGID string
		if exists {
			vpcCfg, verr := aws.GetClusterVPCConfig(ctx, awsCreds, region, clusterName)
			if verr != nil {
				return fmt.Errorf("eks: failed to get VPC config for %s: %w", clusterName, verr)
			}
			subnetIDs = vpcCfg.SubnetIDs
			clusterSGIDs = vpcCfg.SecurityGroupIDs
			vpcID = vpcCfg.VpcID
			clusterSGID = vpcCfg.ClusterSecurityGroupID
		} else {
			lookupVPCID := provisionedVPCID
			if lookupVPCID == "" {
				id, found, verr := aws.GetVpcID(ctx, awsCreds, region, compoundName)
				if verr != nil {
					return fmt.Errorf("eks: failed to look up VPC for %s: %w", compoundName, verr)
				}
				if !found {
					return fmt.Errorf("eks: no PTD-managed VPC found for %s (run persistent first)", compoundName)
				}
				lookupVPCID = id
			}
			vpcID = lookupVPCID
			subnetIDs, err = aws.GetWorkloadPrivateSubnetIDs(ctx, awsCreds, region, compoundName, lookupVPCID, provisionedSubnetNames)
			if err != nil {
				return fmt.Errorf("eks: failed to look up private subnets for %s: %w", compoundName, err)
			}
		}

		// Assemble launch-template security groups the way Python
		// _build_with_vpc_config does: FSx NFS SG (+ EFS NFS SG if EFS enabled) +
		// the cluster security groups (cluster SG + any additional SGs from the
		// live VPC config).
		var sgIDs []string
		fsxSG, fsxOK, ferr := aws.GetNFSSecurityGroupID(ctx, awsCreds, region, vpcID, "eks-nodes-fsx-nfs.posit.team")
		if ferr != nil {
			return fmt.Errorf("eks: failed to look up FSx NFS SG for %s: %w", clusterName, ferr)
		}
		if fsxOK {
			sgIDs = append(sgIDs, fsxSG)
		}
		spec := clusterCfg.Spec
		efsSGID := ""
		if spec.EnableEfsCsiDriver || spec.EfsConfig != nil {
			efsSG, efsOK, eerr := aws.GetNFSSecurityGroupID(ctx, awsCreds, region, vpcID, "eks-nodes-efs-nfs.posit.team")
			if eerr != nil {
				return fmt.Errorf("eks: failed to look up EFS NFS SG for %s: %w", clusterName, eerr)
			}
			if efsOK {
				sgIDs = append(sgIDs, efsSG)
				efsSGID = efsSG
			}
		}
		sgIDs = append(sgIDs, clusterSGIDs...)

		// Access-entry import state (only needed on the access-entries auth path;
		// cheap to fetch unconditionally and a no-op on a greenfield cluster).
		var accessEntries aws.AccessEntryData
		if spec.UsesEksAccessEntries() && exists {
			accessEntries, err = aws.GetAccessEntryData(ctx, awsCreds, region, clusterName)
			if err != nil {
				return fmt.Errorf("eks: failed to fetch access entries for %s: %w", clusterName, err)
			}
		}

		// Kubeconfig for the K8s provider (built from live endpoint/CA when the
		// cluster exists). For a greenfield cluster the provider would be built
		// from cluster outputs in a later phase; here we still need a valid string
		// so the program constructs. When the cluster doesn't exist yet, leave the
		// kubeconfig empty — the core (cluster/node/oidc) does not use the provider.
		kubeconfig := ""
		oidcThumbprint := ""
		if exists {
			endpoint, caCert, oidcIssuerURL, cerr := aws.GetClusterInfo(ctx, awsCreds, region, clusterName)
			if cerr != nil {
				return fmt.Errorf("eks: failed to get cluster info for %s: %w", clusterName, cerr)
			}
			proxyURL := ""
			if !cfg.TailscaleEnabled {
				proxyURL = fmt.Sprintf("socks5://localhost:%d", proxy.WorkloadPort(compoundName))
			}
			kubeconfig, cerr = kube.BuildEKSKubeconfigString(endpoint, caCert, clusterName, region, proxyURL)
			if cerr != nil {
				return fmt.Errorf("eks: %w", cerr)
			}
			// OIDC provider thumbprint: dial the issuer's jwks_uri netloc exactly
			// as Python's ptd.oidc.get_thumbprint does. oidc.eks.<region>.amazonaws.com
			// is a public endpoint, so this needs no proxy.
			if oidcIssuerURL != "" {
				oidcThumbprint, cerr = aws.GetOIDCThumbprint(ctx, oidcIssuerURL)
				if cerr != nil {
					return fmt.Errorf("eks: failed to compute OIDC thumbprint for %s: %w", clusterName, cerr)
				}
			}
		}

		perCluster[release] = eksClusterData{
			subnetIDs:        subnetIDs,
			clusterExists:    exists,
			currentAuthMode:  authMode,
			kubeconfig:       kubeconfig,
			securityGroupIDs: sgIDs,
			vpcID:            vpcID,
			clusterSGID:      clusterSGID,
			accessEntries:    accessEntries,
			efsSGID:          efsSGID,
			oidcThumbprint:   oidcThumbprint,
		}
	}

	adminRoleARN := ""
	if cfg.CustomRole != nil {
		adminRoleARN = cfg.CustomRole.RoleArn
	}

	// callerARN: the deploying identity's ARN, used as the IRSA trust fallback
	// principal when no OIDC issuer is available (mirrors the persistent step). A
	// failed lookup (or a nil ARN) is a hard error: an empty Principal ARN would
	// produce an invalid IAM trust policy, so we refuse to proceed rather than
	// emit one.
	caller, callerErr := aws.GetCallerIdentity(ctx)
	if callerErr != nil {
		return fmt.Errorf("eks: failed to get caller identity: %w", callerErr)
	}
	if caller.Arn == nil {
		return fmt.Errorf("eks: caller identity returned a nil ARN")
	}
	callerARN := *caller.Arn

	// protect_persistent_resources defaults True in Python (aws_eks_cluster.py) and
	// is never set false in any ptd.yaml. The Go config field is a plain bool, so an
	// absent value would resolve to false and UNPROTECT the OIDC provider — force it
	// true to match Python, mirroring the persistent step.
	cfg.ProtectPersistentResources = true

	params := awsEKSParams{
		compoundName:               compoundName,
		region:                     region,
		requiredTags:               buildEKSRequiredTags(compoundName, cfg.ResourceTags),
		iamPermissionsBoundaryARN:  fmt.Sprintf("arn:aws:iam::%s:policy/PositTeamDedicatedAdmin", awsCreds.AccountID()),
		protectPersistentResources: cfg.ProtectPersistentResources,
		tailscaleEnabled:           cfg.TailscaleEnabled,
		accountID:                  awsCreds.AccountID(),
		credentials:                awsCreds,
		customerManagedBastionID:   cfg.CustomerManagedBastionId,
		adminRoleARN:               adminRoleARN,
		secretsStoreAddonEnabled:   cfg.IsSecretsStoreAddonEnabled(),
		thirdPartyTelemetry:        cfg.IsThirdPartyTelemetryEnabled(),
		clusters:                   cfg.Clusters,
		perCluster:                 perCluster,
		callerARN:                  callerARN,
	}
	// Source ExternalDNS hosted-zone ids from the persistent step's
	// hosted_zone_name_servers stack output (the created/adopted zone ids it
	// already exports), so the workload IRSA ExternalDNS policy ARNs are
	// byte-identical to persistent's with no route53 lookup. persistent always
	// runs (and writes this output) before eks; a missing output is surfaced as an
	// error in the phase-2 ExternalDNS builder (mirrors the bastion_id ordering
	// constraint). Pre-fetched here, NOT inside the pulumi program.
	siteZoneIDs, zonesPresent := persistentSiteZoneIDs(ctx, s.DstTarget)
	params.workloadIRSA = buildWorkloadIRSAParams(params, irsaConfigFromWorkload(cfg), siteZoneIDs, zonesPresent)

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *awspulumi.Context, target types.Target) error {
		return awsEKSDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return runPulumi(ctx, stack, s.Options)
}

// buildEKSRequiredTags mirrors Python AWSWorkloadEKS.required_tags:
//
//	resource_tags | { posit.team/true-name, posit.team/environment } | { posit.team/managed-by }
//
// where true-name/environment are derived by splitting the compound name on the
// last "-".
func buildEKSRequiredTags(compoundName string, resourceTags map[string]string) map[string]string {
	trueName, environment := compoundName, ""
	if idx := strings.LastIndex(compoundName, "-"); idx >= 0 {
		trueName = compoundName[:idx]
		environment = compoundName[idx+1:]
	}
	tags := map[string]string{}
	for k, v := range resourceTags {
		tags[k] = v
	}
	tags["posit.team/true-name"] = trueName
	tags["posit.team/environment"] = environment
	tags["posit.team/managed-by"] = eksManagedByValue
	return tags
}

// awsEKSDeploy is the package-level AWS eks deploy function, callable from tests.
//
// For each cluster release it ports aws_workload_eks.py _build_with_vpc_config in
// order: EKS cluster (auth-mode preserving) + IAM role + K8s provider (+ bastion/
// tailscale SG access) → default node role + default node group + additional node
// groups → Tigera/Calico CNI → aws-auth ConfigMap OR EKS access entries →
// EBS CSI add-on (+IRSA role) → (EFS CSI add-on +IRSA role + mount-target SG
// attach) → (secrets-store CSI provider) → gp3 storage class → encrypted default
// storage class → OIDC provider.
func awsEKSDeploy(ctx *awspulumi.Context, _ types.Target, params awsEKSParams) error {
	releases := make([]string, 0, len(params.clusters))
	for r := range params.clusters {
		releases = append(releases, r)
	}
	sort.Strings(releases)

	// Phase 1 accumulates each cluster's OIDC provider URL so the workload-scoped
	// IRSA roles (phase 2) can build their trust policy from the cluster issuers.
	var oidcURLs []awspulumi.StringOutput

	for _, release := range releases {
		spec := params.clusters[release].Spec
		data := params.perCluster[release]
		fullName := params.compoundName + "-" + release

		// Sorted control-plane log types (Python: sorted(enabled_cluster_log_types)).
		logTypes := []string{"api", "audit", "authenticator", "controllerManager", "scheduler"}

		eksCluster, err := aws.NewEKSCluster(ctx, aws.EKSClusterConfig{
			Name:                       fullName,
			SubnetIDs:                  data.subnetIDs,
			Version:                    spec.EKSClusterVersion(),
			Tags:                       params.requiredTags,
			DefaultAddonsToRemove:      []string{"vpc-cni"},
			EnabledClusterLogTypes:     logTypes,
			EksRoleName:                fullName + "-eks.posit.team",
			IAMPermissionsBoundary:     params.iamPermissionsBoundaryARN,
			ForceUpdateVersion:         spec.ForceMaintenance,
			ProtectPersistentResources: params.protectPersistentResources,
			OIDCThumbprint:             data.oidcThumbprint,
			ClusterExists:              data.clusterExists,
			CurrentAuthMode:            data.currentAuthMode,
			Kubeconfig:                 data.kubeconfig,
			ProjectName:                eksOldProjectName,
			ParentTypeChain:            eksWorkloadWrapperType + "$ptd:AWSEKSCluster",
			WrapperTypeChain:           eksWorkloadWrapperType,
			SgPrefix:                   params.compoundName,
			Region:                     params.region,
			Credentials:                params.credentials,
			TailscaleEnabled:           params.tailscaleEnabled,
			CustomerManagedBastionID:   params.customerManagedBastionID,
			VpcID:                      data.vpcID,
			ClusterSecurityGroupID:     data.clusterSGID,
			AccessEntries:              data.accessEntries,
			AccountID:                  params.accountID,
		})
		if err != nil {
			return err
		}

		// Default node group FIRST (Python creates "default" before additional
		// groups; node role MUST precede the node group).
		eksCluster.
			WithNodeRole(fmt.Sprintf("%s-eks-node.posit.team", fullName)).
			WithNodeGroup(aws.NodeGroupParams{
				Name:             fullName,
				SecurityGroupIDs: data.securityGroupIDs,
				InstanceType:     spec.EKSMpInstanceType(),
				VolumeSize:       spec.EKSRootDiskSize(),
				AmiType:          spec.EKSAmiType(),
				MinSize:          spec.EKSMpMinSize(),
				MaxSize:          spec.EKSMpMaxSize(),
				DesiredSize:      spec.EKSMpMinSize(), // Python passes mp_min_size as desired.
				Version:          spec.EKSClusterVersion(),
				Tags:             params.requiredTags,
			})

		// Additional node groups (deterministic order for stable resource graph).
		ngNames := make([]string, 0, len(spec.AdditionalNodeGroups))
		for n := range spec.AdditionalNodeGroups {
			ngNames = append(ngNames, n)
		}
		sort.Strings(ngNames)
		for _, ngName := range ngNames {
			ng := spec.AdditionalNodeGroups[ngName]
			sgIDs := append(append([]string{}, data.securityGroupIDs...), ng.AdditionalSecurityGroupIDs...)
			var taints []aws.NodeGroupTaint
			for _, t := range ng.Taints {
				taints = append(taints, aws.NodeGroupTaint{Effect: t.Effect, Key: t.Key, Value: t.Value})
			}
			eksCluster.WithNodeGroup(aws.NodeGroupParams{
				Name:             fullName + "-" + ngName,
				SecurityGroupIDs: sgIDs,
				InstanceType:     ng.NGInstanceType(),
				VolumeSize:       ng.NGRootDiskSize(),
				AmiType:          ng.NGAmiType(spec.EKSAmiType()),
				MinSize:          ng.NGMinSize(),
				MaxSize:          ng.NGMaxSize(),
				DesiredSize:      ng.NGDesiredSize(),
				Version:          spec.EKSClusterVersion(),
				Tags:             params.requiredTags,
				Labels:           ng.Labels,
				Taints:           taints,
			})
		}

		if err := eksCluster.Err(); err != nil {
			return err
		}

		// Tigera/Calico CNI (ptd:TigeraOperator nested component). Created with the
		// cluster's K8s provider; runs in parallel with node-group readiness.
		tigeraVersion := spec.Components.TigeraOperatorVersionOrDefault()
		if err := deployTigeraOperator(ctx, params.compoundName, release, tigeraVersion,
			params.thirdPartyTelemetryEnabled(), awspulumi.Provider(eksCluster.Provider())); err != nil {
			return err
		}

		// aws-auth ConfigMap OR EKS access entries.
		useAccessEntries := spec.UsesEksAccessEntries()
		authParams := aws.AwsAuthParams{
			UseEksAccessEntries: useAccessEntries,
			AdminRoleARN:        params.adminRoleARN,
		}
		if spec.EksAccessEntries != nil {
			authParams.AdditionalAccessEntries = spec.EksAccessEntries.AdditionalEntries
			authParams.IncludePoweruser = spec.EksAccessEntries.IncludeSameAccountPoweruser
		}
		eksCluster.WithAwsAuth(authParams)

		// EBS CSI driver (must precede the encrypted storage class).
		eksCluster.WithEbsCsiDriver(fullName+"-ebs-csi-driver.posit.team", spec.EKSEbsCsiAddonVersion())

		// EFS CSI driver (+ mount-target SG attach) when enabled.
		if spec.EnableEfsCsiDriver {
			eksCluster.WithEfsCsiDriver(fullName + "-efs-csi-driver.posit.team")
			if spec.EfsConfig != nil && data.efsSGID != "" {
				eksCluster.AttachEfsSecurityGroup(
					spec.EfsConfig.FileSystemID,
					data.efsSGID,
					spec.EfsConfig.IsMountTargetsManaged(),
				)
			}
		}

		// Secrets-store CSI provider (workload-level toggle).
		if params.secretsStoreAddonEnabled {
			eksCluster.WithAwsSecretsStoreCsiDriverProvider()
		}

		// Storage classes, then OIDC provider (Python ordering).
		eksCluster.WithGp3()
		eksCluster.WithEncryptedEbsStorageClass()
		eksCluster.WithOidcProvider()

		if err := eksCluster.Err(); err != nil {
			return err
		}

		// Accumulate this cluster's OIDC provider URL for the phase-2 IRSA trust.
		if oidc := eksCluster.OidcProvider(); oidc != nil {
			oidcURLs = append(oidcURLs, oidc.Url)
		}
	}

	// Append any extra_cluster_oidc_urls (configured external issuers). persistent
	// previously folded these into the IRSA trust (cfg.ExtraClusterOidcUrls); the
	// trust builder strips scheme + sorts, so adding them here keeps the trust
	// deterministic and byte-identical for workloads that set the field.
	for _, u := range params.workloadIRSA.extraClusterOidcURLs {
		oidcURLs = append(oidcURLs, awspulumi.String(u).ToStringOutput())
	}

	// Phase 2: create the 8 workload-scoped IRSA roles ONCE, with a trust policy
	// built from every cluster's OIDC issuer. These roles moved here from the
	// persistent step (persistent_reprise removal).
	if err := deployWorkloadIRSARoles(ctx, params.workloadIRSA, oidcURLs); err != nil {
		return err
	}

	return nil
}

// thirdPartyTelemetryEnabled resolves the workload third_party_telemetry_enabled
// flag (Python default True).
func (p awsEKSParams) thirdPartyTelemetryEnabled() bool {
	return p.thirdPartyTelemetry
}
