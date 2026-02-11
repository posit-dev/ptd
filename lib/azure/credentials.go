package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/posit-dev/ptd/lib/types"

	"github.com/posit-dev/ptd/lib/azure/cli"
)

type Credentials struct {
	subscriptionID string
	tenantID       string
	credentials    azcore.TokenCredential
}

func (c *Credentials) Expired() bool {
	return false
}

func (c *Credentials) Refresh(ctx context.Context) error {
	// HACK: support both go-based and cli-based credentials
	// this is a no-op for go-based credentials, but pulumi uses `az` cli to determine credentials
	az := cli.GetAzInstance()
	if err := az.SetSubscription(ctx, c.subscriptionID); err != nil {
		return fmt.Errorf("failed to set Azure subscription: %w", err)
	}

	// Get access token to ensure credentials are valid, we don't actually use the token
	if err := az.GetAccessToken(ctx); err != nil {
		return fmt.Errorf("failed to get Azure access token: %w", err)
	}

	return nil
}

func (c *Credentials) EnvVars() map[string]string {
	return map[string]string{
		// ARM_* variables are used by Pulumi Azure Native provider
		"ARM_USE_CLI":         "true",
		"ARM_SUBSCRIPTION_ID": c.subscriptionID,
		"ARM_TENANT_ID":       c.tenantID,
		// AZURE_TENANT_ID is used by the Azure SDK's DefaultAzureCredential
		// (e.g. for azblob backend auth). This is distinct from ARM_TENANT_ID.
		"AZURE_TENANT_ID": c.tenantID,

		// IMDS workaround: Pulumi's gocloud.dev azurekeyvault secrets provider
		// uses DefaultAzureCredential(nil) internally. On AWS workspaces,
		// ManagedIdentityCredential's IMDS probe reaches
		// AWS's metadata service at 169.254.169.254, which responds but isn't
		// Azure IMDS. The probe succeeds, the real token request fails with
		// authenticationFailedError (not CredentialUnavailableError), and the
		// credential chain stops before reaching AzureCLICredential.
		//
		// Fix: Route the IMDS probe (plain HTTP to 169.254.169.254) through a
		// non-existent proxy so it fails immediately. ALL IMDS probe failures
		// return CredentialUnavailableError, allowing the chain to continue to
		// AzureCLICredential. Azure API calls (HTTPS) bypass the proxy via NO_PROXY.
		"HTTP_PROXY": "http://127.0.0.1:1",
		// NOTE: If Azure needs new service domains, they must be added here to bypass the proxy.
		"NO_PROXY": ".azure.com,.azure.net,.windows.net,.microsoft.com,.microsoftonline.com,localhost,127.0.0.1",
	}
}

func (c *Credentials) AccountID() string {
	return c.subscriptionID
}

func (c *Credentials) Identity() string {
	return c.tenantID
}

func (c *Credentials) TenantID() string {
	return c.tenantID
}

func NewCredentials(subscriptionID string, tenantID string) (c *Credentials) {
	// Use AzureCLICredential directly instead of DefaultAzureCredential.
	// DefaultAzureCredential tries ManagedIdentityCredential before CLI auth,
	// which hangs in AWS workspaces where IMDS exists
	// at 169.254.169.254 but isn't Azure IMDS.
	azCreds, _ := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{
		TenantID: tenantID,
	})

	return &Credentials{
		subscriptionID: subscriptionID,
		tenantID:       tenantID,
		credentials:    azCreds,
	}
}

func OnlyAzureCredentials(c types.Credentials) (*Credentials, error) {
	v, ok := c.(*Credentials)
	if !ok {
		return nil, fmt.Errorf("reached Azure registry with non-Azure credentials")
	}
	return v, nil
}
