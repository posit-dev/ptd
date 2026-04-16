package eject

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

var removeAccessTemplate = template.Must(template.New("remove-access").Parse(
	"# Removing Posit Access to {{ .TargetName }}\n" +
		"\n" +
		"This runbook describes how to remove Posit's ability to access the AWS account\n" +
		"hosting the {{ .TargetName }} workload. This is the final step in a full\n" +
		"severance — after the automated eject has disconnected Mimir and Alloy, this\n" +
		"removes the IAM trust that allows Posit engineers to assume a role in your account.\n" +
		"\n" +
		"**This is a one-way operation.** Re-adopting the workload later will require\n" +
		"re-establishing this trust. Contact Posit if you need to reverse this.\n" +
		"\n" +
		"## What to Remove\n" +
		"\n" +
		"The PTD control room (AWS account {{ .AccountID }}) has cross-account access to\n" +
		"your workload account via an IAM role trust policy. The role is named:\n" +
		"\n" +
		"    admin.posit.team\n" +
		"\n" +
		"This role's trust policy contains an entry allowing the control room account\n" +
		"({{ .AccountID }}) to assume it.\n" +
		"\n" +
		"## Procedure\n" +
		"\n" +
		"### Option A: Remove the trust policy entry (recommended)\n" +
		"\n" +
		"This removes Posit's access while keeping the role intact for other uses.\n" +
		"\n" +
		"1. Open the AWS IAM Console in the workload account\n" +
		"2. Navigate to **Roles** and find admin.posit.team\n" +
		"3. Select the **Trust relationships** tab\n" +
		"4. Click **Edit trust policy**\n" +
		"5. Find and remove the statement that references account {{ .AccountID }}\n" +
		"6. Save the updated policy\n" +
		"\n" +
		"Or via CLI:\n" +
		"\n" +
		"    aws iam get-role --role-name admin.posit.team --query 'Role.AssumeRolePolicyDocument'\n" +
		"\n" +
		"Review the output, remove the statement referencing arn:aws:iam::{{ .AccountID }}:root (or\n" +
		"the specific role ARN), then update:\n" +
		"\n" +
		"    aws iam update-assume-role-policy --role-name admin.posit.team --policy-document file://updated-trust-policy.json\n" +
		"\n" +
		"### Option B: Delete the role entirely\n" +
		"\n" +
		"If the admin.posit.team role is only used for Posit access:\n" +
		"\n" +
		"    aws iam delete-role --role-name admin.posit.team\n" +
		"\n" +
		"**Warning:** Ensure no other services or users depend on this role before deleting.\n" +
		"\n" +
		"## Verification\n" +
		"\n" +
		"After removing access, verify that the control room can no longer assume the role:\n" +
		"\n" +
		"    aws sts assume-role --role-arn arn:aws:iam::<your-account-id>:role/admin.posit.team --role-session-name test\n" +
		"\n" +
		"This should fail with an \"AccessDenied\" error if the trust was removed correctly.\n" +
		"\n" +
		"## Azure\n" +
		"\n" +
		"If this is an Azure workload, the equivalent operation is removing Posit's RBAC\n" +
		"role assignments from the Azure subscription. Contact Posit for the specific\n" +
		"principal IDs to remove if they were not documented during onboarding.\n"))

type removeAccessData struct {
	TargetName string
	AccountID  string
}

func WriteRemoveAccessRunbook(outputDir string, details *ControlRoomDetails, targetName string) error {
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

	if err := removeAccessTemplate.Execute(f, data); err != nil {
		return fmt.Errorf("failed to render runbook template: %w", err)
	}

	return nil
}
