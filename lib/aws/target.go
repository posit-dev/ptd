package aws

import (
	"context"
	"fmt"

	"github.com/rstudio/ptd/lib/helpers"
	"github.com/rstudio/ptd/lib/pulumi"
	"github.com/rstudio/ptd/lib/types"
)

type Target struct {
	name                              string
	credentials                       *Credentials
	region                            string
	registry                          *Registry
	secretStore                       *SecretStore
	isControlRoom                     bool
	tailscaleEnabled                  bool
	createAdminPolicyAsResource       bool
	skipControlRoomMimirPasswordWrite bool
	sites                             map[string]types.SiteConfig

	// clusters is currently only relevant/supported for aws.
	Clusters map[string]types.AWSWorkloadClusterConfig
}

func NewTarget(targetName string, accountID string, profile string, customRole *types.CustomRoleConfig, region string, isControlRoom bool, tailscaleEnabled bool, createAdminPolicyAsResource bool, sites map[string]types.SiteConfig, clusters map[string]types.AWSWorkloadClusterConfig) Target {
	if region == "" {
		region = "us-east-2"
	}

	var customRoleArn, externalID string
	if customRole != nil {
		customRoleArn = customRole.RoleArn
		externalID = customRole.ExternalID

		// Validate: external ID requires a role ARN
		if externalID != "" && customRoleArn == "" {
			panic(fmt.Sprintf("custom_role.external_id is set but custom_role.role_arn is not provided for target %s", targetName))
		}
	}

	return Target{
		name:                        targetName,
		credentials:                 NewCredentials(accountID, profile, customRoleArn, externalID),
		region:                      region,
		registry:                    NewRegistry(accountID, region),
		secretStore:                 NewSecretStore(region),
		isControlRoom:               isControlRoom,
		tailscaleEnabled:            tailscaleEnabled,
		createAdminPolicyAsResource: createAdminPolicyAsResource,
		sites:                       sites,
		Clusters:                    clusters,
	}
}

func (t Target) Name() string {
	return t.name
}

func (t Target) Credentials(ctx context.Context) (types.Credentials, error) {
	err := t.credentials.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	return t.credentials, nil
}

func (t Target) Region() string {
	return t.region
}

func (t Target) CloudProvider() types.CloudProvider {
	return types.AWS
}

func (t Target) Registry() types.Registry {
	return t.registry
}

func (t Target) SecretStore() types.SecretStore {
	return t.secretStore
}

func (t Target) ControlRoom() bool {
	return t.isControlRoom
}

func (t Target) Type() types.TargetType {
	if t.ControlRoom() {
		return types.TargetTypeControlRoom
	}
	return types.TargetTypeWorkload
}

func (t Target) BastionId(ctx context.Context) (string, error) {
	// get the credentials for the target
	creds, err := t.Credentials(ctx)
	if err != nil {
		return "", err
	}
	envVars := creds.EnvVars()

	persistentStack, err := pulumi.NewPythonPulumiStack(
		ctx,
		"aws",
		"workload",
		"persistent",
		t.Name(),
		t.Region(),
		t.PulumiBackendUrl(),
		t.PulumiSecretsProviderKey(),
		envVars,
		false,
	)
	if err != nil {
		return "", err
	}

	persistentOutputs, err := persistentStack.Outputs(ctx)
	if err != nil {
		return "", err
	}

	bastionId := persistentOutputs["bastion_id"].Value.(string)

	return bastionId, nil
}

func (t Target) StateBucketName() string {
	return fmt.Sprintf("ptd-%s", t.name)
}

func (t Target) Sites() map[string]types.SiteConfig {
	return t.sites
}

func (t Target) TailscaleEnabled() bool {
	return t.tailscaleEnabled
}

func (t Target) CreateAdminPolicyAsResource() bool {
	return t.createAdminPolicyAsResource
}

func (t Target) PulumiBackendUrl() string {
	return fmt.Sprintf("s3://%s?region=%s", t.StateBucketName(), t.Region())
}

func (t Target) PulumiSecretsProviderKey() string {
	return fmt.Sprintf("awskms://alias/posit-team-dedicated?region=%s", t.Region())
}

// HashName returns an obfuscated name for the target that can be used as a unique identifier.
func (t Target) HashName() string {
	return helpers.Sha256Hash(t.Name(), 6)
}
