package steps

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	awsprovider "github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/acm"
	awscloudwatch "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	awsec2 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	awsecs "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ecs"
	awsfsx "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/fsx"
	awsiam "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	awsrds "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/rds"
	awsroute53 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	awss3 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/s3"
	awssecretsmanager "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/secretsmanager"
	awsvpc "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/vpc"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
)

// persistentManagedByValue is the posit.team/managed-by tag value Python set on
// AWS workload persistent resources (the Python module __name__).
const persistentManagedByValue = "ptd.pulumi_resources.aws_workload_persistent"

// persistentInternalSite mirrors the Python InternalSiteConfig: a site's domain,
// optional pre-existing zone id, and (after zone creation) its created zone.
type persistentInternalSite struct {
	siteName string
	domain   string
	zoneID   string // pre-existing zone id from config (may be empty)
	zone     *awsroute53.Zone
}

// awsWorkloadPersistentParams bundles pre-fetched data for the AWS workload
// persistent deploy function. Pulumi data sources are used inside deploy for
// cloud lookups (e.g. the RDS master-user-secret); this struct carries values
// that have no Pulumi data source (config, secrets, naming).
type awsWorkloadPersistentParams struct {
	compoundName string
	prefix       string // always "ptd"
	accountID    string
	region       string
	environment  string // suffix of compoundName; multi-AZ RDS iff "production"
	cfg          types.AWSWorkloadConfig

	requiredTags map[string]string // resource_tags + posit.team/{true-name,environment} + managed-by

	iamPermissionsBound string // arn:aws:iam::<acct>:policy/PositTeamDedicatedAdmin

	// existingDBIdentifier is the already-deployed RDS instance's physical name,
	// read from this stack's prior "db" output (empty for a greenfield workload).
	// When set, the RDS resource adopts it via an explicit Identifier instead of
	// the write-only identifier_prefix (see applyRDSIdentifier).
	existingDBIdentifier string

	// resolvedPrivateSubnetIDs are the real subnet IDs (subnet-xxxx) of an
	// adopted provisioned VPC, resolved from provisioned_vpc.private_subnets
	// (which are Name tags, not IDs). Empty for greenfield. See
	// aws.ResolveSubnetIDsByName / Python AWSWorkload.subnets("private").
	resolvedPrivateSubnetIDs []string

	vpcCIDR string // resolved VPC CIDR string

	// IAM / S3 resource names still consumed by the persistent deploy. The IRSA
	// role/policy name fields moved to the eks step (eks_irsa_aws.go) along with the
	// roles themselves; only the team-operator policy, the FSx NFS SG, and the
	// Mimir/Loki S3 bucket names (the buckets persistent still owns) remain here.
	teamOperatorPolicyName string
	fsxNfsSGName           string
	mimirS3BucketName      string
	lokiS3BucketName       string
}

// existingPersistentDBIdentifier reads this target's persistent stack "db"
// output (the physical RDS instance name) so the RDS resource can adopt an
// already-deployed instance via an explicit Identifier instead of the write-only
// identifier_prefix (see applyRDSIdentifier). Returns "" for a greenfield target
// whose stack has no prior outputs, or on any read error — in which case the
// builder falls back to identifier_prefix.
func existingPersistentDBIdentifier(ctx context.Context, target types.Target) string {
	outs, err := getPersistentStackOutputs(ctx, target)
	if err != nil {
		return ""
	}
	if v, ok := outs["db"]; ok {
		if id, ok := v.Value.(string); ok {
			return id
		}
	}
	return ""
}

// runAWSInlineGo is the AWS-workload entry point for the persistent step. It
// pre-fetches external data and dispatches to awsWorkloadPersistentDeploy.
func (s *PersistentStep) runAWSInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("persistent: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSWorkloadConfig)
	if !ok {
		return fmt.Errorf("persistent: expected AWSWorkloadConfig, got %T", rawConfig)
	}

	// Apply Python AWSWorkloadConfig dataclass defaults for fields not set in
	// ptd.yaml. Go's zero-values (0 / "") would otherwise diff live resources
	// (RDS storage/class, FSx capacity/throughput, etc.). See workload.py.
	if cfg.DBAllocatedStorage == 0 {
		cfg.DBAllocatedStorage = 100
	}
	if cfg.DBEngineVersion == "" {
		cfg.DBEngineVersion = "15.18"
	}
	if cfg.DBInstanceClass == "" {
		cfg.DBInstanceClass = "db.t3.small"
	}
	if cfg.BastionInstanceType == "" {
		cfg.BastionInstanceType = "t4g.nano"
	}
	if cfg.FsxOpenzfsDailyAutomaticBackupStartTime == "" {
		cfg.FsxOpenzfsDailyAutomaticBackupStartTime = "02:00"
	}
	if cfg.FsxOpenzfsStorageCapacity == 0 {
		cfg.FsxOpenzfsStorageCapacity = 100
	}
	if cfg.FsxOpenzfsThroughputCapacity == 0 {
		cfg.FsxOpenzfsThroughputCapacity = 320
	}
	if cfg.VpcAzCount == 0 {
		cfg.VpcAzCount = 3
	}
	// protect_persistent_resources defaults True in Python; never set false in config.
	cfg.ProtectPersistentResources = true

	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}
	accountID := awsCreds.AccountID()

	compoundName := s.DstTarget.Name()
	trueName, environment := compoundName, ""
	if idx := strings.LastIndex(compoundName, "-"); idx >= 0 {
		trueName = compoundName[:idx]
		environment = compoundName[idx+1:]
	}

	// Provisioned-VPC adoption: provisioned_vpc.private_subnets are Name tags, not
	// IDs. Resolve them to real subnet IDs (mirrors Python AWSWorkload.subnets),
	// so the adopted VPC, RDS subnet group, FSx, and route-table lookup all use
	// the live subnet IDs instead of churning to subnet names.
	var resolvedPrivateSubnetIDs []string
	if cfg.ProvisionedVpc != nil && len(cfg.ProvisionedVpc.PrivateSubnets) > 0 {
		resolvedPrivateSubnetIDs, err = aws.ResolveSubnetIDsByName(
			ctx, awsCreds, s.DstTarget.Region(), cfg.ProvisionedVpc.VpcID, cfg.ProvisionedVpc.PrivateSubnets)
		if err != nil {
			return fmt.Errorf("persistent: resolve provisioned VPC subnets: %w", err)
		}
	}

	// NOTE: the workload IRSA roles (and the live EKS OIDC-URL + caller-identity
	// lookups that fed their trust policies) moved to the eks step. The persistent
	// step no longer needs a managed-cluster OIDC-URL listing or GetCallerIdentity
	// here; removing them is what let the persistent_reprise step go away (the
	// reprise existed only to re-run that lookup once the cluster OIDC issuer
	// existed). The eks step now folds cfg.ExtraClusterOidcUrls into the trust.

	// required_tags = resource_tags | {true-name, environment} then + managed-by.
	requiredTags := map[string]string{}
	for k, v := range cfg.ResourceTags {
		requiredTags[k] = v
	}
	requiredTags["posit.team/true-name"] = trueName
	requiredTags["posit.team/environment"] = environment
	requiredTags["posit.team/managed-by"] = persistentManagedByValue

	// Resolve VPC CIDR (mirrors AWSWorkload.vpc_cidr: provisioned_vpc.cidr →
	// vpc_cidr → derived 10.<octet>.0.0/16). The derived form is computed from a
	// char-sum of the fully-qualified name when neither is set.
	vpcCIDR := ""
	switch {
	case cfg.ProvisionedVpc != nil:
		vpcCIDR = cfg.ProvisionedVpc.Cidr
	case cfg.VpcCidr != "":
		vpcCIDR = cfg.VpcCidr
	default:
		octet := 0
		for _, c := range compoundName {
			octet += int(c)
		}
		octet %= 255
		vpcCIDR = fmt.Sprintf("10.%d.0.0/16", octet)
	}

	params := awsWorkloadPersistentParams{
		compoundName:             compoundName,
		prefix:                   "ptd",
		accountID:                accountID,
		region:                   s.DstTarget.Region(),
		environment:              environment,
		existingDBIdentifier:     existingPersistentDBIdentifier(ctx, s.DstTarget),
		resolvedPrivateSubnetIDs: resolvedPrivateSubnetIDs,
		cfg:                      cfg,
		requiredTags:             requiredTags,
		iamPermissionsBound:      fmt.Sprintf("arn:aws:iam::%s:policy/PositTeamDedicatedAdmin", accountID),
		vpcCIDR:                  vpcCIDR,
		teamOperatorPolicyName:   fmt.Sprintf("team-operator.%s.posit.team", compoundName),
		fsxNfsSGName:             fmt.Sprintf("fsx-nfs.%s.posit.team", compoundName),
		mimirS3BucketName:        fmt.Sprintf("%s-mimir", compoundName),
		lokiS3BucketName:         fmt.Sprintf("%s-loki", compoundName),
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return awsWorkloadPersistentDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return s.runPersistentStack(ctx, stack, creds)
}

