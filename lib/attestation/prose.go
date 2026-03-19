package attestation

import (
	"fmt"
	"strings"
)

// Shared document copy used by both markdown and PDF renderers.
const (
	docTitle = "Installation Confirmation"

	confirmationText = "We make this confirmation based on our knowledge as of " +
		"the date hereof, formed after reasonable inquiry, and limited to " +
		"information within our possession or control regarding the installation " +
		"described in this document."
)

// Cloud-aware copy functions

func purposeTextFor(cloud string) string {
	target := "target AWS account"
	if cloud == "azure" {
		target = "target Azure subscription"
	}
	return "This document confirms that the Posit Team Dedicated (PTD) " +
		"platform has been installed and configured in the " + target + " as " +
		"specified in the declarative configuration files maintained in version " +
		"control. It provides a summary of all infrastructure and application " +
		"resources provisioned, the product versions deployed, and references to " +
		"the Pulumi state files that serve as the authoritative record of the " +
		"installation."
}

func infraSummaryTextFor(cloud string) string {
	if cloud == "azure" {
		return "PTD provisions infrastructure through a series of ordered " +
			"Pulumi stacks, each managing a distinct layer of the deployment. All " +
			"stacks use the Pulumi self-managed backend with state stored in Azure " +
			"Blob Storage and secrets encrypted via Azure Key Vault."
	}
	return "PTD provisions infrastructure through a series of ordered " +
		"Pulumi stacks, each managing a distinct layer of the deployment. All " +
		"stacks use the Pulumi self-managed backend with state stored in S3 and " +
		"secrets encrypted via AWS KMS."
}

func verificationTextFor(cloud string) string {
	store := "workload's S3 bucket"
	if cloud == "azure" {
		store = "workload's Azure Blob Storage container"
	}
	return "The authoritative proof of installation is the set of " +
		"Pulumi state files stored in the " + store + ". Each state file " +
		"contains a complete inventory of every resource managed by that stack, " +
		"including resource types, cloud provider IDs, configuration values, and " +
		"timestamps."
}

func encryptionTextFor(cloud string) string {
	if cloud == "azure" {
		return "Sensitive values (database passwords, API keys, TLS private " +
			"keys) are encrypted at rest using the Azure Key Vault key " +
			"posit-team-dedicated in the target subscription."
	}
	return "Sensitive values (database passwords, API keys, TLS private " +
		"keys) are encrypted at rest using the AWS KMS key " +
		"alias/posit-team-dedicated in the target account."
}

func productDisplayName(name string) string {
	switch name {
	case "connect":
		return "Posit Connect"
	case "workbench":
		return "Posit Workbench"
	case "package-manager":
		return "Posit Package Manager"
	case "chronicle":
		return "Chronicle"
	case "chronicle-agent":
		return "Chronicle Agent"
	default:
		return name
	}
}

func accountLabel(cloud string) string {
	if cloud == "azure" {
		return "Subscription"
	}
	return "AWS Account"
}

// InfraConfig holds config flags parsed from ptd.yaml that influence prose generation.
// These are parsed separately from the typed config because the typed config
// doesn't fully handle the spec: nesting for all fields.
type InfraConfig struct {
	// Cloud provider: "aws" or "azure"
	Cloud string

	// VPC (AWS)
	ProvisionedVpcID string
	ProvisionedCidr  string
	PrivateSubnets   []string
	VpcCidr          string
	VpcAzCount       int

	// VNet (Azure)
	VnetCidr          string
	ProvisionedVnetID string

	// Cluster
	ClusterVersion string
	InstanceType   string
	RootDiskSize   int

	// Storage
	FsxMultiAz bool

	// Certificates (AWS)
	CertValidationEnabled bool
	CertARNProvided       bool
	PrivateZone           bool

	// Features
	KeycloakEnabled             bool
	ExternalDNSEnabled          bool
	PublicLoadBalancer          bool
	SecretsStoreAddonEnabled    bool
	HostedZoneManagementEnabled bool
	CustomerManagedBastionID    string
	LoadBalancerPerSite         bool

	// Sites
	SiteDomains map[string]string
}

