package aws

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// GetNFSSecurityGroupID looks up a security group in the given VPC whose name starts with namePrefix.
// Returns the group ID, true if found, or "", false, nil if no matching group exists.
func GetNFSSecurityGroupID(ctx context.Context, c *Credentials, region, vpcID, namePrefix string) (string, bool, error) {
	client := ec2.New(ec2.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcID},
			},
		},
	})
	if err != nil {
		return "", false, err
	}

	for _, sg := range output.SecurityGroups {
		if sg.GroupName != nil && strings.HasPrefix(*sg.GroupName, namePrefix) {
			if sg.GroupId != nil {
				return *sg.GroupId, true, nil
			}
		}
	}

	return "", false, nil
}
