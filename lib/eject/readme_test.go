package eject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteReadme(t *testing.T) {
	outputDir := t.TempDir()
	metadata := &Metadata{
		EjectTimestamp: "2026-04-15T14:30:00Z",
		CLIVersion:     "1.2.3",
		TargetName:     "acme01-production",
		CloudProvider:  "aws",
		Region:         "us-east-2",
		AccountID:      "123456789012",
		DryRun:         true,
	}

	err := WriteReadme(metadata, outputDir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outputDir, "README.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "# Eject Bundle: acme01-production")
	assert.Contains(t, content, "PTD CLI 1.2.3")
	assert.Contains(t, content, "2026-04-15T14:30:00Z")
}

func TestGenerateReadme_ContainsDirectoryLayout(t *testing.T) {
	m := &Metadata{
		TargetName:     "test-workload",
		EjectTimestamp: "2026-04-15T14:30:00Z",
		CLIVersion:     "1.0.0",
		CloudProvider:  "aws",
		Region:         "us-east-1",
		AccountID:      "111222333444",
		DryRun:         false,
	}

	content := generateReadme(m)

	assert.Contains(t, content, "config/ptd.yaml")
	assert.Contains(t, content, "config/site_*/site.yaml")
	assert.Contains(t, content, "config/customizations/")
	assert.Contains(t, content, "metadata.json")
}

func TestGenerateReadme_DryRunNote(t *testing.T) {
	dryRun := &Metadata{DryRun: true}
	notDryRun := &Metadata{DryRun: false}

	assert.Contains(t, generateReadme(dryRun), "Dry-run mode")
	assert.NotContains(t, generateReadme(notDryRun), "Dry-run mode")
}