// awsWorkloadPersistentDeploy replicates AWSWorkloadPersistent.__init__ from
// python-pulumi/src/ptd/pulumi_resources/aws_workload_persistent.py. Resource
// logical names (first ctor arg) match the Python source verbatim. Every
// resource carries a pulumi.Aliases option pointing at the old Python URN under
// the ptd:AWSWorkloadPersistent component so existing state is adopted, not
// replaced.
func awsWorkloadPersistentDeploy(ctx *pulumi.Context, _ types.Target, params awsWorkloadPersistentParams) error {
	cn := params.compoundName
	tags := params.requiredTags
	protect := params.cfg.ProtectPersistentResources // Python default True (never set false in config)

	// componentURN is the old Python AWSWorkloadPersistent component URN. Direct
	// children alias to it via ParentURN.
	componentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s",
		ctx.Stack(), persistentAWSWorkloadProjectName, persistentAWSWorkloadCompType, cn)

	// withAlias: alias for a resource that was a direct child of the persistent
	// component (parent == the component).
	withAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(componentURN)}})
	}

	// withVPCParentAlias: many persistent resources were parented to self.vpc
	// (the ptd:AWSVpc child component), so their old URN has the VPC component as
	// parent: ptd:AWSWorkloadPersistent$ptd:AWSVpc::<cn>. ParentURN points there.
	vpcComponentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s",
		ctx.Stack(), persistentAWSWorkloadProjectName, persistentAWSVpcOuterCompType, cn)
	withVPCParentAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(vpcComponentURN)}})
	}

	// ── VPC ───────────────────────────────────────────────────────────────────
	vpc, privateSubnetIDs, err := buildPersistentVPC(ctx, params)
	if err != nil {
		return fmt.Errorf("persistent: VPC: %w", err)
	}
	vpcID := vpc.VpcID()

	// ── Bastion / tailscale branch ─────────────────────────────────────────────
	// Mirrors __init__: tailscale_enabled → SubnetRouter (bastion_id nil);
	// customer_managed_bastion_id → use it; else AwsBastion (bastion_id = id).
	var bastionID pulumi.StringInput = pulumi.String("")
	switch {
	case params.cfg.TailscaleEnabled:
		if err := buildPersistentTailscale(ctx, workloadTailscaleParams(params), vpc); err != nil {
			return fmt.Errorf("persistent: tailscale: %w", err)
		}
	case params.cfg.CustomerManagedBastionId != "":
		bastionID = pulumi.String(params.cfg.CustomerManagedBastionId)
	default:
		var firstSubnet pulumi.StringInput
		if len(privateSubnetIDs) > 0 {
			firstSubnet = privateSubnetIDs[0]
		}
		id, berr := buildPersistentBastion(ctx, params, vpcID, firstSubnet, withVPCParentAlias)
		if berr != nil {
			return fmt.Errorf("persistent: bastion: %w", berr)
		}
		bastionID = id
	}

	// ── RDS ────────────────────────────────────────────────────────────────────
	db, dbAddress, dbSecretARN, err := buildPersistentRDS(ctx, params, vpc, privateSubnetIDs, withVPCParentAlias)
	if err != nil {
		return fmt.Errorf("persistent: RDS: %w", err)
	}

	// ── S3: ppm + chronicle (prefixed buckets) ────────────────────────────────
	ppmBucket, err := definePrefixedBucket(ctx, params, "ppm", protect, withAlias)
	if err != nil {
		return fmt.Errorf("persistent: ppm bucket: %w", err)
	}
	chronicleBucket, err := definePrefixedBucket(ctx, params, "chronicle", protect, withAlias)
	if err != nil {
		return fmt.Errorf("persistent: chronicle bucket: %w", err)
	}

	// ── team operator IAM ──────────────────────────────────────────────────────
	if _, err := awsiam.NewPolicy(ctx, params.teamOperatorPolicyName, &awsiam.PolicyArgs{
		Name:        pulumi.String(params.teamOperatorPolicyName),
		Description: pulumi.String(fmt.Sprintf("Posit Team Dedicated policy for %s Team Operator", cn)),
		Policy:      pulumi.String(teamOperatorPolicyJSON()),
		Tags:        awsTagMap(tags, map[string]string{"Name": fmt.Sprintf("%s-team-operator-policy", cn)}),
	},
		withAlias(),
		// Python merged an alias to parent=chronicle_bucket for vintage stacks.
		pulumi.Aliases([]pulumi.Alias{{Parent: chronicleBucket}}),
	); err != nil {
		return fmt.Errorf("persistent: team operator policy: %w", err)
	}

	// ── Route53 / ACM zones + certs ────────────────────────────────────────────
	internalSites, certARNs, certValidationRecords, err := buildPersistentZonesAndCerts(ctx, params, withAlias)
	if err != nil {
		return fmt.Errorf("persistent: zones/certs: %w", err)
	}

	// ── FSx OpenZFS (role + SG + filesystem) ──────────────────────────────────
	fsxFS, fsxNfsSG, err := buildPersistentFSx(ctx, params, vpc, vpcID, privateSubnetIDs, protect, withAlias, withVPCParentAlias)
	if err != nil {
		return fmt.Errorf("persistent: FSx: %w", err)
	}

	// ── FSx NFS SG (eks-nodes-fsx-nfs) + fsx endpoint ──────────────────────────
	// fsxNfsSG above is the per-workload fsx_openzfs_sg; the EKS-nodes FSX NFS SG
	// is a separate always-on SG that also gates the fsx VPC endpoint.
	eksFsxNfsSG, err := buildPersistentEKSFsxNfsSG(ctx, params, vpc, vpcID, withVPCParentAlias)
	if err != nil {
		return fmt.Errorf("persistent: eks fsx nfs sg: %w", err)
	}
	// The per-workload FSx SG (fsx_openzfs_sg) is created for adoption/state parity;
	// its ID isn't needed downstream (the EKS FSX NFS SG gates the fsx endpoint).
	_ = fsxNfsSG

	// fsx endpoint (gated on enabled && "fsx" not excluded) using the EKS FSX NFS SG.
	vpcEndpointsEnabled, excluded := persistentVPCEndpointConfig(params)
	if vpcEndpointsEnabled && !slices.Contains(excluded, "fsx") {
		if err := vpc.WithEndpoint("fsx", eksFsxNfsSG.ID()); err != nil {
			return fmt.Errorf("persistent: fsx endpoint: %w", err)
		}
	}

	// ── EFS NFS SG (only if any cluster enables EFS) ──────────────────────────
	if err := buildPersistentEFSNfsSG(ctx, params, vpc, vpcID, withVPCParentAlias); err != nil {
		return fmt.Errorf("persistent: efs nfs sg: %w", err)
	}

	// NOTE: The 8 workload-scoped IRSA roles (FSx, LBC, ExternalDNS,
	// TraefikForwardAuth, Mimir, Loki, EBS-CSI, Alloy) were relocated to the eks
	// step (lib/steps/eks_irsa_aws.go), where they belong by lifecycle (the trust
	// policy binds to the cluster's OIDC issuer). This removed the need for the
	// persistent_reprise step. The persistent step still owns the Mimir/Loki S3
	// buckets and the Mimir password (created below); only the IRSA roles and
	// their permission policies moved.

	// ── Mimir (password + bucket) ──────────────────────────────────────────────
	mimirPassword, mimirBucket, err := buildPersistentMimir(ctx, params, protect, withAlias)
	if err != nil {
		return fmt.Errorf("persistent: mimir: %w", err)
	}

	// ── Loki (bucket) ──────────────────────────────────────────────────────────
	if err := buildPersistentLoki(ctx, params, protect, withAlias); err != nil {
		return fmt.Errorf("persistent: loki: %w", err)
	}

	// ── Outputs (must match Python register_outputs verbatim) ──────────────────
	exportPersistentOutputs(ctx, params, persistentOutputData{
		bastionID:             bastionID,
		chronicleBucket:       chronicleBucket.Bucket,
		db:                    db.Identifier,
		dbAddress:             dbAddress,
		dbSecretARN:           dbSecretARN,
		dbURL:                 db.Address.ApplyT(func(a string) string { return fmt.Sprintf("postgres://%s/postgres?sslmode=require", a) }).(pulumi.StringOutput),
		certARNs:              certARNs,
		fsDNSName:             fsxFS.dnsName,
		fsRootVolumeID:        fsxFS.rootVolumeID,
		internalSites:         internalSites,
		certValidationRecords: certValidationRecords,
		mimirBucket:           mimirBucket.Bucket,
		mimirPassword:         mimirPassword.Result,
		packagemanagerBucket:  ppmBucket.Bucket,
		privateSubnetIDs:      privateSubnetIDs,
		rdsHost:               db.Address,
		vpcID:                 vpcID,
	})

	return nil
}

