package aws

import (
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eksMocks records every registered resource and answers the data-source calls
// the builder makes. The OIDC thumbprint is no longer a data-source lookup — it
// is pre-fetched and injected via EKSClusterConfig.OIDCThumbprint.
type eksMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *eksMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	outputs := args.Inputs
	// The EKS cluster output must expose an OIDC issuer for WithOidcProvider.
	if args.TypeToken == "aws:eks/cluster:Cluster" {
		outputs = resource.PropertyMap{}
		for k, v := range args.Inputs {
			outputs[k] = v
		}
		outputs["identities"] = resource.NewArrayProperty([]resource.PropertyValue{
			resource.NewObjectProperty(resource.PropertyMap{
				"oidcs": resource.NewArrayProperty([]resource.PropertyValue{
					resource.NewObjectProperty(resource.PropertyMap{
						"issuer": resource.NewStringProperty("https://oidc.eks.us-east-1.amazonaws.com/id/TESTOIDC"),
					}),
				}),
			}),
		})
	}
	return args.Name + "_id", outputs, nil
}

func (m *eksMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	switch args.Token {
	case "tls:index/getCertificate:getCertificate":
		return resource.PropertyMap{
			"certificates": resource.NewArrayProperty([]resource.PropertyValue{
				resource.NewObjectProperty(resource.PropertyMap{
					"sha1Fingerprint": resource.NewStringProperty("0123456789abcdef0123456789abcdef01234567"),
				}),
			}),
		}, nil
	case "aws:iam/getRoles:getRoles":
		return resource.PropertyMap{
			"names": resource.NewArrayProperty([]resource.PropertyValue{
				resource.NewStringProperty("AWSReservedSSO_PowerUser_abc123"),
			}),
		}, nil
	case "aws:iam/getRole:getRole":
		return resource.PropertyMap{
			"arn": resource.NewStringProperty("arn:aws:iam::123456789012:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_PowerUser_abc123"),
		}, nil
	case "aws:ec2/getSecurityGroup:getSecurityGroup":
		return resource.PropertyMap{
			"id": resource.NewStringProperty("sg-bastion"),
		}, nil
	}
	return resource.PropertyMap{}, nil
}

// nameOf returns the registered resource with the given name, or nil.
func (m *eksMocks) nameOf(name string) *pulumi.MockResourceArgs {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.resources {
		if m.resources[i].Name == name {
			return &m.resources[i]
		}
	}
	return nil
}

func (m *eksMocks) byType(typeToken string) []pulumi.MockResourceArgs {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []pulumi.MockResourceArgs
	for _, r := range m.resources {
		if r.TypeToken == typeToken {
			out = append(out, r)
		}
	}
	return out
}

func (m *eksMocks) aliasURNsFor(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var urns []string
	for _, r := range m.resources {
		if r.Name != name || r.RegisterRPC == nil {
			continue
		}
		for _, a := range r.RegisterRPC.GetAliases() {
			if urn := a.GetUrn(); urn != "" {
				urns = append(urns, urn)
			}
		}
	}
	return urns
}

// indexOfType returns the index in the registration order of the first resource
// with the given type token, or -1.
func (m *eksMocks) indexOfType(typeToken string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.resources {
		if r.TypeToken == typeToken {
			return i
		}
	}
	return -1
}

func runEKSBuilder(t *testing.T, mocks *eksMocks) {
	t.Helper()
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, EKSClusterConfig{
			Name:                   "wl01-staging-20250101",
			SubnetIDs:              []string{"subnet-aaa", "subnet-bbb"},
			Version:                "1.35",
			Tags:                   map[string]string{"posit.team/managed-by": "ptd.pulumi_resources.aws_workload_eks"},
			DefaultAddonsToRemove:  []string{"vpc-cni"},
			EnabledClusterLogTypes: []string{"api", "audit"},
			EksRoleName:            "wl01-staging-20250101-eks.posit.team",
			IAMPermissionsBoundary: "arn:aws:iam::123456789012:policy/PositTeamDedicatedAdmin",
			Kubeconfig:             "apiVersion: v1\nkind: Config\n",
			ProjectName:            "ptd-aws-workload-eks",
			ParentTypeChain:        "ptd:AWSWorkloadEKS$ptd:AWSEKSCluster",
			WrapperTypeChain:       "ptd:AWSWorkloadEKS",
		})
		require.NoError(t, err)

		c.
			WithNodeRole("wl01-staging-20250101-eks-node.posit.team").
			WithNodeGroup(NodeGroupParams{
				Name:             "wl01-staging-20250101",
				SecurityGroupIDs: []string{"sg-fsx"},
				InstanceType:     "t3.large",
				VolumeSize:       200,
				AmiType:          "AL2023_x86_64_STANDARD",
				MinSize:          4,
				MaxSize:          10,
				DesiredSize:      4,
				Version:          "1.35",
				Tags:             map[string]string{"posit.team/managed-by": "ptd.pulumi_resources.aws_workload_eks"},
			}).
			WithOidcProvider()

		return c.Err()
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)
}

