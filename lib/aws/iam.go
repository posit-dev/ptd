package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/posit-dev/ptd/lib/consts"
	"gopkg.in/yaml.v3"
)

const (
	WILDCARD = "*"
)

type Action struct {
	Action                     string
	SupportsResourceLimit      bool // is the action able to be limited by the aws:ResourceAccount value of the request
	SupportsManagedByCondition bool // is the action able to be limited by the aws:ResourceTag/posit.team/managed-by value of the request
}

func (a Action) MarshalYAML() (interface{}, error) {
	return string(a.Action), nil
}

func (a Action) SupportsGlobalResourceCondition() bool {
	// not the full list, but the most likely ones we are to encounter
	// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_policies_condition-keys.html#condition-keys-resourceaccount
	specialActions := []string{
		"route53:AssociateVPCWithHostedZone",
		"route53:CreateVPCAssociationAuthorization",
		"route53:DeleteVPCAssociationAuthorization",
		"route53:DisassociateVPCFromHostedZone",
		"route53:ListHostedZonesByVPC",
		"ec2:AcceptTransitGatewayPeeringAttachment",
		"ec2:AcceptVpcEndpointConnections",
		"ec2:AcceptVpcPeeringConnection",
		"ec2:CopyImage",
		"ec2:CopySnapshot",
		"ec2:CreateTransitGatewayPeeringAttachment",
		"ec2:CreateVolume",
		"ec2:CreateVpcEndpoint",
		"ec2:CreateVpcPeeringConnection",
		"ec2:DeleteTransitGatewayPeeringAttachment",
		"ec2:DeleteVpcPeeringConnection",
		"ec2:RejectTransitGatewayPeeringAttachment",
		"ec2:RejectVpcEndpointConnections",
		"ec2:RejectVpcPeeringConnection",
		"events:PutEvents",
		"guardduty:AcceptAdministratorInvitation",
		"es:AcceptInboundConnection",
		"securityhub:AcceptAdministratorInvitation",
		"macie2:AcceptInvitation",
	}

	return !slices.Contains(specialActions, a.Action)
}

func (a Action) RequiresRequestTagKeys() bool {
	actions := []string{
		"acm:RequestCertificate",
		"acm:AddTagsToCertificate",
		"acm:RemoveTagsFromCertificate",
		"kms:TagResource",
		"kms:UntagResource",
		"kms:CreateKey",
		"secretsmanager:TagResource",
		"secretsmanager:UntagResource",
		"secretsmanager:CreateSecret",
	}

	return slices.Contains(actions, a.Action)
}

type ManagedPolicy struct {
	ManagedPolicyName string         `yaml:"ManagedPolicyName"`
	PolicyDocument    PolicyDocument `yaml:"PolicyDocument"`
}

type PolicyRef string

func (p PolicyRef) MarshalYAML() (interface{}, error) {
	return yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!Ref",
		Value: string(p),
	}, nil
}

type Role struct {
	RoleName                 string              `yaml:"RoleName"`
	ManagedPolicyArns        []PolicyRef         `yaml:"ManagedPolicyArns"`
	RolePolicyList           []Policy            `yaml:"Policies"`
	AssumeRolePolicyDocument PolicyDocument      `yaml:"AssumeRolePolicyDocument"`
	PermissionsBoundary      PolicyRef           `yaml:"PermissionsBoundary,omitempty"`
	Path                     string              `yaml:"Path"`
	Tags                     []map[string]string `yaml:"Tags"`
}

type Policy struct {
	PolicyName     string         `yaml:"PolicyName"`
	PolicyDocument PolicyDocument `yaml:"PolicyDocument"`
}

type PolicyDocument struct {
	Version   string            `yaml:"Version"`
	Statement []PolicyStatement `yaml:"Statement"`
}

func NewAdminPolicyDocument() PolicyDocument {
	return PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{
			{
				Sid:    "WildcardActions",
				Effect: "Allow",
				Action: []string{},
				Resource: []yaml.Node{
					{Kind: yaml.ScalarNode, Value: "*"},
				},
			},
			{
				Sid:    "ResourceConditionLimitingActions",
				Effect: "Allow",
				Action: []string{},
				Resource: []yaml.Node{
					{Kind: yaml.ScalarNode, Value: "*"},
				},
				Condition: *resourceAccountCondition(),
			},
			{
				Sid:    "ResourceAccountAndTagConditionLimitingActions",
				Effect: "Allow",
				Action: []string{},
				Resource: []yaml.Node{
					{Kind: yaml.ScalarNode, Value: "*"},
				},
				Condition: resourceAccountAndTagCondition(),
			},
			{
				Sid:    "ResourceAccountAndTagWriteConditionLimitingActions",
				Effect: "Allow",
				Action: []string{},
				Resource: []yaml.Node{
					{Kind: yaml.ScalarNode, Value: "*"},
				},
				Condition: *writeRequiredTagsCondition(),
			},
			{
				Sid:      "Ec2NonResourceConditionActions",
				Effect:   "Allow",
				Action:   []string{},
				Resource: []yaml.Node{accountWildcardArn("ec2", "", "")},
			},
			{
				Sid:    "Route53NonResourceConditionActions",
				Effect: "Allow",
				Action: []string{},
				Resource: []yaml.Node{
					{
						Kind:  yaml.ScalarNode,
						Value: "arn:aws:route53:::*",
					},
				},
			},
		},
	}
}

