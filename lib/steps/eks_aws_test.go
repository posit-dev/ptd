package steps

import (
	"context"
	"encoding/json"
	"strings"
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
	mu         sync.Mutex
	resources  []pulumi.MockResourceArgs
	callTokens []string
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
	m.mu.Lock()
	m.callTokens = append(m.callTokens, args.Token)
	m.mu.Unlock()
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
	case "aws:route53/getZone:getZone":
		// ExternalDNS IRSA policy resolves each site's hosted-zone ARN.
		return resource.PropertyMap{
			"id":     resource.NewStringProperty("Z0123456789ABC"),
			"zoneId": resource.NewStringProperty("Z0123456789ABC"),
			"arn":    resource.NewStringProperty("arn:aws:route53:::hostedzone/Z0123456789ABC"),
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

func (m *eksStepMocks) countCalls(token string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.callTokens {
		if t == token {
			n++
		}
	}
	return n
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
	p := awsEKSParams{
		compoundName:              "wl01-staging",
		region:                    "us-east-1",
		requiredTags:              map[string]string{"posit.team/managed-by": eksManagedByValue},
		iamPermissionsBoundaryARN: "arn:aws:iam::123456789012:policy/PositTeamDedicatedAdmin",
		accountID:                 "123456789012",
		callerARN:                 "arn:aws:sts::123456789012:assumed-role/admin/x",
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
	// Workload-scoped IRSA roles (phase 2). ExternalDNS is off by default here so
	// the core test doesn't depend on the persistent zone-id output; dedicated
	// tests exercise the ExternalDNS path. zoneOutputPresent=true so the
	// (gated-off) ExternalDNS builder wouldn't error even if reached.
	p.workloadIRSA = buildWorkloadIRSAParams(p, awsWorkloadConfigForIRSA{
		ExternalDNSEnabled:          false,
		HostedZoneManagementEnabled: true,
	}, map[string]string{}, true)
	return p
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
	// IAM roles: cluster + node + EBS CSI add-on IRSA (EFS off, secrets-store off)
	// = 3 per-cluster roles, PLUS the 7 workload-scoped IRSA roles created in
	// phase 2 (FSx, LBC, TraefikForwardAuth, Mimir, Loki, EBS-CSI, Alloy;
	// ExternalDNS is off in this test) = 10 total.
	assert.Len(t, mocks.byType("aws:iam/role:Role"), 10)

	// The 7 workload-scoped IRSA roles are present by their logical names. NOTE:
	// the FSx role keeps the persistent-era logical name "aws-fsx-openzfs-csi-driver.posit.team"
	// (its physical Name is the compound-scoped fsxOpenzfsRoleName); the other six
	// use the compound-scoped name for both logical and physical.
	for _, want := range []string{
		"aws-fsx-openzfs-csi-driver.posit.team",
		"aws-load-balancer-controller.wl01-staging.posit.team",
		"traefik-forward-auth.wl01-staging.posit.team",
		"mimir.wl01-staging.posit.team",
		"loki.wl01-staging.posit.team",
		"aws-ebs-csi.wl01-staging.posit.team",
		"alloy.wl01-staging.posit.team",
	} {
		assert.NotNilf(t, mocks.findResource(want), "expected workload IRSA role %q", want)
	}
	// ExternalDNS role is gated off in this test.
	assert.Nil(t, mocks.findResource("external-dns.wl01-staging.posit.team"))

	// The FSx role keeps the persistent-era logical name but its physical IAM
	// Name is the compound-scoped fsxOpenzfsRoleName (required for state adoption).
	fsxRole := mocks.findResource("aws-fsx-openzfs-csi-driver.posit.team")
	require.NotNil(t, fsxRole)
	assert.Equal(t, resource.NewStringProperty("aws-fsx-openzfs-csi-driver.wl01-staging.posit.team"), fsxRole.Inputs["name"])
	// Every workload IRSA role carries the workload permissions boundary.
	assert.Equal(t, resource.NewStringProperty("arn:aws:iam::123456789012:policy/PositTeamDedicatedAdmin"), fsxRole.Inputs["permissionsBoundary"])

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
	// IAM roles: cluster + node + EBS add-on IRSA + EFS add-on IRSA = 4 per-cluster
	// roles, PLUS the 7 workload-scoped IRSA roles (ExternalDNS off) = 11 total.
	assert.Len(t, mocks.byType("aws:iam/role:Role"), 11)
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

// TestAWSEKSDeployWorkloadIRSAExternalDNS exercises phase 2 with ExternalDNS
// enabled: the external-dns IRSA role + dns-update policy are created, and the
// policy grants ChangeResourceRecordSets on the hosted-zone ARN sourced from the
// persistent hosted_zone_name_servers stack output (no route53 lookup).
func TestAWSEKSDeployWorkloadIRSAExternalDNS(t *testing.T) {
	params := newTestEKSParams()
	params.workloadIRSA = buildWorkloadIRSAParams(params, awsWorkloadConfigForIRSA{
		ExternalDNSEnabled:          true,
		HostedZoneManagementEnabled: true,
	}, map[string]string{"main": "Z0123456789ABC"}, true)

	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	// ExternalDNS role present (now 8 workload IRSA roles → 11 total IAM roles:
	// cluster + node + EBS add-on + 8 workload roles).
	assert.NotNil(t, mocks.findResource("external-dns.wl01-staging.posit.team"))
	assert.Len(t, mocks.byType("aws:iam/role:Role"), 11)

	// The dns-update policy embeds the hosted-zone ARN from the stack output.
	dnsPolicy := mocks.findResource("dns-update.wl01-staging.posit.team")
	require.NotNil(t, dnsPolicy)
	assert.Contains(t, dnsPolicy.Inputs["policy"].StringValue(), "arn:aws:route53:::hostedzone/Z0123456789ABC")
	// Zone ARNs come from the persistent output — NO runtime route53 lookup.
	assert.Equal(t, 0, mocks.countCalls("aws:route53/getZone:getZone"))
}

// TestAWSEKSDeployWorkloadIRSAExternalDNSStripsHostedzonePrefix confirms a
// zone_id arriving with a leading "/hostedzone/" prefix is normalized so the ARN
// is byte-identical to the bare-id form (no double prefix).
func TestAWSEKSDeployWorkloadIRSAExternalDNSStripsHostedzonePrefix(t *testing.T) {
	params := newTestEKSParams()
	params.workloadIRSA = buildWorkloadIRSAParams(params, awsWorkloadConfigForIRSA{
		ExternalDNSEnabled:          true,
		HostedZoneManagementEnabled: true,
	}, map[string]string{"main": "/hostedzone/ZPREFIXED1"}, true)

	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	dnsPolicy := mocks.findResource("dns-update.wl01-staging.posit.team")
	require.NotNil(t, dnsPolicy)
	policy := dnsPolicy.Inputs["policy"].StringValue()
	assert.Contains(t, policy, "arn:aws:route53:::hostedzone/ZPREFIXED1")
	assert.NotContains(t, policy, "hostedzone//hostedzone/")
}

// TestAWSEKSDeployWorkloadIRSAExternalDNSSharedDomain confirms persistent's
// per-site ARN multiplicity is reproduced: two sites sharing a domain carry the
// SAME zone_id in the persistent output, so that ARN appears once PER SITE in the
// policy resource list (multiplicity = 2), matching persistent's policy.
func TestAWSEKSDeployWorkloadIRSAExternalDNSSharedDomain(t *testing.T) {
	params := newTestEKSParams()
	// persistent attaches the per-domain primary's zone to every site of the
	// domain, so the export carries the same zone_id under each site.
	params.workloadIRSA = buildWorkloadIRSAParams(params, awsWorkloadConfigForIRSA{
		ExternalDNSEnabled:          true,
		HostedZoneManagementEnabled: true,
	}, map[string]string{"main": "Z0123456789ABC", "alt": "Z0123456789ABC"}, true)

	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	// No runtime route53 lookups.
	assert.Equal(t, 0, mocks.countCalls("aws:route53/getZone:getZone"))

	// The zone ARN appears once per site (2x) in the ChangeResourceRecordSets list.
	dnsPolicy := mocks.findResource("dns-update.wl01-staging.posit.team")
	require.NotNil(t, dnsPolicy)
	policy := dnsPolicy.Inputs["policy"].StringValue()
	assert.Equal(t, 2, strings.Count(policy, "arn:aws:route53:::hostedzone/Z0123456789ABC"))
}

// TestAWSEKSDeployWorkloadIRSAExternalDNSMissingOutput confirms eks fails loudly
// when the persistent hosted_zone_name_servers output is absent (persistent has
// not applied), rather than emitting a silently-empty ExternalDNS policy.
func TestAWSEKSDeployWorkloadIRSAExternalDNSMissingOutput(t *testing.T) {
	params := newTestEKSParams()
	params.workloadIRSA = buildWorkloadIRSAParams(params, awsWorkloadConfigForIRSA{
		ExternalDNSEnabled:          true,
		HostedZoneManagementEnabled: true,
	}, map[string]string{}, false) // present=false → output missing

	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hosted_zone_name_servers")
}

// TestAWSEKSDeployWorkloadIRSATrustBoundToOIDC confirms an IRSA role's trust
// policy is built from the cluster OIDC issuer (the Output-aware path), federating
// the OIDC provider and constraining the expected service-account subject.
func TestAWSEKSDeployWorkloadIRSATrustBoundToOIDC(t *testing.T) {
	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), newTestEKSParams())
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	alloyRole := mocks.findResource("alloy.wl01-staging.posit.team")
	require.NotNil(t, alloyRole)
	trust := alloyRole.Inputs["assumeRolePolicy"].StringValue()
	// The mock cluster issuer is https://oidc.eks.us-east-1.amazonaws.com/id/TEST;
	// the tail (no scheme) is federated and the alloy SA subject is constrained.
	assert.Contains(t, trust, "oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/TEST")
	assert.Contains(t, trust, "system:serviceaccount:alloy:alloy.posit.team")
	assert.Contains(t, trust, "sts:AssumeRoleWithWebIdentity")
}

// TestAWSEKSDeployWorkloadIRSATrustGoldenParity locks the byte-parity the whole
// migration depends on: the Output-aware trust the eks role gets must equal the
// shared pure irsaTrustPolicyLogic output for the same inputs. If the two ever
// diverge, the import-based state migration would see a diff and replace.
func TestAWSEKSDeployWorkloadIRSATrustGoldenParity(t *testing.T) {
	params := newTestEKSParams()
	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	alloyRole := mocks.findResource("alloy.wl01-staging.posit.team")
	require.NotNil(t, alloyRole)
	got := alloyRole.Inputs["assumeRolePolicy"].StringValue()

	// The mock cluster issuer tail (scheme stripped) is the sole OIDC tail.
	want := irsaTrustPolicyLogic(
		"alloy",
		[]string{"alloy.posit.team"},
		[]string{"oidc.eks.us-east-1.amazonaws.com/id/TEST"},
		params.accountID,
		params.callerARN,
	)
	assert.Equal(t, want, got)
}

// TestAWSEKSDeployWorkloadIRSATrustExtraOidcUrls confirms configured
// extra_cluster_oidc_urls are folded into the IRSA trust (parity with the
// persistent step, which appended cfg.ExtraClusterOidcUrls before building it).
func TestAWSEKSDeployWorkloadIRSATrustExtraOidcUrls(t *testing.T) {
	params := newTestEKSParams()
	params.workloadIRSA.extraClusterOidcURLs = []string{"https://oidc.example.com/id/EXTRA"}

	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), params)
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	alloyRole := mocks.findResource("alloy.wl01-staging.posit.team")
	require.NotNil(t, alloyRole)
	trust := alloyRole.Inputs["assumeRolePolicy"].StringValue()
	// Both the cluster issuer AND the extra issuer (scheme stripped) are federated.
	assert.Contains(t, trust, "oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/TEST")
	assert.Contains(t, trust, "oidc-provider/oidc.example.com/id/EXTRA")
}