func (c *InfraConfig) IsAzure() bool {
	return c != nil && c.Cloud == "azure"
}

// generatePersistentProse generates narrative text for the persistent stack
func generatePersistentProse(cfg *InfraConfig) string {
	if cfg.IsAzure() {
		return generateAzurePersistentProse(cfg)
	}
	return generateAWSPersistentProse(cfg)
}

func generateAWSPersistentProse(cfg *InfraConfig) string {
	var lines []string

	if cfg.ProvisionedVpcID != "" {
		lines = append(lines, fmt.Sprintf(
			"Integration with customer-provisioned VPC (`%s`, CIDR `%s`) and %d private subnets",
			cfg.ProvisionedVpcID, cfg.ProvisionedCidr, len(cfg.PrivateSubnets)))
	} else {
		cidr := cfg.VpcCidr
		if cidr == "" {
			cidr = "10.0.0.0/16"
		}
		azCount := cfg.VpcAzCount
		if azCount == 0 {
			azCount = 3
		}
		lines = append(lines, fmt.Sprintf(
			"VPC with CIDR `%s` across %d availability zones, with public and private subnets, NAT gateways, and internet gateway",
			cidr, azCount))
	}

	lines = append(lines, "RDS PostgreSQL instance with custom parameter group")
	lines = append(lines, "S3 buckets for Loki log storage, Mimir metrics, Package Manager package cache, and Chronicle telemetry")

	if cfg.FsxMultiAz {
		lines = append(lines, "FSx for OpenZFS file system (multi-AZ) for persistent session storage")
	} else {
		lines = append(lines, "FSx for OpenZFS file system (single-AZ) for persistent session storage")
	}

	if cfg.CertARNProvided {
		lines = append(lines, "ACM TLS certificates (customer-provided) for load balancer termination")
	} else if cfg.CertValidationEnabled {
		lines = append(lines, "ACM TLS certificates with DNS validation for load balancer termination")
	} else {
		lines = append(lines, "ACM TLS certificates for load balancer termination")
	}

	for _, domain := range cfg.SiteDomains {
		if cfg.PrivateZone {
			lines = append(lines, fmt.Sprintf("Private Route 53 hosted zone for `%s`", domain))
		} else if cfg.HostedZoneManagementEnabled {
			lines = append(lines, fmt.Sprintf("Route 53 hosted zone for `%s`", domain))
		}
		break
	}

	lines = append(lines, "IAM roles and policies for all service components")
	lines = append(lines, "Security groups for RDS, EKS, and internal communication")

	if cfg.CustomerManagedBastionID != "" {
		lines = append(lines, "Integration with customer-managed bastion host for cluster access")
	}

	return "Provisions the foundational infrastructure layer:\n\n" + bulletList(lines)
}

func generateAzurePersistentProse(cfg *InfraConfig) string {
	var lines []string

	if cfg.ProvisionedVnetID != "" {
		lines = append(lines, fmt.Sprintf(
			"Integration with customer-provisioned VNet (`%s`)", cfg.ProvisionedVnetID))
	} else {
		cidr := cfg.VnetCidr
		if cidr == "" {
			cidr = "10.0.0.0/16"
		}
		lines = append(lines, fmt.Sprintf(
			"VNet with CIDR `%s`, with public, private, database, and NetApp subnets", cidr))
	}

	lines = append(lines, "Azure Database for PostgreSQL Flexible Server")
	lines = append(lines, "Azure Storage accounts for Loki logs, Mimir metrics, Package Manager cache, and Chronicle telemetry")
	lines = append(lines, "Azure NetApp Files for persistent session storage")
	lines = append(lines, "Azure Key Vault for secrets management")
	lines = append(lines, "Managed identities for all service components")
	lines = append(lines, "Network security groups for database, AKS, and internal communication")

	return "Provisions the foundational infrastructure layer:\n\n" + bulletList(lines)
}