func TestEKSBuilderResourceCount(t *testing.T) {
	mocks := &eksMocks{}
	runEKSBuilder(t, mocks)

	// Core resources: cluster role, cluster-policy attachment, cluster, k8s
	// provider, node role, 4 node-role policy attachments, launch template, node
	// group, OIDC provider = 11.
	assert.Len(t, mocks.byType("aws:eks/cluster:Cluster"), 1)
	assert.Len(t, mocks.byType("aws:iam/role:Role"), 2) // cluster role + node role
	assert.Len(t, mocks.byType("aws:iam/rolePolicyAttachment:RolePolicyAttachment"), 5)
	assert.Len(t, mocks.byType("aws:ec2/launchTemplate:LaunchTemplate"), 1)
	assert.Len(t, mocks.byType("aws:eks/nodeGroup:NodeGroup"), 1)
	assert.Len(t, mocks.byType("aws:iam/openIdConnectProvider:OpenIdConnectProvider"), 1)
	assert.Len(t, mocks.byType("pulumi:providers:kubernetes"), 1)
}

func TestEKSClusterLogicalName(t *testing.T) {
	mocks := &eksMocks{}
	runEKSBuilder(t, mocks)

	clusters := mocks.byType("aws:eks/cluster:Cluster")
	require.Len(t, clusters, 1)
	// CRITICAL: the EKS cluster logical name must be byte-identical to the Python
	// `name` (the compound "{compound}-{release}"). A changed name replaces the
	// live cluster.
	assert.Equal(t, "wl01-staging-20250101", clusters[0].Name)
	// And the cluster's `name` input equals the logical name.
	assert.Equal(t, resource.NewStringProperty("wl01-staging-20250101"), clusters[0].Inputs["name"])
}

func TestEKSAuthModePreservation(t *testing.T) {
	// Existing cluster → no access_config (preserve live auth mode, avoid replace).
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewEKSCluster(ctx, EKSClusterConfig{
			Name:             "wl01-staging-20250101",
			SubnetIDs:        []string{"subnet-aaa"},
			Version:          "1.35",
			ClusterExists:    true,
			CurrentAuthMode:  "API_AND_CONFIG_MAP",
			Kubeconfig:       "apiVersion: v1\n",
			ProjectName:      "ptd-aws-workload-eks",
			ParentTypeChain:  "ptd:AWSWorkloadEKS$ptd:AWSEKSCluster",
			WrapperTypeChain: "ptd:AWSWorkloadEKS",
		})
		return err
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)

	clusters := mocks.byType("aws:eks/cluster:Cluster")
	require.Len(t, clusters, 1)
	_, hasAccessConfig := clusters[0].Inputs["accessConfig"]
	assert.False(t, hasAccessConfig, "existing cluster must not set accessConfig (would force replace)")

	// Greenfield cluster → access_config with API_AND_CONFIG_MAP.
	mocks2 := &eksMocks{}
	err = pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewEKSCluster(ctx, EKSClusterConfig{
			Name:             "wl01-staging-20250101",
			SubnetIDs:        []string{"subnet-aaa"},
			Version:          "1.35",
			ClusterExists:    false,
			Kubeconfig:       "apiVersion: v1\n",
			ProjectName:      "ptd-aws-workload-eks",
			ParentTypeChain:  "ptd:AWSWorkloadEKS$ptd:AWSEKSCluster",
			WrapperTypeChain: "ptd:AWSWorkloadEKS",
		})
		return err
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks2))
	require.NoError(t, err)
	clusters2 := mocks2.byType("aws:eks/cluster:Cluster")
	require.Len(t, clusters2, 1)
	_, hasAccessConfig2 := clusters2[0].Inputs["accessConfig"]
	assert.True(t, hasAccessConfig2, "greenfield cluster must set accessConfig")
}

