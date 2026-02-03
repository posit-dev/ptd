package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/posit-dev/ptd/lib/types"
)

// ErrECRDeprecated is returned when ECR functionality is accessed.
// ECR has been removed in favor of public Docker Hub images.
var ErrECRDeprecated = errors.New("ECR functionality has been removed; images are now pulled from public Docker Hub")

type Registry struct {
	accountID string
	region    string
}

func NewRegistry(accountID string, region string) *Registry {
	return &Registry{
		accountID: accountID,
		region:    region,
	}
}

func (r Registry) Region() string {
	return r.region
}

func (r Registry) RegistryURI() string {
	return fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", r.accountID, r.region)
}

// GetAuthForCredentials is deprecated - ECR is no longer used.
// Images are now pulled from public Docker Hub.
func (r Registry) GetAuthForCredentials(ctx context.Context, c types.Credentials) (username string, password string, err error) {
	return "", "", ErrECRDeprecated
}

// GetLatestDigestForRepository is deprecated - ECR is no longer used.
// Images are now pulled from public Docker Hub.
func (r Registry) GetLatestDigestForRepository(ctx context.Context, c types.Credentials, repository string) (string, error) {
	return "", ErrECRDeprecated
}

// GetLatestImageForRepository is deprecated - ECR is no longer used.
// Images are now pulled from public Docker Hub.
func (r Registry) GetLatestImageForRepository(ctx context.Context, c types.Credentials, repository string) (details types.ImageDetails, err error) {
	return types.ImageDetails{}, ErrECRDeprecated
}
