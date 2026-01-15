package aws

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSubstituteCloudFormationRefs(t *testing.T) {
	testAccountID := "123456789012"

	t.Run("substitutes AWS::AccountId in Resource", func(t *testing.T) {
		doc := PolicyDocument{
			Version: "2012-10-17",
			Statement: []PolicyStatement{
				{
					Effect: "Allow",
					Action: []string{"s3:GetObject"},
					Resource: []yaml.Node{
						{Kind: yaml.ScalarNode, Value: "arn:aws:s3:::bucket-AWS::AccountId/*"},
					},
				},
			},
		}

		result := doc.SubstituteCloudFormationRefs(testAccountID)

		if result.Statement[0].Resource[0].Value != "arn:aws:s3:::bucket-123456789012/*" {
			t.Errorf("Expected substituted resource, got: %s", result.Statement[0].Resource[0].Value)
		}
	})

	t.Run("substitutes ${AWS::AccountId} in Resource", func(t *testing.T) {
		doc := PolicyDocument{
			Version: "2012-10-17",
			Statement: []PolicyStatement{
				{
					Effect: "Allow",
					Action: []string{"ec2:*"},
					Resource: []yaml.Node{
						{Kind: yaml.ScalarNode, Value: "arn:aws:ec2:*:${AWS::AccountId}:*"},
					},
				},
			},
		}

		result := doc.SubstituteCloudFormationRefs(testAccountID)

		if result.Statement[0].Resource[0].Value != "arn:aws:ec2:*:123456789012:*" {
			t.Errorf("Expected substituted resource, got: %s", result.Statement[0].Resource[0].Value)
		}
	})

	t.Run("substitutes AWS::AccountId in Condition", func(t *testing.T) {
		doc := PolicyDocument{
			Version: "2012-10-17",
			Statement: []PolicyStatement{
				{
					Effect:   "Allow",
					Action:   []string{"s3:*"},
					Resource: []yaml.Node{{Kind: yaml.ScalarNode, Value: "*"}},
					Condition: yaml.Node{
						Kind: yaml.MappingNode,
						Content: []*yaml.Node{
							{Kind: yaml.ScalarNode, Value: "StringLike"},
							{
								Kind: yaml.MappingNode,
								Content: []*yaml.Node{
									{Kind: yaml.ScalarNode, Value: "aws:ResourceAccount"},
									{
										Kind: yaml.SequenceNode,
										Content: []*yaml.Node{
											{Kind: yaml.ScalarNode, Value: "AWS::AccountId"},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		result := doc.SubstituteCloudFormationRefs(testAccountID)

		// Check that the condition was substituted
		if result.Statement[0].Condition.Content[1].Content[1].Content[0].Value != testAccountID {
			t.Errorf("Expected substituted condition value, got: %s",
				result.Statement[0].Condition.Content[1].Content[1].Content[0].Value)
		}
	})

	t.Run("does not modify Action array", func(t *testing.T) {
		doc := PolicyDocument{
			Version: "2012-10-17",
			Statement: []PolicyStatement{
				{
					Effect:   "Allow",
					Action:   []string{"iam:CreateRole", "iam:DeleteRole"},
					Resource: []yaml.Node{{Kind: yaml.ScalarNode, Value: "*"}},
				},
			},
		}

		result := doc.SubstituteCloudFormationRefs(testAccountID)

		if len(result.Statement[0].Action) != 2 {
			t.Errorf("Expected 2 actions, got: %d", len(result.Statement[0].Action))
		}
	})

	t.Run("preserves wildcard resources", func(t *testing.T) {
		doc := PolicyDocument{
			Version: "2012-10-17",
			Statement: []PolicyStatement{
				{
					Effect:   "Allow",
					Action:   []string{"s3:*"},
					Resource: []yaml.Node{{Kind: yaml.ScalarNode, Value: "*"}},
				},
			},
		}

		result := doc.SubstituteCloudFormationRefs(testAccountID)

		if result.Statement[0].Resource[0].Value != "*" {
			t.Errorf("Expected wildcard preserved, got: %s", result.Statement[0].Resource[0].Value)
		}
	})
}

func TestBuildCompleteAdminPolicyDocumentWithSubstitution(t *testing.T) {
	testAccountID := "999888777666"

	doc := BuildCompleteAdminPolicyDocument()
	docForIAM := doc.SubstituteCloudFormationRefs(testAccountID)

	// Marshal to JSON
	policyJSON, err := json.Marshal(docForIAM)
	if err != nil {
		t.Fatalf("Failed to marshal policy: %v", err)
	}

	policyStr := string(policyJSON)

	t.Run("no CloudFormation references remain in JSON", func(t *testing.T) {
		if strings.Contains(policyStr, "AWS::AccountId") {
			t.Error("Policy still contains 'AWS::AccountId' references")
		}
		if strings.Contains(policyStr, "${AWS::AccountId}") {
			t.Error("Policy still contains '${AWS::AccountId}' references")
		}
	})

	t.Run("contains actual account ID", func(t *testing.T) {
		if !strings.Contains(policyStr, testAccountID) {
			t.Error("Policy does not contain the test account ID")
		}
	})

	t.Run("JSON is valid and parseable", func(t *testing.T) {
		// Validate it's valid JSON by unmarshaling into a generic structure
		var testDoc map[string]interface{}
		err := json.Unmarshal(policyJSON, &testDoc)
		if err != nil {
			t.Errorf("Generated JSON is not valid: %v", err)
		}

		// Verify it has the expected top-level structure
		if testDoc["Version"] == nil {
			t.Error("Policy missing Version field")
		}
		if testDoc["Statement"] == nil {
			t.Error("Policy missing Statement field")
		}
	})

	t.Run("policy is within AWS size limits", func(t *testing.T) {
		const maxPolicySize = 6144 // AWS managed policy size limit
		if len(policyJSON) > maxPolicySize {
			t.Errorf("Policy exceeds AWS size limit: %d bytes (limit: %d, over by: %d)",
				len(policyJSON), maxPolicySize, len(policyJSON)-maxPolicySize)
		}
	})
}

func TestSubstituteCloudFormationValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "replaces AWS::AccountId",
			input:    "arn:aws:iam::AWS::AccountId:role/test",
			expected: "arn:aws:iam::123456789012:role/test",
		},
		{
			name:     "replaces ${AWS::AccountId}",
			input:    "arn:aws:ec2:*:${AWS::AccountId}:*",
			expected: "arn:aws:ec2:*:123456789012:*",
		},
		{
			name:     "replaces multiple occurrences",
			input:    "AWS::AccountId-${AWS::AccountId}",
			expected: "123456789012-123456789012",
		},
		{
			name:     "leaves other values unchanged",
			input:    "arn:aws:s3:::my-bucket/*",
			expected: "arn:aws:s3:::my-bucket/*",
		},
		{
			name:     "handles wildcard",
			input:    "*",
			expected: "*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteCloudFormationValue(tt.input, "123456789012")
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}
