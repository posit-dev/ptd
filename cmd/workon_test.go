package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/posit-dev/ptd/lib/customization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestFindCustomStep(t *testing.T) {
	tests := []struct {
		name         string
		manifest     *customization.Manifest
		stepName     string
		expectFound  bool
		expectedPath string
	}{
		{
			name: "finds enabled custom step",
			manifest: &customization.Manifest{
				Version: 1,
				CustomSteps: []customization.CustomStep{
					{
						Name:        "test-step",
						Description: "Test custom step",
						Path:        "test-step/",
						Enabled:     boolPtr(true),
					},
				},
			},
			stepName:     "test-step",
			expectFound:  true,
			expectedPath: "test-step/",
		},
		{
			name: "does not find disabled custom step",
			manifest: &customization.Manifest{
				Version: 1,
				CustomSteps: []customization.CustomStep{
					{
						Name:        "disabled-step",
						Description: "Disabled custom step",
						Path:        "disabled-step/",
						Enabled:     boolPtr(false),
					},
				},
			},
			stepName:    "disabled-step",
			expectFound: false,
		},
		{
			name: "does not find non-existent step",
			manifest: &customization.Manifest{
				Version: 1,
				CustomSteps: []customization.CustomStep{
					{
						Name:        "existing-step",
						Description: "Existing custom step",
						Path:        "existing-step/",
					},
				},
			},
			stepName:    "non-existent",
			expectFound: false,
		},
		{
			name: "finds step when enabled is nil (default true)",
			manifest: &customization.Manifest{
				Version: 1,
				CustomSteps: []customization.CustomStep{
					{
						Name:        "default-enabled",
						Description: "Default enabled custom step",
						Path:        "default-enabled/",
						Enabled:     nil,
					},
				},
			},
			stepName:     "default-enabled",
			expectFound:  true,
			expectedPath: "default-enabled/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary manifest file for testing
			tempDir := t.TempDir()
			workloadPath := filepath.Join(tempDir, "workload")
			customizationsPath := filepath.Join(workloadPath, "customizations")
			require.NoError(t, os.MkdirAll(customizationsPath, 0755))

			manifestPath := filepath.Join(customizationsPath, "manifest.yaml")
			manifestData, err := yaml.Marshal(tt.manifest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(manifestPath, manifestData, 0644))

			// Test the findCustomStep function
			step, found := findCustomStep(workloadPath, tt.stepName)

			assert.Equal(t, tt.expectFound, found)
			if tt.expectFound {
				assert.NotNil(t, step)
				assert.Equal(t, tt.expectedPath, step.Path)
			} else {
				assert.Nil(t, step)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}