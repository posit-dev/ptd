package aws

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func BucketExists(ctx context.Context, c *Credentials, region string, bucketName string) bool {
	client := s3.New(s3.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &bucketName,
	})
	if err != nil {
		return false
	}

	return true
}

func CreateBucket(ctx context.Context, c *Credentials, region string, bucketName string) error {
	client := s3.New(s3.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	// The if/else is because us-east-1 does not accept a LocationConstraint
	// api error InvalidLocationConstraint: The specified location-constraint is not valid
	if region == "us-east-1" {
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket:          &bucketName,
			ObjectOwnership: types.ObjectOwnershipBucketOwnerEnforced,
		})
		if err != nil {
			return err
		}
		return nil
	} else {
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: &bucketName,
			CreateBucketConfiguration: &types.CreateBucketConfiguration{
				LocationConstraint: types.BucketLocationConstraint(region),
			},
			ObjectOwnership: types.ObjectOwnershipBucketOwnerEnforced,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// ListStateFiles lists all .json Pulumi state files under the .pulumi/stacks/ prefix in a bucket.
// It returns the full S3 key paths, excluding .bak files.
func ListStateFiles(ctx context.Context, c *Credentials, region string, bucketName string) ([]string, error) {
	client := s3.New(s3.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	prefix := ".pulumi/stacks/"
	var keys []string

	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects in bucket %s: %w", bucketName, err)
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			// Only include .json files (exclude .bak files)
			if strings.HasSuffix(key, ".json") {
				keys = append(keys, key)
			}
		}
	}

	return keys, nil
}

// GetStateFile downloads a Pulumi state file from S3 and returns its contents as bytes.
func GetStateFile(ctx context.Context, c *Credentials, region string, bucketName string, key string) ([]byte, error) {
	client := s3.New(s3.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get object %s from bucket %s: %w", key, bucketName, err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object body for %s: %w", key, err)
	}

	return data, nil
}
