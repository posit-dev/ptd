package pulumi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildProjectName(t *testing.T) {
	testCases := []struct {
		name           string
		cloud          string
		targetType     string
		stackBaseName  string
		expectedResult string
	}{
		{
			name:           "AWS control room",
			cloud:          "aws",
			targetType:     "control-room",
			stackBaseName:  "cluster",
			expectedResult: "ptd-aws-control-room-cluster",
		},
		{
			name:           "Azure workload",
			cloud:          "azure",
			targetType:     "workload",
			stackBaseName:  "persistent",
			expectedResult: "ptd-azure-workload-persistent",
		},
		{
			name:           "With underscores",
			cloud:          "aws",
			targetType:     "workload",
			stackBaseName:  "postgres_config",
			expectedResult: "ptd-aws-workload-postgres-config",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := buildProjectName(tc.cloud, tc.targetType, tc.stackBaseName)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

func TestK8sEnvVars(t *testing.T) {
	// Test that k8sEnvVars returns the expected environment variables
	envVars := k8sEnvVars()

	assert.Equal(t, "false", envVars["PULUMI_K8S_DELETE_UNREACHABLE"])
	assert.Equal(t, "true", envVars["PULUMI_K8S_ENABLE_SERVER_SIDE_APPLY"])
	assert.Equal(t, "true", envVars["PULUMI_K8S_ENABLE_PATCH_FORCE"])
	assert.Equal(t, 3, len(envVars))
}

func TestWriteMainPy(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "pulumi-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	module := "aws_workload_cluster"
	class := "AWSWorkloadCluster"

	// Write the main.py file
	err = WriteMainPy(tmpDir, module, class)
	require.NoError(t, err)

	// Verify the file was created
	mainPyPath := filepath.Join(tmpDir, "__main__.py")
	_, err = os.Stat(mainPyPath)
	assert.NoError(t, err)

	// Read the file contents
	content, err := os.ReadFile(mainPyPath)
	require.NoError(t, err)

	// Verify the content is as expected
	expectedContent := "import ptd.pulumi_resources.aws_workload_cluster\n\nptd.pulumi_resources.aws_workload_cluster.AWSWorkloadCluster.autoload()\n"
	assert.Equal(t, expectedContent, string(content))
}

// TestUvRuntime - we're not testing this function because it depends on an external viper configuration
// and would require significant setup/mocking

// We're skipping the BackendUrl test as it would require extensive mocking
// of the types.Target interface. Testing would require creating a fully compliant
// mock implementation of the Target interface with all required methods.
