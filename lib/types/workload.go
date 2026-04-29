package types

import (
	"fmt"

	"github.com/google/uuid"
)

type AWSWorkloadClusterConfig struct {
	Spec AWSWorkloadClusterSpec `json:"spec" yaml:"spec"`
}

// AWSWorkloadClusterSpec holds the actual cluster configuration fields,
// nested under "spec" in the ptd.yaml cluster entries.
type AWSWorkloadClusterSpec struct {
	ClusterName             string                        `json:"cluster_name" yaml:"cluster_name"`
	NodeGroupName           string                        `json:"node_group_name" yaml:"node_group_name"`
	NodeInstanceType        string                        `json:"node_instance_type" yaml:"node_instance_type"`
	NodeGroupMinSize        int                           `json:"node_group_min_size" yaml:"node_group_min_size"`
	NodeGroupMaxSize        int                           `json:"node_group_max_size" yaml:"node_group_max_size"`
	NodeGroupDesiredSize    int                           `json:"node_group_desired_size" yaml:"node_group_desired_size"`
	K8sVersion              string                        `json:"k8s_version" yaml:"k8s_version"`
	VpcID                   string                        `json:"vpc_id" yaml:"vpc_id"`
	SubnetIDs               []string                      `json:"subnet_ids" yaml:"subnet_ids"`
	SecurityGroupIDs        []string                      `json:"security_group_ids" yaml:"security_group_ids"`
	IAMRoleARN              string                        `json:"iam_role_arn" yaml:"iam_role_arn"`
	ClusterEndpoint         string                        `json:"cluster_endpoint" yaml:"cluster_endpoint"`
	ClusterCA               string                        `json:"cluster_ca" yaml:"cluster_ca"`
	ClusterOIDCIssuerURL    string                        `json:"cluster_oidc_issuer_url" yaml:"cluster_oidc_issuer_url"`
	ClusterOIDCClientID     string                        `json:"cluster_oidc_client_id" yaml:"cluster_oidc_client_id"`
	ClusterOIDCClientSecret string                        `json:"cluster_oidc_client_secret" yaml:"cluster_oidc_client_secret"`
	EnableEfsCsiDriver      bool                          `json:"enable_efs_csi_driver" yaml:"enable_efs_csi_driver"`
	EfsConfig               *EFSConfig                    `json:"efs_config" yaml:"efs_config"`
	KarpenterConfig         *KarpenterConfig              `json:"karpenter_config" yaml:"karpenter_config"`
	EksAccessEntries        *EKSAccessEntriesConfig       `json:"eks_access_entries" yaml:"eks_access_entries"`
	Components              *AWSWorkloadClusterComponents `json:"components" yaml:"components"`
	TeamOperatorTolerations []TeamOperatorToleration      `json:"team_operator_tolerations" yaml:"team_operator_tolerations"`
	// TeamOperatorImage overrides the team-operator container image (repository:tag or repository@digest).
	// If both TeamOperatorImage and AdhocTeamOperatorImage are set, AdhocTeamOperatorImage takes precedence.
	// If neither is set, the Helm chart uses its default appVersion.
	TeamOperatorImage *string `json:"team_operator_image" yaml:"team_operator_image"`
	// AdhocTeamOperatorImage sets a one-off team-operator image for testing PR builds.
	// Takes precedence over TeamOperatorImage when set.
	AdhocTeamOperatorImage *string `json:"adhoc_team_operator_image" yaml:"adhoc_team_operator_image"`
	// TeamOperatorChartVersion pins the team-operator Helm chart version.
	// Defaults to clustersDefaultTeamOperatorChartVersion when not set.
	TeamOperatorChartVersion *string `json:"team_operator_chart_version" yaml:"team_operator_chart_version"`
	// TeamOperatorSkipCRDs skips CRD installation for the team-operator Helm release.
	// When true, sets crd.enable=false in Helm values and passes --skip-crds to Helm.
	TeamOperatorSkipCRDs bool `json:"team_operator_skip_crds" yaml:"team_operator_skip_crds"`
	// CustomK8sResources lists subfolder names under custom_k8s_resources/ in the workload directory.
	// Each subfolder's YAML files are applied to this cluster in alphabetical order.
	CustomK8sResources []string `json:"custom_k8s_resources" yaml:"custom_k8s_resources"`
	// RoutingWeight is the external-dns routing weight for this cluster's ALB ingress.
	// Defaults to "100".
	RoutingWeight *string `json:"routing_weight" yaml:"routing_weight"`
}