func (pd *PolicyDocument) GetStatementBySid(sid string) *PolicyStatement {
	for i := range pd.Statement {
		if pd.Statement[i].Sid == sid {
			return &pd.Statement[i]
		}
	}
	return nil
}

func (pd *PolicyDocument) AddActions(actions []Action) {
	for _, a := range actions {

		// if the action supports no limitations, add it to the wildcard statement
		if !a.SupportsResourceLimit {
			pd.GetStatementBySid("WildcardActions").AddAction(a.Action)
			continue
		}

		// action supports resource condition and managed by tag condition
		if a.SupportsResourceLimit && a.SupportsManagedByCondition && a.SupportsGlobalResourceCondition() {
			if a.RequiresRequestTagKeys() {
				// if it's a known write action it requires specific tags in req.
				pd.GetStatementBySid("ResourceAccountAndTagWriteConditionLimitingActions").AddAction(a.Action)
			} else {
				// otherwise the normal managed by tag condition is used
				pd.GetStatementBySid("ResourceAccountAndTagConditionLimitingActions").AddAction(a.Action)
			}
			continue
		}

		// if the action supports resource condition, add it to the resource condition limiting statement
		if a.SupportsResourceLimit && a.SupportsGlobalResourceCondition() {
			pd.GetStatementBySid("ResourceConditionLimitingActions").AddAction(a.Action)
			continue
		}

		// if the action supports resource condition but not global resource condition, add it to the service specific statement
		if a.SupportsResourceLimit && !a.SupportsGlobalResourceCondition() {
			if strings.HasPrefix(a.Action, "ec2:") {
				pd.GetStatementBySid("Ec2NonResourceConditionActions").AddAction(a.Action)
			}
			if strings.HasPrefix(a.Action, "route53:") {
				pd.GetStatementBySid("Route53NonResourceConditionActions").AddAction(a.Action)
			}
			continue
		}

		// we should not reach here
		panic(fmt.Sprintf("Action does not appear valid: %s", a.Action))
	}
}

func (pd *PolicyDocument) AddStatements(statements []PolicyStatement) {
	// check if the new statements adds non-resource based actions.
	// if so add them to the wildcard statement instead
	// this is to minimize the number of statements attached to the policy
	for i := len(statements) - 1; i >= 0; i-- {
		s := statements[i]
		if reflect.DeepEqual(s.Resource, []yaml.Node{{Kind: yaml.ScalarNode, Value: "*"}}) && reflect.DeepEqual(s.Condition, yaml.Node{}) {
			for _, a := range s.Action {
				pd.Statement[0].AddAction(a)
			}
		} else {
			pd.Statement = append(pd.Statement, s)
		}
	}
}

// SubstituteCloudFormationRefs returns a new PolicyDocument with CloudFormation
// intrinsic function references replaced with actual values.
// This is needed when using the policy with direct IAM API calls (not CloudFormation).
//
// Substitutions performed:
//   - "AWS::AccountId" -> accountID (from !Ref AWS::AccountId)
//   - "${AWS::AccountId}" -> accountID (from !Sub templates)
func (pd PolicyDocument) SubstituteCloudFormationRefs(accountID string) PolicyDocument {
	// Create a deep copy with substitutions
	newDoc := PolicyDocument{
		Version:   pd.Version,
		Statement: make([]PolicyStatement, len(pd.Statement)),
	}

	for i, stmt := range pd.Statement {
		newStmt := PolicyStatement{
			Effect:    stmt.Effect,
			Action:    make([]string, len(stmt.Action)),
			Sid:       stmt.Sid,
			Principal: stmt.Principal,
			Resource:  make([]yaml.Node, len(stmt.Resource)),
		}

		// Copy actions (no substitution needed)
		copy(newStmt.Action, stmt.Action)

		// Substitute in Resource values
		for j, node := range stmt.Resource {
			newNode := node
			newNode.Value = substituteCloudFormationValue(node.Value, accountID)
			newStmt.Resource[j] = newNode
		}

		// Substitute in Condition values
		if stmt.Condition.Kind != 0 {
			newStmt.Condition = substituteConditionNode(stmt.Condition, accountID)
		}

		newDoc.Statement[i] = newStmt
	}

	return newDoc
}

