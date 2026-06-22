package aws

import (
	"fmt"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// TestBcryptHashpwDeterministic verifies our explicit-salt bcrypt reproduces a
// deterministic, stdlib-verifiable modular-crypt hash. This is load-bearing for
// the control-room mimir basic-auth Secret: Python's bcrypt.hashpw(pw, salt) is
// deterministic and the resulting hash is stored in Pulumi state; a
// non-deterministic Go hash would churn the mimir helm release on every apply.
func TestBcryptHashpwDeterministic(t *testing.T) {
	cases := []struct{ pw, salt string }{
		{"", "$2a$06$DCq7YPn5Rq63x1Lad4cll."},
		{"U*U", "$2a$05$CCCCCCCCCCCCCCCCCCCCC."},
		{"mimir-pass", "$2b$12$R9h/cIPz0gi.URNNX3kh2O"},
	}
	for _, c := range cases {
		got, err := bcryptHashpw(c.pw, c.salt)
		require.NoError(t, err)
		assert.Len(t, got, 60, "modular-crypt bcrypt hash must be 60 chars")

		// stdlib accepts our hash as a valid bcrypt hash of the password.
		require.NoError(t, bcrypt.CompareHashAndPassword([]byte(got), []byte(c.pw)))

		// Deterministic: same salt+pw → identical hash (the whole point).
		got2, err := bcryptHashpw(c.pw, c.salt)
		require.NoError(t, err)
		assert.Equal(t, got, got2)
	}
}

// TestBcryptHashpwRejectsMalformedSalt verifies a malformed salt errors rather
// than producing a bogus hash.
func TestBcryptHashpwRejectsMalformedSalt(t *testing.T) {
	_, err := bcryptHashpw("pw", "not-a-salt")
	require.Error(t, err)
}

// ── Control-room builder URN/logical-name reconciliation tests ───────────────
//
// These guard the control-room (AWSControlRoomCluster) path against the
// logical-name + alias-parent-chain regressions found when dry-running the
// migrated CR cluster step against live state. The CR path was built without
// live state to validate against, so Pulumi was planning CREATEs (orphaning live
// resources) instead of adopting them. Each test asserts the exact alias URN the
// live state stores so adoption (not replacement) is guaranteed.
//
// crName is a generic name; the live internal control room differs but the bug
// class is name-independent.
const crName = "cr01-staging"
const crProject = "ptd-aws-control-room-cluster"
const crParentChain = "ptd:AWSControlRoomCluster$ptd:AWSEKSCluster"

// crBaseCfg returns an EKSClusterConfig wired with the control-room project /
// parent-type-chain (mirrors cluster.go's awsClusterDeploy wiring).
func crBaseCfg() EKSClusterConfig {
	return EKSClusterConfig{
		Name:             crName,
		SubnetIDs:        []string{"subnet-aaa"},
		Version:          "1.35",
		Kubeconfig:       "apiVersion: v1\n",
		ProjectName:      crProject,
		ParentTypeChain:  crParentChain,
		WrapperTypeChain: "ptd:AWSControlRoomCluster",
		AccountID:        "123456789012",
		ClusterExists:    true,
		CurrentAuthMode:  "API_AND_CONFIG_MAP",
	}
}

// crFullURN builds the full old-Python URN under the AWSEKSCluster component for
// the CR stack, matching fullURNAlias's construction.
func crFullURN(stack, typeChain, name string) string {
	return fmt.Sprintf("urn:pulumi:%s::%s::%s$%s::%s", stack, crProject, crParentChain, typeChain, name)
}

// TestCREbsCsiRoleNameNoPositTeamSuffix asserts the control-room EBS CSI driver
// IRSA role uses the bare "<name>-ebs-csi-driver" logical name (NO .posit.team),
// matching Python's with_ebs_csi_driver() default role_name and the live state
// URN …$aws:iam/role:Role::<name>-ebs-csi-driver. (The workload path keeps the
// .posit.team suffix; this is the CR-specific behaviour.)
func TestCREbsCsiRoleNameNoPositTeamSuffix(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, crBaseCfg())
		require.NoError(t, err)
		// Control-room call site passes the bare name (cluster_aws.go).
		c.WithNodeRole("").
			WithEbsCsiDriver(crName+"-ebs-csi-driver", "v1.41.0-eksbuild.1")
		return c.Err()
	}, pulumi.WithMocks(crProject, crName, mocks))
	require.NoError(t, err)

	// Bare role name exists; the .posit.team-suffixed name does NOT.
	require.NotNil(t, mocks.nameOf(crName+"-ebs-csi-driver"),
		"CR EBS CSI role must use the bare name (no .posit.team)")
	assert.Nil(t, mocks.nameOf(crName+"-ebs-csi-driver.posit.team"),
		"CR EBS CSI role must NOT carry the .posit.team suffix")

	// The role's alias adopts the live state URN exactly.
	assert.Contains(t, mocks.aliasURNsFor(crName+"-ebs-csi-driver"),
		crFullURN(crName, "aws:iam/role:Role", crName+"-ebs-csi-driver"))

	// The managed-policy attachment keeps the role-child chain (parent=sa_role).
	assert.Contains(t, mocks.aliasURNsFor(crName+"-ebs-csi-driver"),
		fmt.Sprintf("urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
			crName, crProject, crParentChain, crName+"-ebs-csi-driver"))
}

