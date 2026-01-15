package customization

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Manifest defines the structure of the customizations/manifest.yaml file
type Manifest struct {
	Version     int          `yaml:"version"`
	CustomSteps []CustomStep `yaml:"customSteps"`
}

type CustomStep struct {
	Name          string `yaml:"name"`
	Description   string `yaml:"description"`
	Path          string `yaml:"path"`
	InsertAfter   string `yaml:"insertAfter,omitempty"`
	InsertBefore  string `yaml:"insertBefore,omitempty"`
	ProxyRequired bool   `yaml:"proxyRequired"`
	Enabled       *bool  `yaml:"enabled,omitempty"`
}

func (cs *CustomStep) IsEnabled() bool {
	if cs.Enabled == nil {
		return true
	}
	return *cs.Enabled
}

func LoadManifest(manifestPath string) (*Manifest, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &manifest, nil
}

func FindManifest(workloadPath string) (string, bool) {
	manifestPath := filepath.Join(workloadPath, "customizations", "manifest.yaml")
	if _, err := os.Stat(manifestPath); err == nil {
		return manifestPath, true
	}
	return "", false
}
