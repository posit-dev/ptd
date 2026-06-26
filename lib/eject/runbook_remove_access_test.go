package eject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRemoveAccessRunbook_AWS_CreatesFile(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{AccountID: "999888777666"}

	err := WriteRemoveAccessRunbook(outputDir, details, "acme-prod", "aws")
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(outputDir, "runbooks", "remove-posit-access.md"))
}

func TestWriteRemoveAccessRunbook_AWS_Content(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{AccountID: "123456789012"}

	err := WriteRemoveAccessRunbook(outputDir, details, "customer-workload", "aws")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outputDir, "runbooks", "remove-posit-access.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "customer-workload")
	assert.Contains(t, content, "123456789012")
	assert.Contains(t, content, "admin.posit.team")
	assert.Contains(t, content, "Do **not** delete the role")
	assert.Contains(t, content, "Add your own trusted principals")
	assert.Contains(t, content, "Remove Posit's trusted principals")
	assert.Contains(t, content, "assumed-role/AWSReservedSSO_PowerUser_")
	assert.NotContains(t, content, "Azure")
	assert.NotContains(t, content, "azure")
	assert.NotContains(t, content, "RBAC")
}

func TestWriteRemoveAccessRunbook_Azure_Content(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{AccountID: "ctrl-account"}

	err := WriteRemoveAccessRunbook(outputDir, details, "azure-workload", "azure")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outputDir, "runbooks", "remove-posit-access.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "azure-workload")
	assert.Contains(t, content, "RBAC role assignments")
	assert.Contains(t, content, "az role assignment")
	assert.Contains(t, content, "Posit engineer")
	assert.NotContains(t, content, "admin.posit.team")
	assert.NotContains(t, content, "IAM role trust policy")
}