// AWSWorkloadClusterComponents holds optional component version overrides for a cluster.
type AWSWorkloadClusterComponents struct {
	// versions (all optional; Go code provides defaults)
	AlloyVersion *string `json:"alloy_version" yaml:"alloy_version"`
	// AwsEbsCsiDriverVersion is not consumed by the helm step; the EBS CSI driver is installed
	// as an EKS managed add-on in the persistent step.
	AwsEbsCsiDriverVersion                 *string `json:"aws_ebs_csi_driver_version" yaml:"aws_ebs_csi_driver_version"`
	AwsFsxOpenzfsCsiDriverVersion          *string `json:"aws_fsx_openzfs_csi_driver_version" yaml:"aws_fsx_openzfs_csi_driver_version"`
	AwsLoadBalancerControllerVersion       *string `json:"aws_load_balancer_controller_version" yaml:"aws_load_balancer_controller_version"`
	ExternalDNSVersion                     *string `json:"external_dns_version" yaml:"external_dns_version"`
	GrafanaVersion                         *string `json:"grafana_version" yaml:"grafana_version"`
	KarpenterVersion                       *string `json:"karpenter_version" yaml:"karpenter_version"`
	KubeStateMetricsVersion                *string `json:"kube_state_metrics_version" yaml:"kube_state_metrics_version"`
	LokiVersion                            *string `json:"loki_version" yaml:"loki_version"`
	LokiReplicas                           *int    `json:"loki_replicas" yaml:"loki_replicas"`
	MetricsServerVersion                   *string `json:"metrics_server_version" yaml:"metrics_server_version"`
	MimirVersion                           *string `json:"mimir_version" yaml:"mimir_version"`
	MimirReplicas                          *int    `json:"mimir_replicas" yaml:"mimir_replicas"`
	NvidiaDevicePluginVersion              *string `json:"nvidia_device_plugin_version" yaml:"nvidia_device_plugin_version"`
	SecretStoreCsiDriverVersion            *string `json:"secret_store_csi_driver_version" yaml:"secret_store_csi_driver_version"`
	SecretStoreCsiDriverAwsProviderVersion *string `json:"secret_store_csi_driver_aws_provider_version" yaml:"secret_store_csi_driver_aws_provider_version"`
	// TraefikForwardAuthVersion is not consumed by the helm step; traefik-forward-auth is
	// deployed in the clusters step (lib/steps/clusters_aws.go).
	TraefikForwardAuthVersion *string `json:"traefik_forward_auth_version" yaml:"traefik_forward_auth_version"`
	TraefikVersion            *string `json:"traefik_version" yaml:"traefik_version"`
}

// ResolvedAWSComponents is the result of resolving AWSWorkloadClusterComponents with defaults applied.
type ResolvedAWSComponents struct {
	AlloyVersion                           string
	AwsFsxOpenzfsCsiDriverVersion          string
	AwsLoadBalancerControllerVersion       string
	ExternalDNSVersion                     string
	GrafanaVersion                         string
	KarpenterVersion                       string
	KubeStateMetricsVersion                string
	LokiVersion                            string
	LokiReplicas                           int
	MetricsServerVersion                   string
	MimirVersion                           string
	MimirReplicas                          int
	NvidiaDevicePluginVersion              string
	SecretStoreCsiDriverVersion            string
	SecretStoreCsiDriverAwsProviderVersion string
	TraefikVersion                         string
}

func resolveString(ptr *string, def string) string {
	if ptr != nil {
		return *ptr
	}
	return def
}

func resolveInt(ptr *int, def int) int {
	if ptr != nil {
		return *ptr
	}
	return def
}

