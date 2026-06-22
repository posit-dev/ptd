package steps

import (
	"context"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/posit-dev/ptd/lib/types/typestest"
)

// --- mocks ---

type eksStepMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *eksStepMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	outputs := args.Inputs
	if args.TypeToken == "aws:eks/cluster:Cluster" {
		outputs = resource.PropertyMap{}
		for k, v := range args.Inputs {
			outputs[k] = v
		}
		outputs["identities"] = resource.NewArrayProperty([]resource.PropertyValue{
			resource.NewObjectProperty(resource.PropertyMap{
				"oidcs": resource.NewArrayProperty([]resource.PropertyValue{
					resource.NewObjectProperty(resource.PropertyMap{
						"issuer": resource.NewStringProperty("https://oidc.eks.us-east-1.amazonaws.com/id/TEST"),
					}),
				}),
			}),
		})
	}
	return args.Name + "_id", outputs, nil
}

func (m *eksStepMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
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
		// PowerUser SSO permission-set role lookup (aws-auth + access-entries paths).
		return resource.PropertyMap{
			"names": resource.NewArrayProperty([]resource.PropertyValue{
				resource.NewStringProperty("AWSReservedSSO_PowerUser_abc123"),
			}),
			"arns": resource.NewArrayProperty([]resource.PropertyValue{
				resource.NewStringProperty("arn:aws:iam::123456789012:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_PowerUser_abc123"),
			}),
		}, nil
	case "aws:iam/getRole:getRole":
		return resource.PropertyMap{
			"arn": resource.NewStringProperty("arn:aws:iam::123456789012:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_PowerUser_abc123"),
		}, nil
	}
	return resource.PropertyMap{}, nil
}

func (m *eksStepMocks) byType(typeToken string) []pulumi.MockResourceArgs {
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

func (m *eksStepMocks) findResource(name string) *pulumi.MockResourceArgs {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.resources {
		if m.resources[i].Name == name {
			return &m.resources[i]
		}
	}
	return nil
}

// mockAWSWorkloadTarget is defined in persistent_test.go (same package).

// --- step metadata ---

func TestEKSStepName(t *testing.T) {
	assert.Equal(t, "eks", (&EKSStep{}).Name())
}

func TestEKSStepProxyRequired(t *testing.T) {
	assert.True(t, (&EKSStep{}).ProxyRequired())
}

func TestEKSStepNilTarget(t *testing.T) {
	err := (&EKSStep{}).Run(context.Background())
	require.Error(t, err)
}

func TestEKSStepUnsupportedCloud(t *testing.T) {
	tgt := &typestest.MockTarget{}
	tgt.On("CloudProvider").Return(types.Azure)
	err := (&EKSStep{DstTarget: tgt}).Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported cloud provider for eks")
}

func TestClusterStepName(t *testing.T) {
	assert.Equal(t, "cluster", (&ClusterStep{}).Name())
}

func TestClusterStepRejectsNonControlRoom(t *testing.T) {
	tgt := &typestest.MockTarget{}
	tgt.On("ControlRoom").Return(false)
	err := (&ClusterStep{DstTarget: tgt}).Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "control room")
}

func TestClusterStepUnsupportedCloud(t *testing.T) {
	tgt := &typestest.MockTarget{}
	tgt.On("ControlRoom").Return(true)
	tgt.On("CloudProvider").Return(types.Azure)
	err := (&ClusterStep{DstTarget: tgt}).Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported cloud provider for cluster")
}

// --- deploy core ---

func newTestEKSParams() awsEKSParams {
	return awsEKSParams{
		compoundName:              "wl01-staging",
		region:                    "us-east-1",
		requiredTags:              map[string]string{"posit.team/managed-by": eksManagedByValue},
		iamPermissionsBoundaryARN: "arn:aws:iam::123456789012:policy/PositTeamDedicatedAdmin",
		accountID:                 "123456789012",
		thirdPartyTelemetry:       true,
		clusters: map[string]types.AWSWorkloadClusterConfig{
			// Default to the modern access-entries auth path (avoids the aws-auth
			// ConfigMap branch; dedicated tests cover both branches).
			"20250101": {Spec: types.AWSWorkloadClusterSpec{
				EksAccessEntries: &types.EKSAccessEntriesConfig{Enabled: boolPtr(true)},
			}},
		},
		perCluster: map[string]eksClusterData{
			"20250101": {
				subnetIDs:        []string{"subnet-aaa", "subnet-bbb"},
				clusterExists:    true,
				currentAuthMode:  "API_AND_CONFIG_MAP",
				kubeconfig:       "apiVersion: v1\n",
				securityGroupIDs: []string{"sg-fsx", "sg-cluster"},
				vpcID:            "vpc-123",
				clusterSGID:      "sg-cluster",
			},
		},
	}
}

