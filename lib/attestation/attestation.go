package attestation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	ptdaws "github.com/posit-dev/ptd/lib/aws"
	ptdazure "github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/consts"
	"github.com/posit-dev/ptd/lib/customization"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/pulumistate"
	"github.com/posit-dev/ptd/lib/secrets"
	"github.com/posit-dev/ptd/lib/types"
	"gopkg.in/yaml.v3"
)

// AttestationData contains all collected information about a workload deployment
type AttestationData struct {
	TargetName         string                 `json:"target_name"`
	Title              string                 `json:"title,omitempty"`
	CloudProvider      string                 `json:"cloud_provider"`
	Region             string                 `json:"region"`
	AccountID          string                 `json:"account_id"`
	Profile            string                 `json:"profile"`
	GeneratedAt        time.Time              `json:"generated_at"`
	Sites              []SiteInfo             `json:"sites"`
	Stacks             []StackSummary         `json:"stacks"`
	BootstrapResources []ResourceDetail       `json:"bootstrap_resources"`
	CustomSteps        []CustomStepInfo       `json:"custom_steps"`
	ClusterConfig      map[string]interface{} `json:"cluster_config"`
	Infra              *InfraConfig           `json:"infra"`
	ProductSummary     string                 `json:"product_summary"`
	StateBackendURL    string                 `json:"state_backend_url,omitempty"`
	RawStateFiles      map[string][]byte      `json:"-"`
}

// DisplayTitle returns the title to render at the top of the attestation
// document. When Title is set (e.g. via the --title CLI flag) it is used
// verbatim; otherwise the default "Installation Confirmation — <target>" is
// returned.
func (a *AttestationData) DisplayTitle() string {
	if a.Title != "" {
		return a.Title
	}
	return fmt.Sprintf("%s — %s", docTitle, a.TargetName)
}

// SiteInfo contains information extracted from a site.yaml file
type SiteInfo struct {
	SiteName string        `json:"site_name"`
	Domain   string        `json:"domain"`
	Products []ProductInfo `json:"products"`
}

// ProductInfo contains information about a deployed product
type ProductInfo struct {
	Name         string      `json:"name"`
	Image        string      `json:"image"`
	Version      string      `json:"version"`
	Replicas     int         `json:"replicas"`
	DomainPrefix string      `json:"domain_prefix,omitempty"`
	Auth         *AuthDetail `json:"auth,omitempty"`
}

// AuthDetail contains authentication configuration for a product
type AuthDetail struct {
	Type     string `json:"type"`
	Issuer   string `json:"issuer,omitempty"`
	ClientID string `json:"client_id,omitempty"`
}

// StackSummary contains summary information from a Pulumi stack state file
type StackSummary struct {
	ProjectName       string             `json:"project_name"`
	StackName         string             `json:"stack_name"`
	Purpose           string             `json:"purpose"`
	Timestamp         time.Time          `json:"timestamp"`
	PulumiVersion     string             `json:"pulumi_version"`
	ResourceCount     int                `json:"resource_count"`
	ResourceTypes     []string           `json:"resource_types"`
	Resources         []ResourceDetail   `json:"resources"`
	KubernetesObjects []KubernetesObject `json:"kubernetes_objects"`
	StateKey          string             `json:"state_key"`
}

// KubernetesObject describes a Kubernetes object managed in a Pulumi stack,
// captured from the resource's state outputs for the Kubernetes appendix.
type KubernetesObject struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// DisplayNamespace returns the namespace, or "—" for cluster-scoped objects.
func (k KubernetesObject) DisplayNamespace() string {
	if k.Namespace == "" {
		return "—"
	}
	return k.Namespace
}

// DisplayUID returns the cluster-assigned UID. Helm releases wrap multiple
// objects and carry no single UID, so they render as "— (release)".
func (k KubernetesObject) DisplayUID() string {
	if k.UID != "" {
		return k.UID
	}
	if k.Kind == "Helm Release" {
		return "— (release)"
	}
	return "—"
}

// ResourceDetail describes a single managed resource within a Pulumi stack,
// used to render the full resource appendix.
type ResourceDetail struct {
	Name string `json:"name"` // logical name (last segment of the URN)
	Type string `json:"type"` // Pulumi resource type token
	ID   string `json:"id"`   // cloud provider resource ID (empty for logical/component resources)
	URN  string `json:"urn"`  // full Pulumi URN, used as the identifier when no cloud ID exists
}

// DisplayID returns the identifier to show in the resource inventory: the cloud
// provider resource ID when one exists, otherwise the full Pulumi URN. Some
// resources (e.g. component resources) carry no ID, and others report a literal
// "none" (e.g. random.RandomPassword); in both cases the URN is the stable
// identifier.
func (r ResourceDetail) DisplayID() string {
	if r.ID == "" || r.ID == "none" {
		return r.URN
	}
	return r.ID
}

