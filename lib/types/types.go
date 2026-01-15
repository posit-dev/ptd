package types

import "context"

type CloudProvider string

const (
	None  CloudProvider = ""
	AWS   CloudProvider = "aws"
	Azure CloudProvider = "azure"
)

type TargetType string

const (
	TargetTypeNone        TargetType = ""
	TargetTypeControlRoom TargetType = "control-room"
	TargetTypeWorkload    TargetType = "workload"
)

// Credentials is an interface that defines the required methods for a credentials set from any cloud provider.
type Credentials interface {
	Refresh(ctx context.Context) error
	Expired() bool
	EnvVars() map[string]string

	// AccountID returns the subscription ID for Azure or the account ID for AWS.
	AccountID() string

	// Identity is abstract and can be used to return the assumed role ARN for AWS or _something_ for azure
	Identity() string
}

type ImageDetails struct {
	Digest string
	Tags   []string
}

type Registry interface {
	Region() string
	RegistryURI() string
	GetAuthForCredentials(ctx context.Context, c Credentials) (username string, password string, err error)
	GetLatestDigestForRepository(ctx context.Context, c Credentials, repository string) (string, error)
	GetLatestImageForRepository(ctx context.Context, c Credentials, repository string) (ImageDetails, error)
}

type SecretStore interface {
	SecretExists(ctx context.Context, c Credentials, secretName string) bool
	GetSecretValue(ctx context.Context, c Credentials, secretName string) (string, error)
	PutSecretValue(ctx context.Context, c Credentials, secretName string, secretString string) error
	CreateSecret(ctx context.Context, c Credentials, secretName string, secretString string) error
	CreateSecretIfNotExists(ctx context.Context, c Credentials, secretName string, secret any) error
	EnsureWorkloadSecret(ctx context.Context, c Credentials, workloadName string, secret any) error
}

// Target is an interface to abstract reference any control room or workload account.
type Target interface {
	Name() string
	Credentials(ctx context.Context) (Credentials, error)
	Region() string
	CloudProvider() CloudProvider
	Registry() Registry
	ControlRoom() bool // TODO: This likely should be refactored out in favor of TargetType
	Type() TargetType
	SecretStore() SecretStore
	StateBucketName() string
	Sites() map[string]SiteConfig
	TailscaleEnabled() bool
	PulumiBackendUrl() string
	PulumiSecretsProviderKey() string
	HashName() string
}
