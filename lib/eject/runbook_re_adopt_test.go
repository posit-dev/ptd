package eject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteReAdoptRunbook_CreatesFile(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{
		AccountID:   "999888777666",
		ClusterName: "main01",
		Domain:      "ctrl.example.com",
		Region:      "us-west-2",
	}

	err := WriteReAdoptRunbook(outputDir, details, "workload01")
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(outputDir, "runbooks", "re-adopt.md"))
}

func TestWriteReAdoptRunbook_ContainsTargetAndDetails(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{
		AccountID:   "111222333444",
		ClusterName: "prod-ctrl",
		Domain:      "control.posit.team",
		Region:      "eu-west-1",
	}

	require.NoError(t, WriteReAdoptRunbook(outputDir, details, "acme-prod"))

	data, err := os.ReadFile(filepath.Join(outputDir, "runbooks", "re-adopt.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "acme-prod")
	assert.Contains(t, content, "111222333444")
	assert.Contains(t, content, "prod-ctrl")
	assert.Contains(t, content, "control.posit.team")
	assert.Contains(t, content, "eu-west-1")
}

func TestWriteReAdoptRunbook_ContainsProcedureSteps(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{
		AccountID:   "999888777666",
		ClusterName: "main01",
		Domain:      "ctrl.example.com",
		Region:      "us-west-2",
	}

	require.NoError(t, WriteReAdoptRunbook(outputDir, details, "workload01"))

	data, err := os.ReadFile(filepath.Join(outputDir, "runbooks", "re-adopt.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "### 1. Restore control room configuration")
	assert.Contains(t, content, "### 2. Run full ensure")
	assert.Contains(t, content, "ptd ensure workload01")
	assert.Contains(t, content, "### 3. Verify")
	assert.Contains(t, content, "https://mimir.ctrl.example.com")
	assert.Contains(t, content, "## Known Gotchas")
}
