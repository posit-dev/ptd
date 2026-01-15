package azure

import (
	"context"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets"
)

func getSecretValue(ctx context.Context, credentials *Credentials, vaultName string, secretName string) (string, error) {
	client, err := azsecrets.NewClient(vaultUriFromName(vaultName), credentials.credentials, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.GetSecret(ctx, secretName, "", nil)
	if err != nil {
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
