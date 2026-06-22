package steps

import (
	"context"
	"fmt"
	"strings"

	awsprovider "github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	awsdirectoryservice "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/directoryservice"
	awsec2 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	awsiam "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	awskms "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/kms"
	awsworkspaces "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/workspaces"
	commandlocal "github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
)

// ref is a helper that returns a pointer to a string (for optional args).
func ref(s string) *string { return &s }

// workspacesProjectName is the Pulumi project name used by the Python
// aws_control_room_workspaces step.  Aliases in this file reference this
// name so that existing state is matched correctly.
const workspacesProjectName = "ptd-aws-control-room-workspaces"

// workspacesVPCCIDR is the fixed VPC CIDR block for the workspaces environment.
// Chosen to avoid overlapping with the control-room persistent VPC (10.x.x.x/16).
const workspacesVPCCIDR = "172.16.0.0/20"

// workspacesAZs are the two AZs used in the workspaces environment (always us-east-1).
var workspacesAZs = []string{"use1-az4", "use1-az6"}

// awsWorkspacesParams bundles pre-fetched data for the workspaces deploy function.
type awsWorkspacesParams struct {
	compoundName string
	trustedUsers []types.TrustedUser
	requiredTags map[string]string
}

func (s *WorkspacesStep) runAWSInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("workspaces: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSControlRoomConfig)
	if !ok {
		return fmt.Errorf("workspaces: expected AWSControlRoomConfig, got %T", rawConfig)
	}

	// Build required_tags matching Python's AWSControlRoom.required_tags + workspaces managed-by.
	// Python derives true_name/environment from the directory name at runtime, not from YAML,
	// so we replicate the same split logic rather than reading cfg.TrueName/cfg.Environment.
	compoundName := s.DstTarget.Name()
	trueName, environment := compoundName, ""
	if idx := strings.LastIndex(compoundName, "-"); idx >= 0 {
		trueName = compoundName[:idx]
		environment = compoundName[idx+1:]
	}
	requiredTags := map[string]string{}
	for k, v := range cfg.ResourceTags {
		requiredTags[k] = v
	}
	requiredTags["posit.team/true-name"] = trueName
	requiredTags["posit.team/environment"] = environment
	requiredTags["posit.team/managed-by"] = "ptd.pulumi_resources.aws_control_room_workspaces"

	params := awsWorkspacesParams{
		compoundName: s.DstTarget.Name(),
		trustedUsers: cfg.TrustedUsers,
		requiredTags: requiredTags,
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return awsWorkspacesDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}

	return runPulumi(ctx, stack, s.Options)
}

