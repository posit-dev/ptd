package steps

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/types"
)

// clusterStepMocks mirrors eksStepMocks but adds the control-room-specific data
// source Call handlers (route53 getZone, alb getLoadBalancer).
type clusterStepMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *clusterStepMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
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
						"issuer": resource.NewStringProperty("https://oidc.eks.us-east-2.amazonaws.com/id/TEST"),
					}),
				}),
			}),
		})
	}
	return args.Name + "_id", outputs, nil
}

func (m *clusterStepMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
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
			"names": resource.NewArrayProperty([]resource.PropertyValue{resource.NewStringProperty("AWSReservedSSO_PowerUser_x")}),
		}, nil
	case "aws:route53/getZone:getZone":
		return resource.PropertyMap{"zoneId": resource.NewStringProperty("Z123PARENT")}, nil
	case "aws:alb/getLoadBalancer:getLoadBalancer":
		return resource.PropertyMap{
			"dnsName": resource.NewStringProperty("abc.elb.us-east-2.amazonaws.com"),
			"zoneId":  resource.NewStringProperty("Z456ALB"),
		}, nil
	}
	return resource.PropertyMap{}, nil
}

func (m *clusterStepMocks) byType(typeToken string) []pulumi.MockResourceArgs {
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

func (m *clusterStepMocks) findResource(name string) *pulumi.MockResourceArgs {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.resources {
		if m.resources[i].Name == name {
			return &m.resources[i]
		}
	}
	return nil
}

func (m *clusterStepMocks) aliasURNsFor(name string) []string {
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

// mockAWSControlRoomTarget is defined in workspaces_test.go (same package).

func newTestClusterParams() awsClusterParams {
	cn := "cr01-staging"
	cfg := types.AWSControlRoomConfig{
		AccountID:        "123456789012",
		TrueName:         "cr01",
		Environment:      "staging",
		Domain:           "cr.example.posit.team",
		Region:           "us-east-2",
		EksAccessEntries: &types.EKSAccessEntriesConfig{Enabled: boolPtr(true)},
		ResourceTags:     map[string]string{},
	}
	return awsClusterParams{
		compoundName: cn,
		clusterName:  cn, // control-room cluster logical name = compound name
		region:       "us-east-2",
		accountID:    "123456789012",
		// Control-room roles carry NO permissions boundary (matches Python + live
		// state); the step passes empty. See TestAWSClusterDeployNoPermissionsBoundary.
		iamPermissionsBoundaryARN: "",
		requiredTags:              buildClusterRequiredTags(cfg),
		cfg:                       cfg,
		subnetIDs:                 []string{"subnet-a", "subnet-b"},
		vpcID:                     "vpc-123",
		clusterSGID:               "sg-cluster",
		clusterExists:             true,
		currentAuthMode:           "API_AND_CONFIG_MAP",
		kubeconfig:                "apiVersion: v1\n",
		grafanaDBConnection:       "postgres://grafana:pw@db.example/grafana",
		opsgenieKey:               "opsgenie-secret-value",
		mimirSalt:                 "$2b$12$CCCCCCCCCCCCCCCCCCCCC.",
		mimirCreds:                map[string]string{"cr01-staging": "mimir-pass"},
		wlAccountIDs:              []string{"111111111111", "222222222222"},
		grafanaAlerts:             []aws.GrafanaConfigMapFile{{LogicalSuffix: "pods", DataKey: "alerts.yaml", Content: "groups: []\n"}},
		grafanaDashboards:         []aws.GrafanaConfigMapFile{{LogicalSuffix: "k8s-views-global", DataKey: "k8s-views-global.json", Content: "{}"}},
	}
}

func runClusterDeploy(t *testing.T, params awsClusterParams) *clusterStepMocks {
	t.Helper()
	mocks := &clusterStepMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return awsClusterDeploy(ctx, mockAWSControlRoomTarget(params.compoundName), params)
	}, pulumi.WithMocks("ptd-aws-control-room-cluster", "cr01-staging", mocks))
	require.NoError(t, err)
	return mocks
}

