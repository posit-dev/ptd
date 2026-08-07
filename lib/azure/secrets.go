package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets"
	"github.com/posit-dev/ptd/lib/types"
)

func getSecretValue(ctx context.Context, credentials *Credentials, vaultName string, secretName string) (string, error) {
	client, err := azsecrets.NewClient(vaultUriFromName(vaultName), credentials.credentials, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.GetSecret(ctx, secretName, "", nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("%w: %s: %w", types.ErrSecretNotFound, secretName, err)
		}
		return "", err
	}

	return *resp.Value, nil
}

func createSecret(ctx context.Context, credentials *Credentials, vaultName string, secretName string, secretString string) error {
	client, err := azsecrets.NewClient(vaultUriFromName(vaultName), credentials.credentials, nil)
	if err != nil {
		return err
	}

	_, err = client.SetSecret(ctx, secretName, azsecrets.SetSecretParameters{Value: &secretString}, nil)
	if err != nil {
		return err
	}

	return nil

}

func vaultUriFromName(name string) string {
	return fmt.Sprintf("https://%s.vault.azure.net/", name)
}
