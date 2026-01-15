package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCloudProviderConstants ensures the cloud provider constants have the expected string values
func TestCloudProviderConstants(t *testing.T) {
	assert.Equal(t, "aws", string(AWS), "AWS CloudProvider should be 'aws'")
	assert.Equal(t, "azure", string(Azure), "Azure CloudProvider should be 'azure'")
}

// MockCredentials implements the Credentials interface for testing
type MockCredentials struct {
	isExpired    bool
	accountIDVal string
	identityVal  string
	envVarsVal   map[string]string
	refreshErr   error
}

func (m *MockCredentials) Refresh(ctx context.Context) error {
	return m.refreshErr
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

// MockRegistry implements the Registry interface for testing
type MockRegistry struct {
	regionVal      string
	registryURIVal string
	authError      error
	digestError    error
}

func (m *MockRegistry) Region() string {
	return m.regionVal
}

func (m *MockRegistry) RegistryURI() string {
	return m.registryURIVal
}

func (m *MockRegistry) GetAuthForCredentials(ctx context.Context, c Credentials) (username string, password string, err error) {
	return "username", "password", m.authError
}

func (m *MockRegistry) GetLatestDigestForRepository(ctx context.Context, c Credentials, repository string) (string, error) {
	return "sha256:digest123", m.digestError
}

func (m *MockRegistry) GetLatestImageForRepository(ctx context.Context, c Credentials, repository string) (ImageDetails, error) {
	return ImageDetails{
		Digest: "sha256:digest123",
		Tags:   []string{"latest", "v1.0.0"},
	}, m.digestError
}

// MockSecretStore implements the SecretStore interface for testing
type MockSecretStore struct {
	secrets map[string]string
	exists  bool
	getErr  error
	putErr  error
}

func (m *MockSecretStore) SecretExists(ctx context.Context, c Credentials, secretName string) bool {
	return m.exists
}

func (m *MockSecretStore) GetSecretValue(ctx context.Context, c Credentials, secretName string) (string, error) {
	if !m.exists {
		return "", m.getErr
	}
	return m.secrets[secretName], nil
}

func (m *MockSecretStore) PutSecretValue(ctx context.Context, c Credentials, secretName string, secretString string) error {
	if m.secrets == nil {
		m.secrets = make(map[string]string)
	}
	m.secrets[secretName] = secretString
	return m.putErr
}

func (m *MockSecretStore) CreateSecret(ctx context.Context, c Credentials, secretName string, secretString string) error {
	return m.PutSecretValue(ctx, c, secretName, secretString)
}

func (m *MockSecretStore) CreateSecretIfNotExists(ctx context.Context, c Credentials, secretName string, secret any) error {
	if !m.exists {
		return m.PutSecretValue(ctx, c, secretName, "mock-value")
	}
	return nil
}

func (m *MockSecretStore) EnsureWorkloadSecret(ctx context.Context, c Credentials, workloadName string, secret any) error {
	return m.CreateSecretIfNotExists(ctx, c, workloadName+"-secret", secret)
}

// TestMockImplementations ensures our mock implementations satisfy the interfaces
func TestMockImplementations(t *testing.T) {
	// This test doesn't really test behavior, it just ensures our mock types
	// correctly implement the interfaces we need for testing
	var _ Credentials = &MockCredentials{}
	var _ Registry = &MockRegistry{}
	var _ SecretStore = &MockSecretStore{}

	// Create mock instances
	creds := &MockCredentials{
		isExpired:    false,
		accountIDVal: "123456789012",
		identityVal:  "mock-identity",
		envVarsVal:   map[string]string{"key": "value"},
	}

	registry := &MockRegistry{
		regionVal:      "us-west-2",
		registryURIVal: "mock-registry-uri",
	}

	secretStore := &MockSecretStore{
		exists:  true,
		secrets: map[string]string{"test-secret": "test-value"},
	}

	// Basic assertions to ensure the mocks work as expected
	assert.Equal(t, "123456789012", creds.AccountID())
	assert.Equal(t, "mock-identity", creds.Identity())
	assert.Equal(t, "value", creds.EnvVars()["key"])

	assert.Equal(t, "us-west-2", registry.Region())
	assert.Equal(t, "mock-registry-uri", registry.RegistryURI())

	assert.True(t, secretStore.SecretExists(context.Background(), creds, "test-secret"))
	value, err := secretStore.GetSecretValue(context.Background(), creds, "test-secret")
	assert.NoError(t, err)
	assert.Equal(t, "test-value", value)
}