// substituteCloudFormationValue replaces CloudFormation intrinsic function references
// with actual values in a string.
func substituteCloudFormationValue(value string, accountID string) string {
	// Replace !Sub references first (longer pattern, appear as "${AWS::AccountId}" in JSON)
	value = strings.ReplaceAll(value, "${AWS::AccountId}", accountID)
	// Then replace !Ref AWS::AccountId references (appear as literal "AWS::AccountId" in JSON)
	value = strings.ReplaceAll(value, "AWS::AccountId", accountID)
	return value
}

// substituteConditionNode recursively substitutes CloudFormation references in a YAML node tree.
func substituteConditionNode(node yaml.Node, accountID string) yaml.Node {
	// Create a copy of the node
	newNode := node

	// Recursively process child nodes
	if node.Content != nil {
		newNode.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			childCopy := substituteConditionNode(*child, accountID)
			newNode.Content[i] = &childCopy
		}
	}

	// Substitute value if present
	if node.Value != "" {
		newNode.Value = substituteCloudFormationValue(node.Value, accountID)
	}

	return newNode
}

// PolicyStatement defines a statement in a policy document.
type PolicyStatement struct {
	Effect    string      `yaml:"Effect"`
	Action    []string    `yaml:"Action"`
	Sid       string      `yaml:"Sid,omitempty"`
	Principal Principal   `yaml:"Principal,omitempty" json:"Principal,omitempty"`
	Resource  []yaml.Node `yaml:"Resource,omitempty" json:"Resource,omitempty"`
	Condition yaml.Node   `yaml:"Condition,omitempty" json:"Condition,omitempty"`
}

func (ps *PolicyStatement) AddAction(a string) {
	if !slices.Contains(ps.Action, a) {
		ps.Action = append(ps.Action, a)
	}
}

type Principal map[string]map[string]string

// MarshalJSON implements custom JSON marshaling for PolicyStatement.
// This converts yaml.Node fields (Resource, Condition) to simple types for the IAM API,
// while still preserving yaml.Node for YAML/CloudFormation template generation.
func (ps PolicyStatement) MarshalJSON() ([]byte, error) {
	// Use an anonymous struct to avoid infinite recursion
	type simpleStatement struct {
		Effect    string                 `json:"Effect"`
		Action    []string               `json:"Action"`
		Sid       string                 `json:"Sid,omitempty"`
		Principal Principal              `json:"Principal,omitempty"`
		Resource  []string               `json:"Resource,omitempty"`
		Condition map[string]interface{} `json:"Condition,omitempty"`
	}

	simple := simpleStatement{
		Effect:    ps.Effect,
		Action:    ps.Action,
		Sid:       ps.Sid,
		Principal: ps.Principal,
	}

	// Convert Resource yaml.Nodes to strings
	if len(ps.Resource) > 0 {
		simple.Resource = make([]string, 0, len(ps.Resource))
		for _, resourceNode := range ps.Resource {
			if resourceNode.Value != "" {
				simple.Resource = append(simple.Resource, resourceNode.Value)
			}
		}
	}

	// Convert Condition yaml.Node to map
	if ps.Condition.Kind != 0 && ps.Condition.Content != nil {
		conditionMap := make(map[string]interface{})
		if err := ps.Condition.Decode(&conditionMap); err == nil {
			simple.Condition = conditionMap
		}
	}

	return json.Marshal(simple)
}

func NewRole(name string) Role {
	return Role{
		RoleName: name,
		Path:     "/",
	}
}

func (r *Role) AddPolicy(p Policy) {
	r.RolePolicyList = append(r.RolePolicyList, p)
}

func resourceAccountCondition() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{
				Kind:  yaml.ScalarNode,
				Value: "StringLike",
			},
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					{
						Kind:  yaml.ScalarNode,
						Value: "aws:ResourceAccount",
					},
					{
						Kind: yaml.SequenceNode,
						Content: []*yaml.Node{
							{
								Kind:  yaml.ScalarNode,
								Tag:   "!Ref",
								Value: "AWS::AccountId",
							},
						},
					},
				},
			},
		},
	}
}

func resourceAccountAndTagCondition() yaml.Node {
	node := yaml.Node{
		Kind:    yaml.MappingNode,
		Content: []*yaml.Node{},
	}
	node.Content = append(node.Content, managedByCondition().Content...)
	return node
}