// stackPurposes returns human-readable descriptions for standard stack types, keyed by cloud
var stackPurposes = map[string]map[string]string{
	"aws": {
		"persistent":      "VPC, RDS PostgreSQL, S3 buckets, FSx storage, IAM roles, TLS certificates, DNS zones, bastion host",
		"postgres-config": "PostgreSQL database users, databases, and grants",
		"eks":             "EKS cluster, managed node groups, OIDC provider, storage classes",
		"clusters":        "Kubernetes namespaces, network policies, IAM-to-K8s role bindings, Team Operator, Traefik",
		"helm":            "Helm chart deployments: monitoring (Loki, Grafana, Mimir, Alloy), cert-manager, Secrets Store CSI",
		"sites":           "Posit product deployments (TeamSite CRDs), ingress resources, site-specific configuration",
	},
	"azure": {
		"persistent":      "VNet, Azure PostgreSQL, Storage accounts, NetApp Files, Key Vault, managed identities, NSGs",
		"postgres-config": "PostgreSQL database users, databases, and grants",
		"aks":             "AKS cluster, node pools, managed identity, storage classes",
		"acr-cache":       "Azure Container Registry cache rules for container image pull-through",
		"clusters":        "Kubernetes namespaces, network policies, workload identity bindings, Team Operator, Traefik",
		"helm":            "Helm chart deployments: monitoring (Loki, Grafana, Mimir, Alloy), cert-manager, Secrets Store CSI",
		"sites":           "Posit product deployments (TeamSite CRDs), ingress resources, site-specific configuration",
	},
}

// StepNameFromProjectName extracts the step name from a Pulumi project name like "ptd-aws-workload-persistent".
func StepNameFromProjectName(projectName string) string {
	parts := strings.SplitN(projectName, "-", 4)
	if len(parts) >= 4 {
		return parts[3]
	}
	return projectName
}

// StepNameFromProject extracts the step name from this stack's project name.
func (s *StackSummary) StepNameFromProject() string {
	return StepNameFromProjectName(s.ProjectName)
}

func purposeForStack(projectName string, cloud string) string {
	purposes, ok := stackPurposes[cloud]
	if !ok {
		purposes = stackPurposes["aws"]
	}
	for suffix, purpose := range purposes {
		if strings.HasSuffix(projectName, suffix) {
			return purpose
		}
	}
	return ""
}

// CustomStepInfo contains information about a custom deployment step
type CustomStepInfo struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Path          string `json:"path"`
	InsertAfter   string `json:"insert_after,omitempty"`
	InsertBefore  string `json:"insert_before,omitempty"`
	ProxyRequired bool   `json:"proxy_required"`
	Enabled       bool   `json:"enabled"`
}

// SiteYAML represents the structure of a site.yaml file
type SiteYAML struct {
	Spec SiteSpec `yaml:"spec"`
}

// SiteSpec contains the specification section of a site.yaml
type SiteSpec struct {
	Domain         string           `yaml:"domain"`
	Connect        *ProductConfig   `yaml:"connect,omitempty"`
	Workbench      *ProductConfig   `yaml:"workbench,omitempty"`
	PackageManager *ProductConfig   `yaml:"packageManager,omitempty"`
	Chronicle      *ChronicleConfig `yaml:"chronicle,omitempty"`
}

// ProductConfig contains configuration for a product deployment
type ProductConfig struct {
	Image        string      `yaml:"image"`
	Replicas     int         `yaml:"replicas,omitempty"`
	DomainPrefix string      `yaml:"domainPrefix,omitempty"`
	Auth         *AuthConfig `yaml:"auth,omitempty"`
}

// AuthConfig contains authentication configuration
type AuthConfig struct {
	Type     string `yaml:"type"`
	Issuer   string `yaml:"issuer,omitempty"`
	ClientID string `yaml:"clientId,omitempty"`
}

// ChronicleConfig contains Chronicle-specific configuration
type ChronicleConfig struct {
	Image      string `yaml:"image"`
	AgentImage string `yaml:"agentImage,omitempty"`
}

