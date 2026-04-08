package eject

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testStateJSON = `{
	"version": 3,
	"checkpoint": {
		"latest": {
			"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
			"resources": [
				{
					"type": "pulumi:pulumi:Stack",
					"urn": "urn:pulumi:prod::ptd-aws-workload-persistent::pulumi:pulumi:Stack::ptd-aws-workload-persistent-prod"
				},
				{
					"type": "pulumi:providers:aws",
					"urn": "urn:pulumi:prod::ptd-aws-workload-persistent::pulumi:providers:aws::default",
					"id": "us-east-1"
				},
				{
					"type": "aws:ec2/vpc:Vpc",
					"urn": "urn:pulumi:prod::ptd-aws-workload-persistent::aws:ec2/vpc:Vpc::main-vpc",
					"id": "vpc-0abc123",
					"provider": "urn:pulumi:prod::ptd-aws-workload-persistent::pulumi:providers:aws::default::id",
					"outputs": {
						"id": "vpc-0abc123",
						"arn": "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-0abc123",
						"cidrBlock": "10.0.0.0/16"
					}
				},
				{
					"type": "aws:s3/bucket:Bucket",
					"urn": "urn:pulumi:prod::ptd-aws-workload-persistent::aws:s3/bucket:Bucket::data-bucket",
					"id": "my-data-bucket",
					"provider": "urn:pulumi:prod::ptd-aws-workload-persistent::pulumi:providers:aws::default::id",
					"outputs": {
						"bucket": "my-data-bucket",
						"arn": "arn:aws:s3:::my-data-bucket"
					}
				},
				{
					"type": "aws:iam/role:Role",
					"urn": "urn:pulumi:prod::ptd-aws-workload-persistent::aws:iam/role:Role::node-role",
					"id": "node-role-name",
					"provider": "urn:pulumi:prod::ptd-aws-workload-persistent::pulumi:providers:aws::default::id",
					"outputs": {
						"arn": "arn:aws:iam::123456789012:role/node-role-name",
						"name": "node-role-name"
					}
				}
			]
		}
	}
}`

func TestParseResourceInventory(t *testing.T) {
	entries, err := ParseResourceInventory(
		[]byte(testStateJSON),
		".pulumi/stacks/ptd-aws-workload-persistent/prod.json",
	)

	require.NoError(t, err)
	assert.Len(t, entries, 3) // excludes pulumi:pulumi: and pulumi:providers:

	vpc := entries[0]
	assert.Equal(t, "aws:ec2/vpc:Vpc", vpc.Type)
	assert.Equal(t, "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-0abc123", vpc.PhysicalID) // prefers ARN
	assert.Equal(t, "aws", vpc.Provider)
	assert.Equal(t, "ptd-aws-workload-persistent/prod", vpc.Stack)
	assert.Equal(t, "persistent", vpc.Purpose)

	bucket := entries[1]
	assert.Equal(t, "arn:aws:s3:::my-data-bucket", bucket.PhysicalID)

	role := entries[2]
	assert.Equal(t, "arn:aws:iam::123456789012:role/node-role-name", role.PhysicalID)
}

func TestParseResourceInventory_NoARNFallsBackToID(t *testing.T) {
	stateJSON := `{
		"version": 3,
		"checkpoint": {
			"latest": {
				"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
				"resources": [
					{
						"type": "aws:ec2/securityGroup:SecurityGroup",
						"urn": "urn:pulumi:prod::proj::aws:ec2/securityGroup:SecurityGroup::sg",
						"id": "sg-0abc123",
						"provider": "urn:pulumi:prod::proj::pulumi:providers:aws::default::id",
						"outputs": {
							"id": "sg-0abc123",
							"name": "my-sg"
						}
					}
				]
			}
		}
	}`

	entries, err := ParseResourceInventory([]byte(stateJSON), ".pulumi/stacks/proj/prod.json")

	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "sg-0abc123", entries[0].PhysicalID) // falls back to outputs.id
}

func TestParseResourceInventory_NoOutputsFallsBackToTopLevelID(t *testing.T) {
	stateJSON := `{
		"version": 3,
		"checkpoint": {
			"latest": {
				"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
				"resources": [
					{
						"type": "random:index/randomPassword:RandomPassword",
						"urn": "urn:pulumi:prod::proj::random:index/randomPassword:RandomPassword::pw",
						"id": "none",
						"provider": "urn:pulumi:prod::proj::pulumi:providers:random::default::id"
					}
				]
			}
		}
	}`

	entries, err := ParseResourceInventory([]byte(stateJSON), ".pulumi/stacks/proj/prod.json")

	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "none", entries[0].PhysicalID)
}

func TestParseResourceInventory_MalformedJSON(t *testing.T) {
	_, err := ParseResourceInventory([]byte(`{invalid`), ".pulumi/stacks/proj/prod.json")
	assert.Error(t, err)
}

func TestParseResourceInventory_EmptyResources(t *testing.T) {
	stateJSON := `{
		"version": 3,
		"checkpoint": {
			"latest": {
				"manifest": {"time": "2025-01-15T10:30:00Z", "version": "3.100.0"},
				"resources": []
			}
		}
	}`

	entries, err := ParseResourceInventory([]byte(stateJSON), ".pulumi/stacks/proj/prod.json")

	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestParseResourceInventory_OutputIsValidJSON(t *testing.T) {
	entries, err := ParseResourceInventory(
		[]byte(testStateJSON),
		".pulumi/stacks/ptd-aws-workload-persistent/prod.json",
	)
	require.NoError(t, err)

	data, err := json.MarshalIndent(entries, "", "  ")
	require.NoError(t, err)

	var roundTrip []ResourceInventoryEntry
	require.NoError(t, json.Unmarshal(data, &roundTrip))
	assert.Equal(t, entries, roundTrip)
}

func TestProviderName(t *testing.T) {
	tests := []struct {
		urn  string
		want string
	}{
		{"urn:pulumi:prod::proj::pulumi:providers:aws::default::id", "aws"},
		{"urn:pulumi:prod::proj::pulumi:providers:azure-native::default::id", "azure-native"},
		{"urn:pulumi:prod::proj::pulumi:providers:random::default::id", "random"},
		{"", ""},
		{"garbage", ""},
	}

	for _, tt := range tests {
		t.Run(tt.urn, func(t *testing.T) {
			assert.Equal(t, tt.want, providerName(tt.urn))
		})
	}
}

func TestStackNameFromKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{".pulumi/stacks/ptd-aws-workload-persistent/prod.json", "ptd-aws-workload-persistent/prod"},
		{".pulumi/stacks/ptd-aws-workload-eks/staging.json", "ptd-aws-workload-eks/staging"},
		{"short.json", "short.json"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.want, stackNameFromKey(tt.key))
		})
	}
}

func TestPurposeFromKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{".pulumi/stacks/ptd-aws-workload-persistent/prod.json", "persistent"},
		{".pulumi/stacks/ptd-aws-workload-eks/staging.json", "eks"},
		{".pulumi/stacks/ptd-aws-workload-postgres-config/prod.json", "postgres-config"},
		{".pulumi/stacks/short/prod.json", "short"},
		{"no-slashes", ""},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.want, purposeFromKey(tt.key))
		})
	}
}