func accountWildcardArn(service string, region string, resource string) yaml.Node {
	if resource == "" {
		resource = WILDCARD
	}
	if region == "" && !slices.Contains([]string{"route53", "iam"}, service) { // route53 and iam do not allow region in the ARN
		region = WILDCARD
	}

	s := strings.Join([]string{"arn", "aws", service, region, "${AWS::AccountId}", resource}, ":")
	if service == "logs" {
		s = fmt.Sprintf("%s:*", s)
	}

	return yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!Sub",
		Value: s,
		Style: yaml.SingleQuotedStyle,
	}
}

func writeRequiredTagsCondition() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{
				Kind:  yaml.ScalarNode,
				Value: "ForAnyValue:StringEquals",
			},
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					{
						Kind:  yaml.ScalarNode,
						Value: "aws:TagKeys",
					},
					{
						Kind: yaml.SequenceNode,
						Content: []*yaml.Node{
							{
								Kind:  yaml.ScalarNode,
								Value: consts.POSIT_TEAM_MANAGED_BY_TAG,
							},
						},
					},
				},
			},
			{
				Kind:  yaml.ScalarNode,
				Value: "StringLike",
			},
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					// limit resources to those in this account (copied from above as this node uses two stringlike clauses
					{
						Kind:  yaml.ScalarNode,
						Value: "aws:ResourceAccount",
					},
					{
						Kind: yaml.SequenceNode,
						Content: []*yaml.Node{
							{
								Kind:  yaml.ScalarNode,
								Tag:   "!Ref",
								Value: "AWS::AccountId",
							},
						},
					},
				},
			},
		},
	}
}

func managedByCondition() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{
				Kind:  yaml.ScalarNode,
				Value: "StringLike",
			},
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					// limit resources to those with the managed by tag
					{
						Kind:  yaml.ScalarNode,
						Value: fmt.Sprintf("aws:ResourceTag/%s", consts.POSIT_TEAM_MANAGED_BY_TAG),
					},
					{
						Kind:  yaml.ScalarNode,
						Value: WILDCARD,
					},
					// limit resources to those in this account (copied from above as this node uses two stringlike clauses
					{
						Kind:  yaml.ScalarNode,
						Value: "aws:ResourceAccount",
					},
					{
						Kind: yaml.SequenceNode,
						Content: []*yaml.Node{
							{
								Kind:  yaml.ScalarNode,
								Tag:   "!Ref",
								Value: "AWS::AccountId",
							},
						},
					},
				},
			},
		},
	}
}

// CreateAdminPolicyIfNotExists creates the PositTeamDedicatedAdmin IAM policy if it doesn't already exist.
// This policy is used as both a permissions boundary and attached managed policy for PTD roles.
// It's particularly important for workloads using custom_role where the standard admin setup
// doesn't automatically create the policy.
//
// Returns nil if the policy already exists or was successfully created.
// Returns an error if:
//   - The custom role lacks permission to check for or create policies (AccessDenied)
//   - Network or transient AWS API errors occur (AWS SDK handles retries automatically)
//   - Policy document marshaling fails
func CreateAdminPolicyIfNotExists(ctx context.Context, c *Credentials, region string, accountID string, policyName string) error {
	client := iam.NewFromConfig(aws.Config{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	// Check if policy already exists
	policyArn := fmt.Sprintf("arn:aws:iam::%s:policy/%s", accountID, policyName)
	_, err := client.GetPolicy(ctx, &iam.GetPolicyInput{
		PolicyArn: aws.String(policyArn),
	})

	if err == nil {
		// Policy already exists
		return nil
	}

	// Check if the error is because policy doesn't exist (expected) vs permission/other errors
	var noSuchEntity *types.NoSuchEntityException
	if !errors.As(err, &noSuchEntity) {
		// This is NOT a "policy doesn't exist" error - it's something else
		// Could be permission denied, network error, etc.
		return fmt.Errorf("failed to check if IAM policy %s exists: %w", policyName, err)
	}

	// Policy doesn't exist - proceed to create it
	doc := BuildCompleteAdminPolicyDocument()

	// Substitute CloudFormation references with actual account ID for IAM API
	// This converts things like "AWS::AccountId" and "${AWS::AccountId}" to the real account ID
	docForIAM := doc.SubstituteCloudFormationRefs(accountID)

	// Marshal to JSON for AWS API
	// PolicyStatement.MarshalJSON() handles converting yaml.Node to simple types
	policyDocJSON, err := json.Marshal(docForIAM)
	if err != nil {
		return fmt.Errorf("failed to marshal policy document for %s: %w", policyName, err)
	}

	// Create the policy
	_, err = client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String(policyName),
		PolicyDocument: aws.String(string(policyDocJSON)),
		Description:    aws.String("Admin policy for Posit Team Dedicated - created by bootstrap"),
		Tags: []types.Tag{
			{
				Key:   aws.String(consts.POSIT_TEAM_MANAGED_BY_TAG),
				Value: aws.String("ptd-bootstrap"),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create IAM policy %s (check if custom role has iam:CreatePolicy permission): %w", policyName, err)
	}

	return nil
}

// IamPermissionBoundaryCondition returns a CloudFormation-compatible YAML condition node
// that enforces the PositTeamDedicatedAdmin policy as a permissions boundary.
// This condition is used in IAM policy statements to ensure that any IAM role or user
// created by PTD must have the admin policy set as their permissions boundary.
//
// The returned YAML node structure represents:
//
//	Condition:
//	  StringEquals:
//	    iam:PermissionsBoundary:
//	      - !Sub 'arn:aws:iam::${AWS::AccountId}:policy/PositTeamDedicatedAdmin'
//
// This prevents privilege escalation by ensuring created principals cannot exceed
// the permissions defined in the admin policy.
func IamPermissionBoundaryCondition() yaml.Node {
	policyArn := fmt.Sprintf("arn:aws:iam::${AWS::AccountId}:policy/%s", consts.PositTeamDedicatedAdminPolicyName)
	return yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{
				Kind:  yaml.ScalarNode,
				Value: "StringEquals",
			},
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					{
						Kind:  yaml.ScalarNode,
						Value: "iam:PermissionsBoundary",
					},
					{
						Kind: yaml.SequenceNode,
						Content: []*yaml.Node{
							{
								Kind:  yaml.ScalarNode,
								Tag:   "!Sub",
								Value: policyArn,
								Style: yaml.SingleQuotedStyle,
							},
						},
					},
				},
			},
		},
	}
}

