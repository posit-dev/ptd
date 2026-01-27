package azure

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/types"
)

type Target struct {
	name           string
	credentials    *Credentials
	region         string
	subscriptionID string
	sites          map[string]types.SiteConfig
	tenantID       string
	registry       *Registry
	secretStore    *SecretStore
	adminGroupID   string
	vnetRsgName    string
}

func NewTarget(targetName string, subscriptionID string, tenantID string, region string, sites map[string]types.SiteConfig, adminGroupID string, vnetRsgName string) Target {
	if region == "" {
		region = "eastus2"
	}
	t := Target{
		name:           targetName,
		credentials:    NewCredentials(subscriptionID, tenantID),
		region:         region,
		subscriptionID: subscriptionID,
		tenantID:       tenantID,
		registry:       NewRegistry(targetName, subscriptionID, region),
		sites:          sites,
		adminGroupID:   adminGroupID,
		vnetRsgName:    vnetRsgName,
	}

	// add secret store after instantiation so we can consume the vault name
	t.secretStore = NewSecretStore(region, t.VaultName())
	return t
}

func (t Target) Name() string {
	return t.name
}

// Az naming conventions are inconsistent but most allow lowercase alphanumeric and hyphens at the least
func (t Target) sanitizedName() string {
	name := strings.ToLower(t.Name())
	re := regexp.MustCompile(`[^a-z0-9-]`)
	name = re.ReplaceAllString(name, "-")
	return name
}

func (t Target) Credentials(ctx context.Context) (types.Credentials, error) {
	err := t.credentials.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	return t.credentials, nil
}

func (t Target) SubscriptionID() string {
	return t.subscriptionID
}

func (t Target) TenantID() string {
	return t.tenantID
}

func (t Target) AdminGroupID() string {
	return t.adminGroupID
}

func (t Target) Region() string {
	return t.region
}

func (t Target) CloudProvider() types.CloudProvider {
	return types.Azure
}

func (t Target) Registry() types.Registry {
	return t.registry
}

func (t Target) SecretStore() types.SecretStore {
	return t.secretStore
}

func (t Target) ControlRoom() bool {
	// we don't support control rooms in Azure
	return false
}

func (t Target) Type() types.TargetType {
	// we don't support control rooms in Azure
	return types.TargetTypeWorkload
}

// Azure storage accounts limited to 24 characters and actually don't allow hyphens for... reasons
func (t Target) StateBucketName() string {
	name := strings.ReplaceAll(t.sanitizedName(), "-", "")
	if len(name) > 19 {
		name = name[:19]
	}
	return "stptd" + name
}

func (t Target) Sites() map[string]types.SiteConfig {
	// Azure doesn't have sites like AWS does
	return t.sites
}

func (t Target) ResourceGroupName() string {
	return fmt.Sprintf("rsg-ptd-%s", t.sanitizedName())
}

func (t Target) VnetRsgName() string {
	return t.vnetRsgName
}

func (t Target) BlobStorageName() string {
	return fmt.Sprintf("blob-ptd-%s", t.sanitizedName())
}

// Key Vault names are limited to 24 characters
func (t Target) VaultName() string {
	name := t.sanitizedName()
	if len(name) > 17 {
		name = name[:17]
	}
	return fmt.Sprintf("kv-ptd-%s", name)
}

func (t Target) TailscaleEnabled() bool {
	// Azure doesn't support tailscale
	return false
}

func (t Target) PulumiBackendUrl() string {
	return fmt.Sprintf("azblob://%s?storage_account=%s", t.BlobStorageName(), t.StateBucketName())
}

func (t Target) PulumiSecretsProviderKey() string {
	return fmt.Sprintf("azurekeyvault://%s.vault.azure.net/keys/posit-team-dedicated", t.VaultName())
}

func (t Target) BastionName(ctx context.Context) (string, error) {
	// get the credentials for the target
	creds, err := t.Credentials(ctx)
	if err != nil {
		return "", err
	}
	envVars := creds.EnvVars()

	persistentStack, err := pulumi.NewPythonPulumiStack(
		ctx,
		"azure",
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

	if _, ok := persistentOutputs["bastion_name"]; !ok {
		return "", fmt.Errorf("bastion_name output not found in persistent stack outputs")
	}

	bastionName := persistentOutputs["bastion_name"].Value.(string)

	return bastionName, nil
}

func (t Target) JumpBoxId(ctx context.Context) (string, error) {
	// get the credentials for the target
	creds, err := t.Credentials(ctx)
	if err != nil {
		return "", err
	}
	envVars := creds.EnvVars()

	persistentStack, err := pulumi.NewPythonPulumiStack(
		ctx,
		"azure",
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

	if _, ok := persistentOutputs["bastion_jumpbox_id"]; !ok {
		return "", fmt.Errorf("bastion_jumpbox_id output not found in persistent stack outputs")
	}

	jumpBoxId := persistentOutputs["bastion_jumpbox_id"].Value.(string)

	return jumpBoxId, nil
}

// HashName returns an obfuscated name for the target that can be used as a unique identifier.
func (t Target) HashName() string {
	return helpers.Sha256Hash(t.Name(), 6)
}
