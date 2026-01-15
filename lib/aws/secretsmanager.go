package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/rstudio/ptd/lib/consts"
)

func getSecretValue(ctx context.Context, c *Credentials, region string, secretName string) (secretString string, err error) {
	client := secretsmanager.New(secretsmanager.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	result, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretName,
	})
	if err != nil {
		return
	}

	secretString = *result.SecretString

	return
}

func putSecretValue(ctx context.Context, c *Credentials, region string, secretName string, secretString string) (err error) {
	client := secretsmanager.New(secretsmanager.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	_, err = client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     &secretName,
		SecretString: &secretString,
	})
	if err != nil {
		return
	}

	return
}

func createSecret(ctx context.Context, c *Credentials, region string, secretName string, secretString string) (err error) {
	client := secretsmanager.New(secretsmanager.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	tagKey := consts.POSIT_TEAM_MANAGED_BY_TAG
	tagValue := "admin"
	_, err = client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         &secretName,
		SecretString: &secretString,
		Tags: []types.Tag{
			{Key: &tagKey, Value: &tagValue},
		},
	})
	if err != nil {
		return
	}

	return
}
