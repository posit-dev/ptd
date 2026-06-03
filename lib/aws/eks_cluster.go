package aws

// EKSCluster is a Go port of the Python AWSEKSCluster ComponentResource
// (python-pulumi/src/ptd/pulumi_resources/aws_eks_cluster.py, type token
// "ptd:AWSEKSCluster"). It encapsulates the EKS control plane, its IAM role, the
// Kubernetes provider, node IAM role + managed node groups, and the OIDC
// provider.
//
// Like lib/aws/vpc.go this is NOT a Pulumi ComponentResource — every resource is
// created as a direct child of the root stack. pulumi.Aliases bridge from the old
// Python parent URNs so existing state is adopted, not replaced.
//
// CRITICAL: the Pulumi *logical names* (first arg to every resource constructor)
// are byte-identical to the Python implementation. Changing the EKS cluster
// logical name in particular would REPLACE the live cluster (data loss). See the
// repo CLAUDE.md "Danger Zones" and the migration playbook.
//
// Builder method ordering (mirrors the Python with_*() ordering dependencies):
//   - NewEKSCluster() creates the cluster + IAM cluster role + K8s provider.
//   - WithNodeRole() MUST be called before WithNodeGroup() (sets nodeRole).
//   - WithOidcProvider() requires the cluster (created in NewEKSCluster).
//
// Later phases fill in the WithEbsCsiDriver / WithEfsCsiDriver / WithAwsAuth /
// WithEksAccessEntries / WithAwsSecretsStoreCsiDriverProvider / WithGp3 /
// WithEncryptedEbsStorageClass / tigera methods (stubbed below).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	awsec2 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	awseks "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/eks"
	awsiam "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	kubernetes "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	storagev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/storage/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// EKSClusterConfig holds the construction parameters for NewEKSCluster.
type EKSClusterConfig struct {
	// Name is the cluster compound name, e.g. "<compound>-<release>". Used as the
	// resource-name prefix AND as the literal EKS cluster name. MUST match the
	// Python `name` (workload: "{compound_name}-{release}").
	Name string

	// SubnetIDs are the control-plane subnet IDs.
	SubnetIDs []string

	// Version is the EKS/Kubernetes version.
	Version string

	// Tags are applied to the cluster (merged with {"Name": Name}).
	Tags map[string]string

	// DefaultAddonsToRemove is passed to the cluster (Python default_addons_to_remove).
	DefaultAddonsToRemove []string

	// EnabledClusterLogTypes is the set of control-plane log types to enable.
	// Pass already-sorted to match Python's sorted(...) ordering.
	EnabledClusterLogTypes []string

	// EksRoleName, when non-empty, names the cluster IAM role explicitly. When
	// empty the role is created with a generated name (control-room legacy path,
	// see the Python "silly hack" comment).
	EksRoleName string

	// IAMPermissionsBoundary is the permissions boundary ARN for the cluster and
	// node IAM roles (may be empty).
	IAMPermissionsBoundary string

	// ForceUpdateVersion sets EKS ForceUpdateVersion on the cluster and node groups.
	ForceUpdateVersion bool

	// ProtectPersistentResources applies pulumi.Protect to durable resources
	// (the OIDC provider). Mirrors Python protect_persistent_resources.
	ProtectPersistentResources bool

	// OIDCThumbprint is the CA thumbprint for the OIDC provider, pre-fetched by
	// the step via aws.GetOIDCThumbprint (a network dial that replicates Python's
	// ptd.oidc.get_thumbprint exactly). Empty on a greenfield cluster that does
	// not yet exist (no issuer to dial); WithOidcProvider only runs once the
	// cluster's issuer is resolvable.
	OIDCThumbprint string

	// ── Auth-mode preservation (pre-fetched by the step) ───────────────────────
	// ClusterExists / CurrentAuthMode come from a live describe_cluster
	// (lib/aws/eks.go GetClusterAuthMode). When ClusterExists is true the cluster
	// is created WITHOUT access_config so the live authentication mode is
	// preserved and the cluster is NOT replaced. Only a greenfield cluster is
	// created with API_AND_CONFIG_MAP. Mirrors the boto3 probe in the Python
	// __init__.
	ClusterExists   bool
	CurrentAuthMode string

	// ── Kubernetes provider inputs (pre-fetched by the step) ────────────────────
	// Kubeconfig is a ready-to-use kubeconfig string for the cluster
	// (kube.BuildEKSKubeconfigString). The provider is created in NewEKSCluster so
	// later K8s resources can reference it.
	Kubeconfig string

	// ── Alias URN construction ──────────────────────────────────────────────────
	// ProjectName is the OLD Python Pulumi project name, used verbatim in alias
	// URNs (e.g. "ptd-aws-workload-eks"). MUST be the literal old project string,
	// NEVER ctx.Project() — the migration playbook forbids ctx.Project() in alias
	// URNs.
	ProjectName string

	// ParentTypeChain is the OLD Python parent component type chain that wrapped
	// this AWSEKSCluster, ending at ptd:AWSEKSCluster. For the workload wrapper
	// the AWSEKSCluster was a child of AWSWorkloadEKS, so this is
	// "ptd:AWSWorkloadEKS$ptd:AWSEKSCluster". For the control-room wrapper it is
	// "ptd:AWSControlRoomCluster$ptd:AWSEKSCluster". The builder appends
	// "$<resource-type>" to form each resource's full old URN.
	ParentTypeChain string

	// WrapperTypeChain is the OLD parent component type chain WITHOUT the trailing
	// ptd:AWSEKSCluster (e.g. "ptd:AWSWorkloadEKS"). Used for resources that the
	// WRAPPER component created directly (e.g. the per-node-group LaunchTemplate,
	// which in aws_workload_eks.py has parent=self where self is the wrapper).
	WrapperTypeChain string

	// ── SG-access inputs (mirrors aws_eks_cluster.py __init__ sg-access wiring) ──
	// SgPrefix is the security-group name prefix (Python sg_prefix = compound_name).
	// Region / Credentials are used for the EFS mount-target SG modify and any other
	// SDK side effects executed during apply.
	SgPrefix                 string
	Region                   string
	Credentials              *Credentials
	TailscaleEnabled         bool
	CustomerManagedBastionID string
	// VpcID is the cluster's VPC id (pre-fetched). Used by SetupBastionAccess /
	// SetupTailscaleAccess to look up the bastion/tailscale SG.
	VpcID string
	// ClusterSecurityGroupID is the cluster's primary (EKS-managed) security group
	// id (pre-fetched from the live VPC config). The SG-access ingress rule targets
	// it. Empty on a greenfield cluster (the SG-access wiring is then skipped, the
	// same effect as Python's apply on an unset cluster_security_group_id).
	ClusterSecurityGroupID string

	// ── Access-entry import data (pre-fetched; see AccessEntryData) ──────────────
	AccessEntries AccessEntryData

	// AccountID is the workload AWS account id (Python aws.get_caller_identity()).
	AccountID string
}

// EKSCluster holds the Pulumi resource handles produced while building an EKS
// cluster. Builder methods attach more resources and return the same pointer so
// callers can chain (vpc.go shape). A stored err short-circuits the chain.
type EKSCluster struct {
	ctx *pulumi.Context
	cfg EKSClusterConfig

	// err captures the first error in a builder chain; subsequent methods no-op.
	err error

	// Core resources
	eksRole      *awsiam.Role
	cluster      *awseks.Cluster
	provider     *kubernetes.Provider
	nodeRole     *awsiam.Role
	nodeGroups   map[string]*awseks.NodeGroup
	oidcProvider *awsiam.OpenIdConnectProvider

	// ebsCsiAddon is set by WithEbsCsiDriver and consumed by
	// WithEncryptedEbsStorageClass (the storage-class patch must depend on the
	// addon being ready), mirroring Python self.ebs_csi_addon.
	ebsCsiAddon *awseks.Addon
}

