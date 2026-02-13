package azure

import (
	"context"
	"testing"

	"github.com/posit-dev/ptd/lib/azure/cli"
	"github.com/stretchr/testify/assert"
)

func TestNewCredentials(t *testing.T) {
	// Test creating new credentials
	subscriptionID := "12345678-1234-1234-1234-123456789012"
	tenantID := "98765432-9876-9876-9876-987654321098"

	creds := NewCredentials(subscriptionID, tenantID)

	// Verify credentials were created with correct values
	assert.Equal(t, subscriptionID, creds.subscriptionID)
	assert.Equal(t, tenantID, creds.tenantID)
	assert.NotNil(t, creds.credentials)
}

func TestCredentialsAccessors(t *testing.T) {
	subscriptionID := "12345678-1234-1234-1234-123456789012"
	tenantID := "98765432-9876-9876-9876-987654321098"

	creds := NewCredentials(subscriptionID, tenantID)

	// Test AccountID method
	t.Run("AccountID", func(t *testing.T) {
		assert.Equal(t, subscriptionID, creds.AccountID())
	})

	// Test Identity method
	t.Run("Identity", func(t *testing.T) {
		assert.Equal(t, tenantID, creds.Identity())
	})

	// Test TenantID method
	t.Run("TenantID", func(t *testing.T) {
		assert.Equal(t, tenantID, creds.TenantID())
	})

	// Test Expired method (always returns false)
	t.Run("Expired", func(t *testing.T) {
		assert.False(t, creds.Expired())
	})

	// Test Refresh method (always returns nil)
	t.Run("Refresh", func(t *testing.T) {
		// Enable mock mode for Azure CLI
		cli.SetMockMode(true)
		defer cli.SetMockMode(false)

		assert.NoError(t, creds.Refresh(context.Background()))
	})

	// Test EnvVars (returns Azure CLI authentication variables)
	t.Run("EnvVars", func(t *testing.T) {
		envVars := creds.EnvVars()
		assert.NotEmpty(t, envVars)
		assert.Equal(t, "true", envVars["ARM_USE_CLI"])
		assert.Equal(t, subscriptionID, envVars["ARM_SUBSCRIPTION_ID"])
		assert.Equal(t, tenantID, envVars["ARM_TENANT_ID"])
		assert.Equal(t, tenantID, envVars["AZURE_TENANT_ID"])
		// HTTP_PROXY breaks IMDS probe so DefaultAzureCredential falls through to AzureCLICredential
		assert.Equal(t, "http://127.0.0.1:1", envVars["HTTP_PROXY"])
		assert.Contains(t, envVars["NO_PROXY"], ".azure.com")
		assert.Len(t, envVars, 6)
	})
}

func TestOnlyAzureCredentials(t *testing.T) {
	// Test with Azure credentials
	t.Run("With Azure credentials", func(t *testing.T) {
		azureCreds := NewCredentials("sub-id", "tenant-id")

		result, err := OnlyAzureCredentials(azureCreds)
		assert.NoError(t, err)
		assert.Equal(t, azureCreds, result)
	})

	// Test with non-Azure credentials
	t.Run("With non-Azure credentials", func(t *testing.T) {
		nonAzureCreds := &MockCredentials{}

		result, err := OnlyAzureCredentials(nonAzureCreds)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "reached Azure registry with non-Azure credentials")
	})
}

// MockCredentials implements the types.Credentials interface for testing
type MockCredentials struct{}

func (m *MockCredentials) Refresh(ctx context.Context) error { return nil }
func (m *MockCredentials) Expired() bool                     { return false }
func (m *MockCredentials) EnvVars() map[string]string        { return nil }
func (m *MockCredentials) AccountID() string                 { return "mock-account" }
func (m *MockCredentials) Identity() string                  { return "mock-identity" }
