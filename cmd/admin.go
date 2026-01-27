package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"

	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/lib/consts"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	ROLENAME = "admin.posit.team"
)

func init() {
	rootCmd.AddCommand(adminCmd)
	adminCmd.AddCommand(generateRoleCmd)
}

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Run admin commands",
	Long:  `Run admin commands.`,
}

var generateRoleCmd = &cobra.Command{
	Use:   "generate-role <control-room-name>",
	Short: "Generate the admin principal role template",
	Long:  "Generate the admin principal role template for a control room target",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]

		// find the relevant ptd.yaml file, load it.
		t, err := legacy.TargetFromName(target)
		if err != nil {
			slog.Error("Could not load relevant ptd.yaml file", "error", err)
			return
		}

		if !t.ControlRoom() {
			slog.Error("Target is not a control room")
			return
		}

		// Build the path to the control room config
		yamlPath := helpers.YamlPathForTarget(t)

		// Load the control room config
		config, err := helpers.LoadPtdYaml(yamlPath)
		if err != nil {
			slog.Error("Failed to load control room config", "error", err, "path", yamlPath)
			return
		}

		controlRoomConfig, ok := config.(types.AWSControlRoomConfig)
		if !ok {
			slog.Error("Config is not an AWSControlRoomConfig", "target", target)
			return
		}

		// Generate the template
		template, err := awsPrincipalTemplate(controlRoomConfig)
		if err != nil {
			slog.Error("Error generating template", "error", err)
			return
		}
		cmd.Print(template)
	},
}

func awsPrincipalTemplate(controlRoomConfig types.AWSControlRoomConfig) (string, error) {
	doc := aws.BuildCompleteAdminPolicyDocument()

	role := aws.NewRole(ROLENAME)
	role.AssumeRolePolicyDocument = aws.PolicyDocument{
		Version: "2012-10-17",
		Statement: []aws.PolicyStatement{
			{
				Effect: "Allow",
				Action: []string{
					"sts:AssumeRole",
				},
				Principal: aws.Principal{
					"AWS": map[string]string{
						"Ref": "TrustedPrincipals",
					},
				},
			},
		},
	}
	role.Tags = []map[string]string{
		{"Key": "Name", "Value": ROLENAME},
		{"Key": "posit.team/managed-by", "Value": "admin"},
	}
	role.PermissionsBoundary = consts.PositTeamDedicatedAdminPolicyName
	role.ManagedPolicyArns = []aws.PolicyRef{consts.PositTeamDedicatedAdminPolicyName}
	role.RolePolicyList = []aws.Policy{
		{
			PolicyName: "DenySelfUpdateAssumeRolePolicy",
			PolicyDocument: aws.PolicyDocument{
				Version: "2012-10-17",
				Statement: []aws.PolicyStatement{
					{
						Effect: "Deny",
						Action: []string{
							"iam:UpdateAssumeRolePolicy",
						},
						Resource: []yaml.Node{
							{Kind: yaml.ScalarNode, Value: fmt.Sprintf("arn:aws:iam::*:role/%s", ROLENAME)},
						},
					},
				},
			},
		},
	}

	mp := aws.ManagedPolicy{
		ManagedPolicyName: consts.PositTeamDedicatedAdminPolicyName,
		PolicyDocument:    doc,
	}

	cfnTemplate := cloudformationTemplate{
		AWSTemplateFormatVersion: "2010-09-09",
		Description:              "Pre-bootstrap template for Posit Team Dedicated",
		Parameters: map[string]map[string]string{
			"TrustedPrincipals": {
				"Type":           "CommaDelimitedList",
				"Description":    "Comma-delimited list of trusted principal ARNs",
				"AllowedPattern": `^arn:aws:(sts|iam):\S{12,}$`,
				"Default":        generatePrincipalList(controlRoomConfig),
			},
		},
		Resources: map[string]cloudformationResource{
			consts.PositTeamDedicatedAdminPolicyName: {
				Type:       "AWS::IAM::ManagedPolicy",
				Properties: mp,
			},
			"PositTeamDedicatedAdminRole": {
				Type:       "AWS::IAM::Role",
				Properties: role,
			},
		},
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(4) // Set the desired indentation level

	if err := encoder.Encode(cfnTemplate); err != nil {
		return "", err
	}

	slog.Info("CloudFormation template generated successfully")
	return buf.String(), nil
}

type cloudformationTemplate struct {
	AWSTemplateFormatVersion string                            `yaml:"AWSTemplateFormatVersion"`
	Description              string                            `yaml:"Description"`
	Parameters               map[string]map[string]string      `yaml:"Parameters"`
	Resources                map[string]cloudformationResource `yaml:"Resources"`
}

type cloudformationResource struct {
	Type       string      `yaml:"Type"`
	Properties interface{} `yaml:"Properties"`
}

func generatePrincipalList(controlRoomConfig types.AWSControlRoomConfig) string {
	arnPrefix := controlRoomConfig.PowerUserARN

	var principals []string
	for _, user := range controlRoomConfig.TrustedUsers {
		principals = append(principals, arnPrefix+user.Email)
	}
	slog.Debug("Generated principal list", "principals", principals)
	return strings.Join(principals, ",")
}