// generatePostgresConfigProse generates narrative text for the postgres-config stack
func generatePostgresConfigProse(cfg *InfraConfig) string {
	dbType := "RDS PostgreSQL"
	secretsNote := "Passwords generated and stored as KMS-encrypted secrets in Pulumi state"
	if cfg.IsAzure() {
		dbType = "Azure PostgreSQL Flexible Server"
		secretsNote = "Passwords generated and stored as Key Vault-encrypted secrets in Pulumi state"
	}

	return fmt.Sprintf("Configures the %s instance with application-specific databases and credentials:\n\n", dbType) +
		bulletList([]string{
			"Database users for Grafana and internal services",
			"Dedicated databases with appropriate grants",
			secretsNote,
		})
}

// generateEKSProse generates narrative text for the EKS/AKS stack
func generateEKSProse(cfg *InfraConfig) string {
	if cfg.IsAzure() {
		return generateAKSProse(cfg)
	}

	lines := []string{}

	version := cfg.ClusterVersion
	if version == "" {
		version = "latest"
	}
	lines = append(lines, fmt.Sprintf("EKS cluster running Kubernetes %s", version))

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "managed"
	}
	if cfg.RootDiskSize > 0 {
		lines = append(lines, fmt.Sprintf("Managed node group with %s instances and %dGB root disks", instanceType, cfg.RootDiskSize))
	} else {
		lines = append(lines, fmt.Sprintf("Managed node group with %s instances", instanceType))
	}

	lines = append(lines, "OIDC provider for IAM Roles for Service Accounts (IRSA)")
	lines = append(lines, "EBS CSI driver addon")
	lines = append(lines, "GP3 default storage class")
	lines = append(lines, "IAM access entries for cluster administration")

	return "Provisions the Kubernetes control plane and compute:\n\n" + bulletList(lines)
}

func generateAKSProse(cfg *InfraConfig) string {
	lines := []string{}

	version := cfg.ClusterVersion
	if version == "" {
		version = "latest"
	}
	lines = append(lines, fmt.Sprintf("AKS cluster running Kubernetes %s", version))

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "managed"
	}
	lines = append(lines, fmt.Sprintf("System and user node pools with %s instances", instanceType))
	lines = append(lines, "Azure AD workload identity for pod-level authentication")
	lines = append(lines, "Azure Disk CSI driver for persistent volumes")
	lines = append(lines, "Managed identity access entries for cluster administration")

	return "Provisions the Kubernetes control plane and compute:\n\n" + bulletList(lines)
}

// generateClustersProse generates narrative text for the clusters stack
func generateClustersProse(cfg *InfraConfig) string {
	lines := []string{
		"Namespaces: `posit-team`, `loki`, `grafana`, `mimir`, and supporting namespaces",
		"Calico network policies restricting inter-namespace traffic",
		"Team Operator deployment (manages Posit product lifecycle)",
	}

	if cfg.PublicLoadBalancer {
		lines = append(lines, "Traefik ingress controller with public-facing load balancer")
	} else {
		lines = append(lines, "Traefik ingress controller with internal-only load balancer")
	}

	if !cfg.ExternalDNSEnabled {
		lines = append(lines, "External DNS disabled (per customer configuration)")
	} else {
		if cfg.IsAzure() {
			lines = append(lines, "External DNS for automatic Azure DNS record management")
		} else {
			lines = append(lines, "External DNS for automatic Route 53 record management")
		}
	}

	if cfg.IsAzure() {
		lines = append(lines, "Managed identities mapped to Kubernetes service accounts via workload identity")
	} else {
		lines = append(lines, "IAM roles mapped to Kubernetes service accounts via IRSA")
	}

	if cfg.KeycloakEnabled {
		lines = append(lines, "Keycloak operator for identity management")
	}

	return "Configures the Kubernetes cluster with the required namespaces, operators, and network policies:\n\n" + bulletList(lines)
}