// ResolveAWSComponents returns the component versions with defaults applied.
func (c *AWSWorkloadClusterComponents) ResolveAWSComponents() ResolvedAWSComponents {
	return ResolvedAWSComponents{
		AlloyVersion:                           resolveString(c.AlloyVersion, "0.12.6"),
		AwsFsxOpenzfsCsiDriverVersion:          resolveString(c.AwsFsxOpenzfsCsiDriverVersion, "v1.0.0"),
		AwsLoadBalancerControllerVersion:       resolveString(c.AwsLoadBalancerControllerVersion, "1.6.0"),
		ExternalDNSVersion:                     resolveString(c.ExternalDNSVersion, "1.14.4"),
		GrafanaVersion:                         resolveString(c.GrafanaVersion, "7.0.14"),
		KarpenterVersion:                       resolveString(c.KarpenterVersion, "1.6.0"),
		KubeStateMetricsVersion:                resolveString(c.KubeStateMetricsVersion, "5.30.1"),
		LokiVersion:                            resolveString(c.LokiVersion, "5.42.0"),
		LokiReplicas:                           resolveInt(c.LokiReplicas, 2),
		MetricsServerVersion:                   resolveString(c.MetricsServerVersion, "3.11.0"),
		MimirVersion:                           resolveString(c.MimirVersion, "5.2.1"),
		MimirReplicas:                          resolveInt(c.MimirReplicas, 2),
		NvidiaDevicePluginVersion:              resolveString(c.NvidiaDevicePluginVersion, "0.17.1"),
		SecretStoreCsiDriverVersion:            resolveString(c.SecretStoreCsiDriverVersion, "1.3.4"),
		SecretStoreCsiDriverAwsProviderVersion: resolveString(c.SecretStoreCsiDriverAwsProviderVersion, "0.3.5"),
		TraefikVersion:                         resolveString(c.TraefikVersion, "37.1.2"),
	}
}

// TeamOperatorToleration holds a Kubernetes toleration for the team-operator pod.
type TeamOperatorToleration struct {
	Key      string `json:"key" yaml:"key"`
	Operator string `json:"operator" yaml:"operator"`
	Effect   string `json:"effect" yaml:"effect"`
	Value    string `json:"value,omitempty" yaml:"value,omitempty"`
}

// EFSConfig holds the EFS configuration for a workload cluster.
type EFSConfig struct {
	FileSystemID  string `json:"file_system_id" yaml:"file_system_id"`
	AccessPointID string `json:"access_point_id" yaml:"access_point_id"`
}

// KarpenterConfig holds the Karpenter node pool configuration for a workload cluster.
type KarpenterConfig struct {
	NodePools []KarpenterNodePool `json:"node_pools" yaml:"node_pools"`
}

// KarpenterRequirement holds a single node selector requirement for Karpenter.
type KarpenterRequirement struct {
	Key      string   `json:"key" yaml:"key"`
	Operator string   `json:"operator" yaml:"operator"`
	Values   []string `json:"values" yaml:"values"`
}

// KarpenterTaint holds a Kubernetes taint for a Karpenter node pool.
type KarpenterTaint struct {
	Key    string `json:"key" yaml:"key"`
	Value  string `json:"value" yaml:"value"`
	Effect string `json:"effect" yaml:"effect"`
}

// KarpenterNodePoolLimits holds resource limits for a Karpenter node pool.
type KarpenterNodePoolLimits struct {
	CPU          *string `json:"cpu" yaml:"cpu"`
	Memory       *string `json:"memory" yaml:"memory"`
	NvidiaComGPU *string `json:"nvidia.com/gpu" yaml:"nvidia.com/gpu"`
}