// TestAWSEKSDeployWorkloadIRSAMimirLokiBucketPolicy asserts the Mimir and Loki
// IRSA permission policies grant read/write on the LIVE bucket ARNs, which carry
// the persistent "ptd-" prefix (arn:aws:s3:::ptd-<cn>-mimir / -loki). This guards
// the migration-blocking regression where the ARN omitted the prefix.
func TestAWSEKSDeployWorkloadIRSAMimirLokiBucketPolicy(t *testing.T) {
	mocks := &eksStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsEKSDeploy(ctx, mockAWSWorkloadTarget("wl01-staging"), newTestEKSParams())
	}, pulumi.WithMocks("ptd-aws-workload-eks", "wl01-staging", mocks))
	require.NoError(t, err)

	for _, tc := range []struct {
		policyName string
		bucketARN  string
	}{
		{"mimir-s3-bucket.wl01-staging.posit.team", "arn:aws:s3:::ptd-wl01-staging-mimir"},
		{"loki-s3-bucket.wl01-staging.posit.team", "arn:aws:s3:::ptd-wl01-staging-loki"},
	} {
		pol := mocks.findResource(tc.policyName)
		require.NotNilf(t, pol, "expected policy %q", tc.policyName)

		var doc struct {
			Statement []struct {
				Resource []string `json:"Resource"`
			} `json:"Statement"`
		}
		require.NoError(t, json.Unmarshal([]byte(pol.Inputs["policy"].StringValue()), &doc))
		require.Len(t, doc.Statement, 1)
		assert.Equal(t, []string{tc.bucketARN, tc.bucketARN + "/*"}, doc.Statement[0].Resource)
	}
}
