package attestation

import (
	"fmt"
	"sort"
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

// ProductDisplayName returns a human-readable name for a product identifier.
func ProductDisplayName(name string) string {
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

// AccountLabel returns the appropriate label for the cloud account identifier.
func AccountLabel(cloud string) string {
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
	VnetCidr            string
	ProvisionedVnetID   string
	ProvisionedVnetName string

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

	// ChronicleEnabled is true when any site configures Chronicle telemetry.
	// Chronicle is optional and frequently not configured, so chronicle-specific
	// prose (e.g. the Azure storage container) is gated on this flag.
	ChronicleEnabled bool

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
		var vpcDesc string
		if cfg.VpcCidr != "" && cfg.VpcAzCount > 0 {
			vpcDesc = fmt.Sprintf("VPC with CIDR `%s` across %d availability zones, with public and private subnets, NAT gateways, and internet gateway",
				cfg.VpcCidr, cfg.VpcAzCount)
		} else if cfg.VpcCidr != "" {
			vpcDesc = fmt.Sprintf("VPC with CIDR `%s`, with public and private subnets, NAT gateways, and internet gateway", cfg.VpcCidr)
		} else {
			vpcDesc = "PTD-managed VPC with public and private subnets, NAT gateways, and internet gateway"
		}
		lines = append(lines, vpcDesc)
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
	}

	lines = append(lines, "IAM roles and policies for all service components")
	lines = append(lines, "Security groups for RDS, EKS, and internal communication")

	if cfg.CustomerManagedBastionID != "" {
		lines = append(lines, "Integration with customer-managed bastion host for cluster access")
	}

	leadIn := "This stack builds the durable foundation that the rest of the deployment relies on: " +
		"the private network, the application database, the storage where each Posit product keeps " +
		"its data, and the security controls that protect them. In plain terms, this is where customer " +
		"data and configuration physically live. Specifically, it provisions:"

	return leadIn + "\n\n" + BulletList(lines)
}

func generateAzurePersistentProse(cfg *InfraConfig) string {
	var lines []string

	if cfg.ProvisionedVnetID != "" {
		lines = append(lines, fmt.Sprintf(
			"Integration with customer-provisioned VNet (`%s`)", cfg.ProvisionedVnetID))
	} else if cfg.ProvisionedVnetName != "" {
		lines = append(lines, fmt.Sprintf(
			"Integration with customer-provisioned VNet (`%s`)", cfg.ProvisionedVnetName))
	} else if cfg.VnetCidr != "" {
		lines = append(lines, fmt.Sprintf("A private network (VNet) with CIDR `%s`, divided into separate subnets for the application, database, and file storage", cfg.VnetCidr))
	} else {
		lines = append(lines, "A private network (VNet) divided into separate subnets for the application, database, and file storage")
	}

	// Databases (application metadata for all three products).
	lines = append(lines, "An Azure Database for PostgreSQL Flexible Server. This database holds the "+
		"application metadata for Posit Connect, Posit Workbench, and Posit Package Manager "+
		"(for example: user accounts, content listings, schedules, and package index records)")

	// Storage accounts.
	lines = append(lines, "A shared Azure Storage account (created during bootstrap) that holds both the "+
		"Pulumi state files and the observability data described below")
	lines = append(lines, "A separate premium Azure Storage account used only to host the Azure Files (NFS) "+
		"share for Posit Package Manager")

	// Observability containers in the shared account.
	lines = append(lines, "A `loki` blob container in the shared storage account, holding aggregated logs")
	lines = append(lines, "A `mimir-blocks` blob container in the shared storage account, holding metrics. "+
		"This container is created by the Mimir application at runtime (it is not provisioned by Pulumi)")
	if cfg.ChronicleEnabled {
		lines = append(lines, "A `chronicle` blob container in the shared storage account, holding Chronicle "+
			"usage telemetry")
	}

	// File storage for products.
	lines = append(lines, "An Azure Files (NFS) share for Posit Package Manager. This is where Package Manager "+
		"stores the actual package files it serves")
	lines = append(lines, "Azure NetApp Files volumes (one NFS volume per site, per product) for Posit Connect "+
		"published content and Posit Workbench user home directories")

	// Secrets, identity, and network controls.
	lines = append(lines, "An Azure Key Vault for securely storing secrets such as database passwords and keys")
	lines = append(lines, "Managed identities that let each service authenticate to Azure without storing "+
		"long-lived credentials")
	lines = append(lines, "Network security groups that restrict traffic to the database, the Kubernetes "+
		"cluster, and internal communication")

	leadIn := "This stack builds the durable foundation that the rest of the deployment relies on: " +
		"the private network, the application database, the storage where each Posit product keeps " +
		"its data, and the security controls that protect them. In plain terms, this is where customer " +
		"data and configuration physically live. Specifically, it provisions:"

	return leadIn + "\n\n" + BulletList(lines)
}

// generatePostgresConfigProse generates narrative text for the postgres-config stack
func generatePostgresConfigProse(cfg *InfraConfig) string {
	dbType := "RDS PostgreSQL"
	secretsNote := "Passwords generated and stored as KMS-encrypted secrets in Pulumi state"
	if cfg.IsAzure() {
		dbType = "Azure PostgreSQL Flexible Server"
		secretsNote = "Passwords generated and stored as Key Vault-encrypted secrets in Pulumi state"
	}

	leadIn := fmt.Sprintf("This stack prepares the %s for use. It creates a separate database and "+
		"login for each component that needs one, so that the monitoring tools and the Posit products "+
		"each have their own isolated space and credentials. Specifically, it configures:", dbType)

	return leadIn + "\n\n" +
		BulletList([]string{
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

	leadIn := "This stack creates the managed Kubernetes cluster that runs the Posit products and the " +
		"supporting services, along with the pool of virtual machines that provide its computing power. " +
		"Kubernetes is the industry-standard system for running and coordinating containerized " +
		"applications. Specifically, it provisions:"

	return leadIn + "\n\n" + BulletList(lines)
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

	leadIn := "This stack creates the managed Kubernetes cluster that runs the Posit products and the " +
		"supporting services, along with the pools of virtual machines that provide its computing power. " +
		"Kubernetes is the industry-standard system for running and coordinating containerized " +
		"applications. Specifically, it provisions:"

	return leadIn + "\n\n" + BulletList(lines)
}

// generateClustersProse generates narrative text for the clusters stack
func generateClustersProse(cfg *InfraConfig) string {
	var lines []string

	// Namespaces are logical partitions inside the cluster. Enumerate the ones
	// this stack creates, with a short purpose for each. The remaining
	// observability namespaces (loki, mimir, alloy, kube-state-metrics) are
	// created by the helm stack and are described there. The `grafana`
	// namespace is the exception: on AWS it is created here in the clusters
	// stack (see lib/steps/clusters_aws.go), so it is enumerated below for the
	// AWS branch only; on Azure it is created by the helm stack.
	lines = append(lines, "The `posit-team` namespace, which runs the Posit product workloads (Connect, Workbench, Package Manager, and their TeamSites)")
	lines = append(lines, "The `posit-team-system` namespace, which runs the Team Operator (the component that manages the lifecycle of the Posit products)")
	lines = append(lines, "The `helm-controller` namespace, which runs the in-cluster controller that installs Helm charts in response to `HelmChart` custom resources. These custom resources live in this namespace but can target other namespaces, which is why some objects appear grouped here")
	lines = append(lines, "The `cert-manager` namespace, which manages the lifecycle of TLS certificates (used when Let's Encrypt is configured)")
	lines = append(lines, "The `traefik` namespace, which runs the ingress controller that routes incoming web traffic to the right product")
	if !cfg.IsAzure() {
		lines = append(lines, "The `grafana` namespace, which hosts the Grafana observability stack (the dashboards and data sources for logs and metrics)")
	}
	if cfg.IsAzure() {
		lines = append(lines, "CoreDNS customization (`coredns-custom`) in the AKS-managed `kube-system` namespace; PTD only patches DNS configuration there and does not otherwise manage that namespace")
	}

	lines = append(lines, "Calico network policies restricting inter-namespace traffic")
	lines = append(lines, "Team Operator deployment (manages Posit product lifecycle)")

	if cfg.PublicLoadBalancer {
		lines = append(lines, "Traefik ingress controller with public-facing load balancer")
	} else {
		lines = append(lines, "Traefik ingress controller with internal-only load balancer")
	}

	// On AWS, external-dns is created in the clusters stack and is flag-driven.
	// On Azure, external-dns is created in the helm stack and is described there.
	if !cfg.IsAzure() {
		if !cfg.ExternalDNSEnabled {
			lines = append(lines, "External DNS disabled (per customer configuration)")
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

	leadIn := "This stack takes the empty Kubernetes cluster and sets up the shared groundwork that the " +
		"Posit products need before they can be installed: it divides the cluster into named areas " +
		"(namespaces), installs the controllers that manage applications and certificates, configures the " +
		"component that routes incoming web traffic, and applies network rules that limit how those areas " +
		"can talk to each other. Specifically, it configures:"

	return leadIn + "\n\n" + BulletList(lines)
}

// generateHelmProse generates narrative text for the helm stack
func generateHelmProse(cfg *InfraConfig) string {
	lines := []string{
		"Grafana, Loki, and Mimir for observability and log aggregation. These components run in their own namespaces (`grafana`, `loki`, and `mimir`)",
		"Grafana Alloy for metrics and log collection, in the `alloy` namespace, together with kube-state-metrics in the `kube-state-metrics` namespace",
		"cert-manager for TLS certificate lifecycle",
	}

	// On Azure, external-dns is deployed by this stack, unconditionally. The
	// deploy does not consult the enable flag, so there is no "disabled" branch.
	// (On AWS, external-dns is part of the clusters stack and is described there.)
	if cfg.IsAzure() {
		lines = append(lines, "External DNS for automatic Azure DNS record management, in the `external-dns` "+
			"namespace. Its `azure-config-file` secret holds the workload-identity configuration that lets "+
			"it update Azure DNS records on behalf of the cluster")
	}

	if cfg.SecretsStoreAddonEnabled {
		if cfg.IsAzure() {
			lines = append(lines, "Secrets Store CSI Driver for Azure Key Vault integration")
		} else {
			lines = append(lines, "Secrets Store CSI Driver for AWS Secrets Manager integration")
		}
	}

	leadIn := "This stack installs the shared supporting services that the platform depends on, packaged as " +
		"Helm charts (a standard format for deploying applications onto Kubernetes). These services provide " +
		"monitoring and log collection, manage TLS certificates, and—on Azure—keep public DNS records up to " +
		"date. Specifically, it deploys:"

	return leadIn + "\n\n" + BulletList(lines)
}

// generateSitesProse generates narrative text for the sites stack
func generateSitesProse(cfg *InfraConfig) string {
	lines := []string{}

	// Collect site names in a stable order so the per-site config references are
	// deterministic.
	siteNames := make([]string, 0, len(cfg.SiteDomains))
	for name := range cfg.SiteDomains {
		siteNames = append(siteNames, name)
	}
	sort.Strings(siteNames)

	for _, name := range siteNames {
		domain := cfg.SiteDomains[name]
		lines = append(lines, fmt.Sprintf("TeamSite custom resource for the `%s` site at `%s`, as declared in `site_%s/site.yaml`", name, domain, name))
	}

	lines = append(lines, "Posit Connect, Workbench, and Package Manager instances at the versions and settings declared in each site's `site_<name>/site.yaml`")
	if cfg.ChronicleEnabled {
		lines = append(lines, "Chronicle observability agent and sidecar")
	}
	lines = append(lines, "Ingress resources routing traffic from Traefik to each product")

	if cfg.PrivateZone || cfg.HostedZoneManagementEnabled {
		lines = append(lines, "DNS records in the hosted zone for each product subdomain")
	}

	leadIn := "This stack deploys the Posit products themselves into the prepared cluster, one configured " +
		"site at a time. Each site corresponds to a `site_<name>/site.yaml` file that declares which " +
		"products run, their versions, and their settings; this stack turns those declarations into " +
		"running applications reachable at their web addresses. Specifically, it deploys:"

	return leadIn + "\n\n" + BulletList(lines)
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
		} else if cfg.ProvisionedVnetName != "" {
			vnetClause = fmt.Sprintf("deployed into the customer-provisioned VNet (`%s`)", cfg.ProvisionedVnetName)
		} else if cfg.VnetCidr != "" {
			vnetClause = fmt.Sprintf("deployed into a PTD-managed VNet (CIDR `%s`)", cfg.VnetCidr)
		} else {
			vnetClause = "deployed into a PTD-managed VNet"
		}
		return fmt.Sprintf("All products are running within a managed AKS cluster (Kubernetes %s) on %s nodes, %s.",
			version, instanceType, vnetClause)
	}

	var vpcClause string
	if cfg.ProvisionedVpcID != "" {
		vpcClause = fmt.Sprintf("deployed into the customer-provisioned VPC (`%s`, CIDR `%s`)", cfg.ProvisionedVpcID, cfg.ProvisionedCidr)
	} else if cfg.VpcCidr != "" {
		vpcClause = fmt.Sprintf("deployed into a PTD-managed VPC (CIDR `%s`)", cfg.VpcCidr)
	} else {
		vpcClause = "deployed into a PTD-managed VPC"
	}

	return fmt.Sprintf("All products are running within a managed EKS cluster (Kubernetes %s) on %s nodes, %s.",
		version, instanceType, vpcClause)
}

// BulletList formats a slice of strings as a markdown bullet list.
func BulletList(items []string) string {
	var sb strings.Builder
	for _, item := range items {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	return sb.String()
}
