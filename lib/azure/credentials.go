package azure

import (
	"context"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/rstudio/ptd/lib/types"

	"github.com/rstudio/ptd/lib/azure/cli"
)

type Credentials struct {
	subscriptionID string
	tenantID       string
	credentials    *azidentity.DefaultAzureCredential
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
	return map[string]string{}
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
	azCreds, _ := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
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