// KarpenterNodePool holds configuration for a single Karpenter node pool.
type KarpenterNodePool struct {
	Name                          string                   `json:"name" yaml:"name"`
	Requirements                  []KarpenterRequirement   `json:"requirements" yaml:"requirements"`
	Limits                        *KarpenterNodePoolLimits `json:"limits" yaml:"limits"`
	ExpireAfter                   *string                  `json:"expire_after" yaml:"expire_after"`
	Taints                        []KarpenterTaint         `json:"taints" yaml:"taints"`
	Weight                        int                      `json:"weight" yaml:"weight"`
	RootVolumeSize                string                   `json:"root_volume_size" yaml:"root_volume_size"`
	SessionTaints                 bool                     `json:"session_taints" yaml:"session_taints"`
	ConsolidationPolicy           string                   `json:"consolidation_policy" yaml:"consolidation_policy"`
	ConsolidateAfter              string                   `json:"consolidate_after" yaml:"consolidate_after"`
	OverprovisioningReplicas      int                      `json:"overprovisioning_replicas" yaml:"overprovisioning_replicas"`
	OverprovisioningCPURequest    *string                  `json:"overprovisioning_cpu_request" yaml:"overprovisioning_cpu_request"`
	OverprovisioningMemoryRequest *string                  `json:"overprovisioning_memory_request" yaml:"overprovisioning_memory_request"`
	OverprovisioningNvidiaGPU     *string                  `json:"overprovisioning_nvidia_gpu_request" yaml:"overprovisioning_nvidia_gpu_request"`
}

type CustomRoleConfig struct {
	RoleArn    string `json:"role_arn" yaml:"role_arn"`
	ExternalID string `json:"external_id" yaml:"external_id"`
}

