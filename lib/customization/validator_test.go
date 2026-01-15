package customization

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestValidate_Version(t *testing.T) {
	standardSteps := []string{"bootstrap", "persistent", "eks"}

	tests := []struct {
		name        string
		version     int
		expectError bool
	}{
		{"valid version 1", 1, false},
		{"invalid version 0", 0, true},
		{"invalid version 2", 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &Manifest{
				Version:     tt.version,
				CustomSteps: []CustomStep{},
			}

			err := manifest.Validate("", standardSteps)
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestManifestValidate_DuplicateStepNames(t *testing.T) {
	standardSteps := []string{"bootstrap", "persistent", "eks"}

	// Create temp directory structure for testing
	tmpDir := t.TempDir()
	createTestStepDir(t, tmpDir, "step1")
	createTestStepDir(t, tmpDir, "step2")

	tests := []struct {
		name        string
		steps       []CustomStep
		expectError bool
	}{
		{
			name: "unique step names",
			steps: []CustomStep{
				{Name: "step1", Path: "step1/"},
				{Name: "step2", Path: "step2/"},
			},
			expectError: false,
		},
		{
			name: "duplicate step names",
			steps: []CustomStep{
				{Name: "step1", Path: "step1/"},
				{Name: "step1", Path: "step2/"},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &Manifest{
				Version:     1,
				CustomSteps: tt.steps,
			}

			err := manifest.Validate(tmpDir, standardSteps)
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestCustomStepValidate_InsertionPoints(t *testing.T) {
	standardSteps := []string{"bootstrap", "persistent", "eks", "helm"}

	tests := []struct {
		name         string
		insertAfter  string
		insertBefore string
		expectError  bool
	}{
		{"valid insertAfter", "persistent", "", false},
		{"valid insertBefore", "", "eks", false},
		{"both valid and adjacent", "persistent", "eks", false},
		{"invalid insertAfter", "nonexistent", "", true},
		{"invalid insertBefore", "", "nonexistent", true},
		{"both valid but not adjacent", "bootstrap", "helm", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory with test step
			tmpDir := t.TempDir()
			createTestStepDir(t, tmpDir, "test-step")

			cs := CustomStep{
				Name:         "test-step",
				Path:         "test-step/",
				InsertAfter:  tt.insertAfter,
				InsertBefore: tt.insertBefore,
			}

			err := cs.Validate(tmpDir, standardSteps)
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestCustomStepValidate_StepDirectory(t *testing.T) {
	standardSteps := []string{"bootstrap", "persistent"}

	tests := []struct {
		name          string
		setupDir      func(string) // Function to setup test directory
		expectError   bool
		errorContains string
	}{
		{
			name: "valid directory with main.go and go.mod",
			setupDir: func(dir string) {
				createTestStepDir(t, dir, "valid-step")
			},
			expectError: false,
		},
		{
			name: "missing main.go",
			setupDir: func(dir string) {
				stepDir := filepath.Join(dir, "customizations", "missing-main")
				os.MkdirAll(stepDir, 0755)
				os.WriteFile(filepath.Join(stepDir, "go.mod"), []byte("module test"), 0644)
			},
			expectError:   true,
			errorContains: "main.go not found",
		},
		{
			name: "missing go.mod",
			setupDir: func(dir string) {
				stepDir := filepath.Join(dir, "customizations", "missing-mod")
				os.MkdirAll(stepDir, 0755)
				os.WriteFile(filepath.Join(stepDir, "main.go"), []byte("package main"), 0644)
			},
			expectError:   true,
			errorContains: "go.mod not found",
		},
		{
			name: "directory does not exist",
			setupDir: func(dir string) {
				// Don't create directory
			},
			expectError:   true,
			errorContains: "does not exist",
		},
		{
			name: "path is a file, not directory",
			setupDir: func(dir string) {
				os.MkdirAll(filepath.Join(dir, "customizations"), 0755)
				os.WriteFile(filepath.Join(dir, "customizations", "not-a-dir"), []byte("content"), 0644)
			},
			expectError:   true,
			errorContains: "not a directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tt.setupDir(tmpDir)

			// Use the appropriate path for the test case
			stepPath := "valid-step/"
			if tt.name == "missing main.go" {
				stepPath = "missing-main/"
			} else if tt.name == "missing go.mod" {
				stepPath = "missing-mod/"
			} else if tt.name == "directory does not exist" {
				stepPath = "nonexistent/"
			} else if tt.name == "path is a file, not directory" {
				stepPath = "not-a-dir"
			}

			cs := CustomStep{
				Name: "test-step",
				Path: stepPath,
			}

			err := cs.Validate(tmpDir, standardSteps)
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectError && err != nil && tt.errorContains != "" {
				if !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error to contain '%s', got: %v", tt.errorContains, err)
				}
			}
		})
	}
}

func TestManifestValidate_InsertionPointConsistency(t *testing.T) {
	standardSteps := []string{"bootstrap", "persistent", "eks", "helm", "sites"}
	tmpDir := t.TempDir()
	createTestStepDir(t, tmpDir, "step1")
	createTestStepDir(t, tmpDir, "step2")

	tests := []struct {
		name        string
		steps       []CustomStep
		expectError bool
	}{
		{
			name: "valid: insertAfter and insertBefore are adjacent",
			steps: []CustomStep{
				{
					Name:         "step1",
					Path:         "step1/",
					InsertAfter:  "persistent",
					InsertBefore: "eks",
				},
			},
			expectError: false,
		},
		{
			name: "invalid: insertAfter and insertBefore are not adjacent",
			steps: []CustomStep{
				{
					Name:         "step1",
					Path:         "step1/",
					InsertAfter:  "bootstrap",
					InsertBefore: "helm",
				},
			},
			expectError: true,
		},
		{
			name: "valid: only insertAfter specified",
			steps: []CustomStep{
				{
					Name:        "step1",
					Path:        "step1/",
					InsertAfter: "persistent",
				},
			},
			expectError: false,
		},
		{
			name: "valid: only insertBefore specified",
			steps: []CustomStep{
				{
					Name:         "step1",
					Path:         "step1/",
					InsertBefore: "eks",
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &Manifest{
				Version:     1,
				CustomSteps: tt.steps,
			}

			err := manifest.Validate(tmpDir, standardSteps)
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestCustomStepIsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		enabled  *bool
		expected bool
	}{
		{"nil enabled (default true)", nil, true},
		{"explicitly enabled", boolPtr(true), true},
		{"explicitly disabled", boolPtr(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := CustomStep{
				Name:    "test",
				Enabled: tt.enabled,
			}

			if cs.IsEnabled() != tt.expected {
				t.Errorf("IsEnabled() = %v, expected %v", cs.IsEnabled(), tt.expected)
			}
		})
	}
}

func TestManifestValidate_Integration(t *testing.T) {
	// Create a complete test scenario
	tmpDir := t.TempDir()
	standardSteps := []string{"bootstrap", "persistent", "eks", "helm"}

	// Setup test directories
	createTestStepDir(t, tmpDir, "custom-dns")
	createTestStepDir(t, tmpDir, "custom-monitoring")
	createTestStepDir(t, tmpDir, "custom-disabled")

	manifest := &Manifest{
		Version: 1,
		CustomSteps: []CustomStep{
			{
				Name:          "custom-dns",
				Description:   "Custom DNS configuration",
				Path:          "custom-dns/",
				InsertAfter:   "persistent",
				ProxyRequired: false,
			},
			{
				Name:          "custom-monitoring",
				Description:   "Monitoring dashboards",
				Path:          "custom-monitoring/",
				InsertAfter:   "helm",
				ProxyRequired: true,
			},
			{
				Name:        "custom-disabled",
				Description: "Disabled step",
				Path:        "custom-disabled/",
				Enabled:     boolPtr(false),
			},
		},
	}

	err := manifest.Validate(tmpDir, standardSteps)
	if err != nil {
		t.Errorf("valid manifest failed validation: %v", err)
	}
}

// Helper functions

func createTestStepDir(t *testing.T, baseDir, stepName string) {
	t.Helper()
	stepDir := filepath.Join(baseDir, "customizations", stepName)
	if err := os.MkdirAll(stepDir, 0755); err != nil {
		t.Fatalf("failed to create step directory: %v", err)
	}

	mainGo := `package main
import "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		return nil
	})
}
`
	if err := os.WriteFile(filepath.Join(stepDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	goMod := `module test
go 1.21
`
	if err := os.WriteFile(filepath.Join(stepDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to create go.mod: %v", err)
	}
}

func boolPtr(b bool) *bool {
	return &b
}
