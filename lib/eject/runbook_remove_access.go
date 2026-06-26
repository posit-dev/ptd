package eject

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

var removeAccessAWSTemplate = template.Must(template.New("remove-access-aws").Parse(
	"# Removing Posit Access to {{ .TargetName }}\n" +
		"\n" +
		"This runbook describes how to remove Posit's ability to access the AWS account\n" +
		"hosting the {{ .TargetName }} workload. This is the final step in a full\n" +
		"eject — after the automated eject has disconnected Mimir and Alloy, this\n" +
		"removes the IAM trust that allows Posit engineers to assume a role in your account.\n" +
		"\n" +
		"**This is a one-way operation.** Re-adopting the workload later will require\n" +
		"re-establishing this trust. Contact Posit if you need to reverse this.\n" +
		"\n" +
		"## What to Change\n" +
		"\n" +
		"The PTD CLI authenticates to your workload account by assuming an IAM role named\n" +
		"admin.posit.team. This role's trust policy lists individual Posit engineers from\n" +
		"the control room account ({{ .AccountID }}) as trusted principals. The entries\n" +
		"look like:\n" +
		"\n" +
		"    arn:aws:sts::{{ .AccountID }}:assumed-role/AWSReservedSSO_PowerUser_.../engineer@posit.co\n" +
		"\n" +
		"To revoke Posit's access, remove all principal entries referencing account\n" +
		"{{ .AccountID }} from the trust policy. Do **not** delete the role itself — the\n" +
		"PTD CLI uses admin.posit.team to authenticate all operations, so removing the\n" +
		"role would prevent you from running ptd ensure.\n" +
		"\n" +
		"## Step 1: Add your own trusted principals\n" +
		"\n" +
		"Before removing Posit's access, ensure you have your own principals in the trust\n" +
		"policy so you can continue to assume the role. For example, if your team uses\n" +
		"AWS SSO, add your SSO role as a trusted principal:\n" +
		"\n" +
		"    arn:aws:iam::YOUR-WORKLOAD-ACCOUNT-ID:role/aws-reserved/sso.amazonaws.com/REGION/AWSReservedSSO_PowerUser_YOUR-SSO-ID\n" +
		"\n" +
		"Or any other IAM role or user that should have access.\n" +
		"\n" +
		"## Step 2: Remove Posit's trusted principals\n" +
		"\n" +
		"### Via the AWS Console\n" +
		"\n" +
		"1. Open the AWS IAM Console in the workload account\n" +
		"2. Navigate to **Roles** and find admin.posit.team\n" +
		"3. Select the **Trust relationships** tab\n" +
		"4. Click **Edit trust policy**\n" +
		"5. Remove all principal entries containing account {{ .AccountID }} (these are the Posit engineer entries)\n" +
		"6. Verify your own principal entries are present\n" +
		"7. Save the updated policy\n"))

var removeAccessAzureTemplate = template.Must(template.New("remove-access-azure").Parse(
	"# Removing Posit Access to {{ .TargetName }}\n" +
		"\n" +
		"This runbook describes how to remove Posit's ability to access the Azure\n" +
		"subscription hosting the {{ .TargetName }} workload. This is the final step in a\n" +
		"full eject — after the automated eject has disconnected Mimir and Alloy, this\n" +
		"removes the RBAC role assignments that grant Posit engineers access to your\n" +
		"subscription.\n" +
		"\n" +
		"**This is a one-way operation.** Re-adopting the workload later will require\n" +
		"re-establishing these role assignments. Contact Posit if you need to reverse this.\n" +
		"\n" +
		"## What to Remove\n" +
		"\n" +
		"Posit engineers have access to your Azure subscription via RBAC role assignments\n" +
		"granted to Posit service principals during onboarding. These assignments allow\n" +
		"Posit to manage infrastructure and deploy updates to your workload.\n" +
		"\n" +
		"## Procedure\n" +
		"\n" +
		"The specific principal IDs and role assignments vary by workload and were\n" +
		"configured during onboarding. Work with your Posit engineer to identify and\n" +
		"remove the correct role assignments.\n" +
		"\n" +
		"The general approach is:\n" +
		"\n" +
		"1. List role assignments on the subscription or resource group scoped to Posit principals\n" +
		"2. Remove each assignment using the Azure Portal or CLI:\n" +
		"\n" +
		"        az role assignment list --scope /subscriptions/YOUR-SUBSCRIPTION-ID --query \"[?contains(principalName, 'posit')]\"\n" +
		"        az role assignment delete --ids <assignment-id>\n" +
		"\n" +
		"3. Verify that Posit principals can no longer access resources in the subscription\n" +
		"\n" +
		"Your Posit engineer can confirm which principals to remove and verify the\n" +
		"removal was successful.\n"))

type removeAccessData struct {
	TargetName string
	AccountID  string
}

func WriteRemoveAccessRunbook(outputDir string, details *ControlRoomDetails, targetName string, cloudProvider string) error {
	runbooksDir := filepath.Join(outputDir, "runbooks")
	if err := os.MkdirAll(runbooksDir, 0755); err != nil {
		return fmt.Errorf("failed to create runbooks directory: %w", err)
	}

	outPath := filepath.Join(runbooksDir, "remove-posit-access.md")
	f, err := os.Create(filepath.Clean(outPath))
	if err != nil {
		return fmt.Errorf("failed to create runbook file: %w", err)
	}
	defer f.Close()

	data := removeAccessData{
		TargetName: targetName,
		AccountID:  details.AccountID,
	}

	tmpl := removeAccessAWSTemplate
	if cloudProvider == "azure" {
		tmpl = removeAccessAzureTemplate
	}

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("failed to render runbook template: %w", err)
	}

	return nil
}