// S3ResourceAccountCondition returns the condition for S3 resource account
func S3ResourceAccountCondition() yaml.Node {
	return yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{
				Kind:  yaml.ScalarNode,
				Value: "StringEquals",
			},
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					{
						Kind:  yaml.ScalarNode,
						Value: "s3:ResourceAccount",
					},
					{
						Kind: yaml.SequenceNode,
						Content: []*yaml.Node{
							{
								Kind:  yaml.ScalarNode,
								Tag:   "!Ref",
								Value: "AWS::AccountId",
							},
						},
					},
				},
			},
		},
	}
}

// IamResourceWildcards returns the IAM resource wildcards
func IamResourceWildcards() []yaml.Node {
	return []yaml.Node{
		{Kind: yaml.ScalarNode, Value: "arn:aws:iam::*:role/*.posit.team"},
		{Kind: yaml.ScalarNode, Value: "arn:aws:iam::*:policy/*.posit.team"},
		{Kind: yaml.ScalarNode, Value: "arn:aws:iam::*:role/aws-service-role/*"},
		{Kind: yaml.ScalarNode, Value: "arn:aws:iam::*:policy/aws-service-role/*"},
		{Kind: yaml.ScalarNode, Value: "arn:aws:iam::*:instance-profile/*.posit.team"},
		{Kind: yaml.ScalarNode, Value: "arn:aws:iam::*:saml-provider/*.posit.team"},
		{Kind: yaml.ScalarNode, Value: "arn:aws:iam::*:oidc-provider/*"},
	}
}

// SelfConstrainingStatements returns self-constraining policy statements
func SelfConstrainingStatements() []PolicyStatement {
	return []PolicyStatement{
		{
			Effect: "Allow",
			Action: []string{
				"iam:CreateRole",
				"iam:DeleteRole",
				"iam:AttachRolePolicy",
				"iam:DetachRolePolicy",
				"iam:DeleteRolePolicy"},
			Condition: IamPermissionBoundaryCondition(),
			Resource:  IamResourceWildcards(),
		},
		{
			Effect: "Deny",
			Action: []string{"iam:CreatePolicyVersion"},
			Resource: []yaml.Node{
				{
					Kind:  yaml.ScalarNode,
					Tag:   "!Sub",
					Value: "arn:aws:iam::${AWS::AccountId}:policy/PositTeamDedicatedAdmin",
					Style: yaml.SingleQuotedStyle,
				},
			},
		},
	}
}

// BillingActions returns billing-related actions
func BillingActions() []Action {
	return []Action{
		{"ce:Get*", false, false},
		{"ce:Describe*", false, false},
		{"ce:List*", false, false},
		{"account:GetAccountInformation", false, false},
		{"billing:Get*", false, false},
		{"payments:List*", false, false},
		{"payments:Get*", false, false},
		{"tax:List*", false, false},
		{"tax:Get*", false, false},
		{"consolidatedbilling:Get*", false, false},
		{"consolidatedbilling:List*", false, false},
		{"invoicing:List*", false, false},
		{"invoicing:Get*", false, false},
		{"cur:Get*", false, false},
		{"cur:Validate*", false, false},
		{"freetier:Get*", false, false},
	}
}