// persistentVPCEndpointConfig resolves the effective (enabled, excluded) VPC
// endpoint configuration. Nil config → enabled with no exclusions (Python
// VPCEndpointsConfig() default).
func persistentVPCEndpointConfig(params awsWorkloadPersistentParams) (bool, []string) {
	if params.cfg.VPCEndpoints == nil {
		return true, nil
	}
	return params.cfg.VPCEndpoints.Enabled, params.cfg.VPCEndpoints.ExcludedServices
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// protoNum returns the numeric IP protocol for a NACL/SG protocol string.
func protoNum(p string) int {
	switch p {
	case "tcp":
		return 6
	case "udp":
		return 17
	default:
		return -1
	}
}

// buildPersistentVPC builds the VPC (greenfield or existing-VPC adoption) plus
// the NACL rule loop, secure defaults, standard VPC endpoints, and flow logs,
// mirroring AWSWorkloadPersistent._define_vpc / _lookup_existing_vpc_resources.
// It returns the builder and the private subnet IDs.
func buildPersistentVPC(ctx *pulumi.Context, params awsWorkloadPersistentParams) (*aws.AwsVpc, pulumi.StringArray, error) {
	cn := params.compoundName

	// AZ ids: Python uses get_availability_zones().zone_ids[:vpc_az_count]. We
	// cannot enumerate AZs without a cloud call in deploy; use a Pulumi data
	// source. The VPC builder needs AZ id strings up front, so look them up here.
	azCount := params.cfg.VpcAzCount
	if azCount == 0 {
		azCount = 3
	}
	azs, err := awsGetAZIDs(ctx, azCount)
	if err != nil {
		return nil, nil, fmt.Errorf("get availability zones: %w", err)
	}

	publicTags := map[string]string{
		"kubernetes.io/role/elb":    "1",
		"posit.team/network-access": "public",
		"posit.team/managed-by":     persistentManagedByValue,
	}
	privateTags := map[string]string{
		"kubernetes.io/role/internal-elb": "1",
		"posit.team/network-access":       "private",
		"posit.team/managed-by":           persistentManagedByValue,
	}

	vpcTags := map[string]string{}
	for k, v := range params.requiredTags {
		vpcTags[k] = v
	}
	vpcTags["Name"] = cn

	// Existing-VPC adoption (provisioned_vpc set): private-only network tags.
	if params.cfg.ProvisionedVpc != nil {
		networkTags := map[string]map[string]string{"private": privateTags}
		vpc, verr := aws.NewVPC(ctx, aws.VPCConfig{
			Name:                     cn,
			CIDR:                     params.vpcCIDR,
			AZs:                      azs,
			Tags:                     vpcTags,
			NetworkTags:              networkTags,
			OuterCompType:            persistentAWSVpcOuterCompType,
			ProjectName:              persistentAWSWorkloadProjectName,
			ExistingVPCID:            params.cfg.ProvisionedVpc.VpcID,
			ExistingPrivateSubnetIDs: params.resolvedPrivateSubnetIDs,
		})
		if verr != nil {
			return nil, nil, verr
		}
		return vpc, vpc.PrivateSubnetIDs(), nil
	}

	// Greenfield.
	networkTags := map[string]map[string]string{"public": publicTags, "private": privateTags}
	vpc, err := aws.NewVPC(ctx, aws.VPCConfig{
		Name:          cn,
		CIDR:          params.vpcCIDR,
		AZs:           azs,
		Tags:          vpcTags,
		NetworkTags:   networkTags,
		OuterCompType: persistentAWSVpcOuterCompType,
		ProjectName:   persistentAWSWorkloadProjectName,
	})
	if err != nil {
		return nil, nil, err
	}

	if err := vpc.WithNATGateways(); err != nil {
		return nil, nil, fmt.Errorf("NAT gateways: %w", err)
	}
	// NACL: 443 & 80 ingress to 0.0.0.0/0 (public).
	if err := vpc.WithNACLRule("public", 443, 443, 6, "0.0.0.0/0", false); err != nil {
		return nil, nil, err
	}
	if err := vpc.WithNACLRule("public", 80, 80, 6, "0.0.0.0/0", false); err != nil {
		return nil, nil, err
	}
	if err := vpc.WithSecureDefaultSecurityGroup(); err != nil {
		return nil, nil, err
	}
	if err := vpc.WithSecureDefaultNACL(); err != nil {
		return nil, nil, err
	}

	// NACL loop: ports {111, 2049, 20001-20002, all} × {tcp,udp} × {public,private}
	// to the VPC CIDR. Python iterated port_range in (111, 2049, range(20001,20003),
	// range(65536)); a range collapses to from..to (range(65536) => 0..65535).
	portRanges := []struct{ from, to int }{
		{111, 111},
		{2049, 2049},
		{20001, 20002},
		{0, 65535},
	}
	for _, pr := range portRanges {
		for _, proto := range []string{"tcp", "udp"} {
			for _, privacy := range []string{"public", "private"} {
				if err := vpc.WithNACLRule(privacy, pr.from, pr.to, protoNum(proto), params.vpcCIDR, false); err != nil {
					return nil, nil, err
				}
			}
		}
	}

	// egress-all to 0.0.0.0/0 (public then private), matching Python's two
	// with_nacl_rule(egress=True, protocol="-1") calls.
	if err := vpc.WithNACLRule("public", 0, 0, -1, "0.0.0.0/0", true); err != nil {
		return nil, nil, err
	}
	if err := vpc.WithNACLRule("private", 0, 0, -1, "0.0.0.0/0", true); err != nil {
		return nil, nil, err
	}

	// Standard VPC endpoints (minus exclusions).
	enabled, excluded := persistentVPCEndpointConfig(params)
	if enabled {
		for _, svc := range standardVPCEndpointServices {
			if contains(excluded, svc) {
				continue
			}
			if err := vpc.WithEndpoint(svc); err != nil {
				return nil, nil, fmt.Errorf("vpc endpoint %q: %w", svc, err)
			}
		}
	}

	// Flow logs.
	pb := params.iamPermissionsBound
	if err := vpc.WithFlowLog(&pb, nil, params.cfg.ExistingFlowLogTargetARNs); err != nil {
		return nil, nil, fmt.Errorf("flow log: %w", err)
	}

	return vpc, vpc.PrivateSubnetIDs(), nil
}

// awsGetAZIDs returns the first count availability-zone IDs via the Pulumi
// aws.GetAvailabilityZones data source (mirrors Python get_availability_zones().zone_ids[:n]).
func awsGetAZIDs(ctx *pulumi.Context, count int) ([]string, error) {
	res, err := awsprovider.GetAvailabilityZones(ctx, &awsprovider.GetAvailabilityZonesArgs{})
	if err != nil {
		return nil, err
	}
	ids := res.ZoneIds
	if len(ids) > count {
		ids = ids[:count]
	}
	return ids, nil
}

// buildPersistentBastion ports AwsBastion (aws_bastion.py). It creates the
// bastion IAM role (+SSM attach), a security group, an instance profile and the
// EC2 instance, returning the instance ID. Resource logical names match the
// Python AwsBastion._define_iam / _define_instance.
func buildPersistentBastion(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	vpcID pulumi.StringInput,
	subnetID pulumi.StringInput,
	withVPCParentAlias func() pulumi.ResourceOption,
) (pulumi.StringInput, error) {
	cn := params.compoundName
	tags := params.requiredTags

	// The Python AwsBastion was a ptd:AwsBastion component parented to self.vpc.
	// Its children's URNs are ptd:AWSWorkloadPersistent$ptd:AWSVpc$ptd:AwsBastion$<type>::<name>.
	bastionComponentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:AwsBastion::%s",
		ctx.Stack(), persistentAWSWorkloadProjectName, persistentAWSVpcOuterCompType, cn)
	withBastionAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(bastionComponentURN)}})
	}

	assumeRole, err := awsiam.GetPolicyDocument(ctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{
				Actions: []string{"sts:AssumeRole"},
				Principals: []awsiam.GetPolicyDocumentStatementPrincipal{
					{Type: "Service", Identifiers: []string{"ec2.amazonaws.com"}},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	roleName := fmt.Sprintf("%s-bastion.posit.team", cn)
	pb := params.iamPermissionsBound
	role, err := awsiam.NewRole(ctx, roleName, &awsiam.RoleArgs{
		Name:                pulumi.String(roleName),
		AssumeRolePolicy:    pulumi.String(assumeRole.Json),
		PermissionsBoundary: pulumi.String(pb),
		Tags:                awsTagMap(tags, nil),
	}, withBastionAlias(), pulumi.DeleteBeforeReplace(true))
	if err != nil {
		return nil, err
	}

	ssmAttachName := fmt.Sprintf("%s-bastion-ssm", cn)
	if _, err := awsiam.NewRolePolicyAttachment(ctx, ssmAttachName, &awsiam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
	}, pulumi.Parent(role), pulumi.DeleteBeforeReplace(true)); err != nil {
		return nil, err
	}

	sgName := fmt.Sprintf("%s-bastion", cn)
	sg, err := awsec2.NewSecurityGroup(ctx, sgName, &awsec2.SecurityGroupArgs{
		VpcId: vpcID,
		Egress: awsec2.SecurityGroupEgressArray{
			awsec2.SecurityGroupEgressArgs{
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				Protocol:   pulumi.String("-1"),
				CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			},
		},
		Tags: awsTagMap(tags, map[string]string{"Name": sgName}),
	}, withBastionAlias())
	if err != nil {
		return nil, err
	}

	profileName := fmt.Sprintf("%s-bastion-profile", cn)
	profile, err := awsiam.NewInstanceProfile(ctx, profileName, &awsiam.InstanceProfileArgs{
		Name: pulumi.String(fmt.Sprintf("%s-bastion-profile.posit.team", cn)),
		Role: role.Name,
	}, withBastionAlias(), pulumi.DeleteBeforeReplace(true))
	if err != nil {
		return nil, err
	}

	mostRecent := true
	nameRegex := `al2023-ami-2023.*-kernel-6\.18-arm64`
	ami, err := awsec2.LookupAmi(ctx, &awsec2.LookupAmiArgs{
		MostRecent: &mostRecent,
		NameRegex:  &nameRegex,
		Filters: []awsec2.GetAmiFilter{
			{Name: "owner-id", Values: []string{"137112412989"}},
			{Name: "architecture", Values: []string{"arm64"}},
		},
	})
	if err != nil {
		return nil, err
	}

	instName := fmt.Sprintf("%s-bastion", cn)
	inst, err := awsec2.NewInstance(ctx, instName, &awsec2.InstanceArgs{
		IamInstanceProfile:  profile.Name,
		InstanceType:        pulumi.String(params.cfg.BastionInstanceType),
		Ami:                 pulumi.String(ami.Id),
		SubnetId:            subnetID,
		VpcSecurityGroupIds: pulumi.StringArray{sg.ID()},
		Tags:                awsTagMap(tags, map[string]string{"Name": instName}),
	}, withBastionAlias(), pulumi.DependsOn([]pulumi.Resource{profile}))
	if err != nil {
		return nil, err
	}
	return inst.ID(), nil
}

// buildPersistentRDS ports _define_db: SG, SubnetGroup, ParameterGroup and the
// RDS instance, returning the instance, its address, and the master-user-secret
// ARN (looked up via the rds.LookupInstance Pulumi data source — NOT boto3).
// applyRDSIdentifier sets the RDS instance's name onto args and returns any
// extra resource options needed.
//
// Background: the Python step used identifier_prefix=f"{cn}-" with
// ignore_changes=["identifier_prefix"]. identifier_prefix is write-only in AWS —
// it is consumed at create time and never returned on reads — so every refresh
// left it empty ("") in Pulumi state, and ignore_changes was added to silence the
// resulting perpetual diff (commit ff1ba3e9 / aws_control_room_persistent.py from
// inception). Newer aws providers validate identifier_prefix in Check even for an
// ignore_changes'd property, and reject the retained empty string ("first
// character must be a letter" + charset), hard-failing every preview.
//
// Fix: for an already-deployed instance we adopt its stable, AWS-returned
// physical name via an explicit Identifier (read from this stack's prior "db"
// output), dropping identifier_prefix entirely — Identifier matches the live
// value, so there is no replace. Only greenfield (no prior instance) falls back
// to identifier_prefix + ignore_changes, exactly as before.
func applyRDSIdentifier(args *awsrds.InstanceArgs, existingID, cn string) []pulumi.ResourceOption {
	if existingID != "" {
		args.Identifier = pulumi.String(existingID)
		return nil
	}
	args.IdentifierPrefix = pulumi.String(fmt.Sprintf("%s-", cn))
	return []pulumi.ResourceOption{pulumi.IgnoreChanges([]string{"identifierPrefix"})}
}

func buildPersistentRDS(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	vpc *aws.AwsVpc,
	privateSubnetIDs pulumi.StringArray,
	withVPCParentAlias func() pulumi.ResourceOption,
) (*awsrds.Instance, pulumi.StringOutput, pulumi.StringOutput, error) {
	cn := params.compoundName
	tags := params.requiredTags
	protect := params.cfg.ProtectPersistentResources

	sgName := fmt.Sprintf("%s-allow-postgresql-traffic-vpc", cn)
	dbsg, err := awsec2.NewSecurityGroup(ctx, sgName, &awsec2.SecurityGroupArgs{
		Description: pulumi.String(fmt.Sprintf("Allow PostgreSQL traffic from VPC for %s", cn)),
		VpcId:       vpc.VpcID(),
		Ingress: awsec2.SecurityGroupIngressArray{
			awsec2.SecurityGroupIngressArgs{
				Description: pulumi.String("Allow PostgreSQL traffic on port 5432"),
				FromPort:    pulumi.Int(5432),
				ToPort:      pulumi.Int(5432),
				Protocol:    pulumi.String("tcp"),
				CidrBlocks:  pulumi.StringArray{pulumi.String(params.vpcCIDR)},
			},
		},
		Egress: awsec2.SecurityGroupEgressArray{
			awsec2.SecurityGroupEgressArgs{
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				Protocol:   pulumi.String("-1"),
				CidrBlocks: pulumi.StringArray{pulumi.String(params.vpcCIDR)},
			},
		},
		Tags: awsTagMap(tags, map[string]string{"Name": sgName}),
	}, withVPCParentAlias())
	if err != nil {
		return nil, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}

	sngName := fmt.Sprintf("%s-main-database-subnet-group", cn)
	dbsng, err := awsrds.NewSubnetGroup(ctx, sngName, &awsrds.SubnetGroupArgs{
		SubnetIds: privateSubnetIDs,
		Tags:      awsTagMap(tags, map[string]string{"Name": sngName}),
	}, withVPCParentAlias())
	if err != nil {
		return nil, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}

	pgName := fmt.Sprintf("%s-main-database-parameter-group", cn)
	dbpg, err := awsrds.NewParameterGroup(ctx, pgName, &awsrds.ParameterGroupArgs{
		Family: pulumi.String("postgres15"),
		Parameters: awsrds.ParameterGroupParameterArray{
			awsrds.ParameterGroupParameterArgs{Name: pulumi.String("auto_explain.log_min_duration"), Value: pulumi.String("5000")},
			awsrds.ParameterGroupParameterArgs{Name: pulumi.String("log_min_duration_statement"), Value: pulumi.String("1500")},
			awsrds.ParameterGroupParameterArgs{Name: pulumi.String("log_lock_waits"), Value: pulumi.String("1")},
		},
	}, withVPCParentAlias())
	if err != nil {
		return nil, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}

	multiAZ := params.environment == "production"

	dbArgs := &awsrds.InstanceArgs{
		AllocatedStorage:           pulumi.Int(params.cfg.DBAllocatedStorage),
		BackupRetentionPeriod:      pulumi.Int(7),
		CopyTagsToSnapshot:         pulumi.Bool(true),
		DbName:                     pulumi.String("postgres"),
		DbSubnetGroupName:          dbsng.Name,
		Engine:                     pulumi.String("postgres"),
		EngineVersion:              pulumi.String(params.cfg.DBEngineVersion),
		FinalSnapshotIdentifier:    pulumi.String(fmt.Sprintf("%s-final-snapshot", cn)),
		InstanceClass:              pulumi.String(params.cfg.DBInstanceClass),
		ManageMasterUserPassword:   pulumi.Bool(true),
		ParameterGroupName:         dbpg.Name,
		SkipFinalSnapshot:          pulumi.Bool(!protect),
		StorageEncrypted:           pulumi.Bool(true),
		StorageType:                pulumi.String("gp3"),
		Tags:                       awsTagMap(tags, map[string]string{"Name": cn}),
		Username:                   pulumi.String("postgres"),
		VpcSecurityGroupIds:        pulumi.StringArray{dbsg.ID()},
		PerformanceInsightsEnabled: pulumi.Bool(params.cfg.DBPerformanceInsightsEnabled),
		DeletionProtection:         pulumi.Bool(params.cfg.DBDeletionProtection),
		MultiAz:                    pulumi.Bool(multiAZ),
	}
	if params.cfg.DBMaxAllocatedStorage != nil {
		dbArgs.MaxAllocatedStorage = pulumi.Int(*params.cfg.DBMaxAllocatedStorage)
	}

	opts := append([]pulumi.ResourceOption{withVPCParentAlias(), pulumi.Protect(protect)},
		applyRDSIdentifier(dbArgs, params.existingDBIdentifier, cn)...)
	db, err := awsrds.NewInstance(ctx, cn, dbArgs, opts...)
	if err != nil {
		return nil, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}

	// master-user-secret ARN via the RDS Pulumi data source (replaces Python's
	// db.master_user_secrets[0].secret_arn read from the resource output).
	dbSecretARN := db.Identifier.ApplyT(func(id string) (string, error) {
		inst, lerr := awsrds.LookupInstance(ctx, &awsrds.LookupInstanceArgs{DbInstanceIdentifier: &id})
		if lerr != nil {
			return "", lerr
		}
		if len(inst.MasterUserSecrets) == 0 {
			return "", nil
		}
		return inst.MasterUserSecrets[0].SecretArn, nil
	}).(pulumi.StringOutput)

	return db, db.Address, dbSecretARN, nil
}

// definePrefixedBucket ports _define_prefixed_bucket: a private, KMS-encrypted
// S3 bucket with bucket_prefix = "<prefix>-<cn>-<name>-", protected and
// retain-on-delete. Logical name "<cn>-<name>-bucket".
func definePrefixedBucket(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	name string,
	protect bool,
	withAlias func() pulumi.ResourceOption,
) (*awss3.Bucket, error) {
	cn := params.compoundName
	logicalName := fmt.Sprintf("%s-%s-bucket", cn, name)
	bucket, err := awss3.NewBucket(ctx, logicalName, &awss3.BucketArgs{
		BucketPrefix: pulumi.String(fmt.Sprintf("%s-%s-%s-", params.prefix, cn, name)),
		Acl:          pulumi.String("private"),
		Tags:         awsTagMap(params.requiredTags, nil),
		ServerSideEncryptionConfiguration: &awss3.BucketServerSideEncryptionConfigurationArgs{
			Rule: &awss3.BucketServerSideEncryptionConfigurationRuleArgs{
				ApplyServerSideEncryptionByDefault: &awss3.BucketServerSideEncryptionConfigurationRuleApplyServerSideEncryptionByDefaultArgs{
					SseAlgorithm: pulumi.String("aws:kms"),
				},
				BucketKeyEnabled: pulumi.Bool(true),
			},
		},
	}, withAlias(), pulumi.Protect(protect), pulumi.RetainOnDelete(true))
	if err != nil {
		return nil, err
	}
	return bucket, nil
}

// defineNamedBucket ports _define_named_bucket: a private, KMS-encrypted S3
// bucket with an explicit bucket name "<prefix>-<name>", protected and
// retain-on-delete. Logical name "<cn>-<name>-bucket". extraAliasNames are merged
// as additional Name-only aliases (Python passed pulumi.Alias(name=...) opts).
func defineNamedBucket(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	name string,
	protect bool,
	withAlias func() pulumi.ResourceOption,
	extraAliasNames ...string,
) (*awss3.Bucket, error) {
	cn := params.compoundName
	logicalName := fmt.Sprintf("%s-%s-bucket", cn, name)
	opts := []pulumi.ResourceOption{withAlias(), pulumi.Protect(protect), pulumi.RetainOnDelete(true)}
	for _, an := range extraAliasNames {
		opts = append(opts, pulumi.Aliases([]pulumi.Alias{{Name: pulumi.String(an)}}))
	}
	bucket, err := awss3.NewBucket(ctx, logicalName, &awss3.BucketArgs{
		Bucket: pulumi.String(fmt.Sprintf("%s-%s", params.prefix, name)),
		Acl:    pulumi.String("private"),
		Tags:   awsTagMap(params.requiredTags, nil),
		ServerSideEncryptionConfiguration: &awss3.BucketServerSideEncryptionConfigurationArgs{
			Rule: &awss3.BucketServerSideEncryptionConfigurationRuleArgs{
				ApplyServerSideEncryptionByDefault: &awss3.BucketServerSideEncryptionConfigurationRuleApplyServerSideEncryptionByDefaultArgs{
					SseAlgorithm: pulumi.String("aws:kms"),
				},
				BucketKeyEnabled: pulumi.Bool(true),
			},
		},
	}, opts...)
	if err != nil {
		return nil, err
	}
	return bucket, nil
}

// persistentFSxResult holds the FSx file system outputs the step exports.
type persistentFSxResult struct {
	dnsName      pulumi.StringOutput
	rootVolumeID pulumi.StringOutput
}

// buildPersistentFSx ports _define_fsx_openzfs: the FSx OpenZFS CSI driver IAM
// role (+AmazonFSxFullAccess attach), the per-workload fsx_openzfs SG, and the
// FSx OpenZFS file system (MULTI_AZ_1 vs SINGLE_AZ_HA_2). It returns the file
// system outputs and the per-workload fsx SG.
func buildPersistentFSx(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	vpc *aws.AwsVpc,
	vpcID pulumi.StringInput,
	privateSubnetIDs pulumi.StringArray,
	protect bool,
	withAlias func() pulumi.ResourceOption,
	withVPCParentAlias func() pulumi.ResourceOption,
) (persistentFSxResult, *awsec2.SecurityGroup, error) {
	cn := params.compoundName
	tags := params.requiredTags

	// NOTE: the FSx OpenZFS CSI driver IRSA role + AmazonFSxFullAccess attachment
	// moved to the eks step (deployIRSAFsx in eks_irsa_aws.go). The persistent
	// step still owns the FSx file system and the per-workload fsx_openzfs SG.

	deploymentType := "SINGLE_AZ_HA_2"
	if boolPtrOrDefault(params.cfg.FsxOpenzfsMultiAz, true) {
		deploymentType = "MULTI_AZ_1"
	}
	if params.cfg.FsxOpenzfsOverrideDeploymentType != nil && *params.cfg.FsxOpenzfsOverrideDeploymentType != "" {
		deploymentType = *params.cfg.FsxOpenzfsOverrideDeploymentType
	}

	// Per-workload fsx_openzfs SG. Python logical name = fsx_nfs_sg_name, name_prefix = "<fsx_nfs_sg_name>-".
	fsxSGName := params.fsxNfsSGName
	fsxSG, err := awsec2.NewSecurityGroup(ctx, fsxSGName, &awsec2.SecurityGroupArgs{
		NamePrefix:  pulumi.String(fmt.Sprintf("%s-", fsxSGName)),
		Description: pulumi.String(fmt.Sprintf("Allow FSx NFS traffic for %s", cn)),
		VpcId:       vpcID,
		Ingress:     nfsIngressRules(params.vpcCIDR),
		Egress:      awsec2.SecurityGroupEgressArray{},
		Tags:        awsTagMap(tags, map[string]string{"Name": fsxSGName}),
	}, withVPCParentAlias())
	if err != nil {
		return persistentFSxResult{}, nil, err
	}

	// FSx defaulting (capacity 100 / throughput 320 / backup "02:00") is owned by
	// runAWSInlineGo, which applies the Python dataclass defaults to cfg before
	// these params are built; read the resolved values directly here.
	storageCap := params.cfg.FsxOpenzfsStorageCapacity
	throughput := params.cfg.FsxOpenzfsThroughputCapacity
	backupTime := params.cfg.FsxOpenzfsDailyAutomaticBackupStartTime

	mkRootVolCfg := func(opts ...string) *awsfsx.OpenZfsFileSystemRootVolumeConfigurationArgs {
		strOpts := make(pulumi.StringArray, len(opts))
		for i, o := range opts {
			strOpts[i] = pulumi.String(o)
		}
		return &awsfsx.OpenZfsFileSystemRootVolumeConfigurationArgs{
			CopyTagsToSnapshots: pulumi.Bool(true),
			DataCompressionType: pulumi.String("NONE"),
			NfsExports: &awsfsx.OpenZfsFileSystemRootVolumeConfigurationNfsExportsArgs{
				ClientConfigurations: awsfsx.OpenZfsFileSystemRootVolumeConfigurationNfsExportsClientConfigurationArray{
					awsfsx.OpenZfsFileSystemRootVolumeConfigurationNfsExportsClientConfigurationArgs{
						Clients: pulumi.String("*"),
						Options: strOpts,
					},
				},
			},
		}
	}

	var fsArgs *awsfsx.OpenZfsFileSystemArgs
	if strings.HasPrefix(deploymentType, "MULTI") {
		// MULTI_AZ_1 (AWSFsxOpenZfsMulti equivalent): the Python AWSFsxOpenZfsMulti
		// component created the file system named "<cn>-filesystem" under a
		// ptd:aws:AWSFsxOpenZfsMulti component. We keep that name + alias chain.
		subnetIDs := privateSubnetIDs
		if len(privateSubnetIDs) > 2 {
			subnetIDs = privateSubnetIDs[:2]
		}
		rootVolCfg := mkRootVolCfg("rw", "no_root_squash", "crossmnt")
		var preferred pulumi.StringPtrInput
		if len(subnetIDs) > 0 {
			preferred = subnetIDs[0].ToStringOutput().ToStringPtrOutput()
		}
		fsArgs = &awsfsx.OpenZfsFileSystemArgs{
			AutomaticBackupRetentionDays:  pulumi.Int(30),
			DeploymentType:                pulumi.String(deploymentType),
			PreferredSubnetId:             preferred,
			SubnetIds:                     subnetIDs,
			SecurityGroupIds:              pulumi.StringArray{fsxSG.ID()},
			StorageCapacity:               pulumi.Int(storageCap),
			StorageType:                   pulumi.String("SSD"),
			ThroughputCapacity:            pulumi.Int(throughput),
			CopyTagsToBackups:             pulumi.Bool(true),
			CopyTagsToVolumes:             pulumi.Bool(true),
			DailyAutomaticBackupStartTime: pulumi.String(backupTime),
			RouteTableIds:                 vpc.PrivateRouteTableIDs(),
			RootVolumeConfiguration:       rootVolCfg,
			Tags:                          awsTagMap(tags, map[string]string{"Name": cn}),
		}
		// Multi-AZ: Python created aws.fsx.OpenZfsFileSystem named "<cn>-filesystem"
		// as a child of the ptd:aws:AWSFsxOpenZfsMulti component, which itself was a
		// child of self.vpc. Alias to that chain.
		multiComponentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:aws:AWSFsxOpenZfsMulti::%s",
			ctx.Stack(), persistentAWSWorkloadProjectName, persistentAWSVpcOuterCompType, cn)
		fs, ferr := awsfsx.NewOpenZfsFileSystem(ctx, fmt.Sprintf("%s-filesystem", cn), fsArgs,
			pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(multiComponentURN)}}),
			pulumi.Protect(protect),
			pulumi.IgnoreChanges([]string{"dailyAutomaticBackupStartTime"}),
		)
		if ferr != nil {
			return persistentFSxResult{}, nil, ferr
		}
		return persistentFSxResult{dnsName: fs.DnsName, rootVolumeID: fs.RootVolumeId}, fsxSG, nil
	}

	// SINGLE_AZ_HA_2: aws.fsx.OpenZfsFileSystem named "<cn>", parented to self.vpc.
	rootVolCfg := mkRootVolCfg("rw", "no_root_squash")
	singleSubnets := pulumi.StringArray{}
	if len(privateSubnetIDs) > 0 {
		singleSubnets = pulumi.StringArray{privateSubnetIDs[0]}
	}
	fsArgs = &awsfsx.OpenZfsFileSystemArgs{
		AutomaticBackupRetentionDays:  pulumi.Int(30),
		DailyAutomaticBackupStartTime: pulumi.String(backupTime),
		SubnetIds:                     singleSubnets,
		DeploymentType:                pulumi.String(deploymentType),
		SecurityGroupIds:              pulumi.StringArray{fsxSG.ID()},
		StorageCapacity:               pulumi.Int(storageCap),
		ThroughputCapacity:            pulumi.Int(throughput),
		CopyTagsToBackups:             pulumi.Bool(true),
		CopyTagsToVolumes:             pulumi.Bool(true),
		RootVolumeConfiguration:       rootVolCfg,
		Tags:                          awsTagMap(tags, map[string]string{"Name": cn}),
	}
	fs, err := awsfsx.NewOpenZfsFileSystem(ctx, cn, fsArgs,
		withVPCParentAlias(),
		pulumi.Protect(protect),
		pulumi.IgnoreChanges([]string{"dailyAutomaticBackupStartTime"}),
	)
	if err != nil {
		return persistentFSxResult{}, nil, err
	}
	return persistentFSxResult{dnsName: fs.DnsName, rootVolumeID: fs.RootVolumeId}, fsxSG, nil
}

