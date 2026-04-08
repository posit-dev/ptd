package eject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteStackExport(t *testing.T) {
	outputDir := t.TempDir()
	stateKey := ".pulumi/stacks/ptd-aws-workload-persistent/prod.json"
	data := []byte(`{"version":3,"checkpoint":{}}`)

	err := WriteStackExport(outputDir, stateKey, data)

	require.NoError(t, err)

	exportPath := filepath.Join(outputDir, "state", "pulumi-exports", "ptd-aws-workload-persistent.json")
	assert.FileExists(t, exportPath)

	written, err := os.ReadFile(exportPath)
	require.NoError(t, err)
	assert.Equal(t, data, written)
}

func TestWriteStackExport_MultipleStacks(t *testing.T) {
	outputDir := t.TempDir()
	stacks := map[string][]byte{
		".pulumi/stacks/ptd-aws-workload-persistent/prod.json": []byte(`{"step":"persistent"}`),
		".pulumi/stacks/ptd-aws-workload-eks/prod.json":        []byte(`{"step":"eks"}`),
		".pulumi/stacks/ptd-aws-workload-helm/prod.json":       []byte(`{"step":"helm"}`),
	}

	for key, data := range stacks {
		require.NoError(t, WriteStackExport(outputDir, key, data))
	}

	exportDir := filepath.Join(outputDir, "state", "pulumi-exports")
	entries, err := os.ReadDir(exportDir)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

func TestWriteStackExport_CreatesDirectoryStructure(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "nested", "output")

	err := WriteStackExport(outputDir, ".pulumi/stacks/proj/stack.json", []byte("{}"))

	require.NoError(t, err)
	assert.DirExists(t, filepath.Join(outputDir, "state", "pulumi-exports"))
}

func TestExportFileName(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{".pulumi/stacks/ptd-aws-workload-persistent/prod.json", "ptd-aws-workload-persistent.json"},
		{".pulumi/stacks/ptd-aws-workload-eks/staging.json", "ptd-aws-workload-eks.json"},
		{".pulumi/stacks/ptd-azure-workload-aks/prod.json", "ptd-azure-workload-aks.json"},
		{"short.json", "short.json"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.want, ExportFileName(tt.key))
		})
	}
}