// Collect gathers attestation data for a given target and workload path
func Collect(ctx context.Context, target types.Target, workloadPath string) (*AttestationData, error) {
	// Get credentials
	creds, err := target.Credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}

	// Load ptd.yaml config
	config, err := helpers.ConfigForTarget(target)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize attestation data
	attestation := &AttestationData{
		TargetName:      target.Name(),
		CloudProvider:   string(target.CloudProvider()),
		Region:          target.Region(),
		AccountID:       creds.AccountID(),
		GeneratedAt:     time.Now().UTC(),
		Sites:           []SiteInfo{},
		Stacks:          []StackSummary{},
		CustomSteps:     []CustomStepInfo{},
		ClusterConfig:   make(map[string]interface{}),
		StateBackendURL: target.PulumiBackendUrl(),
	}

	// Extract cluster config and profile from ptd.yaml
	switch cfg := config.(type) {
	case types.AWSWorkloadConfig:
		attestation.Profile = cfg.Profile
		if len(cfg.Clusters) > 0 {
			clustersMap := make(map[string]interface{})
			for name, cluster := range cfg.Clusters {
				clusterMap := map[string]interface{}{
					"cluster_name":       cluster.Spec.ClusterName,
					"node_instance_type": cluster.Spec.NodeInstanceType,
					"k8s_version":        cluster.Spec.K8sVersion,
				}
				clustersMap[name] = clusterMap
			}
			attestation.ClusterConfig = clustersMap
		}
	case types.AzureWorkloadConfig:
		attestation.AccountID = cfg.SubscriptionID
		if len(cfg.Clusters) > 0 {
			clustersMap := make(map[string]interface{})
			for name, cluster := range cfg.Clusters {
				clusterMap := map[string]interface{}{
					"kubernetes_version":             cluster.KubernetesVersion,
					"system_node_pool_instance_type": cluster.SystemNodePoolInstanceType,
				}
				clustersMap[name] = clusterMap
			}
			attestation.ClusterConfig = clustersMap
		}
	}

	// Parse infrastructure config from ptd.yaml for prose generation
	infraCfg, err := parseInfraConfigFromPtdYaml(workloadPath)
	if err != nil {
		infraCfg = &InfraConfig{SiteDomains: make(map[string]string)}
	}
	attestation.Infra = infraCfg

	// Read site.yaml files, enriched with domain from ptd.yaml
	sites, err := collectSites(workloadPath, infraCfg.SiteDomains)
	if err != nil {
		return nil, fmt.Errorf("failed to collect sites: %w", err)
	}
	attestation.Sites = sites

	// Read customizations/manifest.yaml
	customSteps, err := collectCustomSteps(workloadPath)
	if err != nil {
		// Custom steps are optional, so we just log the error
		// but don't fail the entire collection
		fmt.Fprintf(os.Stderr, "Warning: failed to collect custom steps: %v\n", err)
	} else {
		attestation.CustomSteps = customSteps
	}

	// List and download Pulumi state files from S3
	stacks, rawStateFiles, err := collectStackSummaries(ctx, creds, target)
	if err != nil {
		return nil, fmt.Errorf("failed to collect stack summaries: %w", err)
	}
	attestation.RawStateFiles = rawStateFiles
	// Set purpose descriptions: custom step descriptions take priority
	customDescriptions := make(map[string]string)
	for _, cs := range attestation.CustomSteps {
		customDescriptions[cs.Name] = cs.Description
	}
	for i := range stacks {
		stepName := stacks[i].StepNameFromProject()
		if desc, ok := customDescriptions[stepName]; ok {
			stacks[i].Purpose = desc
		} else if p := purposeForStack(stacks[i].ProjectName, infraCfg.Cloud); p != "" {
			stacks[i].Purpose = p
		}
	}
	attestation.Stacks = stacks

	// The bootstrap step runs imperatively and produces no Pulumi state, so its
	// resources are synthesized from PTD's naming conventions.
	attestation.BootstrapResources = collectBootstrapResources(target)

	// Generate product summary paragraph
	attestation.ProductSummary = GenerateProductSummary(infraCfg, sites)

	return attestation, nil
}

// defaultPrefix returns the explicit prefix if set, otherwise the default
func defaultPrefix(explicit string, fallback string) string {
	if explicit != "" {
		return explicit
	}
	return fallback
}

// cleanVersion strips OS prefixes like "ubuntu2204-" from image version tags
func cleanVersion(image string) string {
	parts := strings.Split(image, ":")
	if len(parts) < 2 {
		return image
	}
	version := parts[len(parts)-1]
	// Strip common OS prefixes
	for _, prefix := range []string{"ubuntu2204-", "ubuntu2404-", "centos7-", "rhel9-"} {
		version = strings.TrimPrefix(version, prefix)
	}
	return version
}

// parseInfraConfigFromPtdYaml extracts infrastructure configuration from ptd.yaml
// using a flexible YAML structure that handles the spec: nesting.
// Supports both AWS and Azure workload configs.
func parseInfraConfigFromPtdYaml(workloadPath string) (*InfraConfig, error) {
	ptdYamlPath := filepath.Join(workloadPath, "ptd.yaml")
	data, err := os.ReadFile(ptdYamlPath)
	if err != nil {
		return nil, err
	}

	// Detect cloud provider from kind field
	var header struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil, err
	}

	switch header.Kind {
	case "AzureWorkloadConfig":
		return parseAzureInfraConfig(data)
	default:
		return parseAWSInfraConfig(data)
	}
}

