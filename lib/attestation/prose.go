package attestation

import (
	"fmt"
	"strings"
)

// InfraConfig holds config flags parsed from ptd.yaml that influence prose generation.
// These are parsed separately from the typed config because the typed config
// doesn't fully handle the spec: nesting for all fields.
type InfraConfig struct {
	// VPC
	ProvisionedVpcID string
	ProvisionedCidr  string
	PrivateSubnets   []string
	VpcCidr          string
	VpcAzCount       int

	// Cluster
	ClusterVersion string
	InstanceType   string
	RootDiskSize   int

	// Storage
	FsxMultiAz bool

	// Certificates
	CertValidationEnabled bool
	CertARNProvided       bool
	PrivateZone           bool

	// Features
	KeycloakEnabled              bool
	ExternalDNSEnabled           bool
	PublicLoadBalancer           bool
	SecretsStoreAddonEnabled     bool
	HostedZoneManagementEnabled  bool
	CustomerManagedBastionID     string
	LoadBalancerPerSite          bool

	// Sites
	SiteDomains map[string]string
}

// generatePersistentProse generates narrative text for the persistent stack
func generatePersistentProse(cfg *InfraConfig) string {
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
		break // only show the first domain in the persistent description
	}

	lines = append(lines, "IAM roles and policies for all service components")
	lines = append(lines, "Security groups for RDS, EKS, and internal communication")

	if cfg.CustomerManagedBastionID != "" {
		lines = append(lines, "Integration with customer-managed bastion host for cluster access")
	}

	return "Provisions the foundational infrastructure layer:\n\n" + bulletList(lines)
}

// generatePostgresConfigProse generates narrative text for the postgres-config stack
func generatePostgresConfigProse(_ *InfraConfig) string {
	return `Configures the RDS PostgreSQL instance with application-specific databases and credentials:

` + bulletList([]string{
		"Database users for Grafana and internal services",
		"Dedicated databases with appropriate grants",
		"Passwords generated and stored as KMS-encrypted secrets in Pulumi state",
	})
}

// generateEKSProse generates narrative text for the EKS stack
func generateEKSProse(cfg *InfraConfig) string {
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
		lines = append(lines, "External DNS for automatic Route 53 record management")
	}

	lines = append(lines, "IAM roles mapped to Kubernetes service accounts via IRSA")

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
		lines = append(lines, "Secrets Store CSI Driver for AWS Secrets Manager integration")
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