type AWSWorkloadConfig struct {
	AccountID                    string                              `json:"account_id" yaml:"account_id"`
	BastionInstanceType          string                              `json:"bastion_instance_type" yaml:"bastion_instance_type"`
	Clusters                     map[string]AWSWorkloadClusterConfig `json:"clusters" yaml:"clusters"`
	ControlRoomAccountID         string                              `json:"control_room_account_id" yaml:"control_room_account_id"`
	ControlRoomClusterName       string                              `json:"control_room_cluster_name" yaml:"control_room_cluster_name"`
	ControlRoomDomain            string                              `json:"control_room_domain" yaml:"control_room_domain"`
	ControlRoomRegion            string                              `json:"control_room_region" yaml:"control_room_region"`
	CustomerManagedBastionId     string                              `json:"customer_managed_bastion_id" yaml:"customer_managed_bastion_id"`
	Sites                        map[string]SiteConfig               `json:"sites" yaml:"sites"`
	DBAllocatedStorage           int                                 `json:"db_allocated_storage" yaml:"db_allocated_storage"`
	DBEngineVersion              string                              `json:"db_engine_version" yaml:"db_engine_version"`
	DBInstanceClass              string                              `json:"db_instance_class" yaml:"db_instance_class"`
	DBPerformanceInsightsEnabled bool                                `json:"db_performance_insights_enabled" yaml:"db_performance_insights_enabled"`
	DBDeletionProtection         bool                                `json:"db_deletion_protection" yaml:"db_deletion_protection"`
	DBMaxAllocatedStorage        *int                                `json:"db_max_allocated_storage" yaml:"db_max_allocated_storage"`
	DomainSource                 string                              `json:"domain_source" yaml:"domain_source"`
	AutoscalingEnabled           bool                                `json:"autoscaling_enabled" yaml:"autoscaling_enabled"`
	KeycloakEnabled              bool                                `json:"keycloak_enabled" yaml:"keycloak_enabled"`
	// ExternalDNSEnabled controls whether ExternalDNS is deployed. Nil means the field was not set, which defaults
	// to true to match the Python workload default of external_dns_enabled = True.
	ExternalDNSEnabled                      *bool              `json:"external_dns_enabled" yaml:"external_dns_enabled"`
	ExternalID                              *uuid.UUID         `json:"external_id" yaml:"external_id"`
	ExtraClusterOidcUrls                    []string           `json:"extra_cluster_oidc_urls" yaml:"extra_cluster_oidc_urls"`
	ExtraPostgresDbs                        []string           `json:"extra_postgres_dbs" yaml:"extra_postgres_dbs"`
	FsxOpenzfsDailyAutomaticBackupStartTime string             `json:"fsx_openzfs_daily_automatic_backup_start_time" yaml:"fsx_openzfs_daily_automatic_backup_start_time"`
	FsxOpenzfsMultiAz                       bool               `json:"fsx_openzfs_multi_az" yaml:"fsx_openzfs_multi_az"`
	FsxOpenzfsOverrideDeploymentType        *string            `json:"fsx_openzfs_override_deployment_type" yaml:"fsx_openzfs_override_deployment_type"`
	FsxOpenzfsStorageCapacity               int                `json:"fsx_openzfs_storage_capacity" yaml:"fsx_openzfs_storage_capacity"`
	FsxOpenzfsThroughputCapacity            int                `json:"fsx_openzfs_throughput_capacity" yaml:"fsx_openzfs_throughput_capacity"`
	GrafanaScrapeSystemLogs                 bool               `json:"grafana_scrape_system_logs" yaml:"grafana_scrape_system_logs"`
	LoadBalancerPerSite                     bool               `json:"load_balancer_per_site" yaml:"load_balancer_per_site"`
	ProtectPersistentResources              bool               `json:"protect_persistent_resources" yaml:"protect_persistent_resources"`
	Profile                                 string             `json:"profile" yaml:"profile"`
	CustomRole                              *CustomRoleConfig  `json:"custom_role" yaml:"custom_role"`
	CreateAdminPolicyAsResource             bool               `json:"create_admin_policy_as_resource" yaml:"create_admin_policy_as_resource"`
	ProvisionedVpc                          *AWSProvisionedVpc `json:"provisioned_vpc" yaml:"provisioned_vpc"`
	PublicLoadBalancer                      *bool              `json:"public_load_balancer" yaml:"public_load_balancer"`
	Region                                  string             `json:"region" yaml:"region"`
	ResourceTags                            map[string]string  `json:"resource_tags" yaml:"resource_tags"`
	RoleArn                                 *string            `json:"role_arn" yaml:"role_arn"`
	TailscaleEnabled                        bool               `json:"tailscale_enabled" yaml:"tailscale_enabled"`
	SecretsStoreAddonEnabled                bool               `json:"secrets_store_addon_enabled" yaml:"secrets_store_addon_enabled"`
	TrustedPrincipals                       []string           `json:"trusted_principals" yaml:"trusted_principals"`
	HostedZoneID                            *string            `json:"hosted_zone_id" yaml:"hosted_zone_id"`
	HostedZoneManagementEnabled             *bool              `json:"hosted_zone_management_enabled,omitempty" yaml:"hosted_zone_management_enabled,omitempty"`
	VpcAzCount                              int                `json:"vpc_az_count" yaml:"vpc_az_count"`
	VpcCidr                                 string             `json:"vpc_cidr" yaml:"vpc_cidr"`
	ThirdPartyTelemetryEnabled              *bool              `json:"third_party_telemetry_enabled,omitempty" yaml:"third_party_telemetry_enabled,omitempty"`
	NetworkTrust                            string             `json:"network_trust" yaml:"network_trust"`
	NvidiaGpuEnabled                        bool               `json:"nvidia_gpu_enabled" yaml:"nvidia_gpu_enabled"`
}

type AWSProvisionedVpc struct {
	VpcID          string   `json:"vpc_id" yaml:"vpc_id"`
	Cidr           string   `json:"cidr" yaml:"cidr"`
	PrivateSubnets []string `json:"private_subnets" yaml:"private_subnets"`
}