func TestEKSNodeRolePrecedesNodeGroup(t *testing.T) {
	mocks := &eksMocks{}
	runEKSBuilder(t, mocks)

	roleIdx := mocks.indexOfType("aws:iam/role:Role")
	ngIdx := mocks.indexOfType("aws:eks/nodeGroup:NodeGroup")
	require.NotEqual(t, -1, roleIdx)
	require.NotEqual(t, -1, ngIdx)
	assert.Less(t, roleIdx, ngIdx, "node role must be registered before node group")
}

func TestEKSAliasURNs(t *testing.T) {
	mocks := &eksMocks{}
	runEKSBuilder(t, mocks)

	const stack = "wl01-staging-20250101"
	const proj = "ptd-aws-workload-eks"
	const eksParent = "ptd:AWSWorkloadEKS$ptd:AWSEKSCluster"

	// Cluster alias: full URN under the AWSEKSCluster component.
	clusterAliases := mocks.aliasURNsFor("wl01-staging-20250101")
	assert.Contains(t, clusterAliases,
		"urn:pulumi:"+stack+"::"+proj+"::"+eksParent+"$aws:eks/cluster:Cluster::wl01-staging-20250101")
	// The node group shares the logical name with the cluster; it aliases under
	// the cluster as parent.
	assert.Contains(t, clusterAliases,
		"urn:pulumi:"+stack+"::"+proj+"::"+eksParent+"$aws:eks/cluster:Cluster$aws:eks/nodeGroup:NodeGroup::wl01-staging-20250101")
	// The launch template (also shares the name) aliases under the WRAPPER.
	assert.Contains(t, clusterAliases,
		"urn:pulumi:"+stack+"::"+proj+"::ptd:AWSWorkloadEKS$aws:ec2/launchTemplate:LaunchTemplate::wl01-staging-20250101")

	// OIDC provider alias (under the cluster as parent).
	oidcAliases := mocks.aliasURNsFor("wl01-staging-20250101")
	assert.Contains(t, oidcAliases,
		"urn:pulumi:"+stack+"::"+proj+"::"+eksParent+"$aws:eks/cluster:Cluster$aws:iam/openIdConnectProvider:OpenIdConnectProvider::wl01-staging-20250101")

	// K8s provider alias is a top-level resource named "{name}-k8s".
	providerAliases := mocks.aliasURNsFor("wl01-staging-20250101-k8s")
	assert.Contains(t, providerAliases,
		"urn:pulumi:"+stack+"::"+proj+"::pulumi:providers:kubernetes::wl01-staging-20250101-k8s")

	// Node role alias (under the cluster as parent).
	nodeRoleAliases := mocks.aliasURNsFor("wl01-staging-20250101-eks-node")
	assert.Contains(t, nodeRoleAliases,
		"urn:pulumi:"+stack+"::"+proj+"::"+eksParent+"$aws:eks/cluster:Cluster$aws:iam/role:Role::wl01-staging-20250101-eks-node")

	// Every alias URN is well-formed (begins with the urn prefix, no empty segments).
	for _, name := range []string{"wl01-staging-20250101", "wl01-staging-20250101-k8s", "wl01-staging-20250101-eks-node"} {
		for _, urn := range mocks.aliasURNsFor(name) {
			assert.True(t, strings.HasPrefix(urn, "urn:pulumi:"+stack+"::"+proj+"::"), "alias URN %q malformed", urn)
			assert.NotContains(t, urn, "$$", "alias URN %q has empty type segment", urn)
		}
	}
}

// baseCfg returns an EKSClusterConfig wired for the full addon-path builder tests.
func baseCfg() EKSClusterConfig {
	return EKSClusterConfig{
		Name:                   "wl01-staging-20250101",
		SubnetIDs:              []string{"subnet-aaa"},
		Version:                "1.35",
		Kubeconfig:             "apiVersion: v1\n",
		ProjectName:            "ptd-aws-workload-eks",
		ParentTypeChain:        "ptd:AWSWorkloadEKS$ptd:AWSEKSCluster",
		WrapperTypeChain:       "ptd:AWSWorkloadEKS",
		AccountID:              "123456789012",
		IAMPermissionsBoundary: "arn:aws:iam::123456789012:policy/PositTeamDedicatedAdmin",
		ClusterExists:          true,
		CurrentAuthMode:        "API_AND_CONFIG_MAP",
		// Greenfield-style: no live cluster SG → SG-access wiring is skipped (keeps
		// the addon tests focused; a dedicated test covers SG access).
	}
}

