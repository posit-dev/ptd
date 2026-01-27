package helpers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/posit-dev/ptd/lib/testdata"
	"github.com/posit-dev/ptd/lib/types"
	"github.com/posit-dev/ptd/lib/types/typestest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBase64Decode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "Valid Base64",
			input:    "SGVsbG8gV29ybGQ=",
			expected: "Hello World",
			wantErr:  false,
		},
		{
			name:     "Empty String",
			input:    "",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "Invalid Base64",
			input:    "This is not valid base64!",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Base64Decode(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestTitleCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "Hello World"},
		{"HELLO WORLD", "Hello World"},
		{"hElLo WoRlD", "Hello World"},
		{"", ""},
		{"one", "One"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := TitleCase(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateRandomString(t *testing.T) {
	tests := []struct {
		length int
	}{
		{0},   // Empty string
		{1},   // Single character
		{10},  // Short string
		{100}, // Long string
	}

	for _, tt := range tests {
		t.Run("Length_"+string(rune(tt.length+'0')), func(t *testing.T) {
			result := GenerateRandomString(tt.length)

			// Verify length
			assert.Equal(t, tt.length, len(result))

			// Generate another string of same length and verify they're different
			// This is a probabilistic test, but the chance of two random strings being
			// identical is extremely low for any reasonable length
			if tt.length > 0 {
				anotherResult := GenerateRandomString(tt.length)
				assert.NotEqual(t, result, anotherResult, "Generated strings should be random")
			}
		})
	}
}

func TestLoadPtdYaml(t *testing.T) {
	// Create temporary files for testing
	tmpDir, err := os.MkdirTemp("", "ptd-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Test for AWS Workload Config
	awsWorkloadYaml := `apiVersion: v1
kind: AWSWorkloadConfig
spec:
  account_id: "123456789012"
  region: "us-west-2"
  control_room_account_id: "210987654321"
  control_room_region: "us-west-2"
  control_room_cluster_name: "test-cluster"
  control_room_domain: "example.com"
`
	awsWorkloadPath := filepath.Join(tmpDir, "aws-workload.yaml")
	err = os.WriteFile(awsWorkloadPath, []byte(awsWorkloadYaml), 0644)
	require.NoError(t, err)

	// Test for Azure Workload Config
	azureWorkloadYaml := `apiVersion: v1
kind: AzureWorkloadConfig
spec:
  subscription_id: "abcd1234-efgh-5678-ijkl-9012mnop3456"
  tenant_id: "1234abcd-5678-efgh-ijkl-9012mnop3456"
  region: "eastus"
  control_room_account_id: "9876fedc-5432-abcd-efgh-1234ijkl5678"
  control_room_region: "eastus"
  control_room_cluster_name: "test-cluster"
  control_room_domain: "example.com"
`
	azureWorkloadPath := filepath.Join(tmpDir, "azure-workload.yaml")
	err = os.WriteFile(azureWorkloadPath, []byte(azureWorkloadYaml), 0644)
	require.NoError(t, err)

	// Test for AWS Control Room Config
	awsControlRoomYaml := `apiVersion: v1
kind: AWSControlRoomConfig
spec:
  account_id: "123456789012"
  region: "us-west-2"
  domain: "example.com"
  environment: "staging"
  true_name: "test"
`
	awsControlRoomPath := filepath.Join(tmpDir, "aws-control-room.yaml")
	err = os.WriteFile(awsControlRoomPath, []byte(awsControlRoomYaml), 0644)
	require.NoError(t, err)

	// Test for unknown kind
	unknownKindYaml := `apiVersion: v1
kind: UnknownConfig
spec:
  field: "value"
`
	unknownKindPath := filepath.Join(tmpDir, "unknown-kind.yaml")
	err = os.WriteFile(unknownKindPath, []byte(unknownKindYaml), 0644)
	require.NoError(t, err)

	// Invalid YAML
	invalidYaml := `this is not valid: yaml: {`
	invalidYamlPath := filepath.Join(tmpDir, "invalid.yaml")
	err = os.WriteFile(invalidYamlPath, []byte(invalidYaml), 0644)
	require.NoError(t, err)

	tests := []struct {
		name     string
		filePath string
		wantErr  bool
		checkFn  func(t *testing.T, result interface{})
	}{
		{
			name:     "AWS Workload Config",
			filePath: awsWorkloadPath,
			wantErr:  false,
			checkFn: func(t *testing.T, result interface{}) {
				config, ok := result.(types.AWSWorkloadConfig)
				require.True(t, ok, "Expected types.AWSWorkloadConfig type")
				assert.Equal(t, "123456789012", config.AccountID)
				assert.Equal(t, "us-west-2", config.Region)
			},
		},
		{
			name:     "Azure Workload Config",
			filePath: azureWorkloadPath,
			wantErr:  false,
			checkFn: func(t *testing.T, result interface{}) {
				config, ok := result.(types.AzureWorkloadConfig)
				require.True(t, ok, "Expected types.AzureWorkloadConfig type")
				assert.Equal(t, "abcd1234-efgh-5678-ijkl-9012mnop3456", config.SubscriptionID)
				assert.Equal(t, "eastus", config.Region)
			},
		},
		{
			name:     "AWS Control Room Config",
			filePath: awsControlRoomPath,
			wantErr:  false,
			checkFn: func(t *testing.T, result interface{}) {
				config, ok := result.(types.AWSControlRoomConfig)
				require.True(t, ok, "Expected types.AWSControlRoomConfig type")
				assert.Equal(t, "123456789012", config.AccountID)
				assert.Equal(t, "staging", config.Environment)
			},
		},
		{
			name:     "Unknown Kind",
			filePath: unknownKindPath,
			wantErr:  true,
		},
		{
			name:     "Invalid YAML",
			filePath: invalidYamlPath,
			wantErr:  true,
		},
		{
			name:     "Non-existent File",
			filePath: filepath.Join(tmpDir, "nonexistent.yaml"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := LoadPtdYaml(tt.filePath)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tt.checkFn != nil {
				tt.checkFn(t, result)
			}
		})
	}
}

func TestGitTop(t *testing.T) {
	// This test depends on being run inside a Git repository
	// If it fails, it might be running in a non-Git environment
	result, err := GitTop()

	// We're not asserting specific values here since this depends on the environment,
	// but we can check that it returns something sensible if we're in a Git repo
	if err == nil {
		assert.NotEmpty(t, result, "GitTop should return a non-empty path when successful")
		_, err := os.Stat(result)
		assert.NoError(t, err, "GitTop should return an existing directory")
	}
}

func TestConfigForTarget_Workload(t *testing.T) {
	teardown, err := testdata.Setup(t)
	require.NoError(t, err)
	defer teardown()

	tgt := &typestest.MockTarget{}
	tgt.On("Name").Return("test-az-staging")
	tgt.On("Type").Return(types.TargetTypeWorkload)

	// Call the function under test
	config, err := ConfigForTarget(tgt)
	require.NoError(t, err)

	// Assert the expected config type
	_, ok := config.(types.AzureWorkloadConfig)
	require.True(t, ok)
}