// nfsIngressRules builds the TCP/UDP NFS ingress rules (ports 111, 2049, 20001-20003)
// over the VPC CIDR, matching the Python comprehension in _define_fsx_openzfs /
// _define_fsx_nfs_sg.
func nfsIngressRules(cidr string) awsec2.SecurityGroupIngressArray {
	ranges := []struct{ from, to int }{{111, 111}, {2049, 2049}, {20001, 20003}}
	var rules awsec2.SecurityGroupIngressArray
	for _, r := range ranges {
		for _, proto := range []string{"tcp", "udp"} {
			rules = append(rules, awsec2.SecurityGroupIngressArgs{
				Description: pulumi.String(fmt.Sprintf("Allow %s on ports %d-%d", strings.ToUpper(proto), r.from, r.to)),
				FromPort:    pulumi.Int(r.from),
				ToPort:      pulumi.Int(r.to),
				Protocol:    pulumi.String(proto),
				CidrBlocks:  pulumi.StringArray{pulumi.String(cidr)},
			})
		}
	}
	return rules
}

// buildPersistentEKSFsxNfsSG ports _define_fsx_nfs_sg: the always-on EKS-nodes
// FSX NFS security group (logical name = SecurityGroupPrefixes.EKS_NODES_FSX_NFS).
func buildPersistentEKSFsxNfsSG(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	vpc *aws.AwsVpc,
	vpcID pulumi.StringInput,
	withVPCParentAlias func() pulumi.ResourceOption,
) (*awsec2.SecurityGroup, error) {
	cn := params.compoundName
	name := "eks-nodes-fsx-nfs.posit.team"
	sg, err := awsec2.NewSecurityGroup(ctx, name, &awsec2.SecurityGroupArgs{
		NamePrefix:  pulumi.String(fmt.Sprintf("%s-", name)),
		Description: pulumi.String(fmt.Sprintf("Allow NFS traffic for %s", cn)),
		VpcId:       vpcID,
		Ingress:     nfsIngressRules(params.vpcCIDR),
		Egress: awsec2.SecurityGroupEgressArray{
			awsec2.SecurityGroupEgressArgs{
				Description: pulumi.String("Allow all TCP and UDP egress"),
				FromPort:    pulumi.Int(0),
				ToPort:      pulumi.Int(0),
				Protocol:    pulumi.String("-1"),
				CidrBlocks:  pulumi.StringArray{pulumi.String(params.vpcCIDR)},
			},
		},
		Tags: awsTagMap(params.requiredTags, map[string]string{"Name": name}),
	}, withVPCParentAlias())
	if err != nil {
		return nil, err
	}
	return sg, nil
}

