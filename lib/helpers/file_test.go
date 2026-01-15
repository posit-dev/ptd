package helpers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStructForReadWrite is a test structure used for serialization and deserialization
type TestStructForReadWrite struct {
	Name        string   `json:"name"`
	Age         int      `json:"age"`
	IsActive    bool     `json:"is_active"`
	Tags        []string `json:"tags"`
	NestedField struct {
		Value string `json:"value"`
	} `json:"nested_field"`
}

func TestWriteReadStruct(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "ptd-file-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	testFilePath := filepath.Join(tmpDir, "test.json")

	// Create test data structure
	testData := TestStructForReadWrite{
		Name:     "Test User",
		Age:      30,
		IsActive: true,
		Tags:     []string{"tag1", "tag2", "tag3"},
	}
	testData.NestedField.Value = "nested value"

	// Test WriteStruct
	t.Run("WriteStruct", func(t *testing.T) {
		err := WriteStruct(testFilePath, testData)
		assert.NoError(t, err)

		// Verify file exists
		_, err = os.Stat(testFilePath)
		assert.NoError(t, err)

		// Read the file content to verify it's JSON
		content, err := os.ReadFile(testFilePath)
		assert.NoError(t, err)
		assert.Contains(t, string(content), "Test User")
		assert.Contains(t, string(content), "30")
		assert.Contains(t, string(content), "nested value")
	})

	// Test ReadStruct
	t.Run("ReadStruct", func(t *testing.T) {
		var readData TestStructForReadWrite
		err := ReadStruct(testFilePath, &readData)
		assert.NoError(t, err)

		// Verify data matches
		assert.Equal(t, testData.Name, readData.Name)
		assert.Equal(t, testData.Age, readData.Age)
		assert.Equal(t, testData.IsActive, readData.IsActive)
		assert.Equal(t, testData.Tags, readData.Tags)
		assert.Equal(t, testData.NestedField.Value, readData.NestedField.Value)
	})

	// Test ReadStruct with invalid data
	t.Run("ReadStruct with invalid JSON", func(t *testing.T) {
		invalidFile := filepath.Join(tmpDir, "invalid.json")
		err := os.WriteFile(invalidFile, []byte("This is not valid JSON"), 0644)
		require.NoError(t, err)

		var readData TestStructForReadWrite
		err = ReadStruct(invalidFile, &readData)
		assert.Error(t, err)
	})

	// Test ReadStruct with non-existent file
	t.Run("ReadStruct with non-existent file", func(t *testing.T) {
		var readData TestStructForReadWrite
		err := ReadStruct(filepath.Join(tmpDir, "nonexistent.json"), &readData)
		assert.Error(t, err)
	})

	// Test WriteStruct with invalid path
	t.Run("WriteStruct with invalid path", func(t *testing.T) {
		err := WriteStruct(filepath.Join(tmpDir, "invalid/path/test.json"), testData)
		assert.Error(t, err)
	})
}

func TestGetTargetsConfigPath(t *testing.T) {
	// Save original viper state
	originalTargetsConfigDir := viper.GetString("targets_config_dir")
	originalTOP := viper.GetString("TOP")
	defer func() {
		// Restore original values
		if originalTargetsConfigDir != "" {
			viper.Set("targets_config_dir", originalTargetsConfigDir)
		} else {
			viper.Set("targets_config_dir", "")
		}
		if originalTOP != "" {
			viper.SetDefault("TOP", originalTOP)
		}
	}()

	// Use a test TOP value
	testTOP := "/test-project"
	viper.SetDefault("TOP", testTOP)

	tests := []struct {
		name             string
		targetsConfigDir string
		expected         string
	}{
		{
			name:             "default behavior",
			targetsConfigDir: "",
			expected:         filepath.Join(testTOP, "infra"),
		},
		{
			name:             "absolute path",
			targetsConfigDir: "/custom/targets",
			expected:         "/custom/targets",
		},
		{
			name:             "relative path",
			targetsConfigDir: "custom/targets",
			expected:         filepath.Join(testTOP, "custom/targets"),
		},
		{
			name:             "relative path with dots",
			targetsConfigDir: "../other-repo/infra",
			expected:         filepath.Clean(filepath.Join(testTOP, "../other-repo/infra")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("targets_config_dir", tt.targetsConfigDir)

			result := GetTargetsConfigPath()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateTargetsConfigPath(t *testing.T) {
	// Save original viper state
	originalTargetsConfigDir := viper.GetString("targets_config_dir")
	originalTOP := viper.GetString("TOP")
	defer func() {
		// Restore original values
		if originalTargetsConfigDir != "" {
			viper.Set("targets_config_dir", originalTargetsConfigDir)
		} else {
			viper.Set("targets_config_dir", "")
		}
		if originalTOP != "" {
			viper.SetDefault("TOP", originalTOP)
		}
	}()

	t.Run("valid directory with both __ctrl__ and __work__", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "ptd-validate-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create expected subdirectories
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, CtrlDir), 0755))
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, WorkDir), 0755))

		viper.SetDefault("TOP", "/")
		viper.Set("targets_config_dir", tmpDir)

		err = ValidateTargetsConfigPath()
		assert.NoError(t, err)
	})

	t.Run("valid directory with only __ctrl__", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "ptd-validate-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create only control rooms directory
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, CtrlDir), 0755))

		viper.SetDefault("TOP", "/")
		viper.Set("targets_config_dir", tmpDir)

		err = ValidateTargetsConfigPath()
		assert.NoError(t, err)
	})

	t.Run("valid directory with only __work__", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "ptd-validate-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create only workloads directory
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, WorkDir), 0755))

		viper.SetDefault("TOP", "/")
		viper.Set("targets_config_dir", tmpDir)

		err = ValidateTargetsConfigPath()
		assert.NoError(t, err)
	})

	t.Run("directory does not exist", func(t *testing.T) {
		viper.SetDefault("TOP", "/")
		viper.Set("targets_config_dir", "/nonexistent/path")

		err := ValidateTargetsConfigPath()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})

	t.Run("directory exists but missing expected structure", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "ptd-validate-test")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create directory but no subdirectories
		viper.SetDefault("TOP", "/")
		viper.Set("targets_config_dir", tmpDir)

		err = ValidateTargetsConfigPath()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing expected structure")
	})
}
