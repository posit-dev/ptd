package azure

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/posit-dev/ptd/lib/secrets"
	"github.com/posit-dev/ptd/lib/types"
)

type SecretStore struct {
	region    string
	vaultName string
}

func NewSecretStore(region string, vaultName string) *SecretStore {
	return &SecretStore{
		region:    region,
		vaultName: vaultName,
	}
}

func (s *SecretStore) GetSecretValue(ctx context.Context, credentials types.Credentials, secretName string) (string, error) {
	azureCreds, err := OnlyAzureCredentials(credentials)
	if err != nil {
		return "", err
	}
	return getSecretValue(ctx, azureCreds, s.region, secretName)
}

func (s *SecretStore) PutSecretValue(ctx context.Context, credentials types.Credentials, secretName string, secretString string) error {
	azureCreds, err := OnlyAzureCredentials(credentials)
	if err != nil {
		return err
	}
	return createSecret(ctx, azureCreds, s.vaultName, secretName, secretString)
}

func (s *SecretStore) EnsureWorkloadSecret(ctx context.Context, credentials types.Credentials, workloadName string, secret any) (err error) {
	azureCreds, err := OnlyAzureCredentials(credentials)
	if err != nil {
		return
	}

	azureSecret, ok := secret.(secrets.AzureWorkloadSecret)
	if !ok {
		return fmt.Errorf("expected AzureWorkloadSecret, got %T", secret)
	}

	jsonData, err := json.Marshal(azureSecret)
	if err != nil {
		return fmt.Errorf("failed to marshal secret: %w", err)
	}

	var secretMap map[string]any
	if err := json.Unmarshal(jsonData, &secretMap); err != nil {
		return fmt.Errorf("failed to unmarshal secret: %w", err)
	}

	// Create a KeyVault secret for each populated field in the map since Azure Secret Provider
	// doesn't support json blobs, we must create a KV entry for each field.
	for fieldName, fieldValue := range secretMap {
		fieldValueStr := fmt.Sprintf("%v", fieldValue)
		if fieldValueStr == "" {
			continue
		}

		fieldSecretName := fmt.Sprintf("%s-%s", workloadName, fieldName)
		if err := createSecret(ctx, azureCreds, s.vaultName, fieldSecretName, fieldValueStr); err != nil {
			return fmt.Errorf("failed to create secret for field %s: %w", fieldName, err)
		}
	}

	return nil
}

func (s *SecretStore) SecretExists(ctx context.Context, credentials types.Credentials, secretName string) bool {
	azureCreds, err := OnlyAzureCredentials(credentials)
	if err != nil {
		return false
	}
	_, err = getSecretValue(ctx, azureCreds, s.vaultName, secretName)
	// this should really check only for notfound
	if err != nil {
		return false
	}
	return true
}

func (s *SecretStore) CreateSecret(ctx context.Context, credentials types.Credentials, secretName string, secretString string) error {
	azureCreds, err := OnlyAzureCredentials(credentials)
	if err != nil {
		return err
	}
	return createSecret(ctx, azureCreds, s.vaultName, secretName, secretString)
}

func (s *SecretStore) CreateSecretIfNotExists(ctx context.Context, credentials types.Credentials, secretName string, secret any) (err error) {
	azureCreds, err := OnlyAzureCredentials(credentials)
	if err != nil {
		return err
	}

	mSecret, err := json.Marshal(secret)
	if err != nil {
		return err
	}

	if !s.SecretExists(ctx, azureCreds, secretName) {
		return createSecret(ctx, azureCreds, s.vaultName, secretName, string(mSecret))
	}

	return
}