// nodeRolePolicy enumerates the managed policies attached to the default node
// IAM role (mirrors Python NodeRolePolicy + the four managed-policy ARNs).
type nodeRolePolicy struct {
	suffix    string // resource-name suffix, e.g. "worker"
	policyARN string
}

var defaultNodeRolePolicies = []nodeRolePolicy{
	{suffix: "worker", policyARN: "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy"},
	{suffix: "cni", policyARN: "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"},
	{suffix: "registry", policyARN: "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"},
	{suffix: "ssm", policyARN: "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
}

// fullURNAlias returns a pulumi.Aliases option pointing at the full old Python
// URN for a resource of type resourceType named resourceName, parented under the
// AWSEKSCluster component (ParentTypeChain). We use the complete URN (not
// ParentURN) because the Go resources are flat children of the stack while the
// Python resources were nested several levels deep; the full chain is the only
// faithful match (see the playbook "Deep nesting").
func (c *EKSCluster) fullURNAlias(resourceType, resourceName string) pulumi.ResourceOption {
	urn := fmt.Sprintf("urn:pulumi:%s::%s::%s$%s::%s",
		c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, resourceType, resourceName)
	return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
}

// wrapperURNAlias is like fullURNAlias but parents the resource directly under
// the WRAPPER component (WrapperTypeChain) rather than the AWSEKSCluster. Used
// for the per-node-group LaunchTemplate, which aws_workload_eks.py created with
// parent=self (the AWSWorkloadEKS wrapper).
func (c *EKSCluster) wrapperURNAlias(resourceType, resourceName string) pulumi.ResourceOption {
	urn := fmt.Sprintf("urn:pulumi:%s::%s::%s$%s::%s",
		c.ctx.Stack(), c.cfg.ProjectName, c.cfg.WrapperTypeChain, resourceType, resourceName)
	return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
}

func (c *EKSCluster) tagsMap() pulumi.StringMap {
	out := pulumi.StringMap{}
	for k, v := range c.cfg.Tags {
		out[k] = pulumi.String(v)
	}
	return out
}

// Cluster returns the underlying EKS cluster resource.
func (c *EKSCluster) Cluster() *awseks.Cluster { return c.cluster }

// Provider returns the Kubernetes provider for the cluster.
func (c *EKSCluster) Provider() *kubernetes.Provider { return c.provider }

// OidcProvider returns the IAM OIDC provider (nil until WithOidcProvider runs).
func (c *EKSCluster) OidcProvider() *awsiam.OpenIdConnectProvider { return c.oidcProvider }

// NodeRole returns the default node IAM role (nil until WithNodeRole runs).
func (c *EKSCluster) NodeRole() *awsiam.Role { return c.nodeRole }

// NodeGroups returns the managed node groups keyed by logical name.
func (c *EKSCluster) NodeGroups() map[string]*awseks.NodeGroup { return c.nodeGroups }

// Err returns the first error captured in the builder chain.
func (c *EKSCluster) Err() error { return c.err }

// NewEKSCluster creates the EKS control plane, its IAM role + policy attachment,
// and the Kubernetes provider, returning an *EKSCluster for chaining.
func NewEKSCluster(ctx *pulumi.Context, cfg EKSClusterConfig) (*EKSCluster, error) {
	c := &EKSCluster{
		ctx:        ctx,
		cfg:        cfg,
		nodeGroups: map[string]*awseks.NodeGroup{},
	}

	// ── Cluster IAM role ────────────────────────────────────────────────────────
	// Assume-role policy for the EKS control plane (Service: eks.amazonaws.com).
	clusterAssume := `{"Version":"2012-10-17","Statement":[{"Action":"sts:AssumeRole","Effect":"Allow","Principal":{"Service":"eks.amazonaws.com"}}]}`

	roleArgs := &awsiam.RoleArgs{
		AssumeRolePolicy: pulumi.String(clusterAssume),
	}
	if cfg.IAMPermissionsBoundary != "" {
		roleArgs.PermissionsBoundary = pulumi.String(cfg.IAMPermissionsBoundary)
	}
	// Python: when eks_role_name != "" the role is named explicitly; otherwise it
	// gets a generated name (control-room legacy path).
	if cfg.EksRoleName != "" {
		roleArgs.Name = pulumi.String(cfg.EksRoleName)
	}

	eksRole, err := awsiam.NewRole(ctx, cfg.Name+"-eks", roleArgs, c.fullURNAlias("aws:iam/role:Role", cfg.Name+"-eks"))
	if err != nil {
		return nil, fmt.Errorf("eks: failed to create cluster IAM role for %s: %w", cfg.Name, err)
	}
	c.eksRole = eksRole

	// AmazonEKSClusterPolicy attachment. In Python the attachment's parent is the
	// role (with an extra alias to the component); the role's old URN type chain
	// is <ParentTypeChain>$aws:iam/role:Role, so the attachment's full old URN is
	// <ParentTypeChain>$aws:iam/role:Role$aws:iam/rolePolicyAttachment::<name>.
	clusterPolicyURN := fmt.Sprintf(
		"urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
		ctx.Stack(), cfg.ProjectName, cfg.ParentTypeChain, cfg.Name+"-eks")
	_, err = awsiam.NewRolePolicyAttachment(ctx, cfg.Name+"-eks", &awsiam.RolePolicyAttachmentArgs{
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"),
		Role:      eksRole.Name,
	},
		pulumi.Aliases([]pulumi.Alias{
			{URN: pulumi.URN(clusterPolicyURN)},
			// Also alias to the component as parent (Python attached an extra
			// pulumi.Alias(parent=self) on this attachment).
			{URN: pulumi.URN(fmt.Sprintf("urn:pulumi:%s::%s::%s$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
				ctx.Stack(), cfg.ProjectName, cfg.ParentTypeChain, cfg.Name+"-eks"))},
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("eks: failed to attach AmazonEKSClusterPolicy for %s: %w", cfg.Name, err)
	}

	// ── EKS cluster ─────────────────────────────────────────────────────────────
	subnetIDs := pulumi.ToStringArray(cfg.SubnetIDs)
	clusterArgs := &awseks.ClusterArgs{
		Name:    pulumi.String(cfg.Name),
		RoleArn: eksRole.Arn,
		Version: pulumi.String(cfg.Version),
		VpcConfig: &awseks.ClusterVpcConfigArgs{
			SubnetIds:             subnetIDs,
			EndpointPrivateAccess: pulumi.Bool(true),
			EndpointPublicAccess:  pulumi.Bool(false),
		},
		Tags: c.clusterTags(),
	}
	if len(cfg.DefaultAddonsToRemove) > 0 {
		clusterArgs.DefaultAddonsToRemoves = pulumi.ToStringArray(cfg.DefaultAddonsToRemove)
	}
	if len(cfg.EnabledClusterLogTypes) > 0 {
		clusterArgs.EnabledClusterLogTypes = pulumi.ToStringArray(cfg.EnabledClusterLogTypes)
	}
	if cfg.ForceUpdateVersion {
		clusterArgs.ForceUpdateVersion = pulumi.Bool(true)
	}
	// Auth-mode preservation: only a brand-new cluster gets access_config. For an
	// existing cluster we omit it so the live authenticationMode is preserved and
	// the cluster is not replaced (mirrors the Python boto3 probe).
	if !cfg.ClusterExists {
		clusterArgs.AccessConfig = &awseks.ClusterAccessConfigArgs{
			AuthenticationMode: pulumi.String("API_AND_CONFIG_MAP"),
		}
	}

	// PULUMI STATE NAME — the logical name MUST equal the literal cluster name to
	// match Python (aws_eks_cluster.py self.eks = aws.eks.Cluster(name, ...)). A
	// changed logical name REPLACES the live cluster.
	cluster, err := awseks.NewCluster(ctx, cfg.Name, clusterArgs,
		c.fullURNAlias("aws:eks/cluster:Cluster", cfg.Name))
	if err != nil {
		return nil, fmt.Errorf("eks: failed to create cluster %s: %w", cfg.Name, err)
	}
	c.cluster = cluster

	// ── Kubernetes provider ─────────────────────────────────────────────────────
	// Python builds the provider lazily via get_provider_for_cluster(name, ...)
	// which names it "{name}-k8s" as a TOP-LEVEL resource (no parent). So the
	// provider's old URN is pulumi:providers:kubernetes::<name>-k8s at the root.
	providerName := cfg.Name + "-k8s"
	provider, err := kubernetes.NewProvider(ctx, providerName, &kubernetes.ProviderArgs{
		EnableServerSideApply: pulumi.Bool(true),
		Kubeconfig:            pulumi.String(cfg.Kubeconfig),
	},
		pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(fmt.Sprintf(
			"urn:pulumi:%s::%s::pulumi:providers:kubernetes::%s",
			ctx.Stack(), cfg.ProjectName, providerName))}}),
	)
	if err != nil {
		return nil, fmt.Errorf("eks: failed to create K8s provider for %s: %w", cfg.Name, err)
	}
	c.provider = provider

	// SG access: mirror aws_eks_cluster.py __init__ — tailscale takes precedence,
	// else (unless a customer-managed bastion is used) wire the PTD bastion SG.
	if cfg.TailscaleEnabled {
		c.SetupTailscaleAccess()
	} else if cfg.CustomerManagedBastionID == "" {
		c.SetupBastionAccess()
	}
	if c.err != nil {
		return nil, c.err
	}

	return c, nil
}

