package helpers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// WriteStruct writes a struct to a file in JSON format.
func WriteStruct(filePath string, data interface{}) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ") // Pretty print with indentation
	return encoder.Encode(data)
}

// ReadStruct reads a struct from a file in JSON format.
func ReadStruct(filePath string, data interface{}) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	return decoder.Decode(data)
}

// GetTargetsConfigPath returns the path to the infrastructure directory containing target configurations.
// Priority:
//  1. Viper config key "targets_config_dir" (can be set via CLI flag, env var, or config file)
//  2. Default: filepath.Join(TOP, "infra")
//
// The path can be:
//   - Absolute: used as-is
//   - Relative: resolved relative to TOP
func GetTargetsConfigPath() string {
	targetsConfigDir := viper.GetString("targets_config_dir")

	// If not set, use default
	if targetsConfigDir == "" {
		return filepath.Join(viper.GetString("TOP"), "infra")
	}

	// If absolute path, use as-is
	if filepath.IsAbs(targetsConfigDir) {
		return targetsConfigDir
	}

	// If relative path, resolve relative to TOP
	return filepath.Join(viper.GetString("TOP"), targetsConfigDir)
}

// ValidateTargetsConfigPath checks that the targets configuration directory exists and contains
// the expected directory structure (__ctrl__ and/or __work__ directories).
// Returns an error with helpful messaging if validation fails.
func ValidateTargetsConfigPath() error {
	targetsConfigDir := GetTargetsConfigPath()

	// Check if path exists
	if _, err := os.Stat(targetsConfigDir); os.IsNotExist(err) {
		return fmt.Errorf("targets configuration directory does not exist: %s\n"+
			"Set targets_config_dir in config, use --targets-config-dir flag, or set PTD_TARGETS_CONFIG_DIR env var",
			targetsConfigDir)
	}

	// Check for expected subdirectories
	ctrlPath := filepath.Join(targetsConfigDir, CtrlDir)
	workPath := filepath.Join(targetsConfigDir, WorkDir)

	ctrlExists := false
	workExists := false

	if _, err := os.Stat(ctrlPath); err == nil {
		ctrlExists = true
	}
	if _, err := os.Stat(workPath); err == nil {
		workExists = true
	}

	if !ctrlExists && !workExists {
		return fmt.Errorf("targets configuration directory missing expected structure: %s\n"+
			"Expected to find %s and/or %s subdirectories", targetsConfigDir, CtrlDir, WorkDir)
	}

	return nil
}
