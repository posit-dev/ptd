package aws

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

// DBInstanceInfo holds the fields from an RDS DB instance needed by PTD steps.
type DBInstanceInfo struct {
	MasterUserSecretARN string
}

// DescribeDBInstance fetches the DB instance metadata for the given DB instance identifier.
func DescribeDBInstance(ctx context.Context, c *Credentials, region, dbInstanceID string) (*DBInstanceInfo, error) {
	client := rds.New(rds.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: awssdk.String(dbInstanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe DB instance %s: %w", dbInstanceID, err)
	}

	if len(output.DBInstances) == 0 {
		return nil, fmt.Errorf("no DB instance found with ID %s", dbInstanceID)
	}

	db := output.DBInstances[0]
	info := &DBInstanceInfo{}

	if db.MasterUserSecret != nil && db.MasterUserSecret.SecretArn != nil {
		info.MasterUserSecretARN = *db.MasterUserSecret.SecretArn
	}

	return info, nil
}