func parseAWSInfraConfig(data []byte) (*InfraConfig, error) {
	var raw struct {
		Spec struct {
			ProvisionedVpc *struct {
				VpcID          string   `yaml:"vpc_id"`
				Cidr           string   `yaml:"cidr"`
				PrivateSubnets []string `yaml:"private_subnets"`
			} `yaml:"provisioned_vpc"`
			VpcCidr                     string `yaml:"vpc_cidr"`
			VpcAzCount                  int    `yaml:"vpc_az_count"`
			FsxOpenzfsMultiAz           bool   `yaml:"fsx_openzfs_multi_az"`
			KeycloakEnabled             bool   `yaml:"keycloak_enabled"`
			ExternalDNSEnabled          bool   `yaml:"external_dns_enabled"`
			PublicLoadBalancer          bool   `yaml:"public_load_balancer"`
			SecretsStoreAddonEnabled    *bool  `yaml:"secrets_store_addon_enabled"`
			HostedZoneManagementEnabled *bool  `yaml:"hosted_zone_management_enabled"`
			CustomerManagedBastionId    string `yaml:"customer_managed_bastion_id"`
			LoadBalancerPerSite         bool   `yaml:"load_balancer_per_site"`
			Clusters                    map[string]struct {
				Spec struct {
					ClusterVersion string `yaml:"cluster_version"`
					MpInstanceType string `yaml:"mp_instance_type"`
					RootDiskSize   int    `yaml:"root_disk_size"`
				} `yaml:"spec"`
			} `yaml:"clusters"`
			Sites map[string]struct {
				Spec struct {
					Domain                       string `yaml:"domain"`
					PrivateZone                  bool   `yaml:"private_zone"`
					CertificateValidationEnabled bool   `yaml:"certificate_validation_enabled"`
					CertificateARN               string `yaml:"certificate_arn"`
				} `yaml:"spec"`
			} `yaml:"sites"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cfg := &InfraConfig{
		Cloud:                    "aws",
		VpcCidr:                  raw.Spec.VpcCidr,
		VpcAzCount:               raw.Spec.VpcAzCount,
		FsxMultiAz:               raw.Spec.FsxOpenzfsMultiAz,
		KeycloakEnabled:          raw.Spec.KeycloakEnabled,
		ExternalDNSEnabled:       raw.Spec.ExternalDNSEnabled,
		PublicLoadBalancer:       raw.Spec.PublicLoadBalancer,
		SecretsStoreAddonEnabled: raw.Spec.SecretsStoreAddonEnabled == nil || *raw.Spec.SecretsStoreAddonEnabled,
		CustomerManagedBastionID: raw.Spec.CustomerManagedBastionId,
		LoadBalancerPerSite:      raw.Spec.LoadBalancerPerSite,
		SiteDomains:              make(map[string]string),
	}

	if raw.Spec.HostedZoneManagementEnabled != nil {
		cfg.HostedZoneManagementEnabled = *raw.Spec.HostedZoneManagementEnabled
	}

	if raw.Spec.ProvisionedVpc != nil {
		cfg.ProvisionedVpcID = raw.Spec.ProvisionedVpc.VpcID
		cfg.ProvisionedCidr = raw.Spec.ProvisionedVpc.Cidr
		cfg.PrivateSubnets = raw.Spec.ProvisionedVpc.PrivateSubnets
	}

	for _, cluster := range raw.Spec.Clusters {
		cfg.ClusterVersion = cluster.Spec.ClusterVersion
		cfg.InstanceType = cluster.Spec.MpInstanceType
		cfg.RootDiskSize = cluster.Spec.RootDiskSize
	}

	for name, site := range raw.Spec.Sites {
		if site.Spec.Domain != "" {
			cfg.SiteDomains[name] = site.Spec.Domain
		}
		cfg.PrivateZone = cfg.PrivateZone || site.Spec.PrivateZone
		cfg.CertValidationEnabled = cfg.CertValidationEnabled || site.Spec.CertificateValidationEnabled
		cfg.CertARNProvided = cfg.CertARNProvided || site.Spec.CertificateARN != ""
	}

	return cfg, nil
}

func parseAzureInfraConfig(data []byte) (*InfraConfig, error) {
	var raw struct {
		Spec struct {
			SubscriptionID string `yaml:"subscription_id"`
			TenantID       string `yaml:"tenant_id"`
			Region         string `yaml:"region"`
			Network        struct {
				VnetCidr            string `yaml:"vnet_cidr"`
				ProvisionedVnetID   string `yaml:"provisioned_vnet_id"`
				ProvisionedVnetName string `yaml:"provisioned_vnet_name"`
				VnetRsgName         string `yaml:"vnet_rsg_name"`
			} `yaml:"network"`
			KeycloakEnabled          bool `yaml:"keycloak_enabled"`
			ExternalDNSEnabled       bool `yaml:"external_dns_enabled"`
			SecretsStoreAddonEnabled bool `yaml:"secrets_store_addon_enabled"`
			Clusters                 map[string]struct {
				KubernetesVersion          string `yaml:"kubernetes_version"`
				SystemNodePoolInstanceType string `yaml:"system_node_pool_instance_type"`
				UserNodePools              []struct {
					VMSize string `yaml:"vm_size"`
				} `yaml:"user_node_pools"`
			} `yaml:"clusters"`
			Sites map[string]struct {
				Spec struct {
					Domain string `yaml:"domain"`
				} `yaml:"spec"`
			} `yaml:"sites"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cfg := &InfraConfig{
		Cloud:                    "azure",
		VnetCidr:                 raw.Spec.Network.VnetCidr,
		ProvisionedVnetID:        raw.Spec.Network.ProvisionedVnetID,
		ProvisionedVnetName:      raw.Spec.Network.ProvisionedVnetName,
		KeycloakEnabled:          raw.Spec.KeycloakEnabled,
		ExternalDNSEnabled:       raw.Spec.ExternalDNSEnabled,
		SecretsStoreAddonEnabled: raw.Spec.SecretsStoreAddonEnabled,
		SiteDomains:              make(map[string]string),
	}

	for _, cluster := range raw.Spec.Clusters {
		cfg.ClusterVersion = cluster.KubernetesVersion
		cfg.InstanceType = cluster.SystemNodePoolInstanceType
		if len(cluster.UserNodePools) > 0 {
			cfg.InstanceType = cluster.UserNodePools[0].VMSize
		}
	}

	for name, site := range raw.Spec.Sites {
		if site.Spec.Domain != "" {
			cfg.SiteDomains[name] = site.Spec.Domain
		}
	}

	return cfg, nil
}

// collectSites scans for site_*/site.yaml files and extracts information
func collectSites(workloadPath string, siteDomains map[string]string) ([]SiteInfo, error) {
	var sites []SiteInfo

	// Find all site_* directories
	pattern := filepath.Join(workloadPath, "site_*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob site directories: %w", err)
	}

	for _, siteDir := range matches {
		dirName := filepath.Base(siteDir)
		siteName := strings.TrimPrefix(dirName, "site_")
		siteYamlPath := filepath.Join(siteDir, "site.yaml")

		// Check if site.yaml exists
		if _, err := os.Stat(siteYamlPath); os.IsNotExist(err) {
			continue
		}

		// Read and parse site.yaml
		data, err := os.ReadFile(siteYamlPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", siteYamlPath, err)
		}

		var siteYAML SiteYAML
		if err := yaml.Unmarshal(data, &siteYAML); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", siteYamlPath, err)
		}

		// Domain: prefer ptd.yaml, fall back to site.yaml
		domain := siteYAML.Spec.Domain
		if d, ok := siteDomains[siteName]; ok && d != "" {
			domain = d
		}

		// Extract site information
		siteInfo := SiteInfo{
			SiteName: siteName,
			Domain:   domain,
			Products: []ProductInfo{},
		}

		// Extract product information
		if siteYAML.Spec.Connect != nil {
			p := ProductInfo{
				Name:         "connect",
				Image:        siteYAML.Spec.Connect.Image,
				Version:      cleanVersion(siteYAML.Spec.Connect.Image),
				Replicas:     siteYAML.Spec.Connect.Replicas,
				DomainPrefix: defaultPrefix(siteYAML.Spec.Connect.DomainPrefix, "connect"),
			}
			if siteYAML.Spec.Connect.Auth != nil {
				p.Auth = &AuthDetail{
					Type:   siteYAML.Spec.Connect.Auth.Type,
					Issuer: siteYAML.Spec.Connect.Auth.Issuer,
				}
			}
			siteInfo.Products = append(siteInfo.Products, p)
		}

		if siteYAML.Spec.Workbench != nil {
			p := ProductInfo{
				Name:         "workbench",
				Image:        siteYAML.Spec.Workbench.Image,
				Version:      cleanVersion(siteYAML.Spec.Workbench.Image),
				Replicas:     siteYAML.Spec.Workbench.Replicas,
				DomainPrefix: defaultPrefix(siteYAML.Spec.Workbench.DomainPrefix, "workbench"),
			}
			if siteYAML.Spec.Workbench.Auth != nil {
				p.Auth = &AuthDetail{
					Type:   siteYAML.Spec.Workbench.Auth.Type,
					Issuer: siteYAML.Spec.Workbench.Auth.Issuer,
				}
			}
			siteInfo.Products = append(siteInfo.Products, p)
		}

		if siteYAML.Spec.PackageManager != nil {
			siteInfo.Products = append(siteInfo.Products, ProductInfo{
				Name:         "package-manager",
				Image:        siteYAML.Spec.PackageManager.Image,
				Version:      cleanVersion(siteYAML.Spec.PackageManager.Image),
				Replicas:     siteYAML.Spec.PackageManager.Replicas,
				DomainPrefix: defaultPrefix(siteYAML.Spec.PackageManager.DomainPrefix, "packagemanager"),
			})
		}

		if siteYAML.Spec.Chronicle != nil {
			siteInfo.Products = append(siteInfo.Products, ProductInfo{
				Name:    "chronicle",
				Image:   siteYAML.Spec.Chronicle.Image,
				Version: cleanVersion(siteYAML.Spec.Chronicle.Image),
			})
			if siteYAML.Spec.Chronicle.AgentImage != "" {
				siteInfo.Products = append(siteInfo.Products, ProductInfo{
					Name:    "chronicle-agent",
					Image:   siteYAML.Spec.Chronicle.AgentImage,
					Version: cleanVersion(siteYAML.Spec.Chronicle.AgentImage),
				})
			}
		}

		sites = append(sites, siteInfo)
	}

	return sites, nil
}

