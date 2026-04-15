package eject

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShortType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"aws:ec2/vpc:Vpc", "ec2/vpc:Vpc"},
		{"aws:s3/bucket:Bucket", "s3/bucket:Bucket"},
		{"aws:rds/instance:Instance", "rds/instance:Instance"},
		{"azure-native:network:VirtualNetwork", "network:VirtualNetwork"},
		{"kubernetes:helm.sh/v3:Release", "helm.sh/v3:Release"},
		{"nocolon", "nocolon"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, shortType(tt.input))
		})
	}
}

func TestCompactPhysicalID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"AWS ARN", "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-0abc123", "vpc/vpc-0abc123"},
		{"AWS ARN with partition", "arn:aws-us-gov:ec2:us-gov-west-1:123456789012:vpc/vpc-0abc123", "vpc/vpc-0abc123"},
		{"S3 ARN", "arn:aws:s3:::my-bucket", "my-bucket"},
		{"non-ARN ID", "vpc-0abc123", "vpc-0abc123"},
		{"Azure resource ID", "/subscriptions/abc/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet", "/subscriptions/abc/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, compactPhysicalID(tt.input))
		})
	}
}

func TestSplitBacktickSegments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []textSegment
	}{
		{
			"no backticks",
			"plain text",
			[]textSegment{{text: "plain text", isCode: false}},
		},
		{
			"single code segment",
			"run `pulumi login` now",
			[]textSegment{
				{text: "run ", isCode: false},
				{text: "pulumi login", isCode: true},
				{text: " now", isCode: false},
			},
		},
		{
			"code at start",
			"`cmd` does things",
			[]textSegment{
				{text: "cmd", isCode: true},
				{text: " does things", isCode: false},
			},
		},
		{
			"code at end",
			"run `cmd`",
			[]textSegment{
				{text: "run ", isCode: false},
				{text: "cmd", isCode: true},
			},
		},
		{
			"multiple code segments",
			"use `foo` or `bar`",
			[]textSegment{
				{text: "use ", isCode: false},
				{text: "foo", isCode: true},
				{text: " or ", isCode: false},
				{text: "bar", isCode: true},
			},
		},
		{
			"unclosed backtick",
			"text `unclosed",
			[]textSegment{
				{text: "text ", isCode: false},
				{text: "unclosed", isCode: false},
			},
		},
		{
			"empty string",
			"",
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitBacktickSegments(tt.input))
		})
	}
}
