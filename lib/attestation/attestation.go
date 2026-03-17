package attestation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ptdaws "github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/customization"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
	"gopkg.in/yaml.v3"
)

// AttestationData contains all collected information about a workload deployment
type AttestationData struct {
	TargetName     string                 `json:"target_name"`
	CloudProvider  string                 `json:"cloud_provider"`
	Region         string                 `json:"region"`
	AccountID      string                 `json:"account_id"`
	Profile        string                 `json:"profile"`
	GeneratedAt    time.Time              `json:"generated_at"`
	Sites          []SiteInfo             `json:"sites"`
	Stacks         []StackSummary         `json:"stacks"`
	CustomSteps    []CustomStepInfo       `json:"custom_steps"`
	ClusterConfig  map[string]interface{} `json:"cluster_config"`
	Infra          *InfraConfig           `json:"infra"`
	ProductSummary string                 `json:"product_summary"`
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
	ProjectName   string    `json:"project_name"`
	StackName     string    `json:"stack_name"`
	Purpose       string    `json:"purpose"`
	Timestamp     time.Time `json:"timestamp"`
	PulumiVersion string    `json:"pulumi_version"`
	ResourceCount int       `json:"resource_count"`
	ResourceTypes []string  `json:"resource_types"`
	S3Key         string    `json:"s3_key"`
}

// stackPurpose returns a human-readable description for standard stack types
var stackPurposes = map[string]string{
	"persistent":      "VPC integration, RDS PostgreSQL, S3 buckets, FSx storage, IAM roles, TLS certificates, DNS zones, bastion host",
	"postgres-config": "PostgreSQL database users, databases, and grants",
	"eks":             "EKS cluster, managed node groups, OIDC provider, storage classes",
	"aks":             "AKS cluster, node pools, managed identity, storage classes",
	"clusters":        "Kubernetes namespaces, network policies, IAM-to-K8s role bindings, Team Operator, Traefik",
	"helm":            "Helm chart deployments: monitoring (Loki, Grafana, Mimir, Alloy), cert-manager, Secrets Store CSI",
	"sites":           "Posit product deployments (TeamSite CRDs), ingress resources, site-specific configuration",
}

// stepNameFromProject extracts the step name from a project name like "ptd-aws-workload-post-clusters"
func (s *StackSummary) stepNameFromProject() string {
	// Strip "ptd-{cloud}-{type}-" prefix to get the step name
	parts := strings.SplitN(s.ProjectName, "-", 4)
	if len(parts) >= 4 {
		return parts[3]
	}
	return s.ProjectName
}