// TestCRAwsLbcPolicyAndAttachment asserts the aws-lbc custom IAM Policy is a
// child of the role and the RolePolicyAttachment is a child of the POLICY (not
// the role), matching Python (parent=aws_lbc_iam_policy) and the live state:
//
//	…$aws:iam/role:Role$aws:iam/policy:Policy::<name>-aws-lbc
//	…$aws:iam/role:Role$aws:iam/policy:Policy$aws:iam/rolePolicyAttachment:RolePolicyAttachment::<name>-aws-lbc
func TestCRAwsLbcPolicyAndAttachment(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, crBaseCfg())
		require.NoError(t, err)
		c.WithNodeRole("").WithAwsLbc("v2.7.0", "")
		return c.Err()
	}, pulumi.WithMocks(crProject, crName, mocks))
	require.NoError(t, err)

	lbcName := crName + "-aws-lbc"

	// Custom Policy is a child of the role.
	require.NotNil(t, mocks.nameOf(lbcName))
	assert.Contains(t, mocks.aliasURNsFor(lbcName),
		fmt.Sprintf("urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/policy:Policy::%s",
			crName, crProject, crParentChain, lbcName))

	// The attachment alias must be parented to the POLICY, not the role.
	wantPolicyChild := fmt.Sprintf(
		"urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/policy:Policy$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
		crName, crProject, crParentChain, lbcName)
	assert.Contains(t, mocks.aliasURNsFor(lbcName), wantPolicyChild,
		"aws-lbc attachment must alias under Policy (parent=policy)")

	// Regression guard: the OLD (wrong) role-child chain must NOT be emitted.
	wrongRoleChild := fmt.Sprintf(
		"urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
		crName, crProject, crParentChain, lbcName)
	assert.NotContains(t, mocks.aliasURNsFor(lbcName), wrongRoleChild,
		"aws-lbc attachment must NOT alias under the role")
}

// TestCRMimirPolicyAttachmentAndReleaseParent asserts the mimir storage Policy +
// attachment use the Policy-child chain, and the mimir helm Release aliases under
// the mimir Namespace, matching the live state:
//
//	…$aws:iam/role:Role$aws:iam/policy:Policy::<name>-mimir-storage-policy
//	…$aws:iam/role:Role$aws:iam/policy:Policy$…RolePolicyAttachment::<name>-mimir-storage
//	…$kubernetes:core/v1:Namespace$kubernetes:helm.sh/v3:Release::<name>-mimir
func TestCRMimirPolicyAttachmentAndReleaseParent(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, crBaseCfg())
		require.NoError(t, err)
		c.WithMimir(MimirParams{
			BucketPrefix: crName + "-mrs-",
			Domain:       "cr.example",
			Creds:        map[string]string{"mimir": "pw"},
			Salt:         "$2b$12$R9h/cIPz0gi.URNNX3kh2O",
			Region:       "us-east-2",
			Version:      "5.0.0",
		})
		return c.Err()
	}, pulumi.WithMocks(crProject, crName, mocks))
	require.NoError(t, err)

	// Policy child-of-role.
	assert.Contains(t, mocks.aliasURNsFor(crName+"-mimir-storage-policy"),
		fmt.Sprintf("urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/policy:Policy::%s",
			crName, crProject, crParentChain, crName+"-mimir-storage-policy"))

	// Attachment child-of-Policy.
	assert.Contains(t, mocks.aliasURNsFor(crName+"-mimir-storage"),
		fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/policy:Policy$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
			crName, crProject, crParentChain, crName+"-mimir-storage"))

	// Release child-of-Namespace.
	assert.Contains(t, mocks.aliasURNsFor(crName+"-mimir"),
		fmt.Sprintf("urn:pulumi:%s::%s::%s$kubernetes:core/v1:Namespace$kubernetes:helm.sh/v3:Release::%s",
			crName, crProject, crParentChain, crName+"-mimir"))
}