// buildPersistentEFSNfsSG ports _define_efs_nfs_sg: the EKS-nodes EFS NFS SG,
// created only if any cluster has enable_efs_csi_driver or efs_config.
func buildPersistentEFSNfsSG(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	vpc *aws.AwsVpc,
	vpcID pulumi.StringInput,
	withVPCParentAlias func() pulumi.ResourceOption,
) error {
	efsEnabled := false
	for _, c := range params.cfg.Clusters {
		if c.Spec.EnableEfsCsiDriver || c.Spec.EfsConfig != nil {
			efsEnabled = true
			break
		}
	}
	if !efsEnabled {
		return nil
	}

	cn := params.compoundName
	name := "eks-nodes-efs-nfs.posit.team"
	_, err := awsec2.NewSecurityGroup(ctx, name, &awsec2.SecurityGroupArgs{
		NamePrefix:  pulumi.String(fmt.Sprintf("%s-", name)),
		Description: pulumi.String(fmt.Sprintf("Allow EFS NFS traffic for %s", cn)),
		VpcId:       vpcID,
		Ingress: awsec2.SecurityGroupIngressArray{
			awsec2.SecurityGroupIngressArgs{
				Description: pulumi.String("Allow NFS (TCP port 2049)"),
				FromPort:    pulumi.Int(2049),
				ToPort:      pulumi.Int(2049),
				Protocol:    pulumi.String("tcp"),
				CidrBlocks:  pulumi.StringArray{pulumi.String(params.vpcCIDR)},
			},
		},
		Egress: awsec2.SecurityGroupEgressArray{
			awsec2.SecurityGroupEgressArgs{
				Description: pulumi.String("Allow NFS egress within VPC"),
				FromPort:    pulumi.Int(2049),
				ToPort:      pulumi.Int(2049),
				Protocol:    pulumi.String("tcp"),
				CidrBlocks:  pulumi.StringArray{pulumi.String(params.vpcCIDR)},
			},
		},
		Tags: awsTagMap(params.requiredTags, map[string]string{"Name": name}),
	}, withVPCParentAlias())
	return err
}

// buildPersistentMimir ports the persistent half of _define_mimir: the random
// mimir password and the mimir bucket (named, with a name-alias to
// "<cn>-mimir-storage"). Returns the password and bucket.
//
// The mimir IRSA role, its bucket read/write policy, and the attachment moved to
// the eks step (deployIRSAMimir in eks_irsa_aws.go); the eks-side policy derives
// the bucket ARN from the deterministic bucket name (arn:aws:s3:::<cn>-mimir).
func buildPersistentMimir(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	protect bool,
	withAlias func() pulumi.ResourceOption,
) (*random.RandomPassword, *awss3.Bucket, error) {
	cn := params.compoundName

	pw, err := random.NewRandomPassword(ctx, fmt.Sprintf("%s-mimir", cn), &random.RandomPasswordArgs{
		Special:         pulumi.Bool(true),
		OverrideSpecial: pulumi.String("-/_"),
		Length:          pulumi.Int(36),
	}, withAlias())
	if err != nil {
		return nil, nil, err
	}

	bucket, err := defineNamedBucket(ctx, params, params.mimirS3BucketName, protect, withAlias,
		fmt.Sprintf("%s-mimir-storage", cn))
	if err != nil {
		return nil, nil, err
	}

	return pw, bucket, nil
}

// buildPersistentLoki ports _define_loki_bucket: the loki bucket (named,
// name-alias "<cn>-loki-bucket").
//
// The loki bucket read/write policy, the loki IRSA role, and the attachment moved
// to the eks step (deployIRSALoki in eks_irsa_aws.go); the eks-side policy derives
// the bucket ARN from the deterministic bucket name (arn:aws:s3:::<cn>-loki).
func buildPersistentLoki(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	protect bool,
	withAlias func() pulumi.ResourceOption,
) error {
	cn := params.compoundName

	_, err := defineNamedBucket(ctx, params, params.lokiS3BucketName, protect, withAlias,
		fmt.Sprintf("%s-loki-bucket", cn))
	if err != nil {
		return err
	}

	return nil
}

// persistentCertValidationRecords maps a domain to its cert validation records
// output (each a list of {name,type,value}).
type persistentCertValidationRecords map[string]pulumi.ArrayOutput

// buildPersistentZonesAndCerts ports _define_zones_and_domain_certs (and
// _define_hosted_zone / the validation-record builder). It groups sites by
// domain, creates a Route53 zone per domain (or adopts an existing one when a
// zone_id is configured), creates an ACM certificate + validation records per
// domain (unless a certificate_arn is supplied), and optionally a
// CertificateValidation. Returns the internal-site list (with created zones),
// the collected cert ARNs, and the per-domain validation records.
//
// JUDGMENT CALL / known gap: the Go SiteConfigSpec does not yet carry the
// advanced AWSSiteConfig fields (private_zone, vpc_associations,
// auto_associate_provisioned_vpc, certificate_validation_enabled). Zones are
// therefore always created public, and certificate_validation_enabled is treated
// as its Python default (True). No production workload exercises private zones in
// the persistent step today; if one is added, these fields must be threaded onto
// SiteConfigSpec. When hosted_zone_management_enabled is false, only the supplied
// certificate_arns are collected (no zones), matching the Python else-branch.
func buildPersistentZonesAndCerts(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	withAlias func() pulumi.ResourceOption,
) ([]persistentInternalSite, pulumi.StringArray, persistentCertValidationRecords, error) {
	cn := params.compoundName
	tags := params.requiredTags
	protect := params.cfg.ProtectPersistentResources
	hzManagement := boolPtrOrDefault(params.cfg.HostedZoneManagementEnabled, true)

	// Build internal sites: "main" (domain=sites[main].domain, zone_id=hosted_zone_id
	// override else sites[main].zone_id) + each non-main site.
	var internalSites []persistentInternalSite
	mainSite := params.cfg.Sites["main"]
	mainZoneID := mainSite.Spec.ZoneID
	if params.cfg.HostedZoneID != nil && *params.cfg.HostedZoneID != "" {
		mainZoneID = *params.cfg.HostedZoneID
	}
	internalSites = append(internalSites, persistentInternalSite{
		siteName: "main", domain: mainSite.Spec.Domain, zoneID: mainZoneID,
	})
	for _, siteName := range helpers.SortedKeys(params.cfg.Sites) {
		if siteName == "main" {
			continue
		}
		s := params.cfg.Sites[siteName]
		internalSites = append(internalSites, persistentInternalSite{
			siteName: siteName, domain: s.Spec.Domain, zoneID: s.Spec.ZoneID,
		})
	}

	certARNs := pulumi.StringArray{}
	validationRecords := persistentCertValidationRecords{}

	// Disabled zone management: collect supplied certificate ARNs only.
	if !hzManagement {
		for _, siteName := range helpers.SortedKeys(params.cfg.Sites) {
			s := params.cfg.Sites[siteName]
			if s.Spec.CertificateARN != "" {
				certARNs = append(certARNs, pulumi.String(s.Spec.CertificateARN))
			}
		}
		return internalSites, certARNs, validationRecords, nil
	}

	// Group sites by domain (sorted) so each unique domain is processed once.
	domainToSites := map[string][]int{} // domain → indices into internalSites
	var domainOrder []string
	for idx, s := range internalSites {
		if _, ok := domainToSites[s.domain]; !ok {
			domainOrder = append(domainOrder, s.domain)
		}
		domainToSites[s.domain] = append(domainToSites[s.domain], idx)
	}
	sort.Strings(domainOrder)

	for _, domain := range domainOrder {
		idxs := domainToSites[domain]
		primaryIdx := idxs[0]
		primary := internalSites[primaryIdx]

		// Determine the zone resource alias name: "main" → compound_name, else "<cn>-other".
		zoneAliasName := cn
		if primary.siteName != "main" {
			zoneAliasName = fmt.Sprintf("%s-other", cn)
		}

		zoneLogical := fmt.Sprintf("%s-zone", domain)
		primarySpec := params.cfg.Sites[primary.siteName].Spec
		var zone *awsroute53.Zone
		if primary.zoneID == "" {
			// Private vs public zone (mirrors _define_hosted_zone). Private zones
			// associate VPCs: the provisioned VPC (when auto_associate, default
			// true) prepended to any explicit vpc_associations, deduped.
			comment := "Publicly accessible"
			var vpcs awsroute53.ZoneVpcArray
			if primarySpec.PrivateZone {
				comment = "Private"
				autoAssociate := primarySpec.AutoAssociateProvisionedVpc == nil || *primarySpec.AutoAssociateProvisionedVpc
				var vpcIDs []string
				if autoAssociate && params.cfg.ProvisionedVpc != nil && params.cfg.ProvisionedVpc.VpcID != "" {
					vpcIDs = append(vpcIDs, params.cfg.ProvisionedVpc.VpcID)
				}
				for _, v := range primarySpec.VpcAssociations {
					if v == "" || slices.Contains(vpcIDs, v) {
						continue
					}
					vpcIDs = append(vpcIDs, v)
				}
				for _, v := range vpcIDs {
					vpcs = append(vpcs, awsroute53.ZoneVpcArgs{
						VpcId:     pulumi.String(v),
						VpcRegion: pulumi.String(params.region),
					})
				}
			}
			zoneArgs := &awsroute53.ZoneArgs{
				Name:    pulumi.String(domain),
				Comment: pulumi.String(fmt.Sprintf("Hosted Zone for the Posit Team Dedicated service in %s. %s", cn, comment)),
				Tags:    awsTagMap(tags, nil),
			}
			if len(vpcs) > 0 {
				zoneArgs.Vpcs = vpcs
			}
			z, zerr := awsroute53.NewZone(ctx, zoneLogical, zoneArgs,
				withAlias(),
				pulumi.Protect(protect),
				pulumi.Aliases([]pulumi.Alias{{Name: pulumi.String(zoneAliasName)}}),
				pulumi.IgnoreChanges([]string{"comment"}),
			)
			if zerr != nil {
				return nil, nil, nil, zerr
			}
			zone = z
		} else {
			// Adopt the existing hosted zone, matching Python's
			// aws.route53.Zone.get(f"{domain}-zone", id=zone_id). Registering it (a
			// read resource, top-level — no component parent, matching the live
			// state URN) keeps the managed zone in state and lets the cert-validation
			// CNAME records stay parented to it. Without this the zone is discarded
			// and its validation records are deleted (which would break ACM
			// validation).
			z, zerr := awsroute53.GetZone(ctx, zoneLogical, pulumi.ID(primary.zoneID), nil)
			if zerr != nil {
				return nil, nil, nil, zerr
			}
			zone = z
		}
		// Attach the (possibly nil) zone to every site sharing this domain.
		for _, i := range idxs {
			internalSites[i].zone = zone
		}

		// If a cert ARN is supplied, collect and skip cert creation.
		// (Per-site certificate_arn is read from the Go SiteConfigSpec.)
		suppliedCertARN := params.cfg.Sites[primary.siteName].Spec.CertificateARN
		if suppliedCertARN != "" {
			certARNs = append(certARNs, pulumi.String(suppliedCertARN))
			continue
		}
		if primary.zoneID == "" && zone == nil {
			// zone_id and zone both nil → skip domain cert (matches Python info-skip).
			continue
		}

		dashifyDomain := strings.ReplaceAll(domain, ".", "-")
		cert, cerr := acm.NewCertificate(ctx, fmt.Sprintf("%s-domain-cert-%s", cn, dashifyDomain), &acm.CertificateArgs{
			DomainName:              pulumi.String(domain),
			SubjectAlternativeNames: pulumi.StringArray{pulumi.String(fmt.Sprintf("*.%s", domain))},
			ValidationMethod:        pulumi.String("DNS"),
			Tags:                    awsTagMap(tags, nil),
		}, withAlias())
		if cerr != nil {
			return nil, nil, nil, cerr
		}
		certARNs = append(certARNs, cert.Arn)

		// Build the validation records (one Record per unique resource_record_value),
		// parented to the zone, with a name-alias for the "main" site.
		zoneIDInput := pulumi.String(primary.zoneID).ToStringOutput()
		if zone != nil {
			zoneIDInput = zone.ZoneId
		}
		recs := buildCertValidationRecords(ctx, params, cert, zone, zoneIDInput, primary.siteName, dashifyDomain)
		validationRecords[domain] = recs

		// Only create the CertificateValidation when certificate_validation_enabled
		// (Python default True). Validation records are always built (above) for
		// the stack outputs; the CertificateValidation resource that *waits* on
		// them is gated. Sites that disable certificate_validation_enabled set this false.
		if primarySpec.CertificateValidationEnabled == nil || *primarySpec.CertificateValidationEnabled {
			if _, verr := acm.NewCertificateValidation(ctx, fmt.Sprintf("%s-cert-validation-%s", cn, dashifyDomain), &acm.CertificateValidationArgs{
				CertificateArn: cert.Arn,
				ValidationRecordFqdns: recs.ApplyT(func(rs []interface{}) ([]string, error) {
					var fqdns []string
					for _, r := range rs {
						if m, ok := r.(map[string]interface{}); ok {
							if f, ok := m["fqdn"].(string); ok {
								fqdns = append(fqdns, f)
							}
						}
					}
					sort.Strings(fqdns)
					return fqdns, nil
				}).(pulumi.StringArrayOutput),
			}, pulumi.Parent(cert)); verr != nil {
				return nil, nil, nil, verr
			}
		}
	}

	return internalSites, certARNs, validationRecords, nil
}

