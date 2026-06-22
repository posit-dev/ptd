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

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type workspacesMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *workspacesMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	return args.Name + "_id", args.Inputs, nil
}

func (m *workspacesMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	switch args.Token {
	case "aws:ec2/getVpcEndpointService:getVpcEndpointService":
		return resource.PropertyMap{
			"serviceName": resource.NewStringProperty("com.amazonaws.us-east-1.sts"),
			"serviceType": resource.NewStringProperty("Interface"),
		}, nil
	case "aws:iam/getPolicyDocument:getPolicyDocument":
		return resource.PropertyMap{
			"json": resource.NewStringProperty(`{"Version":"2012-10-17"}`),
		}, nil
	case "aws:workspaces/getBundle:getBundle":
		return resource.PropertyMap{
			"id":   resource.NewStringProperty("wsb-test-bundle"),
			"name": resource.NewStringProperty("Power with Ubuntu 22.04"),
		}, nil
	case "aws:kms/getKey:getKey":
		return resource.PropertyMap{
			"arn":   resource.NewStringProperty("arn:aws:kms:us-east-1:123456789012:key/test-key"),
			"keyId": resource.NewStringProperty("alias/aws/workspaces"),
		}, nil
	}
	return resource.PropertyMap{}, nil
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func mockAWSControlRoomTarget(name string) *typestest.MockTarget {
	tgt := &typestest.MockTarget{}
	tgt.On("Name").Return(name)
	tgt.On("CloudProvider").Return(types.AWS)
	tgt.On("ControlRoom").Return(true)
	tgt.On("Type").Return(types.TargetTypeControlRoom)
	return tgt
}

// aliasURNsFor returns the alias URN strings registered for the resource with
// the given logical name. The Go Pulumi SDK conveys aliases through the
// RegisterResource RPC request, so we read them from RegisterRPC.
func (m *workspacesMocks) aliasURNsFor(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var urns []string
	for _, r := range m.resources {
		if r.Name != name {
			continue
		}
		if r.RegisterRPC == nil {
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

// findResource returns the first mock resource with the given logical name, or
// nil if none was registered.
func (m *workspacesMocks) findResource(name string) *pulumi.MockResourceArgs {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.resources {
		if m.resources[i].Name == name {
			return &m.resources[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step metadata tests
// ---------------------------------------------------------------------------

func TestWorkspacesStepName(t *testing.T) {
	step := &WorkspacesStep{}
	assert.Equal(t, "workspaces", step.Name())
}

func TestWorkspacesStepProxyRequired(t *testing.T) {
	step := &WorkspacesStep{}
	assert.False(t, step.ProxyRequired())
}

func TestWorkspacesStepNilTarget(t *testing.T) {
	step := &WorkspacesStep{}
	step.Set(nil, nil, StepOptions{})
	err := step.Run(context.Background())
	assert.ErrorContains(t, err, "workspaces step requires a destination target")
}

// ---------------------------------------------------------------------------
// Subnet CIDR computation tests
// ---------------------------------------------------------------------------

// Note: TestComputeSubnetCIDRs / TestComputeSubnetCIDRsInvalidCIDR moved to
// lib/aws/vpc_test.go alongside the relocated computeSubnetCIDRs function.

// ---------------------------------------------------------------------------
// Deploy function tests
// ---------------------------------------------------------------------------

func TestAWSWorkspacesDeployNoUsers(t *testing.T) {
	mocks := &workspacesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := awsWorkspacesParams{
			compoundName: "main01-staging",
			trustedUsers: nil,
			requiredTags: map[string]string{"env": "staging"},
		}
		return awsWorkspacesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-control-room-workspaces", "main01-staging", mocks))

	require.NoError(t, err)

	// Verify core resources were created.
	var resourceTypes []string
	for _, r := range mocks.resources {
		resourceTypes = append(resourceTypes, r.TypeToken)
	}

	// VPC should exist
	assert.Contains(t, resourceTypes, "aws:ec2/vpc:Vpc")
	// Internet Gateway
	assert.Contains(t, resourceTypes, "aws:ec2/internetGateway:InternetGateway")
	// Public and private subnets (2 each for 2 AZs)
	subnetCount := 0
	for _, r := range mocks.resources {
		if r.TypeToken == "aws:ec2/subnet:Subnet" {
			subnetCount++
		}
	}
	assert.Equal(t, 4, subnetCount, "expected 2 public + 2 private subnets")

	// NACLs
	naclCount := 0
	for _, r := range mocks.resources {
		if r.TypeToken == "aws:ec2/networkAcl:NetworkAcl" {
			naclCount++
		}
	}
	assert.Equal(t, 2, naclCount, "expected public + private NACL")

	// IAM role
	assert.Contains(t, resourceTypes, "aws:iam/role:Role")

	// AD
	assert.Contains(t, resourceTypes, "aws:directoryservice/directory:Directory")

	// IP group
	assert.Contains(t, resourceTypes, "aws:workspaces/ipGroup:IpGroup")

	// Workspaces directory
	assert.Contains(t, resourceTypes, "aws:workspaces/directory:Directory")
}

func TestAWSWorkspacesDeployWithUsers(t *testing.T) {
	mocks := &workspacesMocks{}

	users := []types.TrustedUser{
		{
			GivenName:  "Alice",
			FamilyName: "Smith",
			Email:      "alice@example.com",
			IpAddresses: []types.TrustedUserIpAddress{
				{Ip: "1.2.3.4", Comment: "home"},
			},
		},
		{
			GivenName:  "Bob",
			FamilyName: "Jones",
			Email:      "bob@example.com",
			IpAddresses: []types.TrustedUserIpAddress{
				{Ip: "5.6.7.8", Comment: "office"},
			},
		},
	}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := awsWorkspacesParams{
			compoundName: "main01-staging",
			trustedUsers: users,
			requiredTags: map[string]string{},
		}
		return awsWorkspacesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-control-room-workspaces", "main01-staging", mocks))

	require.NoError(t, err)

	// The AD resources are commandlocal.Command wrappers around the `aws ds` /
	// `aws ds-data` CLI. Expect 1 data-access-enable command + 1 per user.
	commandCount := 0
	for _, r := range mocks.resources {
		if r.TypeToken == "command:local:Command" {
			commandCount++
		}
	}
	assert.Equal(t, 3, commandCount, "expected 1 data-access enable + 2 user creation commands")

	// Should have 2 workspace resources.
	wsCount := 0
	for _, r := range mocks.resources {
		if r.TypeToken == "aws:workspaces/workspace:Workspace" {
			wsCount++
		}
	}
	assert.Equal(t, 2, wsCount, "expected one workspace per user")

	// Verify workspace names use lowercase given names.
	var wsNames []string
	for _, r := range mocks.resources {
		if r.TypeToken == "aws:workspaces/workspace:Workspace" {
			wsNames = append(wsNames, r.Name)
		}
	}
	assert.Contains(t, wsNames, "main01-staging-workspaces-workspaces-workspace-alice")
	assert.Contains(t, wsNames, "main01-staging-workspaces-workspaces-workspace-bob")

	// Verify the AD user command resources exist per user.
	for _, account := range []string{"alice", "bob"} {
		cmdName := "main01-staging-workspaces-ad-user-" + account
		r := mocks.findResource(cmdName)
		require.NotNil(t, r, "AD user command %q not found", cmdName)
		assert.Equal(t, "command:local:Command", r.TypeToken)

		// The Create/Delete command strings must be STATIC: they call the
		// `aws ds-data` CLI and reference all dynamic values only through
		// double-quoted shell variables ("$AD_..."), never interpolated literals.
		create, _ := r.Inputs["create"].V.(string)
		del, _ := r.Inputs["delete"].V.(string)
		assert.Contains(t, create, "aws ds-data create-user", "create must call the aws ds-data CLI")
		assert.Contains(t, create, "aws ds-data describe-user", "create must describe-then-create for idempotency")
		assert.Contains(t, del, "aws ds-data delete-user", "delete must call the aws ds-data CLI")
		assert.Contains(t, create, `"$AD_SAM_ACCOUNT_NAME"`, "create must reference the sAMAccountName via a double-quoted shell variable")
		assert.Contains(t, create, `"$AD_GIVEN_NAME"`, "create must reference the given name via a double-quoted shell variable")
		assert.Contains(t, del, `"$AD_SAM_ACCOUNT_NAME"`, "delete must reference the sAMAccountName via a double-quoted shell variable")
		// User data must never appear literally in the command string.
		assert.NotContains(t, create, account, "account name must be passed via Environment, not the command string")
		assert.NotContains(t, create, "Alice", "given name must be passed via Environment, not the command string")
		assert.NotContains(t, create, "alice@example.com", "email must be passed via Environment, not the command string")
	}

	// Verify user data is passed via the Environment StringMap rather than the
	// command string. Check Alice's command carries her details in env.
	aliceCmd := mocks.findResource("main01-staging-workspaces-ad-user-alice")
	require.NotNil(t, aliceCmd)
	env, ok := aliceCmd.Inputs["environment"].V.(resource.PropertyMap)
	require.True(t, ok, "command Environment should be a property map")
	assert.Equal(t, resource.NewStringProperty("alice"), env["AD_SAM_ACCOUNT_NAME"])
	assert.Equal(t, resource.NewStringProperty("Alice"), env["AD_GIVEN_NAME"])
	assert.Equal(t, resource.NewStringProperty("Smith"), env["AD_SURNAME"])
	assert.Equal(t, resource.NewStringProperty("alice@example.com"), env["AD_EMAIL"])
	assert.Equal(t, resource.NewStringProperty("us-east-1"), env["AWS_REGION"])

	// The data-access-enable command should also be static, call the `aws ds`
	// CLI, and reference the directory ID only via the double-quoted shell
	// variable. The directory ID + region are carried via env (the dir ID is an
	// unknown output in tests).
	dataAccess := mocks.findResource("main01-staging-workspaces-ad-data-access-enabled")
	require.NotNil(t, dataAccess)
	daCreate, _ := dataAccess.Inputs["create"].V.(string)
	daDelete, _ := dataAccess.Inputs["delete"].V.(string)
	assert.Contains(t, daCreate, "aws ds enable-directory-data-access")
	assert.Contains(t, daCreate, `"$AD_DIRECTORY_ID"`)
	assert.Contains(t, daDelete, "aws ds disable-directory-data-access")
	assert.Contains(t, daDelete, `"$AD_DIRECTORY_ID"`)
}

// TestAWSWorkspacesAliasURNs verifies that a representative VPC resource and a
// representative workspaces resource carry alias URNs pointing at the OLD Python
// project name and component/type chain. Alias correctness is the highest-risk
// property of the migration, so it is asserted explicitly.
func TestAWSWorkspacesAliasURNs(t *testing.T) {
	mocks := &workspacesMocks{}

	users := []types.TrustedUser{
		{
			GivenName:  "Alice",
			FamilyName: "Smith",
			Email:      "alice@example.com",
			IpAddresses: []types.TrustedUserIpAddress{
				{Ip: "1.2.3.4", Comment: "home"},
			},
		},
	}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := awsWorkspacesParams{
			compoundName: "main01-staging",
			trustedUsers: users,
			requiredTags: map[string]string{},
		}
		return awsWorkspacesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-control-room-workspaces", "main01-staging", mocks))
	require.NoError(t, err)

	const (
		stack          = "main01-staging"
		oldProject     = "ptd-aws-control-room-workspaces"
		workspacesComp = "ptd:AWSControlRoomWorkspaces"
		vpcComp        = workspacesComp + "$ptd:AWSVpc"
	)

	// Representative VPC resource: the VPC itself. Its old URN nests through both
	// AWSControlRoomWorkspaces and AWSVpc.
	vpcURNs := mocks.aliasURNsFor("main01-staging-workspaces")
	wantVPCURN := "urn:pulumi:" + stack + "::" + oldProject + "::" + vpcComp +
		"$aws:ec2/vpc:Vpc::main01-staging-workspaces"
	assert.Contains(t, vpcURNs, wantVPCURN, "VPC alias must reference old project + AWSVpc chain")
	for _, u := range vpcURNs {
		assert.Contains(t, u, oldProject, "every VPC alias must use the literal old project name")
	}

	// Representative workspaces resource: the IAM default role, a direct child of
	// AWSControlRoomWorkspaces.
	roleURNs := mocks.aliasURNsFor("main01-staging-workspaces-default-role")
	wantRoleURN := "urn:pulumi:" + stack + "::" + oldProject + "::" + workspacesComp +
		"$aws:iam/role:Role::main01-staging-workspaces-default-role"
	assert.Contains(t, roleURNs, wantRoleURN, "workspaces role alias must reference old project + component chain")

	// The AD command resources carry NO aliases. The old Python
	// `pulumi-python:dynamic:Resource` resources cannot be adopted across
	// providers (a cross-provider replace would require loading the old
	// pulumi-python plugin, which fails in the Go runtime), so they are removed
	// from state via a one-time `pulumi state delete` cutover and these commands
	// create fresh. Asserting no aliases guards against reintroducing the
	// destructive cross-provider alias.
	dataAccessURNs := mocks.aliasURNsFor("main01-staging-workspaces-ad-data-access-enabled")
	assert.Empty(t, dataAccessURNs, "data-access command must carry NO aliases")

	adUserURNs := mocks.aliasURNsFor("main01-staging-workspaces-ad-user-alice")
	assert.Empty(t, adUserURNs, "AD user command must carry NO aliases")
}

func TestAWSWorkspacesVPCName(t *testing.T) {
	mocks := &workspacesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := awsWorkspacesParams{
			compoundName: "main01-staging",
			trustedUsers: nil,
			requiredTags: map[string]string{},
		}
		return awsWorkspacesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-control-room-workspaces", "main01-staging", mocks))

	require.NoError(t, err)

	// The VPC should be named "{compoundName}-workspaces"
	var vpcResource *pulumi.MockResourceArgs
	for i, r := range mocks.resources {
		if r.TypeToken == "aws:ec2/vpc:Vpc" {
			vpcResource = &mocks.resources[i]
			break
		}
	}
	require.NotNil(t, vpcResource, "VPC resource not found")
	assert.Equal(t, "main01-staging-workspaces", vpcResource.Name)
}

func TestAWSWorkspacesADPasswordLength(t *testing.T) {
	mocks := &workspacesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := awsWorkspacesParams{
			compoundName: "main01-staging",
			trustedUsers: nil,
			requiredTags: map[string]string{},
		}
		return awsWorkspacesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-control-room-workspaces", "main01-staging", mocks))

	require.NoError(t, err)

	// Find the RandomPassword resource.
	for _, r := range mocks.resources {
		if r.TypeToken == "random:index/randomPassword:RandomPassword" {
			assert.Equal(t, resource.NewNumberProperty(31), r.Inputs["length"])
			assert.Equal(t, resource.NewBoolProperty(true), r.Inputs["special"])
			assert.Equal(t, resource.NewStringProperty("!#^*-_"), r.Inputs["overrideSpecial"])
			return
		}
	}
	t.Fatal("RandomPassword resource not found")
}

func TestAWSWorkspacesDefaultRoleName(t *testing.T) {
	mocks := &workspacesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := awsWorkspacesParams{
			compoundName: "main01-staging",
			trustedUsers: nil,
			requiredTags: map[string]string{},
		}
		return awsWorkspacesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-control-room-workspaces", "main01-staging", mocks))

	require.NoError(t, err)

	for _, r := range mocks.resources {
		if r.TypeToken == "aws:iam/role:Role" {
			assert.Equal(t, resource.NewStringProperty("workspaces_DefaultRole"), r.Inputs["name"])
			return
		}
	}
	t.Fatal("IAM role resource not found")
}

func TestWorkspacesStepNotControlRoom(t *testing.T) {
	tgt := &typestest.MockTarget{}
	tgt.On("Name").Return("some-workload")
	tgt.On("CloudProvider").Return(types.AWS)
	tgt.On("ControlRoom").Return(false)
	tgt.On("Type").Return(types.TargetTypeWorkload)
	tgt.On("Credentials", context.Background()).Return(typestest.DefaultCredentials(), nil)

	step := &WorkspacesStep{}
	step.Set(tgt, nil, StepOptions{})
	err := step.Run(context.Background())
	assert.ErrorContains(t, err, "control room")
}
