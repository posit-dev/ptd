package eject

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteStackExport writes raw Pulumi state bytes to the exports directory.
// stateKey is the S3/Blob path like ".pulumi/stacks/ptd-aws-workload-persistent/prod.json"
// and is used to derive the output filename.
func WriteStackExport(outputDir string, stateKey string, data []byte) error {
	exportDir := filepath.Join(outputDir, "state", "pulumi-exports")
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		return fmt.Errorf("failed to create pulumi-exports directory: %w", err)
	}

	filename := ExportFileName(stateKey)
	path := filepath.Join(exportDir, filename)

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write stack export %s: %w", filename, err)
	}

	return nil
}

// ExportFileName derives "project-name.json" from a state key path.
// e.g. ".pulumi/stacks/ptd-aws-workload-persistent/prod.json" → "ptd-aws-workload-persistent.json"
func ExportFileName(stateKey string) string {
	parts := strings.Split(stateKey, "/")
	if len(parts) >= 4 {
		return parts[2] + ".json"
	}
	return filepath.Base(stateKey)
}
