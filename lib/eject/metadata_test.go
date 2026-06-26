package eject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTime = time.Date(2026, 4, 15, 14, 30, 0, 0, time.UTC)

func TestCollectMetadata_AWSWorkload(t *testing.T) {
	config := types.AWSWorkloadConfig{
		AccountID: "123456789012",
		Region:    "us-east-2",
	}
	opts := Options{
		TargetName: "acme01-production",
		DryRun:     true,
		CLIVersion: "1.2.3",
	}

	m, err := CollectMetadata(config, opts, testTime)
	require.NoError(t, err)

	assert.Equal(t, "2026-04-15T14:30:00Z", m.EjectTimestamp)
	assert.Equal(t, "1.2.3", m.CLIVersion)
	assert.Equal(t, "acme01-production", m.TargetName)
	assert.Equal(t, "aws", m.CloudProvider)
	assert.Equal(t, "us-east-2", m.Region)
	assert.Equal(t, "123456789012", m.AccountID)
	assert.True(t, m.DryRun)
}

func TestCollectMetadata_AzureWorkload(t *testing.T) {
	config := types.AzureWorkloadConfig{
		SubscriptionID: "sub-abc-123",
		Region:         "eastus",
	}
	opts := Options{
		TargetName: "contoso01-staging",
		DryRun:     false,
		CLIVersion: "2.0.0",
	}

	m, err := CollectMetadata(config, opts, testTime)
	require.NoError(t, err)

	assert.Equal(t, "azure", m.CloudProvider)
	assert.Equal(t, "eastus", m.Region)
	assert.Equal(t, "sub-abc-123", m.AccountID)
	assert.False(t, m.DryRun)
}

func TestCollectMetadata_TimestampIsUTC(t *testing.T) {
	eastern, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	localTime := time.Date(2026, 4, 15, 10, 30, 0, 0, eastern)

	m, err := CollectMetadata(types.AWSWorkloadConfig{}, Options{}, localTime)
	require.NoError(t, err)

	assert.Equal(t, "2026-04-15T14:30:00Z", m.EjectTimestamp)
}

func TestWriteMetadata(t *testing.T) {
	outputDir := t.TempDir()
	metadata := &Metadata{
		EjectTimestamp: "2026-04-15T14:30:00Z",
		CLIVersion:     "1.2.3",
		TargetName:     "test-workload",
		CloudProvider:  "aws",
		Region:         "us-east-2",
		AccountID:      "123456789012",
		DryRun:         true,
	}

	err := WriteMetadata(metadata, outputDir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outputDir, "metadata.json"))
	require.NoError(t, err)

	var parsed Metadata
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, *metadata, parsed)
}

func TestWriteMetadata_JSONFormat(t *testing.T) {
	outputDir := t.TempDir()
	metadata := &Metadata{
		EjectTimestamp: "2026-04-15T14:30:00Z",
		CLIVersion:     "1.0.0",
		TargetName:     "test",
		CloudProvider:  "aws",
		Region:         "us-east-1",
		AccountID:      "111222333444",
		DryRun:         false,
	}

	require.NoError(t, WriteMetadata(metadata, outputDir))

	data, err := os.ReadFile(filepath.Join(outputDir, "metadata.json"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "\"dry_run\": false")
	assert.Contains(t, content, "\"cloud_provider\": \"aws\"")
	assert.True(t, content[len(content)-1] == '\n', "should end with newline")
}
