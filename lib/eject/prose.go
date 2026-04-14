package eject

import "fmt"

const ejectDocTitle = "Infrastructure Handoff"

func overviewText(cloud string) string {
	target := "AWS account"
	if cloud == "azure" {
		target = "Azure subscription"
	}
	return "This document describes the complete infrastructure managed by " +
		"Posit Team Dedicated (PTD) in the " + target + ". It is intended " +
		"to serve as a reference for operating, maintaining, and troubleshooting " +
		"the environment independently of Posit. All resource identifiers, " +
		"configuration details, and operational commands are included so that " +
		"a team unfamiliar with PTD internals can take over day-to-day operations."
}

func ownershipOptionsText() string {
	return "PTD infrastructure is provisioned and managed by Pulumi, orchestrated through the " +
		"PTD CLI. Each infrastructure layer is a separate Pulumi stack with its state stored " +
		"in your cloud account. There are three paths to taking ownership of this infrastructure:\n\n" +
		"1. Import into your own Pulumi programs — generate standalone Pulumi code in any " +
		"supported language and manage stacks directly. See Generating a Standalone Pulumi Program below.\n" +
		"2. Migrate to Terraform — generate Terraform import blocks from the resource inventory " +
		"and use Terraform's HCL code generation. See Migrating to Terraform below.\n" +
		"3. Continue using the PTD CLI — run `ptd ensure` to manage updates. " +
		"See Continuing with the PTD CLI below.\n\n" +
		"All three approaches start from the same Pulumi state files stored in your account. " +
		"The sections below provide detailed instructions for each option."
}

func severancePlanDryRunText() string {
	return "This bundle was generated in dry-run mode. No infrastructure " +
		"changes were made. The connections listed below would be severed " +
		"when eject runs without --dry-run. Review these carefully before proceeding."
}

func severancePlanLiveText() string {
	return "The following control room connections were severed during this " +
		"eject run. The workload is now operating independently."
}

func pulumiOwnershipText(cloud string, targetName string) string {
	if cloud == "azure" {
		return fmt.Sprintf("Pulumi state is stored in Azure Blob Storage (storage account %s). "+
			"To manage stacks directly:\n\n"+
			"1. Install the Pulumi CLI: https://www.pulumi.com/docs/install/\n"+
			"2. Set the backend: `pulumi login azblob://<container>?storage_account=%s`\n"+
			"3. Select a stack: `pulumi stack select <project>/<stack>`\n"+
			"4. View resources: `pulumi stack export | jq '.deployment.resources | length'`\n\n"+
			"Secrets in state are encrypted with Azure Key Vault key **posit-team-dedicated**. "+
			"Access to the Key Vault is required to read or modify encrypted values.",
			targetName, targetName)
	}
	return fmt.Sprintf("Pulumi state is stored in S3 at s3://ptd-%s/.pulumi/stacks/. "+
		"To manage stacks directly:\n\n"+
		"1. Install the Pulumi CLI: https://www.pulumi.com/docs/install/\n"+
		"2. Set the backend: `pulumi login s3://ptd-%s`\n"+
		"3. Select a stack: `pulumi stack select <project>/<stack>`\n"+
		"4. View resources: `pulumi stack export | jq '.deployment.resources | length'`\n\n"+
		"Secrets in state are encrypted with AWS KMS key **alias/posit-team-dedicated**. "+
		"IAM access to the KMS key is required to read or modify encrypted values.",
		targetName, targetName)
}

func pulumiImportText() string {
	return "You can use pulumi import to generate a Pulumi program in your language of choice " +
		"that represents all resources currently managed by a stack. The generated code can be run " +
		"against the existing state backend to verify it produces no diffs, giving confidence that " +
		"it accurately describes the deployed infrastructure.\n\n" +
		"For each stack:\n\n" +
		"1. Select the stack: `pulumi stack select <project>/<stack>`\n" +
		"2. Import all resources, generating code:\n`pulumi import --from <stack-name> --generate-code=true --out program/`\n" +
		"3. Review the program/ directory — it will contain complete resource " +
		"definitions in your configured language (TypeScript, Python, Go, C#, Java, or YAML)\n" +
		"4. Verify the code matches the deployed state: `pulumi preview`\n\n" +
		"A clean preview (no creates, updates, or deletes) confirms the generated code is a faithful " +
		"representation of the live infrastructure. From there you can evolve the code independently " +
		"without the PTD CLI."
}

func terraformMigrationText() string {
	return "If you prefer to manage infrastructure with Terraform rather than Pulumi, you can " +
		"generate Terraform configuration from the existing resources. Terraform 1.5+ supports " +
		"import blocks with automatic HCL generation, which can produce a complete Terraform " +
		"program from live infrastructure.\n\n" +
		"The high-level process:\n\n" +
		"1. Export the resource IDs from the Pulumi state for a given stack:\n" +
		"`pulumi stack export | jq -r '.deployment.resources[] | select(.id != null and (.type | startswith(\"pulumi:\") | not)) | \"\\(.type) \\(.id)\"'`\n" +
		"2. Map each Pulumi resource type to its Terraform equivalent (e.g., aws:ec2/vpc:Vpc becomes aws_vpc, " +
		"aws:s3/bucket:Bucket becomes aws_s3_bucket, aws:rds/instance:Instance becomes aws_db_instance).\n" +
		"3. Write a Terraform import block for each resource:\n" +
		"`import { to = aws_vpc.main, id = \"vpc-0abc123\" }`\n" +
		"4. Run Terraform to generate the HCL configuration:\n" +
		"`terraform plan -generate-config-out=generated.tf`\n" +
		"5. Review generated.tf — it will contain full resource definitions matching the deployed state.\n" +
		"6. Run `terraform plan` and confirm it shows no changes, verifying the generated configuration " +
		"accurately represents the live infrastructure.\n\n" +
		"Repeat this process for each Pulumi stack."
}

func arnReconstructionNote(cloud string, accountID string, region string) string {
	if cloud == "azure" {
		return "Resource IDs in the tables below are shown as-is. " +
			"Use the Azure portal or CLI to look up resources by their ID."
	}
	return fmt.Sprintf(
		"Resource IDs in the tables below are shown without the common ARN prefix. "+
			"To reconstruct a full ARN, prepend arn:aws:{service}:%s:%s: to the displayed ID, "+
			"where {service} is the AWS service name (e.g., ec2, s3, rds) matching the resource type.",
		region, accountID)
}

func ptdCommandDescription(stepName string) string {
	switch stepName {
	case "persistent":
		return "Provisions foundational infrastructure (VPC/VNet, RDS/PostgreSQL, S3/Storage, FSx/NetApp, IAM, DNS, certificates)"
	case "postgres-config":
		return "Configures PostgreSQL databases, users, and grants"
	case "eks":
		return "Provisions EKS cluster, node groups, OIDC provider, and storage classes"
	case "aks":
		return "Provisions AKS cluster, node pools, managed identity, and storage classes"
	case "clusters":
		return "Configures Kubernetes namespaces, network policies, Team Operator, Traefik, and external DNS"
	case "helm":
		return "Deploys supporting Helm charts (monitoring, cert-manager, Secrets Store CSI)"
	case "sites":
		return "Deploys Posit products (TeamSite CRDs), ingress resources, and site configuration"
	default:
		return "Custom infrastructure step"
	}
}
