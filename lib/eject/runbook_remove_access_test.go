package eject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRemoveAccessRunbook_CreatesFile(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{AccountID: "999888777666"}

	err := WriteRemoveAccessRunbook(outputDir, details, "acme-prod")
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(outputDir, "runbooks", "remove-posit-access.md"))
}

func TestWriteRemoveAccessRunbook_ContainsTargetAndAccountID(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{AccountID: "123456789012"}

	err := WriteRemoveAccessRunbook(outputDir, details, "customer-workload")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outputDir, "runbooks", "remove-posit-access.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "customer-workload")
	assert.Contains(t, content, "123456789012")
}

func TestWriteRemoveAccessRunbook_ContainsProcedureDetails(t *testing.T) {
	outputDir := t.TempDir()
	details := &ControlRoomDetails{AccountID: "111222333444"}

	err := WriteRemoveAccessRunbook(outputDir, details, "test-workload")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outputDir, "runbooks", "remove-posit-access.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "admin.posit.team")
	assert.Contains(t, content, "arn:aws:iam::111222333444:root")
	assert.Contains(t, content, "aws iam get-role --role-name admin.posit.team")
	assert.Contains(t, content, "aws iam update-assume-role-policy")
	assert.Contains(t, content, "aws iam delete-role --role-name admin.posit.team")
	assert.Contains(t, content, "aws sts assume-role")
}