// clusterTags merges {"Name": Name} with the configured tags (matches Python
// `tags={"Name": name} | tags`).
func (c *EKSCluster) clusterTags() pulumi.StringMap {
	out := pulumi.StringMap{"Name": pulumi.String(c.cfg.Name)}
	for k, v := range c.cfg.Tags {
		out[k] = pulumi.String(v)
	}
	return out
}

// WithNodeRole creates the default node IAM role and its four managed-policy
// attachments. MUST be called before WithNodeGroup (sets c.nodeRole), mirroring
// the Python with_node_role()/with_node_group() ordering dependency.
//
// roleName names the role explicitly (workload always passes a name).
func (c *EKSCluster) WithNodeRole(roleName string) *EKSCluster {
	if c.err != nil {
		return c
	}

	nodeAssume := `{"Version":"2012-10-17","Statement":[{"Action":"sts:AssumeRole","Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"}}]}`

	roleArgs := &awsiam.RoleArgs{
		AssumeRolePolicy: pulumi.String(nodeAssume),
	}
	if c.cfg.IAMPermissionsBoundary != "" {
		roleArgs.PermissionsBoundary = pulumi.String(c.cfg.IAMPermissionsBoundary)
	}
	if roleName != "" {
		roleArgs.Name = pulumi.String(roleName)
	}

	// Python: node role parent=self.eks (the cluster), with an extra
	// pulumi.Alias(parent=self) (the component). Its old URN type chain is
	// <ParentTypeChain>$aws:eks/cluster:Cluster$aws:iam/role:Role.
	nodeRoleName := c.cfg.Name + "-eks-node"
	nodeRole, err := awsiam.NewRole(c.ctx, nodeRoleName, roleArgs,
		c.clusterChildAlias("aws:iam/role:Role", nodeRoleName),
	)
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create node IAM role for %s: %w", c.cfg.Name, err)
		return c
	}
	c.nodeRole = nodeRole

	for _, pol := range defaultNodeRolePolicies {
		attachName := fmt.Sprintf("%s-eks-node-%s", c.cfg.Name, pol.suffix)
		// Python: parent=self.default_node_role, extra alias parent=self. The role
		// is a child of the cluster, so the attachment's old type chain is
		// <ParentTypeChain>$aws:eks/cluster:Cluster$aws:iam/role:Role$aws:iam/rolePolicyAttachment.
		roleChildURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$aws:eks/cluster:Cluster$aws:iam/role:Role$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
			c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, attachName)
		componentChildURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
			c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, attachName)
		if _, err := awsiam.NewRolePolicyAttachment(c.ctx, attachName, &awsiam.RolePolicyAttachmentArgs{
			PolicyArn: pulumi.String(pol.policyARN),
			Role:      nodeRole.ID(),
		},
			pulumi.Aliases([]pulumi.Alias{
				{URN: pulumi.URN(roleChildURN)},
				{URN: pulumi.URN(componentChildURN)},
			}),
		); err != nil {
			c.err = fmt.Errorf("eks: failed to attach node policy %s for %s: %w", pol.suffix, c.cfg.Name, err)
			return c
		}
	}

	return c
}

// clusterChildAlias returns a full-URN alias for a resource that was a child of
// the EKS cluster in Python (parent=self.eks), e.g. the node role / node group.
// Type chain: <ParentTypeChain>$aws:eks/cluster:Cluster$<resourceType>.
func (c *EKSCluster) clusterChildAlias(resourceType, resourceName string) pulumi.ResourceOption {
	urn := fmt.Sprintf("urn:pulumi:%s::%s::%s$aws:eks/cluster:Cluster$%s::%s",
		c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, resourceType, resourceName)
	return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
}

// NodeGroupParams holds the per-node-group configuration for WithNodeGroup.
type NodeGroupParams struct {
	// Name is the node group logical name AND launch-template logical name. For
	// the default group this is the cluster Name; for additional groups it is
	// "{cluster}-{ngName}". MUST match Python full_name.
	Name string
	// SecurityGroupIDs are attached to the launch template.
	SecurityGroupIDs []string
	// InstanceType, VolumeSize, AmiType, MinSize, MaxSize, DesiredSize, Version
	// mirror the Python _create_node_group args.
	InstanceType string
	VolumeSize   int
	AmiType      string
	MinSize      int
	MaxSize      int
	DesiredSize  int
	Version      string
	// Tags are applied to the launch template and node group (Python merges
	// required_tags | {"Name": full_name} on the LT and required_tags | labels on
	// the node group).
	Tags   map[string]string
	Labels map[string]string
	// Taints mirror the Python ptd.Taint list (effect/key/value).
	Taints []NodeGroupTaint
}

// NodeGroupTaint mirrors a Kubernetes taint for a managed node group.
type NodeGroupTaint struct {
	Effect string
	Key    string
	Value  string
}