// BedrockActions returns bedrock-related actions
func BedrockActions() []Action {
	return []Action{
		{"bedrock:*", false, false},
	}
}

// AcmActions returns ACM-related actions
func AcmActions() []Action {
	return []Action{
		{"acm:ListCertificates", false, false},
		{"acm:RequestCertificate", true, true},
		{"acm:*tags*", true, true},
		{"acm:DescribeCertificate", true, false},
		{"acm:DeleteCertificate", true, false},
	}
}

// CloudwatchActions returns CloudWatch-related actions
func CloudwatchActions() []Action {
	return []Action{
		{"cloudwatch:Get*", false, false},
		{"cloudwatch:List*", false, false},
		{"cloudwatch:Describe*", false, false},
		{"cloudwatch:PutMetricData", true, false},
	}
}

// ComputeOptimizerActions returns Compute Optimizer-related actions
func ComputeOptimizerActions() []Action {
	return []Action{
		{"compute-optimizer:Get*", false, false},
	}
}

// DirectoryServiceActions returns Directory Service-related actions
func DirectoryServiceActions() []Action {
	return []Action{
		{"ds:DescribeDirectories", false, false},
	}
}

// Wafv2Actions returns WAFv2-related actions
func Wafv2Actions() []Action {
	return []Action{
		{"wafv2:GetWebACL*", true, false},
		{"wafv2:AssociateWebACL", true, false},
		{"wafv2:DisassociateWebACL", true, false},
		{"waf-regional:GetWebACL*", true, false},
	}
}

// TagActions returns tag-related actions
func TagActions() []Action {
	return []Action{
		{"tag:GetResources", false, false},
		{"tag:*tag*", false, false},
	}
}

// StsActions returns STS-related actions
func StsActions() []Action {
	return []Action{
		{"sts:GetCallerIdentity", false, false},
		{"sts:DecodeAuthorizationMessage", false, false},
	}
}

// SsmActions returns SSM-related actions
func SsmActions() []Action {
	return []Action{
		{"ssm:Describe*", false, false},
		{"ssm:Get*", false, false},
		{"ssm:List*", false, false},
		{"ssm:StartSession", false, false},
		{"ssm:SendCommand", false, false},
		{"ssmmessages:Create*", false, false},
		{"ssmmessages:Open*", false, false},
		{"ec2messages:*", false, false},
		{"ssm:Put*", true, false},
		{"ssm:Update*", true, false},
		{"ssm:TerminateSession", true, false},
	}
}

// ShieldActions returns Shield-related actions
func ShieldActions() []Action {
	return []Action{
		{"shield:Describe*", false, false},
		{"shield:Get*", false, false},
		{"shield:CreateProtection", true, false},
		{"shield:DeleteProtection", true, false},
	}
}

// ResourceExplorerActions returns Resource Explorer-related actions
func ResourceExplorerActions() []Action {
	return []Action{
		{"resource-explorer-2:ListIndexes", false, false},
		{"resource-explorer-2:Search", false, false},
	}
}

// Route53Actions returns Route53-related actions
func Route53Actions() []Action {
	return []Action{
		{"route53:List*", false, false},
		{"route53:Get*", false, false},
		{"route53:Change*", true, false},
		{"route53:CreateHostedZone", true, false},
		{"route53:AssociateVPCWithHostedZone", true, false},
		{"route53:UpdateHostedZoneComment", true, false},
	}
}

// RdsActions returns RDS-related actions
func RdsActions() []Action {
	return []Action{
		{"rds:Describe*", false, false},
		{"rds:Create*", true, false},
		{"rds:Delete*", true, false},
		{"rds:Modify*", true, false},
		{"rds:*tag*", true, false},
	}
}

// S3Statements returns S3-related policy statements
func S3Statements() []PolicyStatement {
	return []PolicyStatement{
		{
			Effect: "Allow",
			Action: []string{
				"s3:CreateBucket",
				"s3:ListBucket*",
				"s3:Get*",
				"s3:Put*",
				"s3:Delete*"},
			Resource: []yaml.Node{
				{Kind: yaml.ScalarNode, Value: "arn:aws:s3:::posit-*"},
				{Kind: yaml.ScalarNode, Value: "arn:aws:s3:::posit-*/*"},
				{Kind: yaml.ScalarNode, Value: "arn:aws:s3:::ptd-*"},
				{Kind: yaml.ScalarNode, Value: "arn:aws:s3:::ptd-*/*"},
			},
			Condition: S3ResourceAccountCondition(),
		},
	}
}

// SecretsManagerActions returns Secrets Manager-related actions
func SecretsManagerActions() []Action {
	return []Action{
		{"secretsmanager:ListSecrets", false, false},
		{"secretsmanager:*tag*", true, true},
		{"secretsmanager:CreateSecret", true, true},
		{"secretsmanager:*", true, true},
	}
}

