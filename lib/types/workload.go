package types

import (
	"fmt"

	"github.com/google/uuid"
)

type AWSWorkloadClusterConfig struct {
	ClusterName             string   `json:"cluster_name" yaml:"cluster_name"`
	NodeGroupName           string   `json:"node_group_name" yaml:"node_group_name"`
	NodeInstanceType        string   `json:"node_instance_type" yaml:"node_instance_type"`
	NodeGroupMinSize        int      `json:"node_group_min_size" yaml:"node_group_min_size"`
	NodeGroupMaxSize        int      `json:"node_group_max_size" yaml:"node_group_max_size"`
	NodeGroupDesiredSize    int      `json:"node_group_desired_size" yaml:"node_group_desired_size"`
	K8sVersion              string   `json:"k8s_version" yaml:"k8s_version"`
	VpcID                   string   `json:"vpc_id" yaml:"vpc_id"`
	SubnetIDs               []string `json:"subnet_ids" yaml:"subnet_ids"`
	SecurityGroupIDs        []string `json:"security_group_ids" yaml:"security_group_ids"`
	IAMRoleARN              string   `json:"iam_role_arn" yaml:"iam_role_arn"`
	ClusterEndpoint         string   `json:"cluster_endpoint" yaml:"cluster_endpoint"`
	ClusterCA               string   `json:"cluster_ca" yaml:"cluster_ca"`
	ClusterOIDCIssuerURL    string   `json:"cluster_oidc_issuer_url" yaml:"cluster_oidc_issuer_url"`
	ClusterOIDCClientID     string   `json:"cluster_oidc_client_id" yaml:"cluster_oidc_client_id"`
	ClusterOIDCClientSecret string   `json:"cluster_oidc_client_secret" yaml:"cluster_oidc_client_secret"`
}

type CustomRoleConfig struct {
	RoleArn    string `json:"role_arn" yaml:"role_arn"`
	ExternalID string `json:"external_id" yaml:"external_id"`
}

type AWSWorkloadConfig struct {
	AccountID                               string                              `json:"account_id" yaml:"account_id"`
	BastionInstanceType                     string                              `json:"bastion_instance_type" yaml:"bastion_instance_type"`
	Clusters                                map[string]AWSWorkloadClusterConfig `json:"clusters" yaml:"clusters"`
	ControlRoomAccountID                    string                              `json:"control_room_account_id" yaml:"control_room_account_id"`
	ControlRoomClusterName                  string                              `json:"control_room_cluster_name" yaml:"control_room_cluster_name"`
	ControlRoomDomain                       string                              `json:"control_room_domain" yaml:"control_room_domain"`
	ControlRoomRegion                       string                              `json:"control_room_region" yaml:"control_room_region"`
	CustomerManagedBastionId                string                              `json:"customer_managed_bastion_id" yaml:"customer_managed_bastion_id"`
	Sites                                   map[string]SiteConfig               `json:"sites" yaml:"sites"`
	DBAllocatedStorage                      int                                 `json:"db_allocated_storage" yaml:"db_allocated_storage"`
	DBEngineVersion                         string                              `json:"db_engine_version" yaml:"db_engine_version"`
	DBInstanceClass                         string                              `json:"db_instance_class" yaml:"db_instance_class"`
	DBPerformanceInsightsEnabled            bool                                `json:"db_performance_insights_enabled" yaml:"db_performance_insights_enabled"`
	DBDeletionProtection                    bool                                `json:"db_deletion_protection" yaml:"db_deletion_protection"`
	DBMaxAllocatedStorage                   *int                                `json:"db_max_allocated_storage" yaml:"db_max_allocated_storage"`
	DomainSource                            string                              `json:"domain_source" yaml:"domain_source"`
	KeycloakEnabled                         bool                                `json:"keycloak_enabled" yaml:"keycloak_enabled"`
	ExternalDNSEnabled                      bool                                `json:"external_dns_enabled" yaml:"external_dns_enabled"`
	ExternalID                              *uuid.UUID                          `json:"external_id" yaml:"external_id"`
	ExtraClusterOidcUrls                    []string                            `json:"extra_cluster_oidc_urls" yaml:"extra_cluster_oidc_urls"`
	ExtraPostgresDbs                        []string                            `json:"extra_postgres_dbs" yaml:"extra_postgres_dbs"`
	FsxOpenzfsDailyAutomaticBackupStartTime string                              `json:"fsx_openzfs_daily_automatic_backup_start_time" yaml:"fsx_openzfs_daily_automatic_backup_start_time"`
	FsxOpenzfsMultiAz                       bool                                `json:"fsx_openzfs_multi_az" yaml:"fsx_openzfs_multi_az"`
	FsxOpenzfsOverrideDeploymentType        *string                             `json:"fsx_openzfs_override_deployment_type" yaml:"fsx_openzfs_override_deployment_type"`
	FsxOpenzfsStorageCapacity               int                                 `json:"fsx_openzfs_storage_capacity" yaml:"fsx_openzfs_storage_capacity"`
	FsxOpenzfsThroughputCapacity            int                                 `json:"fsx_openzfs_throughput_capacity" yaml:"fsx_openzfs_throughput_capacity"`
	GrafanaScrapeSystemLogs                 bool                                `json:"grafana_scrape_system_logs" yaml:"grafana_scrape_system_logs"`
	LoadBalancerPerSite                     bool                                `json:"load_balancer_per_site" yaml:"load_balancer_per_site"`
	ProtectPersistentResources              bool                                `json:"protect_persistent_resources" yaml:"protect_persistent_resources"`
	Profile                                 string                              `json:"profile" yaml:"profile"`
	CustomRole                              *CustomRoleConfig                   `json:"custom_role" yaml:"custom_role"`
	CreateAdminPolicyAsResource             bool                                `json:"create_admin_policy_as_resource" yaml:"create_admin_policy_as_resource"`
	ProvisionedVpc                          *AWSProvisionedVpc                  `json:"provisioned_vpc" yaml:"provisioned_vpc"`
	PublicLoadBalancer                      bool                                `json:"public_load_balancer" yaml:"public_load_balancer"`
	Region                                  string                              `json:"region" yaml:"region"`
	ResourceTags                            map[string]string                   `json:"resource_tags" yaml:"resource_tags"`
	RoleArn          *string `json:"role_arn" yaml:"role_arn"`
	TailscaleEnabled bool    `json:"tailscale_enabled" yaml:"tailscale_enabled"`
	TrustedPrincipals                       []string                            `json:"trusted_principals" yaml:"trusted_principals"`
	HostedZoneID                            *string                             `json:"hosted_zone_id" yaml:"hosted_zone_id"`
	VpcAzCount                              int                                 `json:"vpc_az_count" yaml:"vpc_az_count"`
	VpcCidr                                 string                              `json:"vpc_cidr" yaml:"vpc_cidr"`
}

