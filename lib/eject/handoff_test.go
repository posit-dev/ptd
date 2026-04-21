package eject

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCategorizeResource(t *testing.T) {
	tests := []struct {
		resourceType string
		want         string
	}{
		// Network
		{"aws:ec2/vpc:Vpc", CategoryNetwork},
		{"aws:ec2/subnet:Subnet", CategoryNetwork},
		{"aws:ec2/securityGroup:SecurityGroup", CategoryNetwork},
		{"aws:elasticloadbalancingv2/loadBalancer:LoadBalancer", CategoryNetwork},
		{"aws:ec2/natGateway:NatGateway", CategoryNetwork},
		{"aws:ec2/internetGateway:InternetGateway", CategoryNetwork},
		{"aws:ec2/routeTable:RouteTable", CategoryNetwork},
		{"aws:ec2/eip:Eip", CategoryNetwork},
		{"azure-native:network:VirtualNetwork", CategoryNetwork},
		{"azure-native:network:NetworkSecurityGroup", CategoryNetwork},
		{"azure-native:network:PublicIPAddress", CategoryNetwork},

		// Database
		{"aws:rds/instance:Instance", CategoryDatabase},
		{"aws:rds/cluster:Cluster", CategoryDatabase},
		{"aws:rds/subnetGroup:SubnetGroup", CategoryDatabase},
		{"aws:rds/parameterGroup:ParameterGroup", CategoryDatabase},
		{"azure-native:dbforpostgresql:FlexibleServer", CategoryDatabase},

		// Storage
		{"aws:s3/bucket:Bucket", CategoryStorage},
		{"aws:fsx/lustreFileSystem:LustreFileSystem", CategoryStorage},
		{"azure-native:storage:StorageAccount", CategoryStorage},
		{"azure-native:storage:BlobContainer", CategoryStorage},
		{"azure-native:netapp:Volume", CategoryStorage},

		// DNS
		{"aws:route53/zone:Zone", CategoryDNS},
		{"aws:route53/record:Record", CategoryDNS},
		{"azure-native:network:DnsZone", CategoryDNS},

		// IAM
		{"aws:iam/role:Role", CategoryIAM},
		{"aws:iam/policy:Policy", CategoryIAM},
		{"aws:iam/openIdConnectProvider:OpenIdConnectProvider", CategoryIAM},
		{"azure-native:managedidentity:UserAssignedIdentity", CategoryIAM},
		{"azure-native:authorization:RoleAssignment", CategoryIAM},

		// Other
		{"aws:eks/cluster:Cluster", CategoryOther},
		{"kubernetes:helm.sh/v3:Release", CategoryOther},
		{"random:index/randomPassword:RandomPassword", CategoryOther},
	}

	for _, tt := range tests {
		t.Run(tt.resourceType, func(t *testing.T) {
			assert.Equal(t, tt.want, CategorizeResource(tt.resourceType))
		})
	}
}

func TestResourcesByCategory(t *testing.T) {
	resources := []ResourceInventoryEntry{
		{Type: "aws:ec2/vpc:Vpc", PhysicalID: "vpc-1"},
		{Type: "aws:s3/bucket:Bucket", PhysicalID: "bucket-1"},
		{Type: "aws:ec2/subnet:Subnet", PhysicalID: "subnet-1"},
		{Type: "aws:rds/instance:Instance", PhysicalID: "db-1"},
		{Type: "aws:eks/cluster:Cluster", PhysicalID: "eks-1"},
	}

	result := ResourcesByCategory(resources)

	assert.Len(t, result[CategoryNetwork], 2)
	assert.Len(t, result[CategoryStorage], 1)
	assert.Len(t, result[CategoryDatabase], 1)
	assert.Len(t, result[CategoryOther], 1)
	assert.Empty(t, result[CategoryDNS])
	assert.Empty(t, result[CategoryIAM])
}
