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

// This is a simple mock implementation of the GetAuthForCredentials method
// For a real test, we would need to mock the AWS ECR service
func TestGetAuthForCredentials_Mock(t *testing.T) {
	accountID := "123456789012"
	registry := NewRegistry(accountID, "us-east-1")

	// Create a mock credentials object
	creds := &MockCredentials{
		accountIDVal: accountID,
		identityVal:  "arn:aws:iam::123456789012:role/test-role",
		isExpired:    false,
		envVarsVal:   map[string]string{},
	}

	// We can't actually call the real GetAuthForCredentials since it would try to use AWS
	// But we can check that the function exists and accepts the right parameters
	assert.NotPanics(t, func() {
		// This would normally call the AWS API, but we're not executing it
		registry.GetAuthForCredentials(context.Background(), creds)
	})
}

func TestGetLatestImageForRepository(t *testing.T) {
	accountID := "123456789012"
	registry := NewRegistry(accountID, "us-west-2")

	// Create a mock credentials object
	creds := &MockCredentials{
		accountIDVal: accountID,
		identityVal:  "arn:aws:iam::123456789012:role/test-role",
	}

	// Test that the function doesn't panic
	assert.NotPanics(t, func() {
		registry.GetLatestImageForRepository(context.Background(), creds, "test-repo")
	})

	// Without mocking the AWS SDK, we can't fully test this function
	// A full test would verify it correctly calls through to LatestImageForRepository
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