// buildCertValidationRecords creates the Route53 validation Records for a
// certificate (one per unique resource_record_value) and returns an ArrayOutput
// of {name,type,value,fqdn} maps. Mirrors _return_build_validation_function.
func buildCertValidationRecords(
	ctx *pulumi.Context,
	params awsWorkloadPersistentParams,
	cert *acm.Certificate,
	zone *awsroute53.Zone,
	zoneID pulumi.StringInput,
	siteName, dashifyDomain string,
) pulumi.ArrayOutput {
	cn := params.compoundName

	return cert.DomainValidationOptions.ApplyT(func(dvos []acm.CertificateDomainValidationOption) ([]interface{}, error) {
		// Deduplicate by resource_record_value (preserving first occurrence).
		seen := map[string]bool{}
		var uniq []acm.CertificateDomainValidationOption
		for _, dvo := range dvos {
			v := ""
			if dvo.ResourceRecordValue != nil {
				v = *dvo.ResourceRecordValue
			}
			if seen[v] {
				continue
			}
			seen[v] = true
			uniq = append(uniq, dvo)
		}

		var recOutputs []interface{}
		for i, dvo := range uniq {
			name := ""
			if dvo.ResourceRecordName != nil {
				name = *dvo.ResourceRecordName
			}
			value := ""
			if dvo.ResourceRecordValue != nil {
				value = *dvo.ResourceRecordValue
			}
			rtype := ""
			if dvo.ResourceRecordType != nil {
				rtype = *dvo.ResourceRecordType
			}

			recLogical := fmt.Sprintf("%s-cert-validation-record-%s-%d", cn, dashifyDomain, i)
			recOpts := []pulumi.ResourceOption{pulumi.DeleteBeforeReplace(true)}
			if zone != nil {
				recOpts = append(recOpts, pulumi.Parent(zone))
			}
			if siteName == "main" {
				recOpts = append(recOpts, pulumi.Aliases([]pulumi.Alias{{Name: pulumi.String(fmt.Sprintf("%s-cert-validation-record-%d", cn, i))}}))
			}
			rec, rerr := awsroute53.NewRecord(ctx, recLogical, &awsroute53.RecordArgs{
				Name:    pulumi.String(name),
				Records: pulumi.StringArray{pulumi.String(value)},
				Ttl:     pulumi.Int(60),
				Type:    pulumi.String(rtype),
				ZoneId:  zoneID,
			}, recOpts...)
			if rerr != nil {
				return nil, rerr
			}
			recOutputs = append(recOutputs, pulumi.Map{
				"name":  pulumi.String(name),
				"type":  pulumi.String(rtype),
				"value": pulumi.String(value),
				"fqdn":  rec.Fqdn,
			})
		}
		return recOutputs, nil
	}).(pulumi.ArrayOutput)
}

// tailscaleParams is the minimal data the shared Tailscale subnet-router builder
// needs. It is derived from either the AWS workload or AWS control-room
// persistent params so both targets share a single implementation. projectName
// and outerCompType determine the old Python alias URN chain (which differs
// between workload and control room).
type tailscaleParams struct {
	compoundName        string
	requiredTags        map[string]string
	vpcCIDR             string
	accountID           string
	iamPermissionsBound string
	projectName         string // old Python Pulumi project name (alias URNs)
	outerCompType       string // e.g. "ptd:AWSWorkloadPersistent$ptd:AWSVpc"
}

// workloadTailscaleParams projects the AWS workload persistent params into the
// shared tailscaleParams.
func workloadTailscaleParams(params awsWorkloadPersistentParams) tailscaleParams {
	return tailscaleParams{
		compoundName:        params.compoundName,
		requiredTags:        params.requiredTags,
		vpcCIDR:             params.vpcCIDR,
		accountID:           params.accountID,
		iamPermissionsBound: params.iamPermissionsBound,
		projectName:         persistentAWSWorkloadProjectName,
		outerCompType:       persistentAWSVpcOuterCompType,
	}
}

// buildPersistentTailscale ports aws_tailscale.SubnetRouter (the ECS-Fargate
// Tailscale subnet router). The persistent caller always passes site_id=None, so
// the pulumi_tailscale provider's get4_via6 lookup is never reached; only AWS
// resources (SG + egress rule, ECS cluster, CloudWatch LogGroup, two IAM roles,
// task definition, service) are created. The Python SubnetRouter was a
// rstudio:tailscale/Fargate component parented to self.vpc — its children alias
// to <outerCompType>$rstudio:tailscale/Fargate$<type>::<name>.
//
// Shared by the AWS workload and AWS control-room persistent steps.
func buildPersistentTailscale(
	ctx *pulumi.Context,
	params tailscaleParams,
	vpc *aws.AwsVpc,
) error {
	cn := params.compoundName
	name := fmt.Sprintf("%s-tailscale", cn)

	tsComponentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$rstudio:tailscale/Fargate::%s",
		ctx.Stack(), params.projectName, params.outerCompType, name)
	withTSAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(tsComponentURN)}})
	}

	// tags = required_tags | {rs:project: security, rs:subsystem: tailscale}
	tags := map[string]string{}
	for k, v := range params.requiredTags {
		tags[k] = v
	}
	tags["rs:project"] = "security"
	tags["rs:subsystem"] = "tailscale"

	sg, err := awsec2.NewSecurityGroup(ctx, fmt.Sprintf("%s-sg", name), &awsec2.SecurityGroupArgs{
		Name:        pulumi.String(name),
		Description: pulumi.String("Tailscale Fargate Security Group"),
		VpcId:       vpc.VpcID(),
		Tags:        pulumi.StringMap{"Name": pulumi.String(name)},
	}, withTSAlias())
	if err != nil {
		return err
	}

	if _, err := awsvpc.NewSecurityGroupEgressRule(ctx, fmt.Sprintf("%s-sg-egress", name), &awsvpc.SecurityGroupEgressRuleArgs{
		SecurityGroupId: sg.ID(),
		CidrIpv4:        pulumi.String("0.0.0.0/0"),
		IpProtocol:      pulumi.String("-1"),
	}, withTSAlias()); err != nil {
		return err
	}

	cluster, err := awsecs.NewCluster(ctx, fmt.Sprintf("%s-fargate", name), &awsecs.ClusterArgs{
		Name: pulumi.String(name),
		Tags: awsTagMap(tags, nil),
	}, withTSAlias())
	if err != nil {
		return err
	}

	tsSecret, err := awssecretsmanager.LookupSecret(ctx, &awssecretsmanager.LookupSecretArgs{
		Name: pulumi.StringRef("tailscale-authkey"),
	})
	if err != nil {
		return err
	}

	logGroupName := fmt.Sprintf("/aws/ecs/%s", name)
	logGroup, err := awscloudwatch.NewLogGroup(ctx, fmt.Sprintf("%s-log-group", name), &awscloudwatch.LogGroupArgs{
		Name:            pulumi.String(logGroupName),
		RetentionInDays: pulumi.Int(60),
		Tags:            awsTagMap(tags, nil),
	}, withTSAlias())
	if err != nil {
		return err
	}
	_ = logGroup

	region, err := awsprovider.GetRegion(ctx, &awsprovider.GetRegionArgs{})
	if err != nil {
		return err
	}
	regionName := region.Name
	ssmParameterARN := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/%s/ts-state", regionName, params.accountID, name)

	// container_definitions: built from the VPC CIDR (site_id is nil here).
	containerDefs := pulumi.String(params.vpcCIDR).ToStringOutput().ApplyT(func(cidr string) (string, error) {
		defs := []map[string]interface{}{
			{
				"name":      "tailscale",
				"image":     "tailscale/tailscale:stable",
				"essential": true,
				"environment": []map[string]interface{}{
					{"name": "TS_HOSTNAME", "value": fmt.Sprintf("%s-%s-%s", cn, regionName, params.accountID)},
					{"name": "TS_ROUTES", "value": cidr},
					{"name": "TS_EXTRA_ARGS", "value": "--advertise-tags=tag:ptd"},
				},
				"secrets": []map[string]interface{}{
					{"name": "TS_AUTHKEY", "valueFrom": tsSecret.Arn},
				},
				"logConfiguration": map[string]interface{}{
					"logDriver": "awslogs",
					"options": map[string]interface{}{
						"awslogs-create-group":  "true",
						"awslogs-group":         logGroupName,
						"awslogs-region":        regionName,
						"awslogs-stream-prefix": name,
						"mode":                  "non-blocking",
						"max-buffer-size":       "25m",
					},
				},
				"healthcheck": map[string]interface{}{
					"command":     []string{"tailscale", "status"},
					"interval":    30,
					"timeout":     5,
					"retries":     3,
					"startPeriod": 0,
				},
			},
		}
		return jsonMarshal(defs)
	}).(pulumi.StringOutput)

	execRole, err := buildTailscaleExecutionRole(ctx, params, name, tsSecret.Arn, withTSAlias)
	if err != nil {
		return err
	}
	taskRole, err := buildTailscaleTaskRole(ctx, params, name, ssmParameterARN, withTSAlias)
	if err != nil {
		return err
	}

	task, err := awsecs.NewTaskDefinition(ctx, fmt.Sprintf("%s-task", name), &awsecs.TaskDefinitionArgs{
		Family:                  pulumi.String(name),
		RequiresCompatibilities: pulumi.StringArray{pulumi.String("FARGATE")},
		NetworkMode:             pulumi.String("awsvpc"),
		RuntimePlatform: &awsecs.TaskDefinitionRuntimePlatformArgs{
			CpuArchitecture:       pulumi.String("ARM64"),
			OperatingSystemFamily: pulumi.String("LINUX"),
		},
		Cpu:                  pulumi.String("256"),
		Memory:               pulumi.String("512"),
		ContainerDefinitions: containerDefs,
		ExecutionRoleArn:     execRole.Arn,
		TaskRoleArn:          taskRole.Arn,
		Tags:                 awsTagMap(tags, nil),
	}, withTSAlias())
	if err != nil {
		return err
	}

	publicSubnetIDs := pulumi.StringArray{}
	for _, s := range vpc.PublicSubnets() {
		publicSubnetIDs = append(publicSubnetIDs, s.ID())
	}

	_, err = awsecs.NewService(ctx, fmt.Sprintf("%s-service", name), &awsecs.ServiceArgs{
		Name:                 pulumi.String(name),
		Cluster:              cluster.Arn,
		TaskDefinition:       task.Arn,
		LaunchType:           pulumi.String("FARGATE"),
		DesiredCount:         pulumi.Int(1),
		EnableEcsManagedTags: pulumi.Bool(true),
		EnableExecuteCommand: pulumi.Bool(true),
		WaitForSteadyState:   pulumi.Bool(true),
		NetworkConfiguration: &awsecs.ServiceNetworkConfigurationArgs{
			AssignPublicIp: pulumi.Bool(true),
			Subnets:        publicSubnetIDs,
			SecurityGroups: pulumi.StringArray{sg.ID()},
		},
		PropagateTags: pulumi.String("SERVICE"),
		Tags:          awsTagMap(tags, nil),
	}, withTSAlias())
	return err
}

// buildTailscaleExecutionRole ports SubnetRouter._create_execution_role.
func buildTailscaleExecutionRole(
	ctx *pulumi.Context,
	params tailscaleParams,
	name string,
	secretARN string,
	withTSAlias func() pulumi.ResourceOption,
) (*awsiam.Role, error) {
	assumeRole, err := awsiam.GetPolicyDocument(ctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{Actions: []string{"sts:AssumeRole"}, Principals: []awsiam.GetPolicyDocumentStatementPrincipal{
				{Type: "Service", Identifiers: []string{"ecs-tasks.amazonaws.com"}},
			}},
		},
	})
	if err != nil {
		return nil, err
	}
	inline, err := awsiam.GetPolicyDocument(ctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{Actions: []string{"secretsmanager:GetSecretValue"}, Resources: []string{secretARN}},
		},
	})
	if err != nil {
		return nil, err
	}
	args := &awsiam.RoleArgs{
		Name:              pulumi.String(fmt.Sprintf("%s-TaskExecution.posit.team", name)),
		Description:       pulumi.String(fmt.Sprintf("Role for %s Fargate Task Execution", name)),
		AssumeRolePolicy:  pulumi.String(assumeRole.Json),
		ManagedPolicyArns: pulumi.StringArray{pulumi.String("arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy")},
		InlinePolicies: awsiam.RoleInlinePolicyArray{
			awsiam.RoleInlinePolicyArgs{Name: pulumi.String("tailscale-secrets-access"), Policy: pulumi.String(inline.Json)},
		},
	}
	// Only set the boundary when non-empty; empty means None (control room), matching Python.
	if pb := params.iamPermissionsBound; pb != "" {
		args.PermissionsBoundary = pulumi.String(pb)
	}
	return awsiam.NewRole(ctx, fmt.Sprintf("%s-ecs-task-execution-role.posit.team", name), args, withTSAlias())
}