func TestEKSWithEbsCsiAndStorageClasses(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, baseCfg())
		require.NoError(t, err)
		c.WithNodeRole("wl01-staging-20250101-eks-node.posit.team").
			WithEbsCsiDriver("wl01-staging-20250101-ebs-csi-driver.posit.team", "v1.41.0-eksbuild.1").
			WithGp3().
			WithEncryptedEbsStorageClass()
		return c.Err()
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)

	// EBS CSI addon + IRSA role (with .posit.team suffix) + managed-policy attachment.
	addons := mocks.byType("aws:eks/addon:Addon")
	require.Len(t, addons, 1)
	assert.Equal(t, "wl01-staging-20250101-ebs-csi", addons[0].Name)
	assert.NotNil(t, mocks.nameOf("wl01-staging-20250101-ebs-csi-driver.posit.team"),
		"IRSA EBS role must retain .posit.team suffix")

	// Two storage classes; the encrypted one is the default.
	scs := mocks.byType("kubernetes:storage.k8s.io/v1:StorageClass")
	require.Len(t, scs, 2)
	enc := mocks.nameOf("wl01-staging-20250101-ebs-csi-default-sc-encrypted")
	require.NotNil(t, enc)
	encAnn := enc.Inputs["metadata"].ObjectValue()["annotations"].ObjectValue()
	assert.Equal(t, resource.NewStringProperty("true"), encAnn["storageclass.kubernetes.io/is-default-class"])

	gp3 := mocks.nameOf("wl01-staging-20250101-gp3")
	require.NotNil(t, gp3)
	gp3Ann := gp3.Inputs["metadata"].ObjectValue()["annotations"].ObjectValue()
	assert.Equal(t, resource.NewStringProperty("false"), gp3Ann["storageclass.kubernetes.io/is-default-class"])

	// The non-default patch on the addon-created sc exists.
	assert.Len(t, mocks.byType("kubernetes:storage.k8s.io/v1:StorageClassPatch"), 1)
}

func TestEKSAccessEntriesBranch(t *testing.T) {
	mocks := &eksMocks{}
	cfg := baseCfg()
	// Pre-seed an existing admin entry (so it imports) + an auto-created node entry.
	cfg.AccessEntries = AccessEntryData{
		Entries: map[string]bool{
			"arn:aws:iam::123456789012:role/admin.posit.team":  true,
			"arn:aws:iam::123456789012:role/wl01-eks-node-xyz": true,
		},
		AssociatedPolicies: map[string]map[string]bool{},
	}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, cfg)
		require.NoError(t, err)
		c.WithNodeRole("wl01-staging-20250101-eks-node.posit.team").
			WithAwsAuth(AwsAuthParams{UseEksAccessEntries: true, IncludePoweruser: true})
		return c.Err()
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)

	// admin + poweruser + node = 3 access entries; no aws-auth ConfigMapPatch.
	assert.Len(t, mocks.byType("aws:eks/accessEntry:AccessEntry"), 3)
	assert.GreaterOrEqual(t, len(mocks.byType("aws:eks/accessPolicyAssociation:AccessPolicyAssociation")), 2)
	assert.Len(t, mocks.byType("kubernetes:core/v1:ConfigMapPatch"), 0)
	// Node entry adopts the existing "eks-node" ARN.
	node := mocks.nameOf("wl01-staging-20250101-node-access-entry")
	require.NotNil(t, node)
	assert.Equal(t, resource.NewStringProperty("arn:aws:iam::123456789012:role/wl01-eks-node-xyz"), node.Inputs["principalArn"])
}

func TestEKSAwsAuthConfigMapBranch(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, baseCfg())
		require.NoError(t, err)
		c.WithNodeRole("wl01-staging-20250101-eks-node.posit.team").
			WithAwsAuth(AwsAuthParams{UseEksAccessEntries: false})
		return c.Err()
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)

	// Legacy path: an aws-auth ConfigMapPatch, no access entries.
	assert.Len(t, mocks.byType("kubernetes:core/v1:ConfigMapPatch"), 1)
	assert.Len(t, mocks.byType("aws:eks/accessEntry:AccessEntry"), 0)
}

