package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/posit-dev/ptd/lib/consts"
)

func KmsKeyExists(ctx context.Context, c *Credentials, region string, keyId string) bool {
	client := kms.New(kms.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	_, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{
		KeyId: &keyId,
	})
	// this should really check only for notfound
	if err != nil {
		return false
	}

	return true
}

func CreateKmsKey(ctx context.Context, c *Credentials, region string, keyAlias string, description string) (string, error) {
	client := kms.New(kms.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	tagKey := consts.POSIT_TEAM_MANAGED_BY_TAG
	tagValue := "admin"
	output, err := client.CreateKey(ctx, &kms.CreateKeyInput{
		Description: &description,
		Tags: []types.Tag{
			{TagKey: &tagKey, TagValue: &tagValue},
		},
	})
	if err != nil {
		return "", err
	}

	keyId := *output.KeyMetadata.KeyId

	_, err = client.CreateAlias(ctx, &kms.CreateAliasInput{
		AliasName:   &keyAlias,
		TargetKeyId: &keyId,
	})
	if err != nil {
		return "", err
	}

	return keyId, nil
}