func TestAWSEKSDeployCore(t *testing.T) {
	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), newTestEKSParams())
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	// One cluster, one default node group, OIDC provider, k8s provider.
	assert.Len(t, mocks.byType("aws:eks/cluster:Cluster"), 1)
	assert.Len(t, mocks.byType("aws:eks/nodeGroup:NodeGroup"), 1)
	assert.Len(t, mocks.byType("aws:iam/openIdConnectProvider:OpenIdConnectProvider"), 1)
	assert.Len(t, mocks.byType("pulumi:providers:kubernetes"), 1)
	// IAM roles: cluster + node + EBS CSI IRSA (EFS off, secrets-store off).
	assert.Len(t, mocks.byType("aws:iam/role:Role"), 3)

	// EBS CSI managed add-on present; EFS add-on absent (not enabled).
	addons := mocks.byType("aws:eks/addon:Addon")
	assert.Len(t, addons, 1)
	assert.Equal(t, "wl01-staging-20250101-ebs-csi", addons[0].Name)

	// IRSA EBS role retains the .posit.team suffix.
	assert.NotNil(t, mocks.findResource("wl01-staging-20250101-ebs-csi-driver.posit.team"))

	// Access-entries auth path: admin access entry + node access entry created
	// (no aws-auth ConfigMapPatch).
	assert.Len(t, mocks.byType("kubernetes:core/v1:ConfigMapPatch"), 0)
	assert.GreaterOrEqual(t, len(mocks.byType("aws:eks/accessEntry:AccessEntry")), 2)

	// Tigera/Calico: namespace + helm release + 2 patches.
	assert.Len(t, mocks.byType("kubernetes:helm.sh/v3:Release"), 1)

	// Storage classes: gp3 + encrypted (default), plus the default-sc patch.
	scs := mocks.byType("kubernetes:storage.k8s.io/v1:StorageClass")
	assert.Len(t, scs, 2)
	assert.Len(t, mocks.byType("kubernetes:storage.k8s.io/v1:StorageClassPatch"), 1)
	// The encrypted storage class is marked default; gp3 is not.
	for _, sc := range scs {
		meta := sc.Inputs["metadata"].ObjectValue()
		ann := meta["annotations"].ObjectValue()
		isDefault := ann["storageclass.kubernetes.io/is-default-class"]
		if sc.Name == "wl01-staging-20250101-ebs-csi-default-sc-encrypted" {
			assert.Equal(t, resource.NewStringProperty("true"), isDefault)
		}
		if sc.Name == "wl01-staging-20250101-gp3" {
			assert.Equal(t, resource.NewStringProperty("false"), isDefault)
		}
	}

	// Cluster logical name = "{compound}-{release}".
	clusters := mocks.byType("aws:eks/cluster:Cluster")
	require.Len(t, clusters, 1)
	assert.Equal(t, "wl01-staging-20250101", clusters[0].Name)

	// Default node group uses the Python default sizes (mp_min_size=4, mp_max_size=10).
	for _, r := range mocks.byType("aws:eks/nodeGroup:NodeGroup") {
		sc := r.Inputs["scalingConfig"].ObjectValue()
		assert.Equal(t, resource.NewNumberProperty(4), sc["minSize"])
		assert.Equal(t, resource.NewNumberProperty(10), sc["maxSize"])
		assert.Equal(t, resource.NewStringProperty("AL2023_x86_64_STANDARD"), r.Inputs["amiType"])
	}
}