type AzureWorkloadConfig struct {
	Clusters                            map[string]AzureWorkloadClusterConfig `yaml:"clusters"`
	ControlRoomAccountID                string                                `json:"control_room_account_id" yaml:"control_room_account_id"`
	ControlRoomClusterName              string                                `json:"control_room_cluster_name" yaml:"control_room_cluster_name"`
	ControlRoomDomain                   string                                `json:"control_room_domain" yaml:"control_room_domain"`
	ControlRoomRegion                   string                                `json:"control_room_region" yaml:"control_room_region"`
	Region                              string                                `yaml:"region"`
	SubscriptionID                      string                                `yaml:"subscription_id"`
	TenantID                            string                                `yaml:"tenant_id"`
	AdminGroupID                        string                                `yaml:"admin_group_id"`
	AutomatedVolumeProvisioning         bool                                  `yaml:"automated_volume_provisioning"`
	BastionInstanceType                 string                                `yaml:"bastion_instance_type"`
	NetappBackupRetentionDays           int                                   `yaml:"netapp_backup_retention_days"`
	NetappDailyBackupStartTime          string                                `yaml:"netapp_daily_backup_start_time"`
	NetappSnapshotsToKeep               int                                   `yaml:"netapp_snapshots_to_keep"`
	NetappVolumeConnectCapacity         int                                   `yaml:"netapp_volume_connect_capacity"`
	NetappVolumeWorkbenchCapacity       int                                   `yaml:"netapp_volume_workbench_capacity"`
	NetappVolumeWorkbenchSharedCapacity int                                   `yaml:"netapp_volume_workbench_shared_capacity"`
	ResourceTags                        map[string]string                     `yaml:"resource_tags"`
	Sites                               map[string]SiteConfig                 `json:"sites" yaml:"sites"` // didn't find this on the python side.
	ProtectPersistentResources          bool                                  `yaml:"protect_persistent_resources"`
	ThirdPartyTelemetryEnabled          *bool                                 `yaml:"third_party_telemetry_enabled,omitempty"`
	Network                             NetworkConfig                         `yaml:"network"`
	NetworkTrust                        string                                `yaml:"network_trust"`
	NvidiaGpuEnabled                    bool                                  `yaml:"nvidia_gpu_enabled"`
	PpmFileShareSizeGib                 int                                   `yaml:"ppm_file_share_size_gib"`
	// RootDomain, when set, is used as the sole cert-manager domain instead of per-site domains.
	// Mirrors Python: AzureWorkloadConfig.root_domain (via WorkloadConfig.domains fallback).
	RootDomain *string `yaml:"root_domain"`
}

type NetworkConfig struct {
	VnetCidr                  string                   `yaml:"vnet_cidr"`
	PublicSubnetCidr          string                   `yaml:"public_subnet_cidr"`
	PrivateSubnetCidr         string                   `yaml:"private_subnet_cidr"`
	PrivateSubnetRouteTableID string                   `yaml:"private_subnet_route_table_id"`
	DbSubnetCidr              string                   `yaml:"db_subnet_cidr"`
	NetAppSubnetCidr          string                   `yaml:"netapp_subnet_cidr"`
	AppGatewaySubnetCidr      string                   `yaml:"app_gateway_subnet_cidr"`
	ProvisionedVnetID         string                   `yaml:"provisioned_vnet_id"`
	VnetRsgName               string                   `yaml:"vnet_rsg_name"`
	DnsForwardDomains         []DNSForwardDomainConfig `yaml:"dns_forward_domains"`
}

// DNSForwardDomainConfig holds a domain and its forwarding IP for CoreDNS configuration.
type DNSForwardDomainConfig struct {
	Host string `yaml:"host"`
	IP   string `yaml:"ip"`
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
	// UseLetsEncrypt controls whether CertManager is deployed for this cluster.
	UseLetsEncrypt bool `yaml:"use_lets_encrypt"`

	UserNodePools []AzureUserNodePoolConfig `yaml:"user_node_pools"`

	// Optional: Root disk size for system node pool in GB (defaults to 128)
	SystemNodePoolRootDiskSize *int `yaml:"system_node_pool_root_disk_size,omitempty"`

	// Optional: When true, enables AKS ForceUpgrade which bypasses PDB constraints during cluster upgrades.
	// Use during maintenance windows when you accept disruption to workloads protected by PDBs.
	ForceMaintenance bool `yaml:"force_maintenance,omitempty"`
	// TeamOperatorImage overrides the team-operator container image (repository:tag or repository@digest).
	// If both TeamOperatorImage and AdhocTeamOperatorImage are set, AdhocTeamOperatorImage takes precedence.
	// If neither is set, the Helm chart uses its default appVersion.
	TeamOperatorImage *string `yaml:"team_operator_image"`
	// AdhocTeamOperatorImage sets a one-off team-operator image for testing PR builds.
	// Takes precedence over TeamOperatorImage when set.
	AdhocTeamOperatorImage *string `yaml:"adhoc_team_operator_image"`
	// TeamOperatorChartVersion pins the team-operator Helm chart version.
	// Defaults to clustersDefaultTeamOperatorChartVersion when not set.
	TeamOperatorChartVersion *string `yaml:"team_operator_chart_version"`
	// TeamOperatorSkipCRDs skips CRD installation for the team-operator Helm release.
	// When true, sets crd.enable=false in Helm values and passes --skip-crds to Helm.
	TeamOperatorSkipCRDs bool `yaml:"team_operator_skip_crds"`
	// CustomK8sResources lists subfolder names under custom_k8s_resources/ in the workload directory.
	// Each subfolder's YAML files are applied to this cluster in alphabetical order.
	CustomK8sResources []string `yaml:"custom_k8s_resources"`
}

