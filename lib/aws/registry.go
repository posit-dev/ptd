package aws

import (
	"context"
	"fmt"
	"github.com/rstudio/ptd/lib/types"
	"strings"
)

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

func (r Registry) GetAuthForCredentials(ctx context.Context, c types.Credentials) (username string, password string, err error) {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return
	}
	authToken, err := GetEcrAuthToken(ctx, awsCreds, r.region)
	if err != nil {
		return
	}
	username = "AWS"
	password = strings.TrimPrefix(authToken, "AWS:")
	return
}

func (r Registry) GetLatestDigestForRepository(ctx context.Context, c types.Credentials, repository string) (string, error) {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return "", err
	}
	return LatestDigestForRepository(ctx, awsCreds, r.region, repository)
}

func (r Registry) GetLatestImageForRepository(ctx context.Context, c types.Credentials, repository string) (details types.ImageDetails, err error) {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return
	}

	return LatestImageForRepository(ctx, awsCreds, r.region, repository)
}
