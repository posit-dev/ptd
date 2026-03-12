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
	TargetName    string                 `json:"target_name"`
	CloudProvider string                 `json:"cloud_provider"`
	Region        string                 `json:"region"`
	AccountID     string                 `json:"account_id"`
	GeneratedAt   time.Time              `json:"generated_at"`
	Sites         []SiteInfo             `json:"sites"`
	Stacks        []StackSummary         `json:"stacks"`
	CustomSteps   []CustomStepInfo       `json:"custom_steps"`
	ClusterConfig map[string]interface{} `json:"cluster_config"`
}

// SiteInfo contains information extracted from a site.yaml file
type SiteInfo struct {
	SiteName string          `json:"site_name"`
	Domain   string          `json:"domain"`
	Products []ProductInfo   `json:"products"`
	Auth     *SiteAuthConfig `json:"auth,omitempty"`
}

// ProductInfo contains information about a deployed product
type ProductInfo struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Replicas     int    `json:"replicas"`
	DomainPrefix string `json:"domain_prefix,omitempty"`
}

// SiteAuthConfig contains authentication configuration for a site
type SiteAuthConfig struct {
	Type     string `json:"type"`
	Issuer   string `json:"issuer,omitempty"`
	ClientID string `json:"client_id,omitempty"`
}

// StackSummary contains summary information from a Pulumi stack state file
type StackSummary struct {
	StackName      string    `json:"stack_name"`
	Timestamp      time.Time `json:"timestamp"`
	PulumiVersion  string    `json:"pulumi_version"`
	ResourceCount  int       `json:"resource_count"`
	ResourceTypes  []string  `json:"resource_types"`
	S3Key          string    `json:"s3_key"`
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

	// Extract cluster config from ptd.yaml
	if awsConfig, ok := config.(types.AWSWorkloadConfig); ok {
		if len(awsConfig.Clusters) > 0 {
			// Convert clusters to generic map for JSON serialization
			clustersMap := make(map[string]interface{})
			for name, cluster := range awsConfig.Clusters {
				clusterMap := map[string]interface{}{
					"cluster_name":          cluster.ClusterName,
					"node_group_name":       cluster.NodeGroupName,
					"node_instance_type":    cluster.NodeInstanceType,
					"node_group_min_size":   cluster.NodeGroupMinSize,
					"node_group_max_size":   cluster.NodeGroupMaxSize,
					"node_group_desired_size": cluster.NodeGroupDesiredSize,
					"k8s_version":           cluster.K8sVersion,
				}
				clustersMap[name] = clusterMap
			}
			attestation.ClusterConfig = clustersMap
		}
	}

	// Read site.yaml files
	sites, err := collectSites(workloadPath)
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
	attestation.Stacks = stacks

	return attestation, nil
}

// collectSites scans for site_*/site.yaml files and extracts information
func collectSites(workloadPath string) ([]SiteInfo, error) {
	var sites []SiteInfo

	// Find all site_* directories
	pattern := filepath.Join(workloadPath, "site_*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob site directories: %w", err)
	}

	for _, siteDir := range matches {
		siteName := filepath.Base(siteDir)
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

		// Extract site information
		siteInfo := SiteInfo{
			SiteName: siteName,
			Domain:   siteYAML.Spec.Domain,
			Products: []ProductInfo{},
		}

		// Extract product information
		if siteYAML.Spec.Connect != nil {
			product := ProductInfo{
				Name:         "connect",
				Image:        siteYAML.Spec.Connect.Image,
				Replicas:     siteYAML.Spec.Connect.Replicas,
				DomainPrefix: siteYAML.Spec.Connect.DomainPrefix,
			}
			siteInfo.Products = append(siteInfo.Products, product)

			// Extract auth config if present
			if siteYAML.Spec.Connect.Auth != nil {
				siteInfo.Auth = &SiteAuthConfig{
					Type:     siteYAML.Spec.Connect.Auth.Type,
					Issuer:   siteYAML.Spec.Connect.Auth.Issuer,
					ClientID: siteYAML.Spec.Connect.Auth.ClientID,
				}
			}
		}

		if siteYAML.Spec.Workbench != nil {
			product := ProductInfo{
				Name:         "workbench",
				Image:        siteYAML.Spec.Workbench.Image,
				Replicas:     siteYAML.Spec.Workbench.Replicas,
				DomainPrefix: siteYAML.Spec.Workbench.DomainPrefix,
			}
			siteInfo.Products = append(siteInfo.Products, product)

			// Update auth config if present and not already set
			if siteInfo.Auth == nil && siteYAML.Spec.Workbench.Auth != nil {
				siteInfo.Auth = &SiteAuthConfig{
					Type:     siteYAML.Spec.Workbench.Auth.Type,
					Issuer:   siteYAML.Spec.Workbench.Auth.Issuer,
					ClientID: siteYAML.Spec.Workbench.Auth.ClientID,
				}
			}
		}

		if siteYAML.Spec.PackageManager != nil {
			product := ProductInfo{
				Name:         "package-manager",
				Image:        siteYAML.Spec.PackageManager.Image,
				Replicas:     siteYAML.Spec.PackageManager.Replicas,
				DomainPrefix: siteYAML.Spec.PackageManager.DomainPrefix,
			}
			siteInfo.Products = append(siteInfo.Products, product)
		}

		if siteYAML.Spec.Chronicle != nil {
			product := ProductInfo{
				Name:  "chronicle",
				Image: siteYAML.Spec.Chronicle.Image,
			}
			siteInfo.Products = append(siteInfo.Products, product)

			// Add agent if present
			if siteYAML.Spec.Chronicle.AgentImage != "" {
				agentProduct := ProductInfo{
					Name:  "chronicle-agent",
					Image: siteYAML.Spec.Chronicle.AgentImage,
				}
				siteInfo.Products = append(siteInfo.Products, agentProduct)
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

	// Extract stack name from S3 key (format: .pulumi/stacks/{org}/{project}/{stack}.json)
	parts := strings.Split(s3Key, "/")
	stackName := "unknown"
	if len(parts) > 0 {
		// Get the filename without .json extension
		filename := parts[len(parts)-1]
		stackName = strings.TrimSuffix(filename, ".json")
	}

	return StackSummary{
		StackName:      stackName,
		Timestamp:      timestamp,
		PulumiVersion:  state.Checkpoint.Latest.Manifest.Version,
		ResourceCount:  resourceCount,
		ResourceTypes:  resourceTypes,
		S3Key:          s3Key,
	}, nil
}
