package eject

import (
	"fmt"
	"os"
	"path/filepath"
)

func WriteReadme(metadata *Metadata, hasConfig bool, outputDir string) error {
	content := generateReadme(metadata, hasConfig)
	return os.WriteFile(filepath.Join(outputDir, "README.md"), []byte(content), 0644)
}

func generateReadme(m *Metadata, hasConfig bool) string {
	dryRunNote := ""
	if m.DryRun {
		dryRunNote = `
> **Dry-run mode**: This bundle was generated without modifying any infrastructure.
> No control room connections were modified.
`
	}

	configRows := ""
	if hasConfig {
		configRows = `| config/ | Workload configuration files |
| config/ptd.yaml | Main workload configuration (control_room fields annotated for eject) |
| config/site_*/site.yaml | Per-site product configuration |
| config/customizations/ | Custom Pulumi steps (source + manifest) |
`
	}

	return fmt.Sprintf(`# Eject Bundle: %s
%s
Generated on %s by PTD CLI %s.

## Directory Layout

| Path | Description |
|------|-------------|
| README.md | This file |
| metadata.json | Machine-readable context about this eject run |
%s`, m.TargetName, dryRunNote, m.EjectTimestamp, m.CLIVersion, configRows)
}
