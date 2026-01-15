package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/rstudio/ptd/lib/types"
)

const (
	acrUsername = "00000000-0000-0000-0000-000000000000"
)

type Registry struct {
	name           string
	subscriptionID string
	region         string
}

func NewRegistry(target string, subscriptionID string, region string) *Registry {
	return &Registry{
		name:           target,
		subscriptionID: subscriptionID,
		region:         region,
	}
}

func (r Registry) Region() string {
	return r.region
}

func (r Registry) RegistryURI() string {
	return fmt.Sprintf("crptd%s.azurecr.io", strings.Replace(r.name, "-", "", -1))
}

func (r Registry) GetAuthForCredentials(ctx context.Context, c types.Credentials) (username string, password string, err error) {
	azureCreds, err := OnlyAzureCredentials(c)
	if err != nil {
		return
	}

	token, err := GetAcrAuthToken(ctx, azureCreds, r.RegistryURI())
	if err != nil {
		return
	}

	username = acrUsername
	password = token
	return
}

func (r Registry) GetLatestDigestForRepository(_ context.Context, _ types.Credentials, _ string) (string, error) {
	return "", fmt.Errorf("GetLatestDigestForRepository not implemented for Azure")
}

func (r Registry) GetLatestImageForRepository(ctx context.Context, c types.Credentials, repository string) (details types.ImageDetails, err error) {
	return details, fmt.Errorf("GetLatestImageForRepository not implemented for Azure")
}