func TestEKSEfsCsiDriver(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, baseCfg())
		require.NoError(t, err)
		c.WithNodeRole("wl01-staging-20250101-eks-node.posit.team").
			WithEfsCsiDriver("wl01-staging-20250101-efs-csi-driver.posit.team")
		return c.Err()
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)

	addons := mocks.byType("aws:eks/addon:Addon")
	require.Len(t, addons, 1)
	assert.Equal(t, "wl01-staging-20250101-efs-csi", addons[0].Name)
	// IRSA EFS role retains the .posit.team suffix.
	assert.NotNil(t, mocks.nameOf("wl01-staging-20250101-efs-csi-driver.posit.team"))
}

func TestEKSSecretsStoreCsiProvider(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, baseCfg())
		require.NoError(t, err)
		c.WithAwsSecretsStoreCsiDriverProvider()
		return c.Err()
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)

	addons := mocks.byType("aws:eks/addon:Addon")
	require.Len(t, addons, 1)
	assert.Equal(t, "wl01-staging-20250101-aws-secrets-store-csi-driver-provider", addons[0].Name)
}

func TestEKSSetupBastionAccess(t *testing.T) {
	mocks := &eksMocks{}
	cfg := baseCfg()
	cfg.SgPrefix = "wl01-staging"
	cfg.VpcID = "vpc-123"
	cfg.ClusterSecurityGroupID = "sg-cluster" // present → SG-access wiring runs
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewEKSCluster(ctx, cfg)
		return err
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)

	// A single SG ingress rule named "{sg_prefix}-bastion-internal-vpc-allow-inbound".
	rules := mocks.byType("aws:ec2/securityGroupRule:SecurityGroupRule")
	require.Len(t, rules, 1)
	assert.Equal(t, "wl01-staging-bastion-internal-vpc-allow-inbound", rules[0].Name)
	assert.Equal(t, resource.NewStringProperty("sg-cluster"), rules[0].Inputs["securityGroupId"])
}

// TestEKSOidcProviderUsesInjectedThumbprint asserts WithOidcProvider uses the
// pre-fetched OIDCThumbprint verbatim (not a data-source lookup) for the OIDC
// provider's thumbprint_lists, matching the value Python's get_thumbprint
// produces.
func TestEKSOidcProviderUsesInjectedThumbprint(t *testing.T) {
	const wantThumbprint = "06b25927c42a721631c1efd9431e648fa62e1e39"
	mocks := &eksMocks{}
	cfg := baseCfg()
	cfg.OIDCThumbprint = wantThumbprint
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, cfg)
		require.NoError(t, err)
		c.WithOidcProvider()
		return c.Err()
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
	require.NoError(t, err)

	providers := mocks.byType("aws:iam/openIdConnectProvider:OpenIdConnectProvider")
	require.Len(t, providers, 1)
	tl := providers[0].Inputs["thumbprintLists"].ArrayValue()
	require.Len(t, tl, 1)
	assert.Equal(t, resource.NewStringProperty(wantThumbprint), tl[0],
		"OIDC provider must use the injected thumbprint, not a tls data-source value")
}

// TestEKSOidcProviderProtect asserts the OIDC provider is created with
// pulumi.Protect(true) when ProtectPersistentResources is set, and unprotected
// otherwise. The step forces ProtectPersistentResources=true to match Python's
// default, so the protected case is the real-world path.
func TestEKSOidcProviderProtect(t *testing.T) {
	run := func(protect bool) bool {
		mocks := &eksMocks{}
		cfg := baseCfg()
		cfg.OIDCThumbprint = "06b25927c42a721631c1efd9431e648fa62e1e39"
		cfg.ProtectPersistentResources = protect
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			c, err := NewEKSCluster(ctx, cfg)
			require.NoError(t, err)
			c.WithOidcProvider()
			return c.Err()
		}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging-20250101", mocks))
		require.NoError(t, err)

		providers := mocks.byType("aws:iam/openIdConnectProvider:OpenIdConnectProvider")
		require.Len(t, providers, 1)
		require.NotNil(t, providers[0].RegisterRPC)
		return providers[0].RegisterRPC.GetProtect()
	}

	assert.True(t, run(true), "OIDC provider must be protected when ProtectPersistentResources=true")
	assert.False(t, run(false), "OIDC provider must be unprotected when ProtectPersistentResources=false")
}
