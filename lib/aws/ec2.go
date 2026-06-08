package aws

import (
	"context"
	"fmt"
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

// GetVpcID returns the ID of the PTD-managed VPC for a workload/control room,
// looked up by the compound-name Name tag + the posit.team/managed-by tag-key.
// Mirrors Python aws_vpc / aws_vpc_id. Returns "", false, nil when no VPC
// matches (greenfield).
func GetVpcID(ctx context.Context, c *Credentials, region, name string) (string, bool, error) {
	client := ec2.New(ec2.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})
	out, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:Name"), Values: []string{name}},
			{Name: aws.String("tag-key"), Values: []string{"posit.team/managed-by"}},
		},
	})
	if err != nil {
		return "", false, fmt.Errorf("describe vpcs for %s: %w", name, err)
	}
	for _, v := range out.Vpcs {
		if v.VpcId != nil {
			return *v.VpcId, true, nil
		}
	}
	return "", false, nil
}

// GetWorkloadPrivateSubnetIDs returns the PTD-managed private subnet IDs for a
// workload's VPC, mirroring Python aws_subnets_for_vpc(network_access="private").
// It filters by vpc-id + the PTD network-access tag. When tagNames is non-empty
// (provisioned-VPC case) it instead filters by tag:Name in tagNames. Returns the
// EC2 API result order (the order Python/boto3 used to build the existing state).
func GetWorkloadPrivateSubnetIDs(ctx context.Context, c *Credentials, region, name, vpcID string, tagNames []string) ([]string, error) {
	client := ec2.New(ec2.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	filters := []ec2types.Filter{
		{Name: aws.String("vpc-id"), Values: []string{vpcID}},
	}
	if len(tagNames) > 0 {
		filters = append(filters, ec2types.Filter{Name: aws.String("tag:Name"), Values: tagNames})
	} else {
		filters = append(filters,
			ec2types.Filter{Name: aws.String("tag:Name"), Values: []string{name + "-*"}},
			ec2types.Filter{Name: aws.String("tag-key"), Values: []string{"posit.team/managed-by"}},
			ec2types.Filter{Name: aws.String("tag:posit.team/network-access"), Values: []string{"private"}},
		)
	}

	out, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("describe private subnets for %s: %w", name, err)
	}
	var ids []string
	for _, s := range out.Subnets {
		if s.SubnetId != nil {
			ids = append(ids, *s.SubnetId)
		}
	}
	return ids, nil
}

// ResolveSubnetIDsByName resolves subnet Name-tag values to their real subnet
// IDs within a VPC, mirroring Python's aws_subnets_for_vpc (a describe_subnets
// filtered by vpc-id + tag:Name). Used by the provisioned-VPC adoption path,
// where ptd.yaml's provisioned_vpc.private_subnets lists subnets by Name tag,
// not by ID. The returned IDs preserve the EC2 API result order (the same order
// the Python/boto3 path used to write the existing Pulumi state), so adopting
// RDS subnet groups / FSx subnets does not churn. Returns an error if no
// matching subnet is found (a name typo or wrong VPC would otherwise silently
// drop subnets).
func ResolveSubnetIDsByName(ctx context.Context, c *Credentials, region, vpcID string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	client := ec2.New(ec2.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
			{Name: aws.String("tag:Name"), Values: names},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("describe subnets for vpc %s: %w", vpcID, err)
	}

	var ids []string
	for _, s := range output.Subnets {
		if s.SubnetId != nil {
			ids = append(ids, *s.SubnetId)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no subnets found in vpc %s matching Name tags %v", vpcID, names)
	}
	return ids, nil
}
