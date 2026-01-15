package aws

import (
	"context"

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