// WithNodeGroup creates a managed node group plus its dedicated launch template
// (with IMDSv2 metadata options), mirroring aws_workload_eks.py _create_node_group
// + aws_eks_cluster.py with_node_group. Requires WithNodeRole to have run.
func (c *EKSCluster) WithNodeGroup(p NodeGroupParams) *EKSCluster {
	if c.err != nil {
		return c
	}
	if c.nodeRole == nil {
		c.err = fmt.Errorf("eks: WithNodeGroup called before WithNodeRole for %s", c.cfg.Name)
		return c
	}

	// Launch template tags (Python: required_tags | {"Name": full_name}).
	ltTags := pulumi.StringMap{}
	for k, v := range p.Tags {
		ltTags[k] = pulumi.String(v)
	}
	ltTags["Name"] = pulumi.String(p.Name)

	tagSpec := awsec2.LaunchTemplateTagSpecificationArgs{Tags: ltTags}
	// Build per-resource-type tag specifications (instance + volume), each with
	// the same tag map (Python tag_specifications).
	tagSpecs := awsec2.LaunchTemplateTagSpecificationArray{
		awsec2.LaunchTemplateTagSpecificationArgs{ResourceType: pulumi.String("instance"), Tags: ltTags},
		awsec2.LaunchTemplateTagSpecificationArgs{ResourceType: pulumi.String("volume"), Tags: ltTags},
	}
	_ = tagSpec

	// The launch template in the Python wrapper has parent=self (the wrapper
	// component, e.g. AWSWorkloadEKS), NOT the AWSEKSCluster. So its old URN is
	// <WrapperTypeChain>$aws:ec2/launchTemplate:LaunchTemplate::<full_name>.
	lt, err := awsec2.NewLaunchTemplate(c.ctx, p.Name, &awsec2.LaunchTemplateArgs{
		UpdateDefaultVersion: pulumi.Bool(true),
		MetadataOptions: &awsec2.LaunchTemplateMetadataOptionsArgs{
			HttpEndpoint:            pulumi.String("enabled"),
			HttpPutResponseHopLimit: pulumi.Int(2),
			HttpTokens:              pulumi.String("required"),
			InstanceMetadataTags:    pulumi.String("disabled"),
		},
		BlockDeviceMappings: awsec2.LaunchTemplateBlockDeviceMappingArray{
			awsec2.LaunchTemplateBlockDeviceMappingArgs{
				DeviceName: pulumi.String("/dev/xvda"),
				Ebs: &awsec2.LaunchTemplateBlockDeviceMappingEbsArgs{
					VolumeSize:          pulumi.Int(p.VolumeSize),
					VolumeType:          pulumi.String("gp3"),
					DeleteOnTermination: pulumi.String("true"),
				},
			},
		},
		Tags:                ltTags,
		TagSpecifications:   tagSpecs,
		InstanceType:        pulumi.String(p.InstanceType),
		VpcSecurityGroupIds: pulumi.ToStringArray(p.SecurityGroupIDs),
	},
		c.wrapperURNAlias("aws:ec2/launchTemplate:LaunchTemplate", p.Name),
	)
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create launch template %s: %w", p.Name, err)
		return c
	}

	// Node group tags (Python: required_tags | (labels or {})).
	ngTags := pulumi.StringMap{}
	for k, v := range p.Tags {
		ngTags[k] = pulumi.String(v)
	}
	for k, v := range p.Labels {
		ngTags[k] = pulumi.String(v)
	}

	var taints awseks.NodeGroupTaintArray
	for _, t := range p.Taints {
		taints = append(taints, awseks.NodeGroupTaintArgs{
			Key:    pulumi.String(t.Key),
			Value:  pulumi.String(t.Value),
			Effect: pulumi.String(t.Effect),
		})
	}

	// PULUMI STATE NAME — node group logical name MUST equal Python full_name.
	ng, err := awseks.NewNodeGroup(c.ctx, p.Name, &awseks.NodeGroupArgs{
		ClusterName: c.cluster.Name,
		NodeRoleArn: c.nodeRole.Arn,
		SubnetIds:   pulumi.ToStringArray(c.cfg.SubnetIDs),
		Version:     pulumi.String(p.Version),
		ScalingConfig: &awseks.NodeGroupScalingConfigArgs{
			DesiredSize: pulumi.Int(p.DesiredSize),
			MinSize:     pulumi.Int(p.MinSize),
			MaxSize:     pulumi.Int(p.MaxSize),
		},
		AmiType: pulumi.String(p.AmiType),
		Tags:    ngTags,
		LaunchTemplate: &awseks.NodeGroupLaunchTemplateArgs{
			Id:      lt.ID(),
			Version: lt.LatestVersion.ApplyT(func(v int) string { return fmt.Sprintf("%d", v) }).(pulumi.StringOutput),
		},
		UpdateConfig:       &awseks.NodeGroupUpdateConfigArgs{MaxUnavailable: pulumi.Int(1)},
		ForceUpdateVersion: pulumi.Bool(c.cfg.ForceUpdateVersion),
		Taints:             taints,
	},
		c.clusterChildAlias("aws:eks/nodeGroup:NodeGroup", p.Name),
	)
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create node group %s: %w", p.Name, err)
		return c
	}
	c.nodeGroups[p.Name] = ng

	return c
}

// WithOidcProvider creates the IAM OIDC provider for the cluster, using the CA
// thumbprint pre-fetched by the step (aws.GetOIDCThumbprint, which replicates
// Python's ptd.oidc.get_thumbprint byte-for-byte). Requires the cluster.
func (c *EKSCluster) WithOidcProvider() *EKSCluster {
	if c.err != nil {
		return c
	}

	// Issuer URL from the cluster identity (Python: self.eks.identities[0].oidcs[0].issuer).
	issuerURL := c.cluster.Identities.Index(pulumi.Int(0)).Oidcs().Index(pulumi.Int(0)).Issuer().Elem()

	// Thumbprint: pre-computed by the step via a raw tls.Dial of the issuer's
	// jwks_uri netloc (matching Python's `thumbprint` CLI), NOT the Pulumi
	// tls.GetCertificate data source — that returns a different chain and the
	// wrong value.
	thumbprint := pulumi.String(c.cfg.OIDCThumbprint)

	opts := []pulumi.ResourceOption{
		c.clusterChildAlias("aws:iam/openIdConnectProvider:OpenIdConnectProvider", c.cfg.Name),
	}
	if c.cfg.ProtectPersistentResources {
		opts = append(opts, pulumi.Protect(true))
	}

	oidc, err := awsiam.NewOpenIdConnectProvider(c.ctx, c.cfg.Name, &awsiam.OpenIdConnectProviderArgs{
		Url:             issuerURL,
		ClientIdLists:   pulumi.StringArray{pulumi.String("sts.amazonaws.com")},
		ThumbprintLists: pulumi.StringArray{thumbprint},
	}, opts...)
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create OIDC provider for %s: %w", c.cfg.Name, err)
		return c
	}
	c.oidcProvider = oidc

	return c
}

// providerOpt returns the Kubernetes provider resource option used by all K8s
// resources the builder creates (mirrors Python opts.provider=self.provider).
func (c *EKSCluster) providerOpt() pulumi.ResourceOption {
	return pulumi.Provider(c.provider)
}

// ── ServiceAccount / IRSA roles ─────────────────────────────────────────────

// serviceAccount mirrors the Python ServiceAccount dataclass.
type serviceAccount struct {
	name      string
	namespace string
}

func (s serviceAccount) subject() string {
	return fmt.Sprintf("system:serviceaccount:%s:%s", s.namespace, s.name)
}