// collectCustomSteps reads the customizations/manifest.yaml file
func collectCustomSteps(workloadPath string) ([]CustomStepInfo, error) {
	manifestPath, found := customization.FindManifest(workloadPath)
	if !found {
		return []CustomStepInfo{}, nil
	}

	manifest, err := customization.LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	var customSteps []CustomStepInfo
	for _, step := range manifest.CustomSteps {
		customSteps = append(customSteps, CustomStepInfo{
			Name:          step.Name,
			Description:   step.Description,
			Path:          step.Path,
			InsertAfter:   step.InsertAfter,
			InsertBefore:  step.InsertBefore,
			ProxyRequired: step.ProxyRequired,
			Enabled:       step.IsEnabled(),
		})
	}

	return customSteps, nil
}

// DownloadStateFiles retrieves all Pulumi state files for a target from cloud storage.
// Returns a map of state key to raw JSON bytes.
func DownloadStateFiles(ctx context.Context, creds types.Credentials, target types.Target) (map[string][]byte, error) {
	var keys []string
	var fetchFn func(ctx context.Context, key string) ([]byte, error)

	switch target.CloudProvider() {
	case types.AWS:
		awsCreds, err := ptdaws.OnlyAwsCredentials(creds)
		if err != nil {
			return nil, fmt.Errorf("failed to get AWS credentials: %w", err)
		}
		bucketName := target.StateBucketName()
		region := target.Region()

		keys, err = ptdaws.ListStateFiles(ctx, awsCreds, region, bucketName)
		if err != nil {
			return nil, fmt.Errorf("failed to list state files: %w", err)
		}
		fetchFn = func(ctx context.Context, key string) ([]byte, error) {
			return ptdaws.GetStateFile(ctx, awsCreds, region, bucketName, key)
		}

	case types.Azure:
		azureCreds, err := ptdazure.OnlyAzureCredentials(creds)
		if err != nil {
			return nil, fmt.Errorf("failed to get Azure credentials: %w", err)
		}
		azureTarget, ok := target.(interface {
			BlobStorageName() string
		})
		if !ok {
			return nil, fmt.Errorf("Azure target does not implement BlobStorageName()")
		}
		reader, err := ptdazure.NewBlobStateReader(azureCreds, target.StateBucketName(), azureTarget.BlobStorageName())
		if err != nil {
			return nil, fmt.Errorf("failed to create blob state reader: %w", err)
		}

		keys, err = reader.ListStateFiles(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list state files: %w", err)
		}
		fetchFn = func(ctx context.Context, key string) ([]byte, error) {
			return reader.GetStateFile(ctx, key)
		}

	default:
		return nil, fmt.Errorf("unsupported cloud provider: %s", target.CloudProvider())
	}

	var wg sync.WaitGroup
	results := make([][]byte, len(keys))
	errors := make([]error, len(keys))

	for i, key := range keys {
		wg.Add(1)
		go func(idx int, stateKey string) {
			defer wg.Done()

			data, err := fetchFn(ctx, stateKey)
			if err != nil {
				errors[idx] = err
				return
			}
			results[idx] = data
		}(i, key)
	}

	wg.Wait()

	for i, err := range errors {
		if err != nil {
			return nil, fmt.Errorf("failed to download state file %s: %w", keys[i], err)
		}
	}

	files := make(map[string][]byte, len(keys))
	for i, key := range keys {
		files[key] = results[i]
	}
	return files, nil
}