type AzureWorkloadClusterComponentConfig struct {
	SecretStoreCsiDriverAzureProviderVersion string  `yaml:"secret_store_csi_driver_azure_provider_version"`
	AlloyVersion                             *string `yaml:"alloy_version"`
	ExternalDnsVersion                       *string `yaml:"external_dns_version"`
	GrafanaVersion                           *string `yaml:"grafana_version"`
	KubeStateMetricsVersion                  *string `yaml:"kube_state_metrics_version"`
	LokiVersion                              *string `yaml:"loki_version"`
	MimirVersion                             *string `yaml:"mimir_version"`
	NvidiaDevicePluginVersion                *string `yaml:"nvidia_device_plugin_version"`
}

// ResolvedAzureComponents is the result of resolving AzureWorkloadClusterComponentConfig with defaults applied.
type ResolvedAzureComponents struct {
	AlloyVersion              string
	ExternalDnsVersion        string
	GrafanaVersion            string
	KubeStateMetricsVersion   string
	LokiVersion               string
	MimirVersion              string
	NvidiaDevicePluginVersion string
}

// ResolveAzureComponents returns the component versions with defaults applied.
func (c *AzureWorkloadClusterComponentConfig) ResolveAzureComponents() ResolvedAzureComponents {
	return ResolvedAzureComponents{
		AlloyVersion:              resolveString(c.AlloyVersion, "0.12.6"),
		ExternalDnsVersion:        resolveString(c.ExternalDnsVersion, "1.14.4"),
		GrafanaVersion:            resolveString(c.GrafanaVersion, "7.0.14"),
		KubeStateMetricsVersion:   resolveString(c.KubeStateMetricsVersion, "5.30.1"),
		LokiVersion:               resolveString(c.LokiVersion, "5.42.0"),
		MimirVersion:              resolveString(c.MimirVersion, "5.2.1"),
		NvidiaDevicePluginVersion: resolveString(c.NvidiaDevicePluginVersion, "0.17.1"),
	}
}

type SiteConfig struct {
	Spec SiteConfigSpec `json:"spec" yaml:"spec"`
}

// SiteConfigSpec holds the actual site configuration fields,
// nested under "spec" in the ptd.yaml sites entries.
type SiteConfigSpec struct {
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

// NetworkTrustValue converts a NetworkTrust string (as stored in ptd.yaml) to its
// integer value expected by the Site CRD spec. Valid values: ZERO=0, SAMESITE=50, FULL=100.
// Empty string defaults to FULL (100), matching the Python workload default.
// Unrecognized values also default to FULL.
func NetworkTrustValue(s string) int {
	switch s {
	case "ZERO":
		return 0
	case "SAMESITE":
		return 50
	case "", "FULL":
		return 100
	default:
		// Unrecognized values default to FULL to match Python behavior.
		return 100
	}
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

// ResolveUserNodePools validates that user_node_pools is defined
// All Azure workloads must define user_node_pools in configuration
func (c *AzureWorkloadClusterConfig) ResolveUserNodePools() ([]AzureUserNodePoolConfig, error) {
	if len(c.UserNodePools) == 0 {
		return nil, fmt.Errorf("user_node_pools must be defined in cluster configuration")
	}
	return c.UserNodePools, nil
}