// TestAWSEKSDeployAccessEntriesDefaultOn reproduces the wl01 default-parity bug:
// when the eks_access_entries block is present but `enabled` is unset (nil), the
// cluster must still use the access-entries auth path (Python default True), NOT
// the legacy aws-auth ConfigMap. A regression here would create an aws-auth
// ConfigMapPatch and delete the live access entries.
func TestAWSEKSDeployAccessEntriesDefaultOn(t *testing.T) {
	params := newTestEKSParams()
	// Block present, IncludeSameAccountPoweruser set, but Enabled left nil — the
	// exact shape that previously fell through to aws-auth in Go.
	params.clusters["20250101"] = types.AWSWorkloadClusterConfig{
		Spec: types.AWSWorkloadClusterSpec{
			EksAccessEntries: &types.EKSAccessEntriesConfig{IncludeSameAccountPoweruser: true},
		},
	}

	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	// Access-entries path: no aws-auth ConfigMapPatch, access entries created.
	assert.Len(t, mocks.byType("kubernetes:core/v1:ConfigMapPatch"), 0)
	assert.GreaterOrEqual(t, len(mocks.byType("aws:eks/accessEntry:AccessEntry")), 2)
}

func (m *eksStepMocks) aliasURNsFor(name string) []string {
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

func TestAWSEKSDeployTigeraAliasChain(t *testing.T) {
	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), newTestEKSParams())
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	// Tigera namespace + helm release + 2 patches.
	assert.Len(t, mocks.byType("kubernetes:core/v1:Namespace"), 1)
	assert.Len(t, mocks.byType("kubernetes:helm.sh/v3:Release"), 1)

	// The tigera helm release aliases under ptd:AWSWorkloadEKS$ptd:TigeraOperator.
	const stack = "wl01-staging"
	const proj = "ptd-aws-workload-eks"
	helmAliases := mocks.aliasURNsFor("wl01-staging-20250101-tigera-operator")
	assert.Contains(t, helmAliases,
		"urn:pulumi:"+stack+"::"+proj+"::ptd:AWSWorkloadEKS$ptd:TigeraOperator$kubernetes:helm.sh/v3:Release::wl01-staging-20250101-tigera-operator")

	nsAliases := mocks.aliasURNsFor("wl01-staging-20250101-tigera-ns")
	assert.Contains(t, nsAliases,
		"urn:pulumi:"+stack+"::"+proj+"::ptd:AWSWorkloadEKS$ptd:TigeraOperator$kubernetes:core/v1:Namespace::wl01-staging-20250101-tigera-ns")
}

func TestAWSEKSDeployEfsEnabled(t *testing.T) {
	params := newTestEKSParams()
	params.clusters["20250101"] = types.AWSWorkloadClusterConfig{
		Spec: types.AWSWorkloadClusterSpec{
			EksAccessEntries:   &types.EKSAccessEntriesConfig{Enabled: boolPtr(true)},
			EnableEfsCsiDriver: true,
		},
	}

	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	// EBS + EFS managed add-ons present.
	addonNames := map[string]bool{}
	for _, a := range mocks.byType("aws:eks/addon:Addon") {
		addonNames[a.Name] = true
	}
	assert.True(t, addonNames["wl01-staging-20250101-ebs-csi"])
	assert.True(t, addonNames["wl01-staging-20250101-efs-csi"])
	// IAM roles: cluster + node + EBS IRSA + EFS IRSA = 4.
	assert.Len(t, mocks.byType("aws:iam/role:Role"), 4)
}

func TestAWSEKSDeployAdditionalNodeGroups(t *testing.T) {
	params := newTestEKSParams()
	gpu := "gpu"
	min2 := 2
	params.clusters["20250101"] = types.AWSWorkloadClusterConfig{
		Spec: types.AWSWorkloadClusterSpec{
			AdditionalNodeGroups: map[string]types.NodeGroupConfig{
				gpu: {MinSize: &min2},
			},
		},
	}

	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	// Default + 1 additional node group.
	assert.Len(t, mocks.byType("aws:eks/nodeGroup:NodeGroup"), 2)
	// The additional node group is named "{compound}-{release}-{ngName}".
	assert.NotNil(t, mocks.findResource("wl01-staging-20250101-gpu"))
}