// summarizeStateFiles converts a map of state file keys to raw JSON into
// StackSummary values, filtering out backup files (e.g. "name(1).json") and
// stacks with zero managed resources.
func summarizeStateFiles(files map[string][]byte) ([]StackSummary, error) {
	var summaries []StackSummary
	for key, data := range files {
		// Skip backup/renamed state files like "example01-production(1).json"
		filename := filepath.Base(key)
		if strings.Contains(strings.TrimSuffix(filename, ".json"), "(") {
			continue
		}
		summary, err := parseStateFile(data, key)
		if err != nil {
			return nil, fmt.Errorf("failed to process state file %s: %w", key, err)
		}
		if summary.ResourceCount == 0 {
			continue
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// collectStackSummaries fetches Pulumi state files and extracts summaries.
// Supports both AWS (S3) and Azure (Blob Storage) backends.
func collectStackSummaries(ctx context.Context, creds types.Credentials, target types.Target) ([]StackSummary, map[string][]byte, error) {
	files, err := DownloadStateFiles(ctx, creds, target)
	if err != nil {
		return nil, nil, err
	}
	summaries, err := summarizeStateFiles(files)
	if err != nil {
		return nil, nil, err
	}
	return summaries, files, nil
}

// parseStateFile parses a Pulumi state file from raw JSON bytes.
func parseStateFile(data []byte, stateKey string) (StackSummary, error) {
	var state pulumistate.PulumiState
	if err := json.Unmarshal(data, &state); err != nil {
		return StackSummary{}, fmt.Errorf("failed to parse state JSON: %w", err)
	}

	// Parse timestamp
	timestamp, err := time.Parse(time.RFC3339, state.Checkpoint.Latest.Manifest.Time)
	if err != nil {
		// If parsing fails, use zero time
		timestamp = time.Time{}
	}

	// Count resources (excluding pulumi internal resources) and capture the
	// full per-resource detail for the appendix.
	resourceCount := 0
	resourceTypeSet := make(map[string]bool)
	var resources []ResourceDetail
	var k8sObjects []KubernetesObject
	for _, resource := range state.Checkpoint.Latest.Resources {
		if pulumistate.IsInternalResource(resource.Type) {
			continue
		}
		resourceCount++
		resourceTypeSet[resource.Type] = true
		resources = append(resources, ResourceDetail{
			Name: resourceNameFromURN(resource.URN),
			Type: resource.Type,
			ID:   resource.ID,
			URN:  resource.URN,
		})
		if strings.HasPrefix(resource.Type, "kubernetes:") {
			k8sObjects = append(k8sObjects, extractKubernetesObject(resource))
		}
	}

	// Present resources in a stable order: by type, then by name.
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Type != resources[j].Type {
			return resources[i].Type < resources[j].Type
		}
		return resources[i].Name < resources[j].Name
	})

	// Present Kubernetes objects by kind, then namespace, then name.
	sort.Slice(k8sObjects, func(i, j int) bool {
		if k8sObjects[i].Kind != k8sObjects[j].Kind {
			return k8sObjects[i].Kind < k8sObjects[j].Kind
		}
		if k8sObjects[i].Namespace != k8sObjects[j].Namespace {
			return k8sObjects[i].Namespace < k8sObjects[j].Namespace
		}
		return k8sObjects[i].Name < k8sObjects[j].Name
	})

	// Convert resource type set to sorted slice
	var resourceTypes []string
	for resourceType := range resourceTypeSet {
		resourceTypes = append(resourceTypes, resourceType)
	}

	// Extract project and stack name from state key
	// Format: .pulumi/stacks/{project-name}/{stack-name}.json
	parts := strings.Split(stateKey, "/")
	projectName := "unknown"
	stackName := "unknown"
	if len(parts) >= 4 {
		projectName = parts[2]
		stackName = strings.TrimSuffix(parts[3], ".json")
	}

	return StackSummary{
		ProjectName:       projectName,
		StackName:         stackName,
		Timestamp:         timestamp,
		PulumiVersion:     state.Checkpoint.Latest.Manifest.Version,
		ResourceCount:     resourceCount,
		ResourceTypes:     resourceTypes,
		Resources:         resources,
		KubernetesObjects: k8sObjects,
		StateKey:          stateKey,
	}, nil
}

// collectBootstrapResources synthesizes the resources created by the bootstrap
// step. Bootstrap runs imperatively (it provisions the very state backend that
// Pulumi later uses), so these resources never appear in a Pulumi state file.
// Identifiers are derived from PTD's deterministic naming conventions rather
// than queried from the cloud; the logic mirrors lib/steps/bootstrap.go.
func collectBootstrapResources(target types.Target) []ResourceDetail {
	var resources []ResourceDetail
	switch target.CloudProvider() {
	case types.Azure:
		if t, ok := target.(ptdazure.Target); ok {
			resources = bootstrapAzureResources(t)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: target %q reports Azure but is not an *azure.Target; bootstrap inventory will be empty\n", target.Name())
		}
	case types.AWS:
		if t, ok := target.(ptdaws.Target); ok {
			resources = bootstrapAWSResources(t)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: target %q reports AWS but is not an *aws.Target; bootstrap inventory will be empty\n", target.Name())
		}
	default:
		fmt.Fprintf(os.Stderr, "Warning: unrecognized cloud provider %q for target %q; bootstrap inventory will be empty\n", target.CloudProvider(), target.Name())
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Type != resources[j].Type {
			return resources[i].Type < resources[j].Type
		}
		return resources[i].Name < resources[j].Name
	})
	return resources
}