// LogActions returns CloudWatch Logs-related actions
func LogActions() []Action {
	return []Action{
		{"logs:List*", false, false},
		{"logs:Describe*", false, false},
		{"logs:CreateLog*", true, false},
		{"logs:DeleteLogGroup", true, false},
		{"logs:Put*", true, false},
		{"logs:*tag*", true, false},
	}
}

// FsxActions returns FSx-related actions
func FsxActions() []Action {
	return []Action{
		{"fsx:Describe*", false, false},
		{"fsx:*", true, false},
	}
}

// FirehoseActions returns Firehose-related actions
func FirehoseActions() []Action {
	return []Action{
		{"firehose:PutRecord*", true, false},
	}
}

// EcsActions returns ECS-related actions
func EcsActions() []Action {
	return []Action{
		{"ecs:*", true, false},
	}
}

// EcrActions returns ECR-related actions
func EcrActions() []Action {
	return []Action{
		{"ecr:GetAuthorizationToken", false, false},
		{"ecr:*", true, false},
	}
}

// EcrAwsAccountStatements returns ECR cross-account policy statements
func EcrAwsAccountStatements() []PolicyStatement {
	return []PolicyStatement{
		{
			Effect: "Allow",
			Action: []string{
				"ecr:*",
			},
			Resource: []yaml.Node{
				{Kind: yaml.ScalarNode, Value: "arn:aws:ecr:*:*:repository/*"},
			},
		},
	}
}

// EfsActions returns EFS-related actions
func EfsActions() []Action {
	return []Action{
		{"elasticfilesystem:*", true, true},
		{"elasticfilesystem:Describe*", false, false},
		{"elasticfilesystem:Create*", true, false},
		{"elasticfilesystem:*tag*", true, false},
	}
}

// EksActions returns EKS-related actions
func EksActions() []Action {
	return []Action{
		{"eks:ListClusters", false, false},
		{"eks:DescribeAddonVersions", false, false},
		{"eks:AccessKubernetesApi", true, false},
		{"eks:Describe*", true, false},
		{"eks:List*", true, false},
		{"eks:*tag*", true, false},
		{"eks:Delete*", true, false},
		{"eks:Create*", true, false},
		{"eks:Update*", true, false},
		{"eks:*AccessPolicy", true, false},
	}
}

// ElbActions returns ELB-related actions
func ElbActions() []Action {
	return []Action{
		{"elasticloadbalancing:Describe*", false, false},
		{"elasticloadbalancing:*", true, false},
	}
}

// OrganizationActions returns AWS Organizations-related actions
func OrganizationActions() []Action {
	return []Action{
		{"organizations:DescribeEffectivePolicy", false, false},
	}
}

// KmsActions returns KMS-related actions
func KmsActions() []Action {
	return []Action{
		{"kms:ListAliases", false, false},
		{"kms:CreateGrant", true, true},
		{"kms:CreateAlias", true, false},
		{"kms:DeleteAlias", true, false},
		{"kms:DescribeKey", true, false},
		{"kms:DisableKey", true, true},
		{"kms:Encrypt", true, true},
		{"kms:GenerateDataKey*", true, true},
		{"kms:ListAliases", true, true},
		{"kms:PutKeyPolicy", true, true},
		{"kms:*tag*", true, true},
		{"kms:*KeyDeletion", true, true},
		{"kms:CreateKey", true, true},
		{"kms:Decrypt", true, true},
	}
}

// Ec2Actions returns EC2-related actions
func Ec2Actions() []Action {
	return []Action{
		{"ec2:Describe*", false, false},
		{"ec2:RunInstances", false, false},
		{"ec2:*", true, false},
		{"ec2:CopyImage", true, false},
		{"ec2:CopySnapshot", true, false},
		{"ec2:CreateVolume", true, false},
		{"ec2:CreateVpcEndpoint", true, false},
		{"ec2:CreateSecurityGroup", false, false},
		{"ec2:AuthorizeSecurityGroupIngress", false, false},
		{"ec2:AuthorizeSecurityGroupEgress", false, false},
		{"ec2:DescribeSecurityGroups", false, false},
		{"ec2:GetSecurityGroupsForVpc", false, false},
	}
}

// IamWildcardActions returns IAM wildcard actions
func IamWildcardActions() []Action {
	return []Action{
		{"iam:ListAttachedRolePolicies", false, false},
		{"iam:ListEntitiesForPolicy", false, false},
		{"iam:ListInstanceProfiles", false, false},
		{"iam:ListInstanceProfilesForRole", false, false},
		{"iam:ListOpenIDConnectProviderTags", false, false},
		{"iam:ListOpenIDConnectProviders", false, false},
		{"iam:ListPolicies", false, false},
		{"iam:ListPoliciesGrantingServiceAccess", false, false},
		{"iam:ListPolicyVersions", false, false},
		{"iam:ListRolePolicies", false, false},
		{"iam:ListRoles", false, false},
		{"iam:GetRole", false, false},
	}
}