// createServiceAccountRole creates an IRSA role assumable by the given service
// accounts, mirroring aws_eks_cluster.py create_service_account_role. The trust
// policy is derived from the cluster's OIDC issuer (self.eks.identities[0]...issuer),
// NOT from with_oidc_provider — so it works regardless of OIDC-provider ordering.
// The role's logical name AND IAM name are role_name verbatim, preserving the
// .posit.team-suffixed names that existing IRSA trust policies reference.
func (c *EKSCluster) createServiceAccountRole(roleName string, serviceAccounts []serviceAccount, attachPolicyARN, attachLogicalName string) (*awsiam.Role, error) {
	issuerURL := c.cluster.Identities.Index(pulumi.Int(0)).Oidcs().Index(pulumi.Int(0)).Issuer().Elem()

	accountID := c.cfg.AccountID
	subjects := make([]string, len(serviceAccounts))
	for i, sa := range serviceAccounts {
		subjects[i] = sa.subject()
	}

	trustPolicy := issuerURL.ApplyT(func(url string) (string, error) {
		// url is "https://oidc.eks.<region>.amazonaws.com/id/XXXX"; the OIDC tail
		// is everything after "//" (matches Python url.split("//")[1]).
		parts := strings.SplitN(url, "//", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("eks: malformed OIDC issuer URL %q", url)
		}
		tail := parts[1]
		doc := map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{
				{
					"Sid":    "ServiceAccountTrustPolicy",
					"Effect": "Allow",
					"Principal": map[string]interface{}{
						"Federated": fmt.Sprintf("arn:aws:iam::%s:oidc-provider/%s", accountID, tail),
					},
					"Action": "sts:AssumeRoleWithWebIdentity",
					"Condition": map[string]interface{}{
						"StringEquals": map[string]interface{}{
							fmt.Sprintf("%s:aud", tail): "sts.amazonaws.com",
							fmt.Sprintf("%s:sub", tail): subjects,
						},
					},
				},
			},
		}
		b, mErr := json.Marshal(doc)
		if mErr != nil {
			return "", mErr
		}
		return string(b), nil
	}).(pulumi.StringOutput)

	roleArgs := &awsiam.RoleArgs{
		Name:             pulumi.String(roleName),
		AssumeRolePolicy: trustPolicy,
	}
	if c.cfg.IAMPermissionsBoundary != "" {
		roleArgs.PermissionsBoundary = pulumi.String(c.cfg.IAMPermissionsBoundary)
	}

	// Python: create_service_account_role roles were created with parent=self (the
	// AWSEKSCluster component), so the role's old URN type chain is
	// <ParentTypeChain>$aws:iam/role:Role.
	role, err := awsiam.NewRole(c.ctx, roleName, roleArgs, c.fullURNAlias("aws:iam/role:Role", roleName))
	if err != nil {
		return nil, fmt.Errorf("eks: failed to create SA role %s: %w", roleName, err)
	}

	if attachPolicyARN != "" {
		// Python attached the managed policy with parent=sa_role, so the
		// attachment's old URN type chain is <ParentTypeChain>$aws:iam/role:Role$aws:iam/rolePolicyAttachment.
		roleChildURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
			c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, attachLogicalName)
		if _, err := awsiam.NewRolePolicyAttachment(c.ctx, attachLogicalName, &awsiam.RolePolicyAttachmentArgs{
			PolicyArn: pulumi.String(attachPolicyARN),
			Role:      role.Name,
		}, pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(roleChildURN)}})); err != nil {
			return nil, fmt.Errorf("eks: failed to attach policy %s to %s: %w", attachPolicyARN, roleName, err)
		}
	}

	return role, nil
}

// ── aws-auth ConfigMap vs EKS Access Entries ────────────────────────────────

// AwsAuthParams holds the inputs for WithAwsAuth (mirrors the with_aws_auth args
// the workload wrapper passes).
type AwsAuthParams struct {
	// UseEksAccessEntries dispatches to the modern access-entries path.
	UseEksAccessEntries bool
	// AdditionalAccessEntries are extra access entries (access-entries path only).
	AdditionalAccessEntries []map[string]interface{}
	// IncludePoweruser adds the PowerUser access entry (access-entries path only).
	IncludePoweruser bool
	// AdminRoleARN overrides the default admin role ARN (custom_role.role_arn).
	// Empty → arn:aws:iam::<account>:role/admin.posit.team.
	AdminRoleARN string
}

// WithAwsAuth dispatches to the legacy aws-auth ConfigMap path or the modern EKS
// access-entries path based on UseEksAccessEntries, mirroring
// aws_eks_cluster.py with_aws_auth. Requires WithNodeRole.
func (c *EKSCluster) WithAwsAuth(p AwsAuthParams) *EKSCluster {
	if c.err != nil {
		return c
	}
	if c.nodeRole == nil {
		c.err = fmt.Errorf("eks: WithAwsAuth called before WithNodeRole for %s", c.cfg.Name)
		return c
	}
	if p.UseEksAccessEntries {
		return c.withEksAccessEntries(p)
	}
	return c.withAwsAuthConfigMap(p)
}