// bootstrapAzureResources lists the Azure resources created by the bootstrap
// step (see BootstrapStep.runAzure). Role-assignment IDs are random GUIDs that
// cannot be derived, so the built-in role-definition GUID is shown instead.
func bootstrapAzureResources(t ptdazure.Target) []ResourceDetail {
	add := func(res *[]ResourceDetail, name, typ, id string) {
		*res = append(*res, ResourceDetail{Name: name, Type: typ, ID: id})
	}
	var res []ResourceDetail

	add(&res, t.ResourceGroupName(), "Azure Resource Group", t.ResourceGroupName())
	add(&res, t.StateBucketName(), "Azure Storage Account (Pulumi state)", t.StateBucketName())
	add(&res, t.BlobStorageName(), "Azure Blob Container (Pulumi state)", t.BlobStorageName())
	add(&res, t.VaultName(), "Azure Key Vault", t.VaultName())
	add(&res, consts.AzKeyName, "Azure Key Vault Key (state encryption)", consts.AzKeyName)

	adminGroup := t.AdminGroupID()
	add(&res, fmt.Sprintf("Key Vault Administrator → %s", adminGroup), "Azure Role Assignment", consts.KeyVaultAdminRoleId)
	add(&res, fmt.Sprintf("Storage Blob Data Contributor → %s", adminGroup), "Azure Role Assignment", consts.StorageBlobDataContribRoleId)
	add(&res, fmt.Sprintf("AKS RBAC Cluster Admin → %s", adminGroup), "Azure Role Assignment", consts.AksRbacClusterAdminRoleId)

	for siteName := range t.Sites() {
		for _, field := range bootstrapSiteSecretFields(siteName) {
			name := fmt.Sprintf("%s-%s", siteName, field)
			add(&res, name, "Azure Key Vault Secret", name)
		}
	}
	return res
}

