package eject

import (
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectControlRoomDetails_AWS(t *testing.T) {
	config := types.AWSWorkloadConfig{
		ControlRoomAccountID:   "123456789012",
		ControlRoomClusterName: "ctrl-cluster",
		ControlRoomDomain:      "ctrl.example.com",
		ControlRoomRegion:      "us-east-1",
	}

	details, err := CollectControlRoomDetails(config, "test-workload", "ctrl-prod")

	require.NoError(t, err)
	assert.Equal(t, "123456789012", details.AccountID)
	assert.Equal(t, "ctrl-cluster", details.ClusterName)
	assert.Equal(t, "ctrl.example.com", details.Domain)
	assert.Equal(t, "us-east-1", details.Region)
	assert.Len(t, details.Connections, 3)
}

func TestCollectControlRoomDetails_Azure(t *testing.T) {
	config := types.AzureWorkloadConfig{
		ControlRoomAccountID:   "azure-sub-id",
		ControlRoomClusterName: "ctrl-aks",
		ControlRoomDomain:      "ctrl.azure.example.com",
		ControlRoomRegion:      "eastus",
	}

	details, err := CollectControlRoomDetails(config, "az-workload", "ctrl-prod")

	require.NoError(t, err)
	assert.Equal(t, "azure-sub-id", details.AccountID)
	assert.Equal(t, "ctrl-aks", details.ClusterName)
	assert.Equal(t, "ctrl.azure.example.com", details.Domain)
	assert.Equal(t, "eastus", details.Region)
}

func TestCollectControlRoomDetails_UnsupportedConfig(t *testing.T) {
	_, err := CollectControlRoomDetails("not-a-config", "test", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported config type")
}

func TestCollectControlRoomDetails_EmptyControlRoom(t *testing.T) {
	config := types.AWSWorkloadConfig{}

	details, err := CollectControlRoomDetails(config, "test-workload", "ctrl-prod")

	require.NoError(t, err)
	assert.Empty(t, details.AccountID)
	assert.Empty(t, details.Connections)
}

func TestBuildConnections_AllFieldsPopulated(t *testing.T) {
	details := &ControlRoomDetails{
		AccountID:   "123456789012",
		ClusterName: "ctrl-cluster",
		Domain:      "ctrl.example.com",
		Region:      "us-east-1",
	}

	conns := buildConnections(details, "ctrl-prod")

	assert.Len(t, conns, 3)

	// IAM Trust
	assert.Equal(t, "IAM Trust", conns[0].Category)
	assert.Contains(t, conns[0].Resource, "123456789012")

	// Mimir remote_write
	assert.Equal(t, "Observability", conns[1].Category)
	assert.Equal(t, "https://mimir.ctrl.example.com/api/v1/push", conns[1].Resource)

	// Mimir secret sync
	assert.Equal(t, "Secret Sync", conns[2].Category)
	assert.Equal(t, "ctrl-prod.mimir-auth.posit.team", conns[2].Resource)
}

func TestBuildConnections_NoControlRoom(t *testing.T) {
	details := &ControlRoomDetails{}

	conns := buildConnections(details, "ctrl-prod")

	assert.Empty(t, conns)
}

func TestBuildConnections_PartialConfig(t *testing.T) {
	details := &ControlRoomDetails{
		AccountID: "123456789012",
	}

	conns := buildConnections(details, "ctrl-prod")

	assert.Len(t, conns, 1)
	assert.Equal(t, "IAM Trust", conns[0].Category)
}

func TestBuildConnections_DomainOnly(t *testing.T) {
	details := &ControlRoomDetails{
		Domain: "ctrl.example.com",
	}

	conns := buildConnections(details, "ctrl-prod")

	assert.Len(t, conns, 2)
	assert.Equal(t, "Observability", conns[0].Category)
	assert.Equal(t, "Secret Sync", conns[1].Category)
	assert.Equal(t, "ctrl-prod.mimir-auth.posit.team", conns[1].Resource)
}

func TestBuildConnections_RemovalActions(t *testing.T) {
	details := &ControlRoomDetails{
		AccountID:   "123456789012",
		ClusterName: "ctrl-cluster",
		Domain:      "ctrl.example.com",
		Region:      "us-east-1",
	}

	conns := buildConnections(details, "ctrl-prod")

	for _, conn := range conns {
		assert.NotEmpty(t, conn.RemovalAction, "connection %s should have a removal action", conn.Category)
		assert.NotEmpty(t, conn.Description, "connection %s should have a description", conn.Category)
	}
}