func purposeForStack(projectName string) string {
	for suffix, purpose := range stackPurposes {
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

// PulumiState represents the structure of a Pulumi state JSON file
type PulumiState struct {
	Version    int              `json:"version"`
	Checkpoint PulumiCheckpoint `json:"checkpoint"`
}

// PulumiCheckpoint contains the checkpoint section of Pulumi state
type PulumiCheckpoint struct {
	Latest PulumiLatest `json:"latest"`
}

// PulumiLatest contains the latest deployment information
type PulumiLatest struct {
	Manifest  PulumiManifest   `json:"manifest"`
	Resources []PulumiResource `json:"resources"`
}

// PulumiManifest contains Pulumi version and timestamp information
type PulumiManifest struct {
	Time    string `json:"time"`
	Version string `json:"version"`
}

// PulumiResource represents a single resource in Pulumi state
type PulumiResource struct {
	Type string `json:"type"`
	URN  string `json:"urn"`
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
		TargetName:    target.Name(),
		CloudProvider: string(target.CloudProvider()),
		Region:        target.Region(),
		AccountID:     creds.AccountID(),
		GeneratedAt:   time.Now().UTC(),
		Sites:         []SiteInfo{},
		Stacks:        []StackSummary{},
		CustomSteps:   []CustomStepInfo{},
		ClusterConfig: make(map[string]interface{}),
	}

	// Extract cluster config and profile from ptd.yaml
	if awsConfig, ok := config.(types.AWSWorkloadConfig); ok {
		attestation.Profile = awsConfig.Profile
		if len(awsConfig.Clusters) > 0 {
			clustersMap := make(map[string]interface{})
			for name, cluster := range awsConfig.Clusters {
				clusterMap := map[string]interface{}{
					"cluster_name":       cluster.ClusterName,
					"node_instance_type": cluster.NodeInstanceType,
					"k8s_version":        cluster.K8sVersion,
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
	stacks, err := collectStackSummaries(ctx, creds, target)
	if err != nil {
		return nil, fmt.Errorf("failed to collect stack summaries: %w", err)
	}
	// Set purpose descriptions: custom step descriptions take priority
	customDescriptions := make(map[string]string)
	for _, cs := range attestation.CustomSteps {
		customDescriptions[cs.Name] = cs.Description
	}
	for i := range stacks {
		stepName := stacks[i].stepNameFromProject()
		if desc, ok := customDescriptions[stepName]; ok {
			stacks[i].Purpose = desc
		} else if p := purposeForStack(stacks[i].ProjectName); p != "" {
			stacks[i].Purpose = p
		}
	}
	attestation.Stacks = stacks

	// Generate product summary paragraph
	attestation.ProductSummary = GenerateProductSummary(infraCfg, sites)

	return attestation, nil
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
// using a flexible YAML structure that handles the spec: nesting
func parseInfraConfigFromPtdYaml(workloadPath string) (*InfraConfig, error) {
	ptdYamlPath := filepath.Join(workloadPath, "ptd.yaml")
	data, err := os.ReadFile(ptdYamlPath)
	if err != nil {
		return nil, err
	}

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
			SecretsStoreAddonEnabled    bool   `yaml:"secrets_store_addon_enabled"`
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
					Domain                     string `yaml:"domain"`
					PrivateZone                bool   `yaml:"private_zone"`
					CertificateValidationEnabled bool   `yaml:"certificate_validation_enabled"`
					CertificateARN             string `yaml:"certificate_arn"`
				} `yaml:"spec"`
			} `yaml:"sites"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cfg := &InfraConfig{
		VpcCidr:                     raw.Spec.VpcCidr,
		VpcAzCount:                  raw.Spec.VpcAzCount,
		FsxMultiAz:                  raw.Spec.FsxOpenzfsMultiAz,
		KeycloakEnabled:             raw.Spec.KeycloakEnabled,
		ExternalDNSEnabled:          raw.Spec.ExternalDNSEnabled,
		PublicLoadBalancer:          raw.Spec.PublicLoadBalancer,
		SecretsStoreAddonEnabled:    raw.Spec.SecretsStoreAddonEnabled,
		CustomerManagedBastionID:    raw.Spec.CustomerManagedBastionId,
		LoadBalancerPerSite:         raw.Spec.LoadBalancerPerSite,
		SiteDomains:                 make(map[string]string),
	}

	if raw.Spec.HostedZoneManagementEnabled != nil {
		cfg.HostedZoneManagementEnabled = *raw.Spec.HostedZoneManagementEnabled
	}

	if raw.Spec.ProvisionedVpc != nil {
		cfg.ProvisionedVpcID = raw.Spec.ProvisionedVpc.VpcID
		cfg.ProvisionedCidr = raw.Spec.ProvisionedVpc.Cidr
		cfg.PrivateSubnets = raw.Spec.ProvisionedVpc.PrivateSubnets
	}

	// Use the first (usually only) cluster config
	for _, cluster := range raw.Spec.Clusters {
		cfg.ClusterVersion = cluster.Spec.ClusterVersion
		cfg.InstanceType = cluster.Spec.MpInstanceType
		cfg.RootDiskSize = cluster.Spec.RootDiskSize
		break
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
				DomainPrefix: siteYAML.Spec.Connect.DomainPrefix,
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
				DomainPrefix: siteYAML.Spec.Workbench.DomainPrefix,
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
				DomainPrefix: siteYAML.Spec.PackageManager.DomainPrefix,
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

// collectStackSummaries fetches Pulumi state files from S3 and extracts summaries
func collectStackSummaries(ctx context.Context, creds types.Credentials, target types.Target) ([]StackSummary, error) {
	// Only AWS is supported for now
	awsCreds, err := ptdaws.OnlyAwsCredentials(creds)
	if err != nil {
		return nil, fmt.Errorf("only AWS targets are currently supported: %w", err)
	}

	bucketName := target.StateBucketName()
	region := target.Region()

	// List all state files
	keys, err := ptdaws.ListStateFiles(ctx, awsCreds, region, bucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to list state files: %w", err)
	}

	// Process state files in parallel
	var wg sync.WaitGroup
	summaries := make([]StackSummary, len(keys))
	errors := make([]error, len(keys))

	for i, key := range keys {
		wg.Add(1)
		go func(idx int, s3Key string) {
			defer wg.Done()

			summary, err := processStateFile(ctx, awsCreds, region, bucketName, s3Key)
			if err != nil {
				errors[idx] = err
				return
			}
			summaries[idx] = summary
		}(i, key)
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			return nil, fmt.Errorf("failed to process state file %s: %w", keys[i], err)
		}
	}

	return summaries, nil
}

// processStateFile downloads and parses a single Pulumi state file
func processStateFile(ctx context.Context, creds *ptdaws.Credentials, region string, bucketName string, s3Key string) (StackSummary, error) {
	// Download the state file
	data, err := ptdaws.GetStateFile(ctx, creds, region, bucketName, s3Key)
	if err != nil {
		return StackSummary{}, err
	}

	// Parse the JSON
	var state PulumiState
	if err := json.Unmarshal(data, &state); err != nil {
		return StackSummary{}, fmt.Errorf("failed to parse state JSON: %w", err)
	}

	// Parse timestamp
	timestamp, err := time.Parse(time.RFC3339, state.Checkpoint.Latest.Manifest.Time)
	if err != nil {
		// If parsing fails, use zero time
		timestamp = time.Time{}
	}

	// Count resources (excluding pulumi internal resources)
	resourceCount := 0
	resourceTypeSet := make(map[string]bool)
	for _, resource := range state.Checkpoint.Latest.Resources {
		// Skip pulumi internal resources
		if strings.HasPrefix(resource.Type, "pulumi:pulumi:") ||
			strings.HasPrefix(resource.Type, "pulumi:providers:") {
			continue
		}
		resourceCount++
		resourceTypeSet[resource.Type] = true
	}

	// Convert resource type set to sorted slice
	var resourceTypes []string
	for resourceType := range resourceTypeSet {
		resourceTypes = append(resourceTypes, resourceType)
	}

	// Extract project and stack name from S3 key
	// Format: .pulumi/stacks/{project-name}/{stack-name}.json
	parts := strings.Split(s3Key, "/")
	projectName := "unknown"
	stackName := "unknown"
	if len(parts) >= 4 {
		projectName = parts[2]
		stackName = strings.TrimSuffix(parts[3], ".json")
	}

	return StackSummary{
		ProjectName:    projectName,
		StackName:      stackName,
		Timestamp:      timestamp,
		PulumiVersion:  state.Checkpoint.Latest.Manifest.Version,
		ResourceCount:  resourceCount,
		ResourceTypes:  resourceTypes,
		S3Key:          s3Key,
	}, nil
}