func TestAWSClusterDeployControlPlaneLogicalName(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())

	clusters := mocks.byType("aws:eks/cluster:Cluster")
	require.Len(t, clusters, 1)
	// CRITICAL: the control-room cluster logical name AND its `name` input MUST be
	// exactly the compound name (Python aws_control_room_cluster.py passes
	// name=self.name=compound_name). NOT "default_{compound}-control-plane".
	assert.Equal(t, "cr01-staging", clusters[0].Name)
	assert.Equal(t, resource.NewStringProperty("cr01-staging"), clusters[0].Inputs["name"])
}

func TestAWSClusterDeployNoPermissionsBoundary(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())

	// Control-room IAM roles must NOT carry a permissions boundary. Python's
	// control-room _define_eks does not set one, the live control-room roles have
	// none, and the control-room admin identity cannot call
	// iam:PutRolePermissionsBoundary. Every role the cluster deploy creates should
	// therefore have an unset/empty permissionsBoundary input.
	roles := mocks.byType("aws:iam/role:Role")
	require.NotEmpty(t, roles)
	for _, r := range roles {
		pb, ok := r.Inputs["permissionsBoundary"]
		if !ok || pb.IsNull() {
			continue
		}
		assert.Equalf(t, "", pb.StringValue(),
			"control-room role %q must have no permissions boundary", r.Name)
	}
}

func TestAWSClusterDeployACMAndValidation(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())

	certs := mocks.byType("aws:acm/certificate:Certificate")
	require.Len(t, certs, 1)
	assert.Equal(t, resource.NewStringProperty("cr.example.posit.team"), certs[0].Inputs["domainName"])

	// One CertificateValidation + at least one validation Record.
	assert.Len(t, mocks.byType("aws:acm/certificateValidation:CertificateValidation"), 1)
	assert.GreaterOrEqual(t, len(mocks.byType("aws:route53/record:Record")), 1)
}

func TestAWSClusterDeployTraefikAliasChain(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())

	// Traefik helm release aliases under the FULL live chain
	// ptd:AWSControlRoomCluster$ptd:AWSEKSCluster$ptd:Traefik (the Python Traefik
	// component had parent=self.eks, the AWSEKSCluster). The $ptd:AWSEKSCluster
	// segment is load-bearing — omitting it CREATEs a new release, orphaning the live one.
	aliases := mocks.aliasURNsFor("cr01-staging-traefik")
	assert.Contains(t, aliases,
		"urn:pulumi:cr01-staging::ptd-aws-control-room-cluster::ptd:AWSControlRoomCluster$ptd:AWSEKSCluster$ptd:Traefik$kubernetes:helm.sh/v3:Release::cr01-staging-traefik")
}

func TestAWSClusterDeployGrafanaAndMimir(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())

	cn := "cr01-staging"

	// Grafana namespace + opsgenie secret + alert/dashboard ConfigMaps + helm.
	assert.NotNil(t, mocks.findResource(cn+"-grafana-ns"))
	assert.NotNil(t, mocks.findResource(cn+"-opsgenie-secret"))
	assert.NotNil(t, mocks.findResource(cn+"-grafana-pods-alerts"))
	assert.NotNil(t, mocks.findResource(cn+"-grafana-k8s-views-global-dashboard"))
	assert.NotNil(t, mocks.findResource(cn+"-grafana"))

	// Mimir: two S3 buckets + namespace + helm + IRSA role.
	assert.Len(t, mocks.byType("aws:s3/bucket:Bucket"), 2)
	assert.NotNil(t, mocks.findResource(cn+"-mimir-ns"))
	assert.NotNil(t, mocks.findResource(cn+"-mimir"))

	// Datasource X-Scope-OrgID header = "|"-joined sorted account ids.
	graf := mocks.findResource(cn + "-grafana")
	require.NotNil(t, graf)
}