type AWSProvisionedVpc struct {
	VpcID          string   `json:"vpc_id" yaml:"vpc_id"`
	Cidr           string   `json:"cidr" yaml:"cidr"`
	PrivateSubnets []string `json:"private_subnets" yaml:"private_subnets"`
}

type AzureWorkloadConfig struct {
	Clusters                   map[string]AzureWorkloadClusterConfig `yaml:"clusters"`
	ControlRoomAccountID       string                                `json:"control_room_account_id" yaml:"control_room_account_id"`
	ControlRoomClusterName     string                                `json:"control_room_cluster_name" yaml:"control_room_cluster_name"`
	ControlRoomDomain          string                                `json:"control_room_domain" yaml:"control_room_domain"`
	ControlRoomRegion          string                                `json:"control_room_region" yaml:"control_room_region"`
	Region                     string                                `yaml:"region"`
	SubscriptionID             string                                `yaml:"subscription_id"`
	TenantID                   string                                `yaml:"tenant_id"`
	ClientID                   string                                `yaml:"client_id"`
	SecretsProviderClientID    string                                `yaml:"secrets_provider_client_id"`
	AdminGroupID               string                                `yaml:"admin_group_id"`
	InstanceType               string                                `yaml:"instance_type"`
	ControlPlaneNodeCount      int                                   `yaml:"control_plane_node_count"`
	WorkerNodeCount            int                                   `yaml:"worker_node_count"`
	DBStorageSizeGB            int                                   `yaml:"db_storage_size_gb"`
	ResourceTags map[string]string       `yaml:"resource_tags"`
	Sites        map[string]SiteConfig `json:"sites" yaml:"sites"` // didn't find this on the python side.
	ProtectPersistentResources bool                                  `yaml:"protect_persistent_resources"`
	Network                    NetworkConfig                         `yaml:"network"`
}

type NetworkConfig struct {
	VnetCidr             string `yaml:"vnet_cidr"`
	PublicSubnetCidr     string `yaml:"public_subnet_cidr"`
	PrivateSubnetCidr    string `yaml:"private_subnet_cidr"`
	DbSubnetCidr         string `yaml:"db_subnet_cidr"`
	NetAppSubnetCidr     string `yaml:"netapp_subnet_cidr"`
	AppGatewaySubnetCidr string `yaml:"app_gateway_subnet_cidr"`
	ProvisionedVnetID    string `yaml:"provisioned_vnet_id"`
	VnetRsgName          string `yaml:"vnet_rsg_name"`
}

