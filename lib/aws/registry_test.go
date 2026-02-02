package aws

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRegistry(t *testing.T) {
	accountID := "123456789012"
	region := "us-west-2"
	registry := NewRegistry(accountID, region)

	assert.NotNil(t, registry)
	assert.Equal(t, region, registry.Region())
	assert.Equal(t, "123456789012.dkr.ecr.us-west-2.amazonaws.com", registry.RegistryURI())
}

func TestRegistryMethods(t *testing.T) {
	accountID := "111122223333"
	registry := NewRegistry(accountID, "us-east-1")

	// Test Region method
	assert.Equal(t, "us-east-1", registry.Region())

	// Test RegistryURI method
	assert.Equal(t, "111122223333.dkr.ecr.us-east-1.amazonaws.com", registry.RegistryURI())
}

// Test that ECR methods return deprecation errors since ECR is no longer used
func TestGetAuthForCredentials_Deprecated(t *testing.T) {
	accountID := "123456789012"
	registry := NewRegistry(accountID, "us-east-1")

	// Create a mock credentials object
	creds := &MockCredentials{
		accountIDVal: accountID,
		identityVal:  "arn:aws:iam::123456789012:role/test-role",
		isExpired:    false,
		envVarsVal:   map[string]string{},
	}

	username, password, err := registry.GetAuthForCredentials(context.Background(), creds)

	assert.ErrorIs(t, err, ErrECRDeprecated)
	assert.Empty(t, username)
	assert.Empty(t, password)
}

func TestGetLatestDigestForRepository_Deprecated(t *testing.T) {
	accountID := "123456789012"
	registry := NewRegistry(accountID, "us-west-2")

	creds := &MockCredentials{
		accountIDVal: accountID,
		identityVal:  "arn:aws:iam::123456789012:role/test-role",
	}

	digest, err := registry.GetLatestDigestForRepository(context.Background(), creds, "test-repo")

	assert.ErrorIs(t, err, ErrECRDeprecated)
	assert.Empty(t, digest)
}

func TestGetLatestImageForRepository_Deprecated(t *testing.T) {
	accountID := "123456789012"
	registry := NewRegistry(accountID, "us-west-2")

	creds := &MockCredentials{
		accountIDVal: accountID,
		identityVal:  "arn:aws:iam::123456789012:role/test-role",
	}

	details, err := registry.GetLatestImageForRepository(context.Background(), creds, "test-repo")

	assert.ErrorIs(t, err, ErrECRDeprecated)
	assert.Empty(t, details.Digest)
	assert.Nil(t, details.Tags)
}

// Mock credentials implementation for testing
type MockCredentials struct {
	accountIDVal  string
	identityVal   string
	isExpired     bool
	envVarsVal    map[string]string
	refreshCalled bool
}

func (m *MockCredentials) Refresh(ctx context.Context) error {
	m.refreshCalled = true
	return nil
}

func (m *MockCredentials) Expired() bool {
	return m.isExpired
}

func (m *MockCredentials) EnvVars() map[string]string {
	return m.envVarsVal
}

func (m *MockCredentials) AccountID() string {
	return m.accountIDVal
}

func (m *MockCredentials) Identity() string {
	return m.identityVal
}
