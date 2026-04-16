package eject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const awsPtdYaml = `apiVersion: ptd.posit.co/v1
kind: AWSWorkloadConfig
spec:
  account_id: "123456789012"
  region: us-east-1
  control_room_account_id: "999888777666"
  control_room_cluster_name: ctrl-prod
  control_room_domain: ctrl.posit.team
  control_room_region: us-west-2
  vpc_cidr: "10.0.0.0/16"
  clusters:
    "20240101":
      spec:
        k8s_version: "1.29"
`

const azurePtdYaml = `apiVersion: ptd.posit.co/v1
kind: AzureWorkloadConfig
spec:
  subscription_id: "sub-1234"
  region: eastus
  control_room_account_id: "azure-ctrl-sub"
  control_room_cluster_name: ctrl-aks
  control_room_domain: ctrl.azure.posit.team
  control_room_region: westus2
  tenant_id: "tenant-5678"
`

func writePtdYaml(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ptd.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

func TestSnapshotControlRoomFields_AWS(t *testing.T) {
	path := writePtdYaml(t, awsPtdYaml)

	snapshot, err := SnapshotControlRoomFields(path)

	require.NoError(t, err)
	assert.Equal(t, "999888777666", snapshot.AccountID)
	assert.Equal(t, "ctrl-prod", snapshot.ClusterName)
	assert.Equal(t, "ctrl.posit.team", snapshot.Domain)
	assert.Equal(t, "us-west-2", snapshot.Region)
}

func TestSnapshotControlRoomFields_Azure(t *testing.T) {
	path := writePtdYaml(t, azurePtdYaml)

	snapshot, err := SnapshotControlRoomFields(path)

	require.NoError(t, err)
	assert.Equal(t, "azure-ctrl-sub", snapshot.AccountID)
	assert.Equal(t, "ctrl-aks", snapshot.ClusterName)
	assert.Equal(t, "ctrl.azure.posit.team", snapshot.Domain)
	assert.Equal(t, "westus2", snapshot.Region)
}

func TestSnapshotControlRoomFields_MissingFile(t *testing.T) {
	_, err := SnapshotControlRoomFields("/nonexistent/ptd.yaml")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read ptd.yaml")
}

func TestStripControlRoomFields_SetsValuesToEmpty(t *testing.T) {
	path := writePtdYaml(t, awsPtdYaml)

	err := StripControlRoomFields(path)

	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, `control_room_account_id: ""`)
	assert.Contains(t, content, `control_room_cluster_name: ""`)
	assert.Contains(t, content, `control_room_domain: ""`)
	assert.Contains(t, content, `control_room_region: ""`)
}

func TestStripControlRoomFields_PreservesOtherFields(t *testing.T) {
	path := writePtdYaml(t, awsPtdYaml)

	err := StripControlRoomFields(path)

	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, `account_id: "123456789012"`)
	assert.Contains(t, content, "region: us-east-1")
	assert.Contains(t, content, `vpc_cidr: "10.0.0.0/16"`)
	assert.Contains(t, content, "kind: AWSWorkloadConfig")
	assert.Contains(t, content, "apiVersion: ptd.posit.co/v1")
}

func TestStripControlRoomFields_PreservesComments(t *testing.T) {
	yaml := `apiVersion: ptd.posit.co/v1
kind: AWSWorkloadConfig
spec:
  # This is an important comment
  account_id: "123456789012"
  control_room_domain: ctrl.posit.team  # EJECT: removed during control room severance
  region: us-east-1
`
	path := writePtdYaml(t, yaml)

	err := StripControlRoomFields(path)

	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# This is an important comment")
	assert.Contains(t, content, `control_room_domain: "" # EJECT: removed during control room severance`)
}

func TestStripControlRoomFields_RoundTrip(t *testing.T) {
	path := writePtdYaml(t, awsPtdYaml)

	snapshot, err := SnapshotControlRoomFields(path)
	require.NoError(t, err)

	assert.Equal(t, "999888777666", snapshot.AccountID)
	assert.Equal(t, "ctrl-prod", snapshot.ClusterName)
	assert.Equal(t, "ctrl.posit.team", snapshot.Domain)
	assert.Equal(t, "us-west-2", snapshot.Region)

	err = StripControlRoomFields(path)
	require.NoError(t, err)

	strippedSnapshot, err := SnapshotControlRoomFields(path)
	require.NoError(t, err)

	assert.Empty(t, strippedSnapshot.AccountID)
	assert.Empty(t, strippedSnapshot.ClusterName)
	assert.Empty(t, strippedSnapshot.Domain)
	assert.Empty(t, strippedSnapshot.Region)
}
