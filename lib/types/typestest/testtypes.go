package typestest

import (
	"context"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/stretchr/testify/mock"
)

type MockTarget struct {
	mock.Mock
}

// Create testify mock methods for the Target interface
func (m *MockTarget) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockTarget) Credentials(ctx context.Context) (types.Credentials, error) {
	args := m.Called(ctx)
	return args.Get(0).(types.Credentials), args.Error(1)
}

func (m *MockTarget) Region() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockTarget) CloudProvider() types.CloudProvider {
	args := m.Called()
	return args.Get(0).(types.CloudProvider)
}

func (m *MockTarget) Registry() types.Registry {
	args := m.Called()
	return args.Get(0).(types.Registry)
}

func (m *MockTarget) ControlRoom() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockTarget) Type() types.TargetType {
	args := m.Called()
	return args.Get(0).(types.TargetType)
}

func (m *MockTarget) SecretStore() types.SecretStore {
	args := m.Called()
	return args.Get(0).(types.SecretStore)
}

func (m *MockTarget) StateBucketName() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockTarget) Sites() map[string]types.SiteConfig {
	args := m.Called()
	return args.Get(0).(map[string]types.SiteConfig)
}

func (m *MockTarget) TailscaleEnabled() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockTarget) PulumiBackendUrl() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockTarget) PulumiSecretsProviderKey() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockTarget) HashName() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockTarget) ResourceTags() map[string]string {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(map[string]string)
}

func DefaultAzureTarget() *MockTarget {
	azt := &MockTarget{}
	azt.On("Name").Return("test-az-staging")
	azt.On("Type").Return(types.TargetTypeWorkload)
	azt.On("CloudProvider").Return(types.Azure)
	azt.On("Credentials", mock.Anything).Return(DefaultCredentials(), nil)
	azt.On("PulumiBackendUrl").Return("example://example/backend")
	azt.On("PulumiSecretsProviderKey").Return("example://example/secrets")
	azt.On("HashName").Return("test-az-staging-hash")
	azt.On("ResourceTags").Return(map[string]string(nil))
	return azt
}

type MockCredentials struct {
	mock.Mock
}

func (m *MockCredentials) Refresh(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
func (m *MockCredentials) Expired() bool {
	args := m.Called()
	return args.Bool(0)
}
func (m *MockCredentials) AccountID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockCredentials) EnvVars() map[string]string {
	args := m.Called()
	return args.Get(0).(map[string]string)
}

func (m *MockCredentials) Identity() string {
	args := m.Called()
	return args.String(0)
}

func DefaultCredentials() *MockCredentials {
	creds := &MockCredentials{}
	creds.On("Refresh", mock.Anything).Return(nil)
	creds.On("Expired").Return(false)
	creds.On("AccountID").Return("123456789012")
	creds.On("EnvVars").Return(map[string]string{})
	creds.On("Identity").Return("identity")
	return creds
}
