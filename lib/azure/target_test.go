package azure

import (
	"context"
	"testing"

	"github.com/rstudio/ptd/lib/azure/cli"
	"github.com/rstudio/ptd/lib/types"
	"github.com/stretchr/testify/assert"
)

func TestNewTarget(t *testing.T) {
	// Test parameters
	targetName := "test-target"
	subscriptionID := "12345678-1234-1234-1234-123456789012"
	tenantID := "98765432-9876-9876-9876-987654321098"
	region := "westus"
	sites := map[string]types.SiteConfig{
		"main": {
			Domain: "example.com",
			ZoneID: "zone1",
		},
	}

	// Create a new target
	target := NewTarget(targetName, subscriptionID, tenantID, region, sites, "", "")

	// Verify basic properties
	assert.Equal(t, targetName, target.name)
	assert.Equal(t, region, target.region)
	assert.Equal(t, subscriptionID, target.subscriptionID)
	assert.Equal(t, tenantID, target.tenantID)
	assert.Equal(t, sites, target.sites)

	// Verify objects were created
	assert.NotNil(t, target.credentials)
	assert.NotNil(t, target.registry)
	assert.NotNil(t, target.secretStore)
}

func TestNewTargetWithDefaultRegion(t *testing.T) {
	// Test that the default region is used when region is empty
	target := NewTarget("test", "sub", "tenant", "", nil, "", "")

	assert.Equal(t, "eastus2", target.region)
}

func TestTargetAccessors(t *testing.T) {
	targetName := "test-target"
	subscriptionID := "12345678-1234-1234-1234-123456789012"
	tenantID := "98765432-9876-9876-9876-987654321098"
	region := "westus"
	sites := map[string]types.SiteConfig{
		"main": {
			Domain: "example.com",
			ZoneID: "zone1",
		},
	}

	target := NewTarget(targetName, subscriptionID, tenantID, region, sites, "", "")

	// Test basic accessor methods
	t.Run("Name", func(t *testing.T) {
		assert.Equal(t, targetName, target.Name())
	})

	t.Run("Region", func(t *testing.T) {
		assert.Equal(t, region, target.Region())
	})

	t.Run("SubscriptionID", func(t *testing.T) {
		assert.Equal(t, subscriptionID, target.SubscriptionID())
	})

	t.Run("TenantID", func(t *testing.T) {
		assert.Equal(t, tenantID, target.TenantID())
	})

	t.Run("CloudProvider", func(t *testing.T) {
		assert.Equal(t, types.Azure, target.CloudProvider())
	})

	t.Run("ControlRoom", func(t *testing.T) {
		// Azure doesn't support control rooms
		assert.False(t, target.ControlRoom())
	})

	t.Run("TailscaleEnabled", func(t *testing.T) {
		// Azure doesn't support tailscale
		assert.False(t, target.TailscaleEnabled())
	})

	t.Run("Sites", func(t *testing.T) {
		assert.Equal(t, sites, target.Sites())
	})

	t.Run("Registry", func(t *testing.T) {
		registry := target.Registry()
		assert.NotNil(t, registry)
	})

	t.Run("SecretStore", func(t *testing.T) {
		secretStore := target.SecretStore()
		assert.NotNil(t, secretStore)
	})
}

func TestTargetStateBucketName(t *testing.T) {
	target := NewTarget("test-target", "sub", "tenant", "region", nil, "", "")

	bucketName := target.StateBucketName()
	assert.Equal(t, "stptdtesttarget", bucketName)
}

func TestTargetResourceGroupName(t *testing.T) {
	target := NewTarget("test-target", "sub", "tenant", "region", nil, "", "")

	rgName := target.ResourceGroupName()
	assert.Equal(t, "rsg-ptd-test-target", rgName)
}

func TestTargetVnetRsgName(t *testing.T) {
	t.Run("returns empty string when not set", func(t *testing.T) {
		target := NewTarget("test-target", "sub", "tenant", "region", nil, "", "")

		vnetRsg := target.VnetRsgName()
		assert.Equal(t, "", vnetRsg)
	})

	t.Run("returns custom vnet resource group name when set", func(t *testing.T) {
		customRsg := "custom-vnet-rsg"
		target := NewTarget("test-target", "sub", "tenant", "region", nil, "", customRsg)

		vnetRsg := target.VnetRsgName()
		assert.Equal(t, customRsg, vnetRsg)
	})
}

func TestTargetBlobStorageName(t *testing.T) {
	target := NewTarget("test-target", "sub", "tenant", "region", nil, "", "")

	// We can't predict the exact name since it involves hashing, but we can verify the format
	storageName := target.BlobStorageName()
	assert.True(t, len(storageName) <= 24) // Azure storage account names must be ≤ 24 chars
	assert.Contains(t, storageName, "blob-ptd")
}

func TestTargetVaultName(t *testing.T) {
	target := NewTarget("test-target", "sub", "tenant", "region", nil, "", "")

	// We can't predict the exact name since it involves hashing, but we can verify the format
	vaultName := target.VaultName()
	assert.Contains(t, vaultName, "ptd-")
}

func TestTargetCredentials(t *testing.T) {
	target := NewTarget("test-target", "sub", "tenant", "region", nil, "", "")

	// Enable mock mode for Azure CLI
	cli.SetMockMode(true)
	defer cli.SetMockMode(false)

	// Since Refresh() always returns nil, this should succeed
	creds, err := target.Credentials(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, creds)

	// Verify we got the right type of credentials
	_, ok := creds.(*Credentials)
	assert.True(t, ok)
}