// bootstrapAWSResources lists the AWS resources created by the bootstrap step
// (see BootstrapStep.runAws).
func bootstrapAWSResources(t ptdaws.Target) []ResourceDetail {
	add := func(res *[]ResourceDetail, name, typ string) {
		*res = append(*res, ResourceDetail{Name: name, Type: typ, ID: name})
	}
	var res []ResourceDetail

	add(&res, t.StateBucketName(), "AWS S3 Bucket (Pulumi state)")
	add(&res, consts.KmsAlias, "AWS KMS Key (state encryption)")
	if t.CreateAdminPolicyAsResource() {
		add(&res, consts.PositTeamDedicatedAdminPolicyName, "AWS IAM Policy")
	}
	add(&res, fmt.Sprintf("%s.posit.team", t.Name()), "AWS Secrets Manager Secret")

	for siteName := range t.Sites() {
		add(&res, fmt.Sprintf("%s-%s.posit.team", t.Name(), siteName), "AWS Secrets Manager Secret")
		add(&res, fmt.Sprintf("%s-%s.sessions.posit.team", t.Name(), siteName), "AWS Secrets Manager Secret")
		add(&res, fmt.Sprintf("%s-%s-ssh-ppm-keys.posit.team", t.Name(), siteName), "AWS Secrets Manager Secret")
	}
	return res
}

// bootstrapSiteSecretFields returns the per-site secret field names that the
// bootstrap step writes into the Key Vault, mirroring its logic: marshal the
// NewSiteSecret template and emit one entry per populated field.
func bootstrapSiteSecretFields(siteName string) []string {
	siteSecret := secrets.NewSiteSecret(siteName)
	data, err := json.Marshal(siteSecret)
	if err != nil {
		return nil
	}
	var fieldMap map[string]any
	if err := json.Unmarshal(data, &fieldMap); err != nil {
		return nil
	}
	var fields []string
	for field, value := range fieldMap {
		if fmt.Sprintf("%v", value) == "" {
			continue
		}
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// extractKubernetesObject pulls the kind, namespace, name, and cluster-assigned
// UID for a Kubernetes resource from its Pulumi state outputs. Helm releases
// expose name/namespace at the top level and have no metadata.uid.
func extractKubernetesObject(r pulumistate.PulumiResource) KubernetesObject {
	obj := KubernetesObject{Kind: kubernetesKind(r.Type, r.Outputs)}

	if md, ok := r.Outputs["metadata"].(map[string]interface{}); ok {
		obj.Name = stateString(md, "name")
		obj.Namespace = stateString(md, "namespace")
		obj.UID = stateString(md, "uid")
	}
	// Helm releases (and similar) carry name/namespace at the top level.
	if obj.Name == "" {
		obj.Name = stateString(r.Outputs, "name")
	}
	if obj.Namespace == "" {
		obj.Namespace = stateString(r.Outputs, "namespace")
	}
	if obj.Name == "" {
		obj.Name = resourceNameFromURN(r.URN)
	}
	return obj
}

// kubernetesKind derives a human-readable Kubernetes kind from the resource's
// state outputs, falling back to the last segment of the Pulumi type token.
func kubernetesKind(resType string, outputs map[string]interface{}) string {
	if strings.Contains(resType, "helm.sh") {
		return "Helm Release"
	}
	if kind := stateString(outputs, "kind"); kind != "" {
		return kind
	}
	if i := strings.LastIndex(resType, ":"); i >= 0 && i < len(resType)-1 {
		return resType[i+1:]
	}
	return resType
}

// stateString safely reads a string field from a Pulumi state outputs map.
func stateString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// resourceNameFromURN extracts the logical resource name from a Pulumi URN.
// URNs have the form urn:pulumi:{stack}::{project}::{type-chain}::{name};
// the name is the final "::"-delimited segment.
func resourceNameFromURN(urn string) string {
	if urn == "" {
		return ""
	}
	parts := strings.Split(urn, "::")
	return parts[len(parts)-1]
}
