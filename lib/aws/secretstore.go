package aws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/posit-dev/ptd/lib/secrets"
	"github.com/posit-dev/ptd/lib/types"
)

type SecretStore struct {
	region string
}

func NewSecretStore(region string) *SecretStore {
	return &SecretStore{
		region: region,
	}
}

func (s *SecretStore) SecretExists(ctx context.Context, c types.Credentials, secretName string) bool {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return false
	}
	_, err = s.GetSecretValue(ctx, awsCreds, secretName)
	// this should really check only for notfound
	if err != nil {
		return false
	}
	return true
}

func (s *SecretStore) GetSecretValue(ctx context.Context, c types.Credentials, secretName string) (string, error) {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return "", err
	}
	return getSecretValue(ctx, awsCreds, s.region, secretName)
}

func (s *SecretStore) PutSecretValue(ctx context.Context, c types.Credentials, secretName string, secretString string) error {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return err
	}
	return putSecretValue(ctx, awsCreds, s.region, secretName, secretString)
}

func (s *SecretStore) CreateSecret(ctx context.Context, c types.Credentials, secretName string, secretString string) error {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return err
	}
	return createSecret(ctx, awsCreds, s.region, secretName, secretString)
}

func (s *SecretStore) EnsureWorkloadSecret(ctx context.Context, c types.Credentials, workloadName string, secret any) (err error) {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return
	}

	secretName := workloadName + ".posit.team"

	awsSecret, ok := secret.(secrets.AWSWorkloadSecret)
	if !ok {
		return fmt.Errorf("expected AWSWorkloadSecret, got %T", secret)
	}

	val, err := getSecretValue(ctx, awsCreds, s.region, secretName)
	if err != nil {
		return
	}

	var existingSecret secrets.AWSWorkloadSecret
	err = json.Unmarshal([]byte(val), &existingSecret)
	if err != nil {
		return
	}

	if existingSecret == awsSecret {
		return nil
	}

	secretString, err := json.Marshal(awsSecret)
	if err != nil {
		return
	}

	err = putSecretValue(ctx, awsCreds, s.region, secretName, string(secretString))

	return
}

func (s *SecretStore) CreateSecretIfNotExists(ctx context.Context, c types.Credentials, secretName string, secret any) (err error) {
	awsCreds, err := OnlyAwsCredentials(c)
	if err != nil {
		return
	}

	// marshal current secret to string
	mSecret, err := json.Marshal(secret)
	if err != nil {
		return
	}

	// check if the secret exists, if it doesn't, create it
	if !s.SecretExists(ctx, awsCreds, secretName) {
		return s.CreateSecret(ctx, awsCreds, secretName, string(mSecret))
	}

	return
}
