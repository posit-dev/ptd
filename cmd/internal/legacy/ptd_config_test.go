package legacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestFindTargetDir(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create test directory structure
	workDir := filepath.Join(tempDir, WorkDir)
	ctrlDir := filepath.Join(tempDir, CtrlDir)
	os.MkdirAll(workDir, 0755)
	os.MkdirAll(ctrlDir, 0755)

	// Create a workload target
	workloadTarget := "workload-staging"
	os.MkdirAll(filepath.Join(workDir, workloadTarget), 0755)

	// Create a control room target
	ctrlTarget := "control-production"
	os.MkdirAll(filepath.Join(ctrlDir, ctrlTarget), 0755)

	// Create a target that exists in both directories (ambiguous case)
	ambiguousTarget := "duplicate-target"
	os.MkdirAll(filepath.Join(workDir, ambiguousTarget), 0755)
	os.MkdirAll(filepath.Join(ctrlDir, ambiguousTarget), 0755)

	tests := []struct {
		name        string
		target      string
		expectedDir string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "workload target in WorkDir",
			target:      workloadTarget,
			expectedDir: WorkDir,
			expectError: false,
		},
		{
			name:        "control room target in CtrlDir",
			target:      ctrlTarget,
			expectedDir: CtrlDir,
			expectError: false,
		},
		{
			name:        "target exists in both directories",
			target:      ambiguousTarget,
			expectedDir: "",
			expectError: true,
			errorMsg:    "exists in both",
		},
		{
			name:        "target does not exist",
			target:      "nonexistent-target",
			expectedDir: "",
			expectError: true,
			errorMsg:    "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := findTargetDir(tempDir, tt.target)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.errorMsg)
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if dir != tt.expectedDir {
					t.Errorf("Expected directory '%s', got '%s'", tt.expectedDir, dir)
				}
			}
		})
	}
}

func TestPtdYamlFromTargetName(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create test directory structure
	infraDir := filepath.Join(tempDir, "infra")
	os.MkdirAll(filepath.Join(infraDir, WorkDir), 0755)
	os.MkdirAll(filepath.Join(infraDir, CtrlDir), 0755)

	// Create a workload target
	workloadTarget := "workload-staging"
	os.MkdirAll(filepath.Join(infraDir, WorkDir, workloadTarget), 0755)

	// Create a control room target
	ctrlTarget := "control-production"
	os.MkdirAll(filepath.Join(infraDir, CtrlDir, ctrlTarget), 0755)

	// Save original TOP value
	originalTOP := viper.GetString("TOP")
	defer viper.Set("TOP", originalTOP)

	// Set the test TOP value to our temp directory
	viper.Set("TOP", tempDir)

	tests := []struct {
		name         string
		target       string
		expectedPath string
		expectError  bool
	}{
		{
			name:         "workload target",
			target:       workloadTarget,
			expectedPath: filepath.Join(infraDir, WorkDir, workloadTarget, "ptd.yaml"),
			expectError:  false,
		},
		{
			name:         "control room target",
			target:       ctrlTarget,
			expectedPath: filepath.Join(infraDir, CtrlDir, ctrlTarget, "ptd.yaml"),
			expectError:  false,
		},
		{
			name:         "nonexistent target",
			target:       "nonexistent-target",
			expectedPath: "",
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := ptdYamlFromTargetName(tt.target)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if path != tt.expectedPath {
					t.Errorf("Expected path '%s', got '%s'", tt.expectedPath, path)
				}
			}
		})
	}
}
