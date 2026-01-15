package azure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRegistry(t *testing.T) {
	// Test creating a new registry
	target := "my-target"
	subscriptionID := "12345678-1234-1234-1234-123456789012"
	region := "eastus2"

	registry := NewRegistry(target, subscriptionID, region)

	// Verify registry was created with correct values
	assert.Equal(t, target, registry.name)
	assert.Equal(t, subscriptionID, registry.subscriptionID)
	assert.Equal(t, region, registry.region)
}

func TestRegistryRegion(t *testing.T) {
	// Test Region method
	registry := NewRegistry("target", "subscription", "westus")

	assert.Equal(t, "westus", registry.Region())
}

func TestRegistryURI(t *testing.T) {
	testCases := []struct {
		name           string
		registryTarget string
		expected       string
	}{
		{
			name:           "Simple name",
			registryTarget: "test",
			expected:       "crptdtest.azurecr.io",
		},
		{
			name:           "With hyphens",
			registryTarget: "my-test-registry",
			expected:       "crptdmytestregistry.azurecr.io",
		},
		{
			name:           "Complex name",
			registryTarget: "prod-main-ptd",
			expected:       "crptdprodmainptd.azurecr.io",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewRegistry(tc.registryTarget, "subscription", "region")
			assert.Equal(t, tc.expected, registry.RegistryURI())
		})
	}
}

func TestGetAuthForCredentials(t *testing.T) {
	// This is hard to test properly without mocking the ACR auth token functionality
	// We'll test that it calls OnlyAzureCredentials but nothing more

	t.Run("With non-Azure credentials", func(t *testing.T) {
		registry := NewRegistry("test", "subscription", "eastus")
		nonAzureCreds := &MockCredentials{}

		username, password, err := registry.GetAuthForCredentials(context.Background(), nonAzureCreds)
		assert.Error(t, err)
		assert.Empty(t, username)
		assert.Empty(t, password)
	})

	// We can't test with actual Azure credentials without extensive mocking
}

func TestGetLatestDigestForRepository(t *testing.T) {
	registry := NewRegistry("test", "subscription", "eastus")

	// The function always returns an error since it's not implemented
	digest, err := registry.GetLatestDigestForRepository(context.Background(), nil, "repo")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented for Azure")
	assert.Empty(t, digest)
}

func TestGetLatestImageForRepository(t *testing.T) {
	registry := NewRegistry("test", "subscription", "eastus")

	// Test the function that's not implemented
	details, err := registry.GetLatestImageForRepository(context.Background(), nil, "repo")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented for Azure")
	assert.Empty(t, details.Digest)
	assert.Empty(t, details.Tags)
}