// awsWorkspacesDeploy is the top-level Pulumi deploy function for the workspaces step.
// It replicates AWSControlRoomWorkspaces.__init__ from Python.
func awsWorkspacesDeploy(pctx *pulumi.Context, target types.Target, params awsWorkspacesParams) error {
	name := params.compoundName + "-workspaces"

	// Alias helper: maps a resource to its old Python URN.
	// The Python hierarchy is:
	//   ptd:AWSControlRoomWorkspaces  (outer component, named compoundName)
	//     ptd:AWSVpc  (VPC component, named {name})
	//       <vpc resources>
	//     <direct children of AWSControlRoomWorkspaces>
	workspacesCompType := "ptd:AWSControlRoomWorkspaces"
	vpcCompType := fmt.Sprintf("%s$ptd:AWSVpc", workspacesCompType)

	// aliasForVPCResource returns an alias for a resource that was a grandchild of
	// AWSControlRoomWorkspaces via the AWSVpc component.
	aliasForVPCResource := func(resourceType, resourceName string) pulumi.ResourceOption {
		oldURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$%s::%s",
			pctx.Stack(), workspacesProjectName,
			vpcCompType,
			resourceType,
			resourceName,
		)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}})
	}

	// aliasForWorkspacesResource returns an alias for a direct child of the
	// AWSControlRoomWorkspaces component.
	aliasForWorkspacesResource := func(resourceType, resourceName string) pulumi.ResourceOption {
		oldURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$%s::%s",
			pctx.Stack(), workspacesProjectName,
			workspacesCompType,
			resourceType,
			resourceName,
		)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}})
	}

	// requiredTags already built in runAWSInlineGo (includes posit.team/* tags + managed-by).
	requiredTags := params.requiredTags

	// Create an explicit us-east-1 provider so workspaces resources go to the
	// right region regardless of the control room's native region.
	use1Provider, err := awsprovider.NewProvider(pctx, "use1", &awsprovider.ProviderArgs{
		Region: pulumi.String("us-east-1"),
	}, pulumi.Aliases([]pulumi.Alias{{
		URN: pulumi.URN(fmt.Sprintf(
			"urn:pulumi:%s::%s::pulumi:providers:aws::use1",
			pctx.Stack(), workspacesProjectName,
		)),
	}}))
	if err != nil {
		return fmt.Errorf("use1 provider: %w", err)
	}
	use1Opt := pulumi.Provider(use1Provider)

	// -------------------------------------------------------------------------
	// VPC
	// -------------------------------------------------------------------------
	networkTags := map[string]map[string]string{
		"public":  {"posit.team/network-access": "public"},
		"private": {"posit.team/network-access": "private"},
	}
	vpcTags := map[string]string{}
	for k, v := range requiredTags {
		vpcTags[k] = v
	}
	vpcTags["Name"] = name

	// Build the VPC using the shared aws.NewVPC builder.
	// The OuterCompType for the VPC's resources must include both component levels.
	// Pass use1Provider (the ProviderResource) so the VPC builder can use it for
	// both resource creation and data-source invoke calls (e.g. LookupVpcEndpointService).
	vpc, err := aws.NewVPC(pctx, aws.VPCConfig{
		Name:          name,
		CIDR:          workspacesVPCCIDR,
		AZs:           workspacesAZs,
		Tags:          vpcTags,
		NetworkTags:   networkTags,
		OuterCompType: vpcCompType,
		ProjectName:   workspacesProjectName, // OLD Python project name for VPC alias URNs (literal, not ctx.Project())
		Provider:      use1Provider,
	})
	if err != nil {
		return fmt.Errorf("VPC: %w", err)
	}
	// aliasForVPCResource is used below for VPC-tier resources NOT created by
	// aws.NewVPC (e.g. the DHCP options set).

	if err := vpc.WithSecureDefaultSecurityGroup(); err != nil {
		return fmt.Errorf("secure default SG: %w", err)
	}
	if err := vpc.WithSecureDefaultNACL(); err != nil {
		return fmt.Errorf("secure default NACL: %w", err)
	}

	// Internal all-traffic ingress/egress rules
	if err := vpc.WithNACLRule("public", 0, 0, -1, workspacesVPCCIDR, false); err != nil {
		return err
	}
	if err := vpc.WithNACLRule("public", 0, 0, -1, workspacesVPCCIDR, true); err != nil {
		return err
	}
	// SSH outbound
	if err := vpc.WithNACLRule("public", 22, 22, 6, "0.0.0.0/0", true); err != nil {
		return err
	}

	// STS VPC endpoint
	if err := vpc.WithEndpoint("sts"); err != nil {
		return fmt.Errorf("STS endpoint: %w", err)
	}

	// HTTP outbound
	if err := vpc.WithNACLRule("public", 80, 80, 6, "0.0.0.0/0", true); err != nil {
		return err
	}

	// DHCP Options
	dhcpName := fmt.Sprintf("%s-resolver", name)
	dhcpTags := pulumi.StringMap{}
	for k, v := range requiredTags {
		dhcpTags[k] = pulumi.String(v)
	}
	if _, err := awsec2.NewVpcDhcpOptions(pctx, dhcpName, &awsec2.VpcDhcpOptionsArgs{
		DomainName:        pulumi.String("ec2.internal"),
		DomainNameServers: pulumi.StringArray{pulumi.String("AmazonProvidedDNS")},
		Tags:              dhcpTags,
	},
		pulumi.Parent(vpc.Vpc()),
		aliasForVPCResource("aws:ec2/vpcDhcpOptions:VpcDhcpOptions", dhcpName),
		use1Opt,
	); err != nil {
		return fmt.Errorf("DHCP options: %w", err)
	}

	// -------------------------------------------------------------------------
	// Workspaces resources
	// -------------------------------------------------------------------------

	// IAM assume-role policy for workspaces service
	assumeRolePolicy, err := awsiam.GetPolicyDocument(pctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{
				Actions: []string{"sts:AssumeRole"},
				Principals: []awsiam.GetPolicyDocumentStatementPrincipal{
					{Type: "Service", Identifiers: []string{"workspaces.amazonaws.com"}},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("workspaces assume-role policy: %w", err)
	}

	defaultRoleName := fmt.Sprintf("%s-default-role", name)
	defaultRoleTags := pulumi.StringMap{}
	for k, v := range requiredTags {
		defaultRoleTags[k] = pulumi.String(v)
	}
	defaultRole, err := awsiam.NewRole(pctx, defaultRoleName, &awsiam.RoleArgs{
		// NOTE: This role name is special and must be exactly this value.
		// See https://docs.aws.amazon.com/workspaces/latest/adminguide/workspaces-access-control.html#create-default-role
		Name:                pulumi.String("workspaces_DefaultRole"),
		AssumeRolePolicy:    pulumi.String(assumeRolePolicy.Json),
		MaxSessionDuration:  pulumi.Int(3600),
		ForceDetachPolicies: pulumi.Bool(false),
		Path:                pulumi.String("/"),
		Description:         pulumi.String(""),
		Tags:                defaultRoleTags,
	},
		aliasForWorkspacesResource("aws:iam/role:Role", defaultRoleName),
		use1Opt,
	)
	if err != nil {
		return fmt.Errorf("workspaces default role: %w", err)
	}

	skylightPolicy, err := awsiam.GetPolicyDocument(pctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{
				Effect: ref("Allow"),
				Actions: []string{
					"ec2:CreateNetworkInterface",
					"ec2:DeleteNetworkInterface",
					"ec2:DescribeNetworkInterfaces",
					"galaxy:DescribeDomains",
					"workspaces:RebootWorkspaces",
					"workspaces:RebuildWorkspaces",
					"workspaces:ModifyWorkspaceProperties",
				},
				Resources: []string{"*"},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("skylight policy doc: %w", err)
	}

	skylightPolicyName := fmt.Sprintf("%s-default-role-policy-skylight-self", name)
	if _, err := awsiam.NewRolePolicy(pctx, skylightPolicyName, &awsiam.RolePolicyArgs{
		Role:   defaultRole.ID(),
		Name:   pulumi.String("SkyLightSelfServiceAccess"),
		Policy: pulumi.String(skylightPolicy.Json),
	},
		aliasForWorkspacesResource("aws:iam/rolePolicy:RolePolicy", skylightPolicyName),
	); err != nil {
		return fmt.Errorf("skylight role policy: %w", err)
	}

	// Random AD password
	adPasswordName := fmt.Sprintf("%s-ad-password", name)
	adPassword, err := random.NewRandomPassword(pctx, adPasswordName, &random.RandomPasswordArgs{
		Length:          pulumi.Int(31),
		Special:         pulumi.Bool(true),
		OverrideSpecial: pulumi.String("!#^*-_"),
	},
		aliasForWorkspacesResource("random:index/randomPassword:RandomPassword", adPasswordName),
	)
	if err != nil {
		return fmt.Errorf("AD password: %w", err)
	}

	// Use the first two public subnets (workspaces only needs 2)
	publicSubnet0 := vpc.PublicSubnets()[0]
	publicSubnet1 := vpc.PublicSubnets()[1]

	// Active Directory
	adName := fmt.Sprintf("%s-ad", name)
	adTags := pulumi.StringMap{}
	for k, v := range requiredTags {
		adTags[k] = pulumi.String(v)
	}
	ad, err := awsdirectoryservice.NewDirectory(pctx, adName, &awsdirectoryservice.DirectoryArgs{
		Name:      pulumi.String("corp.amazonworkspaces.com"),
		Password:  adPassword.Result,
		Edition:   pulumi.String("Standard"),
		Type:      pulumi.String("MicrosoftAD"),
		ShortName: pulumi.String("corp"),
		Size:      pulumi.String("Small"),
		VpcSettings: &awsdirectoryservice.DirectoryVpcSettingsArgs{
			VpcId: vpc.VpcID(),
			SubnetIds: pulumi.StringArray{
				publicSubnet0.ID(),
				publicSubnet1.ID(),
			},
		},
		Tags: adTags,
	},
		aliasForWorkspacesResource("aws:directoryservice/directory:Directory", adName),
		use1Opt,
	)
	if err != nil {
		return fmt.Errorf("Active Directory: %w", err)
	}

	// Enable directory data access. There is no native Pulumi AWS resource for
	// this (the Directory Service Data API is newer than the pulumi-aws v6
	// directoryservice coverage), so we keep a commandlocal.Command purely as the
	// Pulumi create/delete lifecycle wrapper. The actual AWS work is done by the
	// `aws ds` CLI (matching the Python aws_directoryservice.py provider).
	//
	// The Create/Delete command strings are STATIC and contain no user data: the
	// directory ID and region are passed via the Command Environment (process
	// env) and referenced inside the static script only as double-quoted shell
	// variables (e.g. "$AD_DIRECTORY_ID"). A double-quoted "$VAR" expansion is
	// not re-parsed for metacharacters or word-splitting, so this avoids any
	// shell-injection / quoting fragility from interpolating values into `aws ...`.
	dataAccessName := fmt.Sprintf("%s-ad-data-access-enabled", name)
	dataAccessEnabled, err := commandlocal.NewCommand(pctx, dataAccessName, &commandlocal.CommandArgs{
		Create: pulumi.String(`set -e
STATUS=$(aws ds describe-directory-data-access --directory-id "$AD_DIRECTORY_ID" --query DataAccessStatus --output text)
if [ "$STATUS" != "Enabled" ] && [ "$STATUS" != "Enabling" ]; then
  aws ds enable-directory-data-access --directory-id "$AD_DIRECTORY_ID"
fi
until [ "$(aws ds describe-directory-data-access --directory-id "$AD_DIRECTORY_ID" --query DataAccessStatus --output text)" = "Enabled" ]; do sleep 2; done`),
		Delete: pulumi.String(`set -e
STATUS=$(aws ds describe-directory-data-access --directory-id "$AD_DIRECTORY_ID" --query DataAccessStatus --output text)
if [ "$STATUS" = "Enabled" ]; then
  aws ds disable-directory-data-access --directory-id "$AD_DIRECTORY_ID"
fi`),
		Triggers: pulumi.Array{ad.ID()},
		Environment: pulumi.StringMap{
			"AD_DIRECTORY_ID":    ad.ID(),
			"AWS_REGION":         pulumi.String("us-east-1"),
			"AWS_DEFAULT_REGION": pulumi.String("us-east-1"),
		},
	},
	// No alias to the old Python `pulumi-python:dynamic:Resource`. Pulumi
	// cannot adopt a dynamic-provider resource across providers — matching the
	// alias forces a cross-provider replace that must load the old
	// `pulumi-resource-pulumi-python` plugin to process the old side, which
	// fails to load in the Go runtime. The old dynamic resource is removed
	// from state via a one-time `pulumi state delete` cutover; this command
	// then creates fresh. The enable script is idempotent (no-op if already
	// Enabled/Enabling), so creating fresh against the existing AD is
	// non-destructive.
	)
	if err != nil {
		return fmt.Errorf("data access enable command: %w", err)
	}

	// IP group rules from trusted users
	var ipGroupRules awsworkspaces.IpGroupRuleArray
	for _, u := range params.trustedUsers {
		for _, ip := range u.IpAddresses {
			ipGroupRules = append(ipGroupRules, awsworkspaces.IpGroupRuleArgs{
				Source:      pulumi.Sprintf("%s/32", ip.Ip),
				Description: pulumi.Sprintf("%s - %s", u.GivenName, ip.Comment),
			})
		}
	}

	ipGroupName := fmt.Sprintf("%s-ptd-users-ip-group", name)
	ipGroupTags := pulumi.StringMap{}
	for k, v := range requiredTags {
		ipGroupTags[k] = pulumi.String(v)
	}
	ipGroup, err := awsworkspaces.NewIpGroup(pctx, ipGroupName, &awsworkspaces.IpGroupArgs{
		Name:        pulumi.String("ptd-users"),
		Description: pulumi.String("PTD trusted users"),
		Rules:       ipGroupRules,
		Tags:        ipGroupTags,
	},
		aliasForWorkspacesResource("aws:workspaces/ipGroup:IpGroup", ipGroupName),
		use1Opt,
	)
	if err != nil {
		return fmt.Errorf("IP group: %w", err)
	}

	// Workspaces Directory
	wsDirName := fmt.Sprintf("%s-workspaces-directory", name)
	wsDirTags := pulumi.StringMap{}
	for k, v := range requiredTags {
		wsDirTags[k] = pulumi.String(v)
	}
	wsDir, err := awsworkspaces.NewDirectory(pctx, wsDirName, &awsworkspaces.DirectoryArgs{
		DirectoryId: ad.ID(),
		IpGroupIds:  pulumi.StringArray{ipGroup.ID()},
		SubnetIds:   pulumi.StringArray{publicSubnet0.ID(), publicSubnet1.ID()},
		SelfServicePermissions: &awsworkspaces.DirectorySelfServicePermissionsArgs{
			ChangeComputeType:  pulumi.Bool(true),
			IncreaseVolumeSize: pulumi.Bool(true),
			RebuildWorkspace:   pulumi.Bool(true),
			RestartWorkspace:   pulumi.Bool(true),
			SwitchRunningMode:  pulumi.Bool(true),
		},
		WorkspaceAccessProperties: &awsworkspaces.DirectoryWorkspaceAccessPropertiesArgs{
			DeviceTypeAndroid:    pulumi.String("ALLOW"),
			DeviceTypeChromeos:   pulumi.String("ALLOW"),
			DeviceTypeIos:        pulumi.String("ALLOW"),
			DeviceTypeLinux:      pulumi.String("ALLOW"),
			DeviceTypeOsx:        pulumi.String("ALLOW"),
			DeviceTypeWeb:        pulumi.String("ALLOW"),
			DeviceTypeWindows:    pulumi.String("ALLOW"),
			DeviceTypeZeroclient: pulumi.String("ALLOW"),
		},
		WorkspaceCreationProperties: &awsworkspaces.DirectoryWorkspaceCreationPropertiesArgs{
			EnableInternetAccess:            pulumi.Bool(true),
			EnableMaintenanceMode:           pulumi.Bool(true),
			UserEnabledAsLocalAdministrator: pulumi.Bool(true),
		},
		Tags: wsDirTags,
	},
		aliasForWorkspacesResource("aws:workspaces/directory:Directory", wsDirName),
		use1Opt,
		// The newer pulumi-aws provider (6.83.x in state) adds `workspaceType` as a
		// defaulted, ForceNew field that did not exist in the Python-era state
		// written by the older provider. Neither this code nor the Python program
		// ever sets it, so a diff would surface as a destructive `+- (replace)` of
		// the existing PERSONAL directory (cascading to replace all live
		// WorkSpaces). Ignoring it keeps the already-PERSONAL directory in place.
		pulumi.IgnoreChanges([]string{"workspaceType"}),
	)
	if err != nil {
		return fmt.Errorf("workspaces directory: %w", err)
	}

	// Look up the workspaces bundle and KMS key (Pulumi data sources).
	wsBundle, err := awsworkspaces.GetBundle(pctx, &awsworkspaces.GetBundleArgs{
		Name:  ref("Power with Ubuntu 22.04"),
		Owner: ref("AMAZON"),
	}, pulumi.Provider(use1Provider))
	if err != nil {
		return fmt.Errorf("workspaces bundle lookup: %w", err)
	}

	kmsKey, err := awskms.LookupKey(pctx, &awskms.LookupKeyArgs{
		KeyId: "alias/aws/workspaces",
	}, pulumi.Provider(use1Provider))
	if err != nil {
		return fmt.Errorf("workspaces KMS key lookup: %w", err)
	}

	// Per-user: create AD user + workspace
	for _, u := range params.trustedUsers {
		accountName := strings.ToLower(u.GivenName)

		// Create/delete the AD user via the `aws ds-data` CLI (matching the Python
		// aws_directoryservicedata.py provider). As with data-access enablement,
		// there is no native Pulumi resource, so a commandlocal.Command wraps the
		// create/delete lifecycle.
		//
		// The Create/Delete command strings are STATIC. ALL user-supplied fields
		// (sAMAccountName, given name, surname, email) are passed via the Command
		// Environment and referenced inside the static script only as double-quoted
		// shell variables (e.g. "$AD_GIVEN_NAME"). A double-quoted "$VAR" expansion
		// is not re-parsed for metacharacters or word-splitting, so apostrophes,
		// spaces, and other shell-special characters in names/emails are handled
		// safely.
		//
		// Create is idempotent: describe-then-create no-ops if the user already
		// exists (staging was cut over with the users already present in AD).
		// Delete swallows the "DS Data feature is not enabled" case so teardown is
		// safe when the ds-data service was disabled before all users were removed.
		adUserName := fmt.Sprintf("%s-ad-user-%s", name, accountName)
		adUser, err := commandlocal.NewCommand(pctx, adUserName, &commandlocal.CommandArgs{
			Create: pulumi.String(`aws ds-data describe-user --directory-id "$AD_DIRECTORY_ID" --sam-account-name "$AD_SAM_ACCOUNT_NAME" >/dev/null 2>&1 || \
aws ds-data create-user --directory-id "$AD_DIRECTORY_ID" --sam-account-name "$AD_SAM_ACCOUNT_NAME" --given-name "$AD_GIVEN_NAME" --surname "$AD_SURNAME" --email-address "$AD_EMAIL"`),
			Delete:   pulumi.String(`aws ds-data delete-user --directory-id "$AD_DIRECTORY_ID" --sam-account-name "$AD_SAM_ACCOUNT_NAME" 2>&1 | grep -v "DS Data feature is not enabled" || true`),
			Triggers: pulumi.Array{ad.ID()},
			Environment: pulumi.StringMap{
				"AD_DIRECTORY_ID":     ad.ID(),
				"AD_SAM_ACCOUNT_NAME": pulumi.String(accountName),
				"AD_GIVEN_NAME":       pulumi.String(u.GivenName),
				"AD_SURNAME":          pulumi.String(u.FamilyName),
				"AD_EMAIL":            pulumi.String(u.Email),
				"AWS_REGION":          pulumi.String("us-east-1"),
				"AWS_DEFAULT_REGION":  pulumi.String("us-east-1"),
			},
		},
			// No alias to the old Python `pulumi-python:dynamic:Resource`. A
			// dynamic-provider resource cannot be adopted across providers: matching
			// the alias forces a cross-provider replace that must load the old
			// `pulumi-resource-pulumi-python` plugin, which fails in the Go runtime.
			// The old dynamic resource is removed from state via a one-time
			// `pulumi state delete` cutover; this command then creates fresh. The
			// create script is idempotent (no-ops if the user already exists), so
			// creating fresh against the existing AD is non-destructive.
			pulumi.DependsOn([]pulumi.Resource{dataAccessEnabled}),
		)
		if err != nil {
			return fmt.Errorf("AD user %q: %w", accountName, err)
		}

		// Workspace
		wsName := fmt.Sprintf("%s-workspaces-workspace-%s", name, accountName)
		wsTags := pulumi.StringMap{}
		for k, v := range requiredTags {
			wsTags[k] = pulumi.String(v)
		}
		wsTags["rs:owner"] = pulumi.String(u.Email)

		if _, err := awsworkspaces.NewWorkspace(pctx, wsName, &awsworkspaces.WorkspaceArgs{
			DirectoryId:                 wsDir.ID(),
			BundleId:                    pulumi.String(wsBundle.Id),
			UserName:                    pulumi.String(accountName),
			RootVolumeEncryptionEnabled: pulumi.Bool(true),
			UserVolumeEncryptionEnabled: pulumi.Bool(true),
			VolumeEncryptionKey:         pulumi.String(kmsKey.Arn),
			WorkspaceProperties: &awsworkspaces.WorkspaceWorkspacePropertiesArgs{
				ComputeTypeName:                     pulumi.String("POWER"),
				UserVolumeSizeGib:                   pulumi.Int(500),
				RootVolumeSizeGib:                   pulumi.Int(200),
				RunningMode:                         pulumi.String("AUTO_STOP"),
				RunningModeAutoStopTimeoutInMinutes: pulumi.Int(60),
			},
			Tags: wsTags,
		},
			aliasForWorkspacesResource("aws:workspaces/workspace:Workspace", wsName),
			use1Opt,
			pulumi.DependsOn([]pulumi.Resource{adUser}),
		); err != nil {
			return fmt.Errorf("workspace %q: %w", accountName, err)
		}
	}

	return nil
}
