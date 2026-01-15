package azure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSecretStore(t *testing.T) {
	// Test creating a new secret store
	region := "eastus"
	vaultName := "test-vault"

	secretStore := NewSecretStore(region, vaultName)

	// Verify secret store was created with correct values
	assert.Equal(t, region, secretStore.region)
	assert.Equal(t, vaultName, secretStore.vaultName)
}

func TestSecretStoreSecretExists(t *testing.T) {
	secretStore := NewSecretStore("region", "vault")
	ctx := context.Background()

	// With non-Azure credentials
	t.Run("With non-Azure credentials", func(t *testing.T) {
		nonAzureCreds := &MockCredentials{}

		exists := secretStore.SecretExists(ctx, nonAzureCreds, "test-secret")
		assert.False(t, exists)
	})

	// We can't easily test the positive case without mocking Azure functions
}

func TestSecretStoreCreateSecret(t *testing.T) {
	secretStore := NewSecretStore("region", "vault")
	ctx := context.Background()

	// With non-Azure credentials
	t.Run("With non-Azure credentials", func(t *testing.T) {
		nonAzureCreds := &MockCredentials{}

		err := secretStore.CreateSecret(ctx, nonAzureCreds, "test-secret", "test-value")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "reached Azure registry with non-Azure credentials")
	})

	// We can't easily test the positive case without mocking Azure functions
}

func TestSecretStoreCreateSecretIfNotExists(t *testing.T) {
	secretStore := NewSecretStore("region", "vault")
	ctx := context.Background()

	// With non-Azure credentials
	t.Run("With non-Azure credentials", func(t *testing.T) {
		nonAzureCreds := &MockCredentials{}

		err := secretStore.CreateSecretIfNotExists(ctx, nonAzureCreds, "test-secret", map[string]string{"key": "value"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "reached Azure registry with non-Azure credentials")
	})

	// We can't easily test the remaining cases without mocking Azure functions
}

func TestSecretStoreGetSecretValue(t *testing.T) {
	secretStore := NewSecretStore("region", "vault")
	ctx := context.Background()

	// With non-Azure credentials
	t.Run("With non-Azure credentials", func(t *testing.T) {
		nonAzureCreds := &MockCredentials{}

		_, err := secretStore.GetSecretValue(ctx, nonAzureCreds, "test-secret")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "reached Azure registry with non-Azure credentials")
	})

	// We can't easily test the positive case without mocking Azure functions
}

func TestSecretStorePutSecretValue(t *testing.T) {
	secretStore := NewSecretStore("region", "vault")
	ctx := context.Background()

	// With non-Azure credentials
	t.Run("With non-Azure credentials", func(t *testing.T) {
		nonAzureCreds := &MockCredentials{}

		err := secretStore.PutSecretValue(ctx, nonAzureCreds, "test-secret", "test-value")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "reached Azure registry with non-Azure credentials")
	})

	// We can't easily test the positive case without mocking Azure functions
}

func TestSecretStoreEnsureWorkloadSecret(t *testing.T) {
	secretStore := NewSecretStore("region", "vault")
	ctx := context.Background()

	// With non-Azure credentials
	t.Run("With non-Azure credentials", func(t *testing.T) {
		nonAzureCreds := &MockCredentials{}

		err := secretStore.EnsureWorkloadSecret(ctx, nonAzureCreds, "test-secret", "test-value")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "reached Azure registry with non-Azure credentials")
	})

	// We can't easily test the positive case without mocking Azure functions
}