// IamStatements returns IAM-related policy statements
func IamStatements() []PolicyStatement {
	return []PolicyStatement{
		{
			Effect: "Allow",
			Action: []string{
				"iam:AddClientIDToOpenIDConnectProvider",
				"iam:AddRoleToInstanceProfile",
				"iam:CreateInstanceProfile",
				"iam:CreateOpenIDConnectProvider",
				"iam:CreatePolicy",
				"iam:CreatePolicyVersion",
				"iam:CreateSAMLProvider",
				"iam:CreateServiceLinkedRole",
				"iam:DeleteInstanceProfile",
				"iam:DeleteOpenIDConnectProvider",
				"iam:DeletePolicy",
				"iam:DeletePolicyVersion",
				"iam:DeleteSAMLProvider",
				"iam:GetInstanceProfile",
				"iam:GetOpenIDConnectProvider",
				"iam:GetPolicy",
				"iam:GetPolicyVersion",
				"iam:GetRolePolicy",
				"iam:PassRole",
				"iam:PutRolePolicy",
				"iam:RemoveClientIDFromOpenIDConnectProvider",
				"iam:RemoveRoleFromInstanceProfile",
				"iam:Tag*",
				"iam:Untag*",
				"iam:UpdateAssumeRolePolicy",
				"iam:UpdateOpenIDConnectProviderThumbprint",
				"iam:UpdateRole",
				"iam:UpdateRoleDescription",
				"iam:UpdateSAMLProvider",
			},
			Resource: IamResourceWildcards(),
		},
	}
}

// SqsActions returns SQS-related actions
func SqsActions() []Action {
	return []Action{
		{"sqs:*Queue*", false, false},
		{"sqs:*Message*", true, false},
	}
}

// EventsActions returns EventBridge-related actions
func EventsActions() []Action {
	return []Action{
		{"events:ListRules", false, false},
		{"events:ListTagsForResource", true, false},
		{"events:*Rule", true, false},
		{"events:*Targets", true, false},
		{"events:*tag*", true, true},
	}
}

// PricingActions returns Pricing-related actions
func PricingActions() []Action {
	return []Action{
		{"pricing:GetProducts", false, false},
	}
}

// BuildCompleteAdminPolicyDocument constructs the complete PositTeamDedicatedAdmin IAM policy document.
// This policy contains all AWS service actions needed for PTD operations, including:
//   - Self-constraining statements (permissions boundary enforcement)
//   - IAM, S3, ECR, EKS, EC2, and other AWS service permissions
//   - Resource account and tag-based conditions to limit scope
//
// The policy is used both as a permissions boundary and as an attached managed policy
// for PTD-created IAM roles to ensure they cannot escalate beyond PTD's allowed permissions.
//
// Returns a PolicyDocument that can be marshaled to JSON for AWS IAM API calls or
// YAML for CloudFormation templates.
func BuildCompleteAdminPolicyDocument() PolicyDocument {
	doc := NewAdminPolicyDocument()
	doc.AddStatements(SelfConstrainingStatements())
	doc.AddStatements(IamStatements())
	doc.AddStatements(S3Statements())
	doc.AddStatements(EcrAwsAccountStatements())
	doc.AddActions(AcmActions())
	doc.AddActions(BedrockActions())
	doc.AddActions(CloudwatchActions())
	doc.AddActions(ComputeOptimizerActions())
	doc.AddActions(DirectoryServiceActions())
	doc.AddActions(Ec2Actions())
	doc.AddActions(EcrActions())
	doc.AddActions(EcsActions())
	doc.AddActions(EksActions())
	doc.AddActions(ElbActions())
	doc.AddActions(EfsActions())
	doc.AddActions(EventsActions())
	doc.AddActions(FirehoseActions())
	doc.AddActions(FsxActions())
	doc.AddActions(IamWildcardActions())
	doc.AddActions(KmsActions())
	doc.AddActions(LogActions())
	doc.AddActions(OrganizationActions())
	doc.AddActions(PricingActions())
	doc.AddActions(RdsActions())
	doc.AddActions(ResourceExplorerActions())
	doc.AddActions(Route53Actions())
	doc.AddActions(SecretsManagerActions())
	doc.AddActions(ShieldActions())
	doc.AddActions(SqsActions())
	doc.AddActions(SsmActions())
	doc.AddActions(StsActions())
	doc.AddActions(TagActions())
	doc.AddActions(Wafv2Actions())
	return doc
}