// TestCRTraefikForwardAuthPolicyAttachment asserts the traefik-forward-auth
// secrets Policy + attachment use the Policy-child chain, matching live state:
//
//	…$aws:iam/role:Role$aws:iam/policy:Policy::<name>-traefik-forward-auth-secrets-policy
//	…$aws:iam/role:Role$aws:iam/policy:Policy$…RolePolicyAttachment::<name>-traefik-forward-auth
func TestCRTraefikForwardAuthPolicyAttachment(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, crBaseCfg())
		require.NoError(t, err)
		c.WithTraefikForwardAuth("cr.example", "0.5.0", nil)
		return c.Err()
	}, pulumi.WithMocks(crProject, crName, mocks))
	require.NoError(t, err)

	assert.Contains(t, mocks.aliasURNsFor(crName+"-traefik-forward-auth-secrets-policy"),
		fmt.Sprintf("urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/policy:Policy::%s",
			crName, crProject, crParentChain, crName+"-traefik-forward-auth-secrets-policy"))

	assert.Contains(t, mocks.aliasURNsFor(crName+"-traefik-forward-auth"),
		fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/policy:Policy$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
			crName, crProject, crParentChain, crName+"-traefik-forward-auth"))
}

// TestCRGrafanaConfigMapNames asserts a representative set of grafana alert and
// dashboard ConfigMap logical names + alias URNs match the live state, including
// the literal "<name>-grafana-alerts-dashboard-dashboard" (the dashboard whose
// stem is "alerts_dashboard").
func TestCRGrafanaConfigMapNames(t *testing.T) {
	mocks := &eksMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		c, err := NewEKSCluster(ctx, crBaseCfg())
		require.NoError(t, err)
		c.WithGrafana(GrafanaParams{
			Domain:          "cr.example",
			DBConnectionURL: "postgres://x",
			OpsgenieKey:     "k",
			Version:         "7.0.0",
			Alerts: []GrafanaConfigMapFile{
				{LogicalSuffix: "pods", DataKey: "alerts.yaml", Content: "x"},
				{LogicalSuffix: "azure-postgres", DataKey: "alerts.yaml", Content: "x"},
			},
			Dashboards: []GrafanaConfigMapFile{
				{LogicalSuffix: "alerts-dashboard", DataKey: "alerts_dashboard.json", Content: "{}"},
				{LogicalSuffix: "k8s-views-global", DataKey: "k8s-views-global.json", Content: "{}"},
			},
		})
		return c.Err()
	}, pulumi.WithMocks(crProject, crName, mocks))
	require.NoError(t, err)

	wantConfigMaps := []string{
		crName + "-grafana-pods-alerts",
		crName + "-grafana-azure-postgres-alerts",
		crName + "-grafana-alerts-dashboard-dashboard",
		crName + "-grafana-k8s-views-global-dashboard",
	}
	for _, cm := range wantConfigMaps {
		require.NotNil(t, mocks.nameOf(cm), "missing grafana ConfigMap %q", cm)
		assert.Contains(t, mocks.aliasURNsFor(cm),
			crFullURN(crName, "kubernetes:core/v1:ConfigMap", cm),
			"grafana ConfigMap %q alias must adopt the live URN", cm)
	}
}