// generateHelmProse generates narrative text for the helm stack
func generateHelmProse(cfg *InfraConfig) string {
	lines := []string{
		"Grafana, Loki, and Mimir for observability and log aggregation",
		"Grafana Alloy for metrics and log collection",
		"cert-manager for TLS certificate lifecycle",
	}

	if cfg.SecretsStoreAddonEnabled {
		if cfg.IsAzure() {
			lines = append(lines, "Secrets Store CSI Driver for Azure Key Vault integration")
		} else {
			lines = append(lines, "Secrets Store CSI Driver for AWS Secrets Manager integration")
		}
	}

	return "Deploys supporting services as Helm charts:\n\n" + bulletList(lines)
}

// generateSitesProse generates narrative text for the sites stack
func generateSitesProse(cfg *InfraConfig) string {
	lines := []string{}

	for name, domain := range cfg.SiteDomains {
		lines = append(lines, fmt.Sprintf("TeamSite custom resource for the `%s` site at `%s`", name, domain))
	}

	lines = append(lines, "Posit Connect, Workbench, and Package Manager instances as declared in `site.yaml`")
	lines = append(lines, "Chronicle observability agent and sidecar")
	lines = append(lines, "Ingress resources routing traffic from Traefik to each product")

	if cfg.PrivateZone || cfg.HostedZoneManagementEnabled {
		lines = append(lines, "DNS records in the hosted zone for each product subdomain")
	}

	return "Deploys the Posit products into the cluster:\n\n" + bulletList(lines)
}

// GenerateStackProse generates prose for a stack given its step name and the infrastructure config
func GenerateStackProse(stepName string, cfg *InfraConfig) string {
	switch stepName {
	case "persistent":
		return generatePersistentProse(cfg)
	case "postgres-config":
		return generatePostgresConfigProse(cfg)
	case "eks":
		return generateEKSProse(cfg)
	case "aks":
		return generateAKSProse(cfg)
	case "clusters":
		return generateClustersProse(cfg)
	case "helm":
		return generateHelmProse(cfg)
	case "sites":
		return generateSitesProse(cfg)
	default:
		return ""
	}
}

// GenerateProductSummary generates the context paragraph after the products table
func GenerateProductSummary(cfg *InfraConfig, sites []SiteInfo) string {
	version := cfg.ClusterVersion
	if version == "" {
		version = "latest"
	}
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "managed"
	}

	if cfg.IsAzure() {
		var vnetClause string
		if cfg.ProvisionedVnetID != "" {
			vnetClause = fmt.Sprintf("deployed into the customer-provisioned VNet (`%s`)", cfg.ProvisionedVnetID)
		} else {
			cidr := cfg.VnetCidr
			if cidr == "" {
				cidr = "10.0.0.0/16"
			}
			vnetClause = fmt.Sprintf("deployed into a PTD-managed VNet (CIDR `%s`)", cidr)
		}
		return fmt.Sprintf("All products are running within a managed AKS cluster (Kubernetes %s) on %s nodes, %s.",
			version, instanceType, vnetClause)
	}

	var vpcClause string
	if cfg.ProvisionedVpcID != "" {
		vpcClause = fmt.Sprintf("deployed into the customer-provisioned VPC (`%s`, CIDR `%s`)", cfg.ProvisionedVpcID, cfg.ProvisionedCidr)
	} else {
		cidr := cfg.VpcCidr
		if cidr == "" {
			cidr = "10.0.0.0/16"
		}
		vpcClause = fmt.Sprintf("deployed into a PTD-managed VPC (CIDR `%s`)", cidr)
	}

	return fmt.Sprintf("All products are running within a managed EKS cluster (Kubernetes %s) on %s nodes, %s.",
		version, instanceType, vpcClause)
}

func bulletList(items []string) string {
	var sb strings.Builder
	for _, item := range items {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	return sb.String()
}