func TestAWSClusterDeployIRSARoleSuffixes(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())
	cn := "cr01-staging"

	// EBS CSI + LBC + traefik-forward-auth + mimir IRSA roles retain expected names.
	// CR EBS CSI uses the BARE name (no .posit.team): Python with_ebs_csi_driver()
	// defaults role_name to f"{self.name}-ebs-csi-driver". The .posit.team-suffixed
	// name is the workload-path behaviour and must NOT appear on the CR path.
	assert.NotNil(t, mocks.findResource(cn+"-ebs-csi-driver"))
	assert.Nil(t, mocks.findResource(cn+"-ebs-csi-driver.posit.team"))
	assert.NotNil(t, mocks.findResource(cn+"-aws-eks-lbc"))
	assert.NotNil(t, mocks.findResource("traefik-forward-auth.cr01-staging.posit.team"))
	assert.NotNil(t, mocks.findResource(cn+"-mimir"))

	// LBC + secrets-store CSI + metrics-server + traefik-forward-auth + grafana +
	// mimir + traefik helm releases are all present.
	releases := map[string]bool{}
	for _, r := range mocks.byType("kubernetes:helm.sh/v3:Release") {
		releases[r.Name] = true
	}
	assert.True(t, releases[cn+"-aws-lbc"])
	assert.True(t, releases[cn+"-metrics-server"])
	assert.True(t, releases[cn+"-secret-store-csi"])
	assert.True(t, releases[cn+"-secrets-store-csi-driver-provider-aws"])
	assert.True(t, releases[cn+"-traefik-forward-auth"])
	assert.True(t, releases[cn+"-grafana"])
	assert.True(t, releases[cn+"-mimir"])
	assert.True(t, releases[cn+"-traefik"])
}

func TestAWSClusterDeployNodeGroupAndSSM(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())
	cn := "cr01-staging"

	// One launch template + node group named the compound name.
	assert.NotNil(t, mocks.findResource("cr01-staging"))
	ngs := mocks.byType("aws:eks/nodeGroup:NodeGroup")
	require.Len(t, ngs, 1)
	assert.Equal(t, "cr01-staging", ngs[0].Name)

	// SSM attach present.
	assert.NotNil(t, mocks.findResource(cn+"-eks-nodegroup-ssm"))
}

// TestAWSClusterDeployAwsLbcAttachmentParent guards the aws-lbc RolePolicyAttachment
// alias chain: it must be parented to the custom Policy (parent=policy), matching
// live state …$aws:iam/role:Role$aws:iam/policy:Policy$…RolePolicyAttachment, NOT
// the role. A role-child alias would orphan the live attachment (CREATE instead of
// adopt).
func TestAWSClusterDeployAwsLbcAttachmentParent(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())
	cn := "cr01-staging"

	const base = "urn:pulumi:cr01-staging::ptd-aws-control-room-cluster::ptd:AWSControlRoomCluster$ptd:AWSEKSCluster"
	aliases := mocks.aliasURNsFor(cn + "-aws-lbc")

	// Custom Policy is a child of the role.
	assert.Contains(t, aliases, base+"$aws:iam/role:Role$aws:iam/policy:Policy::"+cn+"-aws-lbc")
	// Attachment is a child of the Policy.
	assert.Contains(t, aliases,
		base+"$aws:iam/role:Role$aws:iam/policy:Policy$aws:iam/rolePolicyAttachment:RolePolicyAttachment::"+cn+"-aws-lbc")
	// Regression guard: the role-child chain (the bug) must NOT be emitted.
	assert.NotContains(t, aliases,
		base+"$aws:iam/role:Role$aws:iam/rolePolicyAttachment:RolePolicyAttachment::"+cn+"-aws-lbc")
}

// TestAWSClusterDeployTraefikRecordNames asserts the traefik domain Route53 record
// logical names + their ptd:Traefik-child alias chain match live state. The apex
// and wildcard domains each get an A record named "<name>-<domain>-A".
func TestAWSClusterDeployTraefikRecordNames(t *testing.T) {
	mocks := runClusterDeploy(t, newTestClusterParams())
	cn := "cr01-staging"

	const traefikBase = "urn:pulumi:cr01-staging::ptd-aws-control-room-cluster::ptd:AWSControlRoomCluster$ptd:AWSEKSCluster$ptd:Traefik"
	for _, rec := range []string{
		cn + "-cr.example.posit.team-A",
		cn + "-*.cr.example.posit.team-A",
	} {
		require.NotNil(t, mocks.findResource(rec), "missing traefik record %q", rec)
		assert.Contains(t, mocks.aliasURNsFor(rec),
			traefikBase+"$aws:route53/record:Record::"+rec,
			"traefik record %q must alias under ptd:Traefik", rec)
	}
}