// AzureUserNodePoolConfig defines configuration for a single user node pool in AKS
type AzureUserNodePoolConfig struct {
	Name              string            `yaml:"name" json:"name"`                                                 // Pool name (1-12 chars, lowercase alphanumeric)
	VMSize            string            `yaml:"vm_size" json:"vm_size"`                                           // e.g., "Standard_D8s_v6"
	MinCount          int               `yaml:"min_count" json:"min_count"`                                       // Minimum nodes (autoscaling)
	MaxCount          int               `yaml:"max_count" json:"max_count"`                                       // Maximum nodes (autoscaling)
	InitialCount      *int              `yaml:"initial_count,omitempty" json:"initial_count,omitempty"`           // Optional: defaults to MinCount
	EnableAutoScaling bool              `yaml:"enable_auto_scaling" json:"enable_auto_scaling"`                   // Default: true
	AvailabilityZones []string          `yaml:"availability_zones,omitempty" json:"availability_zones,omitempty"` // Optional: defaults to ["2", "3"]
	NodeTaints        []string          `yaml:"node_taints,omitempty" json:"node_taints,omitempty"`               // e.g., ["nvidia.com/gpu=true:NoSchedule"]
	NodeLabels        map[string]string `yaml:"node_labels,omitempty" json:"node_labels,omitempty"`               // Optional labels
	MaxPods           *int              `yaml:"max_pods,omitempty" json:"max_pods,omitempty"`                     // Optional: defaults to 110
	RootDiskSize      *int              `yaml:"root_disk_size,omitempty" json:"root_disk_size,omitempty"`         // Optional: defaults to 256 GB (P15 tier)
}

type AzureWorkloadClusterConfig struct {
	Components                 AzureWorkloadClusterComponentConfig `yaml:"components"`
	KubernetesVersion          string                              `yaml:"kubernetes_version"`
	OutboundType               string                              `yaml:"outbound_type,omitempty"`
	PublicEndpointAccess       bool                                `yaml:"public_endpoint_access"`
	SystemNodePoolInstanceType string                              `yaml:"system_node_pool_instance_type"`

	// Legacy field - maintained for backward compatibility
	// Used to configure the hardcoded "userpool" in AgentPoolProfiles for legacy clusters
	UserNodePoolInstanceType string `yaml:"user_node_pool_instance_type,omitempty"`

	// New field - defines additional user node pools as separate AgentPool resources
	// Works for both new clusters (all user pools) and legacy clusters (additional pools)
	UserNodePools []AzureUserNodePoolConfig `yaml:"user_node_pools,omitempty"`

	// Optional: explicit flag to control whether to include legacy user pool in agentPoolProfiles
	// Set to true for existing clusters to maintain the hardcoded "userpool" in AgentPoolProfiles
	// Set to false (or omit) for new clusters to have all user pools as separate AgentPool resources
	// Legacy clusters can have BOTH the hardcoded userpool AND additional user_node_pools
	UseLegacyUserPool *bool `yaml:"use_legacy_user_pool,omitempty"`

	// Optional: Root disk size for system node pool in GB (defaults to 128)
	SystemNodePoolRootDiskSize *int `yaml:"system_node_pool_root_disk_size,omitempty"`
}

type AzureWorkloadClusterComponentConfig struct {
	SecretStoreCsiDriverAzureProviderVersion string `yaml:"secret_store_csi_driver_azure_provider_version"`
}

type SiteConfig struct {
	ZoneID                string `json:"zone_id" yaml:"zone_id"`
	CertificateARN        string `json:"certificate_arn" yaml:"certificate_arn"`
	Domain                string `json:"domain" yaml:"domain"`
	DomainType            string `json:"domain_type" yaml:"domain_type"`
	UseTraefikForwardAuth bool   `json:"use_traefik_forward_auth" yaml:"use_traefik_forward_auth"`
}

var ValidOutboundTypes = map[string]bool{
	"LoadBalancer":       true,
	"UserDefinedRouting": true,
	"ManagedNatGateway":  true,
	"AssignedNatGateway": true,
}

func (c *AzureWorkloadClusterConfig) ValidateOutboundType() error {
	if c.OutboundType == "" {
		return nil // Optional field, empty is OK
	}
	if !ValidOutboundTypes[c.OutboundType] {
		return fmt.Errorf("invalid outbound_type '%s': must be one of LoadBalancer, UserDefinedRouting, ManagedNatGateway, AssignedNatGateway", c.OutboundType)
	}
	return nil
}

// ResolveUserNodePools resolves which user node pools should be created based on configuration
// For NEW clusters (use_legacy_user_pool = false or omitted): user_node_pools is REQUIRED
// For LEGACY clusters (use_legacy_user_pool = true): user_node_pools is optional (adds ADDITIONAL pools)
func (c *AzureWorkloadClusterConfig) ResolveUserNodePools() ([]AzureUserNodePoolConfig, error) {
	useLegacy := c.UseLegacyUserPool != nil && *c.UseLegacyUserPool

	// If user_node_pools is explicitly defined, use it
	if len(c.UserNodePools) > 0 {
		return c.UserNodePools, nil
	}

	// For legacy clusters, user_node_pools being empty is OK
	// The legacy userpool in AgentPoolProfiles is sufficient
	if useLegacy {
		// But we still need UserNodePoolInstanceType for the legacy pool
		if c.UserNodePoolInstanceType == "" {
			return nil, fmt.Errorf("legacy clusters require user_node_pool_instance_type to be set")
		}
		return []AzureUserNodePoolConfig{}, nil
	}

	// For new clusters, user_node_pools is required
	return nil, fmt.Errorf("new clusters must define user_node_pools in configuration")
}
