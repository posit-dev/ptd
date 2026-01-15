package aws

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

func GetClusterEndpoint(ctx context.Context, c *Credentials, region string, clusterName string) (string, error) {
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if err != nil {
		return "", err
	}

	return *output.Cluster.Endpoint, nil
}