// withAwsAuthConfigMap patches the kube-system/aws-auth ConfigMap with the node
// role + PowerUser + admin roles, mirroring the legacy branch of with_aws_auth.
func (c *EKSCluster) withAwsAuthConfigMap(p AwsAuthParams) *EKSCluster {
	adminRoleARN := p.AdminRoleARN
	if adminRoleARN == "" {
		adminRoleARN = fmt.Sprintf("arn:aws:iam::%s:role/admin.posit.team", c.cfg.AccountID)
	}

	// PowerUser role name from the SSO permission set (Python
	// get_role_name_for_permission_set). The aws-auth ConfigMap requires the role
	// NAME with the SSO path stripped (Python builds arn:...:role/<name>).
	powerUserRoles, err := awsiam.GetRoles(c.ctx, &awsiam.GetRolesArgs{
		NameRegex:  pulumi.StringRef(".*_PowerUser_.*"),
		PathPrefix: pulumi.StringRef("/aws-reserved/sso.amazonaws.com/"),
	}, nil)
	if err != nil {
		c.err = fmt.Errorf("eks: failed to look up PowerUser role for %s: %w", c.cfg.Name, err)
		return c
	}
	powerUserName := ""
	if len(powerUserRoles.Names) > 0 {
		powerUserName = powerUserRoles.Names[0]
	}
	powerUserARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", c.cfg.AccountID, powerUserName)

	// mapRoles is built in an ApplyT because the node role ARN is an Output.
	mapRoles := c.nodeRole.Arn.ApplyT(func(nodeArn string) string {
		type authUser struct {
			arn      string
			username string
			groups   []string
		}
		// "last in wins" dedup by arn, preserving insertion order (matches Python
		// dict-based dedup).
		users := []authUser{
			{nodeArn, "system:node:{{EC2PrivateDNSName}}", []string{"system:bootstrappers", "system:nodes"}},
			{powerUserARN, "admin", []string{"system:masters"}},
			{adminRoleARN, "admin", []string{"system:masters"}},
		}
		seen := map[string]int{}
		var order []string
		dedup := map[string]authUser{}
		for _, u := range users {
			if _, ok := seen[u.arn]; !ok {
				order = append(order, u.arn)
			}
			seen[u.arn] = 1
			dedup[u.arn] = u
		}
		var blocks []string
		for _, arn := range order {
			u := dedup[arn]
			block := fmt.Sprintf("- rolearn: %s\n  username: %s", u.arn, u.username)
			if len(u.groups) > 0 {
				block += "\n  groups:"
				for _, g := range u.groups {
					block += fmt.Sprintf("\n    - %s", g)
				}
			}
			blocks = append(blocks, block)
		}
		return strings.Join(blocks, "\n")
	}).(pulumi.StringOutput)

	_, err = corev1.NewConfigMapPatch(c.ctx, c.cfg.Name+"-aws-auth", &corev1.ConfigMapPatchArgs{
		Data: pulumi.StringMap{"mapRoles": mapRoles},
		Metadata: &metav1.ObjectMetaPatchArgs{
			Name:      pulumi.String("aws-auth"),
			Namespace: pulumi.String("kube-system"),
			Annotations: pulumi.StringMap{
				"pulumi.com/patchForce": pulumi.String("true"),
			},
		},
	}, c.providerOpt(), c.clusterChildAlias("kubernetes:core/v1:ConfigMapPatch", c.cfg.Name+"-aws-auth"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to patch aws-auth ConfigMap for %s: %w", c.cfg.Name, err)
		return c
	}

	return c
}

// withEksAccessEntries creates EKS access entries + policy associations for the
// admin role, optional PowerUser, the node role, and any additional entries,
// importing entries/associations that already exist in AWS (per the pre-fetched
// AccessEntryData). Mirrors with_eks_access_entries.
func (c *EKSCluster) withEksAccessEntries(p AwsAuthParams) *EKSCluster {
	adminRoleARN := p.AdminRoleARN
	if adminRoleARN == "" {
		adminRoleARN = fmt.Sprintf("arn:aws:iam::%s:role/admin.posit.team", c.cfg.AccountID)
	}

	existing := c.cfg.AccessEntries.Entries
	assoc := c.cfg.AccessEntries.AssociatedPolicies

	entryImportID := func(principalARN string) string {
		if existing[principalARN] {
			return fmt.Sprintf("%s:%s", c.cfg.Name, principalARN)
		}
		return ""
	}
	policyImportID := func(principalARN, policyARN string) string {
		if existing[principalARN] && assoc[principalARN] != nil && assoc[principalARN][policyARN] {
			return fmt.Sprintf("%s#%s#%s", c.cfg.Name, principalARN, policyARN)
		}
		return ""
	}
	const adminPolicyARN = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"

	mkEntry := func(logical, principalARN, entryType, importID string, kubernetesGroups []string) (*awseks.AccessEntry, error) {
		args := &awseks.AccessEntryArgs{
			ClusterName:  c.cluster.Name,
			PrincipalArn: pulumi.String(principalARN),
			Type:         pulumi.String(entryType),
		}
		if len(kubernetesGroups) > 0 {
			args.KubernetesGroups = pulumi.ToStringArray(kubernetesGroups)
		}
		opts := []pulumi.ResourceOption{
			c.clusterChildAlias("aws:eks/accessEntry:AccessEntry", logical),
		}
		if importID != "" {
			opts = append(opts, pulumi.Import(pulumi.ID(importID)))
		}
		return awseks.NewAccessEntry(c.ctx, logical, args, opts...)
	}
	mkAssoc := func(logical, principalARN, policyARN, scopeType string, namespaces []string, importID string, parent pulumi.Resource) error {
		scope := &awseks.AccessPolicyAssociationAccessScopeArgs{Type: pulumi.String(scopeType)}
		if len(namespaces) > 0 {
			scope.Namespaces = pulumi.ToStringArray(namespaces)
		}
		opts := []pulumi.ResourceOption{
			c.clusterChildAlias("aws:eks/accessPolicyAssociation:AccessPolicyAssociation", logical),
		}
		if importID != "" {
			opts = append(opts, pulumi.Import(pulumi.ID(importID)))
		}
		_, err := awseks.NewAccessPolicyAssociation(c.ctx, logical, &awseks.AccessPolicyAssociationArgs{
			ClusterName:  c.cluster.Name,
			PrincipalArn: pulumi.String(principalARN),
			PolicyArn:    pulumi.String(policyARN),
			AccessScope:  scope,
		}, opts...)
		return err
	}

	// --- Admin role (always) ---
	if _, err := mkEntry(c.cfg.Name+"-admin-access-entry", adminRoleARN, "STANDARD", entryImportID(adminRoleARN), nil); err != nil {
		c.err = fmt.Errorf("eks: failed to create admin access entry for %s: %w", c.cfg.Name, err)
		return c
	}
	if err := mkAssoc(c.cfg.Name+"-admin-policy-association", adminRoleARN, adminPolicyARN, "cluster", nil, policyImportID(adminRoleARN, adminPolicyARN), nil); err != nil {
		c.err = fmt.Errorf("eks: failed to create admin policy association for %s: %w", c.cfg.Name, err)
		return c
	}

	// --- PowerUser (optional) ---
	if p.IncludePoweruser {
		powerUserARN := c.lookupPowerUserARN()
		if powerUserARN != "" {
			if _, err := mkEntry(c.cfg.Name+"-poweruser-access-entry", powerUserARN, "STANDARD", entryImportID(powerUserARN), nil); err != nil {
				c.err = fmt.Errorf("eks: failed to create poweruser access entry for %s: %w", c.cfg.Name, err)
				return c
			}
			if err := mkAssoc(c.cfg.Name+"-poweruser-policy-association", powerUserARN, adminPolicyARN, "cluster", nil, policyImportID(powerUserARN, adminPolicyARN), nil); err != nil {
				c.err = fmt.Errorf("eks: failed to create poweruser policy association for %s: %w", c.cfg.Name, err)
				return c
			}
		}
	}

	// --- Node role ---
	// AWS auto-creates a node entry (ARN containing "eks-node") when switching auth
	// mode; adopt it by import when present, else create a new one off the node
	// role ARN.
	var existingNodeEntry string
	for arn := range existing {
		if strings.Contains(arn, "eks-node") {
			existingNodeEntry = arn
			break
		}
	}
	if existingNodeEntry != "" {
		nodeImportID := fmt.Sprintf("%s:%s", c.cfg.Name, existingNodeEntry)
		if _, err := mkEntry(c.cfg.Name+"-node-access-entry", existingNodeEntry, "EC2_LINUX", nodeImportID, nil); err != nil {
			c.err = fmt.Errorf("eks: failed to import node access entry for %s: %w", c.cfg.Name, err)
			return c
		}
		// Import any explicitly-associated policies (usually none for EC2_LINUX).
		idx := 0
		for policyARN := range assoc[existingNodeEntry] {
			imp := fmt.Sprintf("%s#%s#%s", c.cfg.Name, existingNodeEntry, policyARN)
			if err := mkAssoc(fmt.Sprintf("%s-node-policy-association-%d", c.cfg.Name, idx), existingNodeEntry, policyARN, "cluster", nil, imp, nil); err != nil {
				c.err = fmt.Errorf("eks: failed to import node policy association for %s: %w", c.cfg.Name, err)
				return c
			}
			idx++
		}
	} else {
		entry, err := awseks.NewAccessEntry(c.ctx, c.cfg.Name+"-node-access-entry", &awseks.AccessEntryArgs{
			ClusterName:  c.cluster.Name,
			PrincipalArn: c.nodeRole.Arn,
			Type:         pulumi.String("EC2_LINUX"),
		}, c.clusterChildAlias("aws:eks/accessEntry:AccessEntry", c.cfg.Name+"-node-access-entry"))
		if err != nil {
			c.err = fmt.Errorf("eks: failed to create node access entry for %s: %w", c.cfg.Name, err)
			return c
		}
		_ = entry
	}

	// --- Additional access entries ---
	for idx, entry := range p.AdditionalAccessEntries {
		principalARN, _ := entry["principalArn"].(string)
		if principalARN == "" {
			continue
		}
		entryType, _ := entry["type"].(string)
		if entryType == "" {
			entryType = "STANDARD"
		}
		var kgroups []string
		if kg, ok := entry["kubernetesGroups"].([]interface{}); ok {
			for _, g := range kg {
				if gs, ok := g.(string); ok {
					kgroups = append(kgroups, gs)
				}
			}
		}
		ae, err := mkEntry(fmt.Sprintf("%s-additional-access-entry-%d", c.cfg.Name, idx), principalARN, entryType, entryImportID(principalARN), kgroups)
		if err != nil {
			c.err = fmt.Errorf("eks: failed to create additional access entry %d for %s: %w", idx, c.cfg.Name, err)
			return c
		}

		policies, _ := entry["accessPolicies"].([]interface{})
		for pIdx, raw := range policies {
			pol, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			policyARN, _ := pol["policyArn"].(string)
			if policyARN == "" {
				continue
			}
			scopeType := "cluster"
			var namespaces []string
			if scope, ok := pol["accessScope"].(map[string]interface{}); ok {
				if st, ok := scope["type"].(string); ok && st != "" {
					scopeType = st
				}
				if ns, ok := scope["namespaces"].([]interface{}); ok {
					for _, n := range ns {
						if nss, ok := n.(string); ok {
							namespaces = append(namespaces, nss)
						}
					}
				}
			}
			// Python parented these associations on the AccessEntry; the entry was a
			// child of the cluster, so the old URN type chain is
			// <ParentTypeChain>$aws:eks/cluster:Cluster$aws:eks/accessEntry:AccessEntry$aws:eks/accessPolicyAssociation.
			logical := fmt.Sprintf("%s-additional-policy-association-%d-%d", c.cfg.Name, idx, pIdx)
			imp := policyImportID(principalARN, policyARN)
			opts := []pulumi.ResourceOption{
				pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(fmt.Sprintf(
					"urn:pulumi:%s::%s::%s$aws:eks/cluster:Cluster$aws:eks/accessEntry:AccessEntry$aws:eks/accessPolicyAssociation:AccessPolicyAssociation::%s",
					c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, logical))}}),
			}
			if imp != "" {
				opts = append(opts, pulumi.Import(pulumi.ID(imp)))
			}
			scope := &awseks.AccessPolicyAssociationAccessScopeArgs{Type: pulumi.String(scopeType)}
			if len(namespaces) > 0 {
				scope.Namespaces = pulumi.ToStringArray(namespaces)
			}
			if _, err := awseks.NewAccessPolicyAssociation(c.ctx, logical, &awseks.AccessPolicyAssociationArgs{
				ClusterName:  c.cluster.Name,
				PrincipalArn: pulumi.String(principalARN),
				PolicyArn:    pulumi.String(policyARN),
				AccessScope:  scope,
			}, opts...); err != nil {
				c.err = fmt.Errorf("eks: failed to create additional policy association for %s: %w", c.cfg.Name, err)
				return c
			}
		}
		_ = ae
	}

	return c
}