// buildTailscaleTaskRole ports SubnetRouter._create_task_role.
func buildTailscaleTaskRole(
	ctx *pulumi.Context,
	params tailscaleParams,
	name string,
	ssmParameterARN string,
	withTSAlias func() pulumi.ResourceOption,
) (*awsiam.Role, error) {
	assumeRole, err := awsiam.GetPolicyDocument(ctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{Actions: []string{"sts:AssumeRole"}, Principals: []awsiam.GetPolicyDocumentStatementPrincipal{
				{Type: "Service", Identifiers: []string{"ecs-tasks.amazonaws.com"}},
			}},
		},
	})
	if err != nil {
		return nil, err
	}
	inline, err := awsiam.GetPolicyDocument(ctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{Actions: []string{"ssm:GetParameter", "ssm:PutParameter"}, Resources: []string{ssmParameterARN}},
		},
	})
	if err != nil {
		return nil, err
	}
	args := &awsiam.RoleArgs{
		Name:              pulumi.String(fmt.Sprintf("%s-Task.posit.team", name)),
		Description:       pulumi.String(fmt.Sprintf("Role for %s Fargate Task", name)),
		AssumeRolePolicy:  pulumi.String(assumeRole.Json),
		ManagedPolicyArns: pulumi.StringArray{pulumi.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore")},
		InlinePolicies: awsiam.RoleInlinePolicyArray{
			awsiam.RoleInlinePolicyArgs{Name: pulumi.String("tailscale-ssm-parameter-access"), Policy: pulumi.String(inline.Json)},
		},
	}
	// Only set the boundary when non-empty; empty means None (control room), matching Python.
	if pb := params.iamPermissionsBound; pb != "" {
		args.PermissionsBoundary = pulumi.String(pb)
	}
	return awsiam.NewRole(ctx, fmt.Sprintf("%s-ecs-task-role.posit.team", name), args, withTSAlias())
}

// persistentOutputData bundles the values the step exports.
type persistentOutputData struct {
	bastionID             pulumi.StringInput
	chronicleBucket       pulumi.StringOutput
	db                    pulumi.StringOutput
	dbAddress             pulumi.StringOutput
	dbSecretARN           pulumi.StringOutput
	dbURL                 pulumi.StringOutput
	certARNs              pulumi.StringArray
	fsDNSName             pulumi.StringOutput
	fsRootVolumeID        pulumi.StringOutput
	internalSites         []persistentInternalSite
	certValidationRecords persistentCertValidationRecords
	mimirBucket           pulumi.StringOutput
	mimirPassword         pulumi.StringOutput
	packagemanagerBucket  pulumi.StringOutput
	privateSubnetIDs      pulumi.StringArray
	rdsHost               pulumi.StringOutput
	vpcID                 pulumi.StringInput
}

// exportPersistentOutputs ctx.Export's every key the Python register_outputs
// emitted, with verbatim key names. Downstream steps read db_address and
// db_secret_arn; the step's own Run reads mimir_password, chronicle_bucket,
// fs_dns_name, fs_root_volume_id, db, db_url and packagemanager_bucket.
func exportPersistentOutputs(ctx *pulumi.Context, params awsWorkloadPersistentParams, d persistentOutputData) {
	hzManagement := boolPtrOrDefault(params.cfg.HostedZoneManagementEnabled, true)

	ctx.Export("bastion_id", d.bastionID)
	ctx.Export("chronicle_bucket", d.chronicleBucket)
	ctx.Export("db", d.db)
	ctx.Export("db_address", d.dbAddress)
	ctx.Export("db_secret_arn", d.dbSecretARN)
	ctx.Export("db_url", d.dbURL)
	ctx.Export("cert_arns", d.certARNs)
	ctx.Export("fs_dns_name", d.fsDNSName)
	ctx.Export("fs_root_volume_id", d.fsRootVolumeID)

	if hzManagement {
		// domain_ns_map: {domain: name_servers} for sites with a created zone.
		domainNSMap := pulumi.Map{}
		hzNameServers := pulumi.Map{}
		for _, s := range d.internalSites {
			if s.zone != nil {
				domainNSMap[s.domain] = s.zone.NameServers
				hzNameServers[s.siteName] = pulumi.Map{
					"domain":       pulumi.String(s.domain),
					"name_servers": s.zone.NameServers,
					"zone_id":      s.zone.ZoneId,
				}
			} else {
				hzNameServers[s.siteName] = pulumi.Map{
					"domain":       pulumi.String(s.domain),
					"name_servers": pulumi.StringArray{},
					"zone_id":      pulumi.String(s.zoneID),
				}
			}
		}
		ctx.Export("domain_ns_map", domainNSMap)
		ctx.Export("hosted_zone_name_servers", hzNameServers)

		cvr := pulumi.Map{}
		for domain, recs := range d.certValidationRecords {
			cvr[domain] = recs
		}
		ctx.Export("certificate_validation_records", cvr)
	} else {
		ctx.Export("domain_ns_map", pulumi.Map{})
		ctx.Export("hosted_zone_name_servers", pulumi.Array{})
		ctx.Export("hosted_zone_info", pulumi.String("Hosted zones are externally managed"))
		ctx.Export("certificate_validation_records", pulumi.Map{})
	}

	ctx.Export("mimir_bucket", d.mimirBucket)
	ctx.Export("mimir_password", d.mimirPassword)
	ctx.Export("packagemanager_bucket", d.packagemanagerBucket)
	ctx.Export("private_subnet_ids", d.privateSubnetIDs)
	ctx.Export("rds_host", d.rdsHost)
	ctx.Export("vpc", d.vpcID)

	// subnet_ids: Python emitted [s["SubnetId"] for s in workload.subnets("private")].
	// In greenfield + adoption modes alike these are the private subnet IDs.
	ctx.Export("subnet_ids", d.privateSubnetIDs)
}

// ── AWS control-room persistent ─────────────────────────────────────────────

// awsControlRoomPersistentParams bundles the pre-fetched data the AWS
// control-room persistent deploy function needs. Mirrors the workload param
// struct but is much smaller (no IAM/feature-branch resources).
type awsControlRoomPersistentParams struct {
	compoundName string
	accountID    string
	region       string
	cfg          types.AWSControlRoomConfig

	requiredTags map[string]string // resource_tags + posit.team/{true-name,environment} + managed-by

	iamPermissionsBound string // arn:aws:iam::<acct>:policy/PositTeamDedicatedAdmin
	vpcCIDR             string // derived 10.<octet_signature(compoundName)>.0.0/16

	// existingDBIdentifier is the already-deployed RDS instance's physical name,
	// read from this stack's prior "db" output (empty for a greenfield control
	// room). See applyRDSIdentifier.
	existingDBIdentifier string
}

// runAWSControlRoomInlineGo is the AWS-control-room entry point for the
// persistent step. It pre-fetches external data and dispatches to
// awsControlRoomPersistentDeploy.
//
// NOTE: this method is intentionally NOT yet wired into PersistentStep.Run
// (Phase F). It exists so the package compiles and the deploy function is
// reachable/testable now. Phase F dispatches workload-vs-control-room here.
func (s *PersistentStep) runAWSControlRoomInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("persistent: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSControlRoomConfig)
	if !ok {
		return fmt.Errorf("persistent: expected AWSControlRoomConfig, got %T", rawConfig)
	}

	// Apply Python AWSControlRoomConfig dataclass defaults for fields not set in
	// ptd.yaml (the control room configs rely on these). Without them Go's
	// zero-values (0 / "") would diff the live RDS instance.
	if cfg.DBAllocatedStorage == 0 {
		cfg.DBAllocatedStorage = 100
	}
	if cfg.DBEngineVersion == "" {
		cfg.DBEngineVersion = "16.14"
	}
	if cfg.DBInstanceClass == "" {
		cfg.DBInstanceClass = "db.t3.small"
	}
	// protect_persistent_resources defaults True in Python and is never set false
	// in any config; force it so durable resources keep Protect + skip_final_snapshot=false.
	cfg.ProtectPersistentResources = true

	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}
	accountID := awsCreds.AccountID()

	compoundName := s.DstTarget.Name()
	trueName, environment := compoundName, ""
	if idx := strings.LastIndex(compoundName, "-"); idx >= 0 {
		trueName = compoundName[:idx]
		environment = compoundName[idx+1:]
	}

	// required_tags = resource_tags | {true-name, environment} then + managed-by.
	requiredTags := map[string]string{}
	for k, v := range cfg.ResourceTags {
		requiredTags[k] = v
	}
	requiredTags["posit.team/true-name"] = trueName
	requiredTags["posit.team/environment"] = environment
	requiredTags["posit.team/managed-by"] = persistentControlRoomManagedByValue

	params := awsControlRoomPersistentParams{
		compoundName:         compoundName,
		accountID:            accountID,
		region:               s.DstTarget.Region(),
		cfg:                  cfg,
		requiredTags:         requiredTags,
		iamPermissionsBound:  fmt.Sprintf("arn:aws:iam::%s:policy/PositTeamDedicatedAdmin", accountID),
		vpcCIDR:              controlRoomVPCCIDR(compoundName),
		existingDBIdentifier: existingPersistentDBIdentifier(ctx, s.DstTarget),
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return awsControlRoomPersistentDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return s.runPersistentStack(ctx, stack, creds)
}

// controlRoomVPCCIDR derives the control-room VPC CIDR (10.<octet>.0.0/16) where
// octet = octet_signature(name) = sum(ord(c)) % 255, matching the Python
// ipaddress.ip_network(f"10.{octet_signature(name)}.0.0/16").
func controlRoomVPCCIDR(name string) string {
	octet := 0
	for _, c := range name {
		octet += int(c)
	}
	octet %= 255
	return fmt.Sprintf("10.%d.0.0/16", octet)
}

// awsControlRoomPersistentDeploy replicates AWSControlRoomPersistent.__init__
// from python-pulumi/src/ptd/pulumi_resources/aws_control_room_persistent.py.
// Resource logical names (first ctor arg) match the Python source verbatim.
// Every resource carries a pulumi.Aliases option pointing at the old Python URN
// under the ptd:AWSControlRoomPersistent component so existing state is adopted.
func awsControlRoomPersistentDeploy(ctx *pulumi.Context, _ types.Target, params awsControlRoomPersistentParams) error {
	cn := params.compoundName
	protect := params.cfg.ProtectPersistentResources // Python default True

	// componentURN is the old Python AWSControlRoomPersistent component URN. Direct
	// children alias to it via ParentURN.
	componentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s",
		ctx.Stack(), persistentAWSControlRoomProjectName, persistentAWSControlRoomCompType, cn)
	withAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(componentURN)}})
	}

	// Many persistent resources were parented to self.vpc (the ptd:AWSVpc child
	// component): their old URN has the VPC component as parent
	// ptd:AWSControlRoomPersistent$ptd:AWSVpc::<cn>.
	vpcComponentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s",
		ctx.Stack(), persistentAWSControlRoomProjectName, persistentAWSControlRoomVpcOuterCompType, cn)
	withVPCParentAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(vpcComponentURN)}})
	}

	// ── VPC ───────────────────────────────────────────────────────────────────
	vpc, privateSubnetIDs, err := buildControlRoomPersistentVPC(ctx, params)
	if err != nil {
		return fmt.Errorf("persistent: control room VPC: %w", err)
	}

	// ── Tailscale ───────────────────────────────────────────────────────────────
	// Python control-room __init__ calls _define_tailscale unconditionally (no
	// config gate), so the subnet router is always created for the control room.
	if err := buildPersistentTailscale(ctx, controlRoomTailscaleParams(params), vpc); err != nil {
		return fmt.Errorf("persistent: control room tailscale: %w", err)
	}

	// ── RDS ────────────────────────────────────────────────────────────────────
	db, dbAddress, dbSecretARN, err := buildControlRoomPersistentRDS(ctx, params, vpc, privateSubnetIDs, withVPCParentAlias)
	if err != nil {
		return fmt.Errorf("persistent: control room RDS: %w", err)
	}

	// ── Releases bucket ─────────────────────────────────────────────────────────
	releasesBucket, err := buildControlRoomReleasesBucket(ctx, params, protect, withAlias)
	if err != nil {
		return fmt.Errorf("persistent: control room releases bucket: %w", err)
	}

	// ── Outputs (must match Python register_outputs verbatim) ──────────────────
	ctx.Export("db", db.Identifier)
	ctx.Export("db_address", dbAddress)
	ctx.Export("db_secret_arn", dbSecretARN)
	ctx.Export("db_host", db.Endpoint)
	ctx.Export("nat_gw_public_ips", vpc.NatGwPublicIps())
	ctx.Export("vpc_name", pulumi.String(vpc.Name()))
	ctx.Export("vpc_id", vpc.VpcID())
	ctx.Export("subnet_ids", privateSubnetIDs)
	ctx.Export("releases_bucket", releasesBucket.Bucket)
	ctx.Export("releases_bucket_arn", releasesBucket.Arn)

	return nil
}

