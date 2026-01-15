package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCredentials(t *testing.T) {
	tests := []struct {
		name             string
		accountID        string
		profile          string
		expectedIdentity string
	}{
		{
			name:             "Regular account ID",
			accountID:        "123456789012",
			profile:          "",
			expectedIdentity: "arn:aws:iam::123456789012:role/admin.posit.team",
		},
		{
			name:             "Empty account ID",
			accountID:        "",
			profile:          "",
			expectedIdentity: "arn:aws:iam:::role/admin.posit.team",
		},
		{
			name:             "With profile",
			accountID:        "123456789012",
			profile:          "ptd-lab-staging",
			expectedIdentity: "profile/ptd-lab-staging",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds := NewCredentials(tt.accountID, tt.profile, "", "")

			assert.Equal(t, tt.accountID, creds.AccountID())
			assert.Equal(t, tt.expectedIdentity, creds.Identity())
			if tt.profile != "" {
				assert.Equal(t, tt.profile, creds.profile)
			}
		})
	}
}

func TestCredentialsEnvVars(t *testing.T) {
	accountID := "123456789012"
	creds := NewCredentials(accountID, "", "", "")

	// Set some mock credential values
	creds.credentialsProvider = credentials.NewStaticCredentialsProvider(
		"AKIAIOSFODNN7EXAMPLE",
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AQoEXAMPLEH4aoAH0gNCAPyJxz4BlCFFxWNE1OPTgk5TthT+FvwqnKwRcOIfrRh3c/LTo6UDdyJwOOvEVPvLXCrrrUtdnniCEXAMPLE/IvU1dYUg2RVAJBanLiHb4IgRmpRV3zrkuWJOgQs8IZZaIv2BXIa2R4OlgkBN9bkUDNCJiBeb/AXlzBBko7b15fjrBs2+cTQtpZ3CYWFXG8C5zqx37wnOE49mRl/+OtkIKGO7fAE",
	)

	envVars := creds.EnvVars()

	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", envVars[AccessKeyIdEnvVar])
	assert.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", envVars[SecretAccessKeyEnvVar])
	assert.Equal(t, "AQoEXAMPLEH4aoAH0gNCAPyJxz4BlCFFxWNE1OPTgk5TthT+FvwqnKwRcOIfrRh3c/LTo6UDdyJwOOvEVPvLXCrrrUtdnniCEXAMPLE/IvU1dYUg2RVAJBanLiHb4IgRmpRV3zrkuWJOgQs8IZZaIv2BXIa2R4OlgkBN9bkUDNCJiBeb/AXlzBBko7b15fjrBs2+cTQtpZ3CYWFXG8C5zqx37wnOE49mRl/+OtkIKGO7fAE", envVars[SessionTokenEnvVar])
}

func TestCredentialsExpired(t *testing.T) {
	creds := NewCredentials("123456789012", "", "", "")

	// Initially credentials should be considered expired because they're empty
	assert.True(t, creds.Expired())

	// Set some mock credential values that are not expired
	creds.credentialsProvider = credentials.NewStaticCredentialsProvider(
		"AKIAIOSFODNN7EXAMPLE",
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"session-token",
	)

	// Now the credentials should not be expired
	assert.False(t, creds.Expired())
}

func TestUseProfile(t *testing.T) {
	// Test without profile
	t.Run("Without profile", func(t *testing.T) {
		creds := NewCredentials("123456789012", "", "", "")
		assert.False(t, creds.useProfile())
	})

	// Test with profile
	t.Run("With profile", func(t *testing.T) {
		creds := NewCredentials("123456789012", "ptd-lab-staging", "", "")
		assert.True(t, creds.useProfile())
	})
}

func TestOnlyAwsCredentials(t *testing.T) {
	// Test with AWS credentials
	t.Run("AWS credentials", func(t *testing.T) {
		awsCreds := NewCredentials("123456789012", "", "", "")

		result, err := OnlyAwsCredentials(awsCreds)
		require.NoError(t, err)
		assert.Equal(t, awsCreds, result)
	})

	// Test with non-AWS credentials
	t.Run("Non-AWS credentials", func(t *testing.T) {
		// Create a mock non-AWS credentials implementation
		nonAwsCreds := &MockNonAwsCredentials{}

		result, err := OnlyAwsCredentials(nonAwsCreds)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "reached AWS specific package with non-AWS credentials")
	})
}

// MockNonAwsCredentials implements the types.Credentials interface but is not an aws.Credentials
type MockNonAwsCredentials struct{}

func (m *MockNonAwsCredentials) Refresh(ctx context.Context) error { return nil }
func (m *MockNonAwsCredentials) Expired() bool                     { return false }
func (m *MockNonAwsCredentials) EnvVars() map[string]string        { return map[string]string{} }
func (m *MockNonAwsCredentials) AccountID() string                 { return "mock-account" }
func (m *MockNonAwsCredentials) Identity() string                  { return "mock-identity" }