// lookupPowerUserARN returns the full ARN of the PowerUser SSO permission-set
// role (Python get_role_arn_for_permission_set), or "" when not found.
func (c *EKSCluster) lookupPowerUserARN() string {
	roles, err := awsiam.GetRoles(c.ctx, &awsiam.GetRolesArgs{
		NameRegex:  pulumi.StringRef(".*_PowerUser_.*"),
		PathPrefix: pulumi.StringRef("/aws-reserved/sso.amazonaws.com/"),
	}, nil)
	if err != nil || len(roles.Names) == 0 {
		return ""
	}
	role, err := awsiam.LookupRole(c.ctx, &awsiam.LookupRoleArgs{Name: roles.Names[0]}, nil)
	if err != nil {
		return ""
	}
	return role.Arn
}

// ── EBS CSI driver + storage classes ────────────────────────────────────────

// WithEbsCsiDriver installs the aws-ebs-csi-driver EKS managed add-on plus its
// IRSA service-account role (ebs-csi-controller-sa) with AmazonEBSCSIDriverPolicyV2.
// Sets c.ebsCsiAddon (required by WithEncryptedEbsStorageClass). Mirrors
// with_ebs_csi_driver. roleName retains the .posit.team suffix.
func (c *EKSCluster) WithEbsCsiDriver(roleName, version string) *EKSCluster {
	if c.err != nil {
		return c
	}
	saRole, err := c.createServiceAccountRole(
		roleName,
		[]serviceAccount{{name: "ebs-csi-controller-sa", namespace: "kube-system"}},
		"arn:aws:iam::aws:policy/AmazonEBSCSIDriverPolicyV2",
		c.cfg.Name+"-ebs-csi-driver",
	)
	if err != nil {
		c.err = err
		return c
	}

	configValues, _ := json.Marshal(map[string]interface{}{
		"defaultStorageClass": map[string]interface{}{"enabled": true},
	})

	addonArgs := &awseks.AddonArgs{
		AddonName:             pulumi.String("aws-ebs-csi-driver"),
		ClusterName:           pulumi.String(c.cfg.Name),
		ServiceAccountRoleArn: saRole.Arn,
		Tags:                  c.cluster.Tags,
		ConfigurationValues:   pulumi.String(string(configValues)),
	}
	if version != "" {
		addonArgs.AddonVersion = pulumi.String(version)
	}
	addon, err := awseks.NewAddon(c.ctx, c.cfg.Name+"-ebs-csi", addonArgs,
		c.clusterChildAlias("aws:eks/addon:Addon", c.cfg.Name+"-ebs-csi"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create EBS CSI addon for %s: %w", c.cfg.Name, err)
		return c
	}
	c.ebsCsiAddon = addon

	return c
}

// WithEncryptedEbsStorageClass creates an encrypted, default StorageClass and
// patches the addon-created ebs-csi-default-sc to non-default. The patch depends
// on the encrypted class AND the EBS CSI addon (so the default sc exists). Mirrors
// with_encrypted_ebs_storage_class. MUST run after WithEbsCsiDriver.
func (c *EKSCluster) WithEncryptedEbsStorageClass() *EKSCluster {
	if c.err != nil {
		return c
	}
	const scName = "ebs-csi-default-sc-encrypted"
	sc, err := storagev1.NewStorageClass(c.ctx, c.cfg.Name+"-"+scName, &storagev1.StorageClassArgs{
		ApiVersion:           pulumi.String("storage.k8s.io/v1"),
		Provisioner:          pulumi.String("ebs.csi.aws.com"),
		AllowVolumeExpansion: pulumi.Bool(true),
		Parameters:           pulumi.StringMap{"encrypted": pulumi.String("true")},
		Metadata: &metav1.ObjectMetaArgs{
			Name:        pulumi.String(scName),
			Annotations: pulumi.StringMap{"storageclass.kubernetes.io/is-default-class": pulumi.String("true")},
		},
		ReclaimPolicy:     pulumi.String("Delete"),
		VolumeBindingMode: pulumi.String("WaitForFirstConsumer"),
	}, c.providerOpt(), c.clusterChildAlias("kubernetes:storage.k8s.io/v1:StorageClass", c.cfg.Name+"-"+scName))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create encrypted storage class for %s: %w", c.cfg.Name, err)
		return c
	}

	dependsOn := []pulumi.Resource{sc}
	if c.ebsCsiAddon != nil {
		dependsOn = append(dependsOn, c.ebsCsiAddon)
	}
	_, err = storagev1.NewStorageClassPatch(c.ctx, c.cfg.Name+"-ebs-csi-default-sc-patch", &storagev1.StorageClassPatchArgs{
		Metadata: &metav1.ObjectMetaPatchArgs{
			Name:        pulumi.String("ebs-csi-default-sc"),
			Annotations: pulumi.StringMap{"storageclass.kubernetes.io/is-default-class": pulumi.String("false")},
		},
	}, c.providerOpt(), pulumi.DependsOn(dependsOn),
		c.clusterChildAlias("kubernetes:storage.k8s.io/v1:StorageClassPatch", c.cfg.Name+"-ebs-csi-default-sc-patch"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to patch default storage class for %s: %w", c.cfg.Name, err)
		return c
	}

	return c
}

// WithGp3 creates the legacy non-default gp3 StorageClass. Mirrors with_gp3.
func (c *EKSCluster) WithGp3() *EKSCluster {
	if c.err != nil {
		return c
	}
	_, err := storagev1.NewStorageClass(c.ctx, c.cfg.Name+"-gp3", &storagev1.StorageClassArgs{
		ApiVersion:           pulumi.String("storage.k8s.io/v1"),
		Provisioner:          pulumi.String("kubernetes.io/aws-ebs"),
		AllowVolumeExpansion: pulumi.Bool(true),
		Parameters:           pulumi.StringMap{"type": pulumi.String("gp3"), "encrypted": pulumi.String("true")},
		Metadata: &metav1.ObjectMetaArgs{
			Name:        pulumi.String("gp3"),
			Annotations: pulumi.StringMap{"storageclass.kubernetes.io/is-default-class": pulumi.String("false")},
		},
		VolumeBindingMode: pulumi.String("WaitForFirstConsumer"),
	}, c.providerOpt(), c.clusterChildAlias("kubernetes:storage.k8s.io/v1:StorageClass", c.cfg.Name+"-gp3"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create gp3 storage class for %s: %w", c.cfg.Name, err)
		return c
	}
	return c
}

// ── EFS CSI driver + secrets-store CSI ───────────────────────────────────────

// WithEfsCsiDriver installs the aws-efs-csi-driver EKS managed add-on plus its
// IRSA service-account role (efs-csi-controller-sa) with AmazonEFSCSIDriverPolicy.
// Mirrors with_efs_csi_driver. roleName retains the .posit.team suffix.
func (c *EKSCluster) WithEfsCsiDriver(roleName string) *EKSCluster {
	if c.err != nil {
		return c
	}
	saRole, err := c.createServiceAccountRole(
		roleName,
		[]serviceAccount{{name: "efs-csi-controller-sa", namespace: "kube-system"}},
		"arn:aws:iam::aws:policy/service-role/AmazonEFSCSIDriverPolicy",
		c.cfg.Name+"-efs-csi-driver",
	)
	if err != nil {
		c.err = err
		return c
	}

	_, err = awseks.NewAddon(c.ctx, c.cfg.Name+"-efs-csi", &awseks.AddonArgs{
		AddonName:             pulumi.String("aws-efs-csi-driver"),
		ClusterName:           pulumi.String(c.cfg.Name),
		ServiceAccountRoleArn: saRole.Arn,
		Tags:                  c.cluster.Tags,
	}, c.clusterChildAlias("aws:eks/addon:Addon", c.cfg.Name+"-efs-csi"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create EFS CSI addon for %s: %w", c.cfg.Name, err)
		return c
	}

	return c
}

// AttachEfsSecurityGroup attaches securityGroupID to the EFS file system's mount
// targets, mirroring attach_efs_security_group. When mountTargetsManaged is false
// this is a no-op (BYO-EFS). The actual modify runs during apply via an ApplyT
// side-effect on the (known) SG id, faithfully reproducing Python's
// pulumi.Output.all(...).apply(...) — there is no Pulumi resource and thus no
// state footprint. Idempotent (skips targets that already have the SG).
func (c *EKSCluster) AttachEfsSecurityGroup(fileSystemID, securityGroupID string, mountTargetsManaged bool) *EKSCluster {
	if c.err != nil {
		return c
	}
	if !mountTargetsManaged {
		return c
	}
	if c.cfg.Credentials == nil {
		c.err = fmt.Errorf("eks: AttachEfsSecurityGroup requires Credentials for %s", c.cfg.Name)
		return c
	}

	// Side-effect on the cluster name (a known input) — matches Python applying on
	// plain config values. The lookup + modify are read-then-write and idempotent.
	c.cluster.Name.ApplyT(func(_ string) (string, error) {
		bg := context.Background()
		targets, err := GetEFSMountTargets(bg, c.cfg.Credentials, c.cfg.Region, fileSystemID)
		if err != nil {
			return "", err
		}
		if err := AttachSecurityGroupToMountTargets(bg, c.cfg.Credentials, c.cfg.Region, securityGroupID, targets); err != nil {
			return "", err
		}
		return securityGroupID, nil
	})

	return c
}

// WithAwsSecretsStoreCsiDriverProvider installs the
// aws-secrets-store-csi-driver-provider EKS managed add-on. Mirrors
// with_aws_secrets_store_csi_driver_provider.
func (c *EKSCluster) WithAwsSecretsStoreCsiDriverProvider() *EKSCluster {
	if c.err != nil {
		return c
	}
	configValues, _ := json.Marshal(map[string]interface{}{
		"tolerations": []map[string]interface{}{
			{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
		},
		"secrets-store-csi-driver": map[string]interface{}{
			"enableSecretRotation": true,
			"rotationPollInterval": "15s",
			"syncSecret":           map[string]interface{}{"enabled": true},
		},
	})

	_, err := awseks.NewAddon(c.ctx, c.cfg.Name+"-aws-secrets-store-csi-driver-provider", &awseks.AddonArgs{
		AddonName:                pulumi.String("aws-secrets-store-csi-driver-provider"),
		ClusterName:              pulumi.String(c.cfg.Name),
		Tags:                     c.cluster.Tags,
		ResolveConflictsOnCreate: pulumi.String("OVERWRITE"),
		ResolveConflictsOnUpdate: pulumi.String("OVERWRITE"),
		ConfigurationValues:      pulumi.String(string(configValues)),
	}, c.clusterChildAlias("aws:eks/addon:Addon", c.cfg.Name+"-aws-secrets-store-csi-driver-provider"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create secrets-store CSI addon for %s: %w", c.cfg.Name, err)
		return c
	}

	return c
}

// ── SG access (bastion / tailscale ingress) ──────────────────────────────────

// SetupTailscaleAccess opens the cluster SG to the tailscale SG. Mirrors
// setup_tailscale_access (sg_name = "{sg_prefix}-tailscale").
func (c *EKSCluster) SetupTailscaleAccess() {
	c.setupSGAccess(c.cfg.SgPrefix + "-tailscale")
}

// SetupBastionAccess opens the cluster SG to the bastion SG. Mirrors
// setup_bastion_access (sg_name = "{sg_prefix}-bastion").
func (c *EKSCluster) SetupBastionAccess() {
	c.setupSGAccess(c.cfg.SgPrefix + "-bastion")
}

// setupSGAccess adds an ingress rule allowing all traffic from the named SG to
// the cluster's primary security group, mirroring _setup_sg_access. Looks up the
// source SG by Name tag in the cluster VPC via the ec2 LookupSecurityGroup data
// source. Skipped when the cluster SG id is not yet known (greenfield) — the same
// effect as Python's apply running against an unset cluster_security_group_id.
func (c *EKSCluster) setupSGAccess(sgName string) {
	if c.err != nil {
		return
	}
	if c.cfg.ClusterSecurityGroupID == "" {
		// Greenfield: no live cluster SG to target yet. (Python's apply on
		// vpc_config would have nothing to do until the cluster's SG exists.)
		return
	}

	sg, err := awsec2.LookupSecurityGroup(c.ctx, &awsec2.LookupSecurityGroupArgs{
		Filters: []awsec2.GetSecurityGroupFilter{
			{Name: "vpc-id", Values: []string{c.cfg.VpcID}},
		},
		Tags: map[string]string{"Name": sgName},
	}, nil)
	if err != nil {
		c.err = fmt.Errorf("eks: failed to look up SG %s for %s: %w", sgName, c.cfg.Name, err)
		return
	}

	// Python created the rule as a top-level resource (no parent, no alias).
	_, err = awsec2.NewSecurityGroupRule(c.ctx, sgName+"-internal-vpc-allow-inbound", &awsec2.SecurityGroupRuleArgs{
		Type:                  pulumi.String("ingress"),
		FromPort:              pulumi.Int(0),
		ToPort:                pulumi.Int(0),
		Protocol:              pulumi.String("-1"),
		SecurityGroupId:       pulumi.String(c.cfg.ClusterSecurityGroupID),
		SourceSecurityGroupId: pulumi.String(sg.Id),
	})
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create SG ingress rule for %s: %w", sgName, err)
		return
	}
}