// controlRoomTailscaleParams projects the AWS control-room persistent params
// into the shared tailscaleParams.
func controlRoomTailscaleParams(params awsControlRoomPersistentParams) tailscaleParams {
	return tailscaleParams{
		compoundName: params.compoundName,
		requiredTags: params.requiredTags,
		vpcCIDR:      params.vpcCIDR,
		accountID:    params.accountID,
		// Python's control-room _define_tailscale does NOT pass permissions_boundary
		// to SubnetRouter (defaults to None), so the tailscale ECS roles carry no
		// boundary. (The workload _define_tailscale DOES pass one.) Leave empty.
		iamPermissionsBound: "",
		projectName:         persistentAWSControlRoomProjectName,
		outerCompType:       persistentAWSControlRoomVpcOuterCompType,
	}
}

// buildControlRoomPersistentVPC builds the control-room VPC plus its NACL rules,
// secure defaults, the hardcoded VPC endpoint set, NAT gateways, and flow logs,
// mirroring AWSControlRoomPersistent._define_vpc. Returns the builder and the
// private subnet IDs.
func buildControlRoomPersistentVPC(ctx *pulumi.Context, params awsControlRoomPersistentParams) (*aws.AwsVpc, pulumi.StringArray, error) {
	cn := params.compoundName

	// Python uses get_availability_zones().zone_ids[:3].
	azs, err := awsGetAZIDs(ctx, 3)
	if err != nil {
		return nil, nil, fmt.Errorf("get availability zones: %w", err)
	}

	// network_access_tags: public {kubernetes.io/role/elb: 1, network-access:
	// public}; private {network-access: private}. (No internal-elb tag — that is
	// workload-only.)
	publicTags := map[string]string{
		"kubernetes.io/role/elb":    "1",
		"posit.team/network-access": "public",
	}
	privateTags := map[string]string{
		"posit.team/network-access": "private",
	}

	// tags = required_tags | {Name: cn, kubernetes.io/cluster/<cn>: shared}.
	vpcTags := map[string]string{}
	for k, v := range params.requiredTags {
		vpcTags[k] = v
	}
	vpcTags["Name"] = cn
	vpcTags[fmt.Sprintf("kubernetes.io/cluster/%s", cn)] = "shared"

	networkTags := map[string]map[string]string{"public": publicTags, "private": privateTags}
	vpc, err := aws.NewVPC(ctx, aws.VPCConfig{
		Name:          cn,
		CIDR:          params.vpcCIDR,
		AZs:           azs,
		Tags:          vpcTags,
		NetworkTags:   networkTags,
		OuterCompType: persistentAWSControlRoomVpcOuterCompType,
		ProjectName:   persistentAWSControlRoomProjectName,
	})
	if err != nil {
		return nil, nil, err
	}

	if err := vpc.WithNATGateways(); err != nil {
		return nil, nil, fmt.Errorf("NAT gateways: %w", err)
	}
	// NACL: 443 & 80 ingress to 0.0.0.0/0 (public).
	if err := vpc.WithNACLRule("public", 443, 443, 6, "0.0.0.0/0", false); err != nil {
		return nil, nil, err
	}
	if err := vpc.WithNACLRule("public", 80, 80, 6, "0.0.0.0/0", false); err != nil {
		return nil, nil, err
	}
	// egress-all to 0.0.0.0/0 (public then private).
	if err := vpc.WithNACLRule("public", 0, 0, -1, "0.0.0.0/0", true); err != nil {
		return nil, nil, err
	}
	if err := vpc.WithNACLRule("private", 0, 0, -1, "0.0.0.0/0", true); err != nil {
		return nil, nil, err
	}
	// Full-range (0-65535) {tcp,udp} × {public,private} to the control-room CIDR.
	for _, proto := range []string{"tcp", "udp"} {
		for _, privacy := range []string{"public", "private"} {
			if err := vpc.WithNACLRule(privacy, 0, 65535, protoNum(proto), params.vpcCIDR, false); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := vpc.WithSecureDefaultSecurityGroup(); err != nil {
		return nil, nil, err
	}
	if err := vpc.WithSecureDefaultNACL(); err != nil {
		return nil, nil, err
	}

	// Hardcoded VPC endpoint set.
	for _, svc := range controlRoomVPCEndpointServices {
		if err := vpc.WithEndpoint(svc); err != nil {
			return nil, nil, fmt.Errorf("vpc endpoint %q: %w", svc, err)
		}
	}

	// Flow logs: Python called with_flow_log() with NO args (no permissions
	// boundary, no role ARN, no existing targets) — creates the LogGroup + role.
	if err := vpc.WithFlowLog(nil, nil, nil); err != nil {
		return nil, nil, fmt.Errorf("flow log: %w", err)
	}

	return vpc, vpc.PrivateSubnetIDs(), nil
}

// buildControlRoomPersistentRDS ports _define_db for the control room: SG,
// SubnetGroup, ParameterGroup (postgres16 family) and the RDS instance, returning
// the instance, its address, and the master-user-secret ARN (looked up via the
// rds.LookupInstance Pulumi data source).
func buildControlRoomPersistentRDS(
	ctx *pulumi.Context,
	params awsControlRoomPersistentParams,
	vpc *aws.AwsVpc,
	privateSubnetIDs pulumi.StringArray,
	withVPCParentAlias func() pulumi.ResourceOption,
) (*awsrds.Instance, pulumi.StringOutput, pulumi.StringOutput, error) {
	cn := params.compoundName
	tags := params.requiredTags
	protect := params.cfg.ProtectPersistentResources

	sgName := fmt.Sprintf("%s-allow-postgresql-traffic-vpc", cn)
	dbsg, err := awsec2.NewSecurityGroup(ctx, sgName, &awsec2.SecurityGroupArgs{
		Description: pulumi.String(fmt.Sprintf("Allow PostgreSQL traffic from VPC for %s", cn)),
		VpcId:       vpc.VpcID(),
		Ingress: awsec2.SecurityGroupIngressArray{
			awsec2.SecurityGroupIngressArgs{
				Description: pulumi.String("Allow PostgreSQL traffic on port 5432"),
				FromPort:    pulumi.Int(5432),
				ToPort:      pulumi.Int(5432),
				Protocol:    pulumi.String("tcp"),
				CidrBlocks:  pulumi.StringArray{pulumi.String(params.vpcCIDR)},
			},
		},
		Egress: awsec2.SecurityGroupEgressArray{
			awsec2.SecurityGroupEgressArgs{
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				Protocol:   pulumi.String("-1"),
				CidrBlocks: pulumi.StringArray{pulumi.String(params.vpcCIDR)},
			},
		},
		Tags: awsTagMap(tags, map[string]string{"Name": sgName}),
	}, withVPCParentAlias())
	if err != nil {
		return nil, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}

	sngName := fmt.Sprintf("%s-main-database-subnet-group", cn)
	dbsng, err := awsrds.NewSubnetGroup(ctx, sngName, &awsrds.SubnetGroupArgs{
		SubnetIds: privateSubnetIDs,
		Tags:      awsTagMap(tags, map[string]string{"Name": sngName}),
	}, withVPCParentAlias())
	if err != nil {
		return nil, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}

	pgName := fmt.Sprintf("%s-main-database-parameter-group", cn)
	dbpg, err := awsrds.NewParameterGroup(ctx, pgName, &awsrds.ParameterGroupArgs{
		Family: pulumi.String("postgres16"),
		Parameters: awsrds.ParameterGroupParameterArray{
			awsrds.ParameterGroupParameterArgs{Name: pulumi.String("auto_explain.log_min_duration"), Value: pulumi.String("5000")},
			awsrds.ParameterGroupParameterArgs{Name: pulumi.String("log_min_duration_statement"), Value: pulumi.String("1500")},
			awsrds.ParameterGroupParameterArgs{Name: pulumi.String("log_lock_waits"), Value: pulumi.String("1")},
		},
	}, withVPCParentAlias())
	if err != nil {
		return nil, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}

	dbArgs := &awsrds.InstanceArgs{
		AllocatedStorage:         pulumi.Int(params.cfg.DBAllocatedStorage),
		BackupRetentionPeriod:    pulumi.Int(7),
		CopyTagsToSnapshot:       pulumi.Bool(true),
		DbName:                   pulumi.String("postgres"),
		DbSubnetGroupName:        dbsng.Name,
		Engine:                   pulumi.String("postgres"),
		EngineVersion:            pulumi.String(params.cfg.DBEngineVersion),
		FinalSnapshotIdentifier:  pulumi.String(fmt.Sprintf("%s-final-snapshot", cn)),
		InstanceClass:            pulumi.String(params.cfg.DBInstanceClass),
		ManageMasterUserPassword: pulumi.Bool(true),
		ParameterGroupName:       dbpg.Name,
		SkipFinalSnapshot:        pulumi.Bool(!protect),
		StorageEncrypted:         pulumi.Bool(true),
		StorageType:              pulumi.String("gp3"),
		Tags:                     awsTagMap(tags, map[string]string{"Name": cn}),
		Username:                 pulumi.String("postgres"),
		VpcSecurityGroupIds:      pulumi.StringArray{dbsg.ID()},
	}
	opts := append([]pulumi.ResourceOption{withVPCParentAlias(), pulumi.Protect(protect)},
		applyRDSIdentifier(dbArgs, params.existingDBIdentifier, cn)...)
	db, err := awsrds.NewInstance(ctx, cn, dbArgs, opts...)
	if err != nil {
		return nil, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}

	// master-user-secret ARN via the RDS Pulumi data source.
	dbSecretARN := db.Identifier.ApplyT(func(id string) (string, error) {
		inst, lerr := awsrds.LookupInstance(ctx, &awsrds.LookupInstanceArgs{DbInstanceIdentifier: &id})
		if lerr != nil {
			return "", lerr
		}
		if len(inst.MasterUserSecrets) == 0 {
			return "", nil
		}
		return inst.MasterUserSecrets[0].SecretArn, nil
	}).(pulumi.StringOutput)

	return db, db.Address, dbSecretARN, nil
}

// buildControlRoomReleasesBucket ports _define_releases_bucket: the
// "<cn>-releases" S3 bucket (KMS-encrypted), its public-access block, and
// versioning. The bucket is protected per protect_persistent_resources.
func buildControlRoomReleasesBucket(
	ctx *pulumi.Context,
	params awsControlRoomPersistentParams,
	protect bool,
	withAlias func() pulumi.ResourceOption,
) (*awss3.Bucket, error) {
	cn := params.compoundName
	bucketName := fmt.Sprintf("%s-releases", cn)

	bucket, err := awss3.NewBucket(ctx, bucketName, &awss3.BucketArgs{
		Bucket: pulumi.String(bucketName),
		Tags:   awsTagMap(params.requiredTags, map[string]string{"Name": bucketName}),
		ServerSideEncryptionConfiguration: &awss3.BucketServerSideEncryptionConfigurationArgs{
			Rule: &awss3.BucketServerSideEncryptionConfigurationRuleArgs{
				ApplyServerSideEncryptionByDefault: &awss3.BucketServerSideEncryptionConfigurationRuleApplyServerSideEncryptionByDefaultArgs{
					SseAlgorithm: pulumi.String("aws:kms"),
				},
				BucketKeyEnabled: pulumi.Bool(true),
			},
		},
	}, withAlias(), pulumi.Protect(protect))
	if err != nil {
		return nil, err
	}

	// Block all public access (access is via signed URLs). Parented to the bucket.
	bucketChildURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$aws:s3/bucket:Bucket::%s",
		ctx.Stack(), persistentAWSControlRoomProjectName, persistentAWSControlRoomCompType, bucketName)
	withBucketChildAlias := pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(bucketChildURN)}})

	if _, err := awss3.NewBucketPublicAccessBlock(ctx, fmt.Sprintf("%s-releases-public-access-block", cn), &awss3.BucketPublicAccessBlockArgs{
		Bucket:                bucket.ID(),
		BlockPublicAcls:       pulumi.Bool(true),
		BlockPublicPolicy:     pulumi.Bool(true),
		IgnorePublicAcls:      pulumi.Bool(true),
		RestrictPublicBuckets: pulumi.Bool(true),
	}, pulumi.Parent(bucket), withBucketChildAlias); err != nil {
		return nil, err
	}

	if _, err := awss3.NewBucketVersioningV2(ctx, fmt.Sprintf("%s-releases-versioning", cn), &awss3.BucketVersioningV2Args{
		Bucket: bucket.ID(),
		VersioningConfiguration: &awss3.BucketVersioningV2VersioningConfigurationArgs{
			Status: pulumi.String("Enabled"),
		},
	}, pulumi.Parent(bucket), withBucketChildAlias); err != nil {
		return nil, err
	}

	return bucket, nil
}
