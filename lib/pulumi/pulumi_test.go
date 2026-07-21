package pulumi

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/stretchr/testify/assert"
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

// TestAWSIgnoreTagsConfig verifies the pure path-config construction for the AWS
// provider's ignoreTags.keys list. The SetConfigWithOptions call inside
// ConfigureStackRegion needs a live automation-API stack (real backend), so only
// this pure helper is unit-tested; the loop simply feeds these entries to the stack.
func TestAWSIgnoreTagsConfig(t *testing.T) {
	t.Run("empty list yields no entries", func(t *testing.T) {
		assert.Empty(t, awsIgnoreTagsConfig(nil))
		assert.Empty(t, awsIgnoreTagsConfig([]string{}))
	})

	t.Run("keys become indexed path config", func(t *testing.T) {
		entries := awsIgnoreTagsConfig([]string{"customer:cost-center", "customer:owner"})
		assert.Equal(t, []ignoreTagsConfigEntry{
			{Path: "aws:ignoreTags.keys[0]", Value: "customer:cost-center"},
			{Path: "aws:ignoreTags.keys[1]", Value: "customer:owner"},
		}, entries)
	})
}

// TestShouldRunProgramOnRefresh verifies the decision gate that enables
// optrefresh.RunProgram(true) only when aws:ignoreTags is configured with a
// non-empty value, so refresh behavior is unchanged for every other stack.
func TestShouldRunProgramOnRefresh(t *testing.T) {
	t.Run("key absent yields false", func(t *testing.T) {
		assert.False(t, shouldRunProgramOnRefresh(map[string]auto.ConfigValue{
			"aws:region": {Value: "us-east-2"},
		}))
	})

	t.Run("key present with non-empty value yields true", func(t *testing.T) {
		assert.True(t, shouldRunProgramOnRefresh(map[string]auto.ConfigValue{
			"aws:ignoreTags": {Value: "{\"keys\":[\"customer:cost-center\"]}"},
		}))
	})

	t.Run("key present with empty value yields false", func(t *testing.T) {
		assert.False(t, shouldRunProgramOnRefresh(map[string]auto.ConfigValue{
			"aws:ignoreTags": {Value: ""},
		}))
	})
}

// We're skipping the BackendUrl test as it would require extensive mocking
// of the types.Target interface. Testing would require creating a fully compliant
// mock implementation of the Target interface with all required methods.
