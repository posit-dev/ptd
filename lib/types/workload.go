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

	// ── EKS step fields (read by the eks step / AWSEKSCluster builder) ──────────
	// These mirror python-pulumi/src/ptd/aws_workload.py AWSWorkloadClusterConfig.
	// They are pointer-typed where the Python default is non-zero so an unset
	// field defaults to the Python value rather than the Go zero value (see the
	// migration playbook "Default Values"). Resolve with the EKS* helpers below.

	// ClusterVersion is the EKS/Kubernetes version (Python default "1.35").
	ClusterVersion *string `json:"cluster_version" yaml:"cluster_version"`
	// AmiType is the managed node group AMI type (Python default "AL2023_x86_64_STANDARD").
	AmiType *string `json:"ami_type" yaml:"ami_type"`
	// MpInstanceType is the default ("managed pool") node group instance type (Python default "t3.large").
	MpInstanceType *string `json:"mp_instance_type" yaml:"mp_instance_type"`
	// MpMinSize is the default node group min size (Python default 4).
	MpMinSize *int `json:"mp_min_size" yaml:"mp_min_size"`
	// MpMaxSize is the default node group max size (Python default 10).
	MpMaxSize *int `json:"mp_max_size" yaml:"mp_max_size"`
	// RootDiskSize is the default node group root disk size in GB (Python default 200).
	RootDiskSize *int `json:"root_disk_size" yaml:"root_disk_size"`
	// EbsCsiAddonVersion pins the EBS CSI driver EKS managed add-on version
	// (Python default "v1.41.0-eksbuild.1").
	EbsCsiAddonVersion *string `json:"ebs_csi_addon_version" yaml:"ebs_csi_addon_version"`
	// AdditionalNodeGroups are extra managed node groups keyed by name.
	AdditionalNodeGroups map[string]NodeGroupConfig `json:"additional_node_groups" yaml:"additional_node_groups"`
	// ForceMaintenance enables EKS ForceUpdateVersion (Python default false).
	ForceMaintenance bool `json:"force_maintenance" yaml:"force_maintenance"`
}

// EKS* helpers resolve the pointer-typed EKS cluster fields to their Python defaults.
func (s AWSWorkloadClusterSpec) EKSClusterVersion() string {
	return resolveString(s.ClusterVersion, "1.35")
}
func (s AWSWorkloadClusterSpec) EKSAmiType() string {
	return resolveString(s.AmiType, "AL2023_x86_64_STANDARD")
}
func (s AWSWorkloadClusterSpec) EKSMpInstanceType() string {
	return resolveString(s.MpInstanceType, "t3.large")
}
func (s AWSWorkloadClusterSpec) EKSMpMinSize() int    { return resolveInt(s.MpMinSize, 4) }
func (s AWSWorkloadClusterSpec) EKSMpMaxSize() int    { return resolveInt(s.MpMaxSize, 10) }
func (s AWSWorkloadClusterSpec) EKSRootDiskSize() int { return resolveInt(s.RootDiskSize, 200) }
func (s AWSWorkloadClusterSpec) EKSEbsCsiAddonVersion() string {
	return resolveString(s.EbsCsiAddonVersion, "v1.41.0-eksbuild.1")
}

// UsesEksAccessEntries reports whether the EKS access-entries auth path should be
// used (as opposed to the legacy aws-auth ConfigMap). Mirrors Python, where
// EKSAccessEntriesConfig.enabled defaults to True and the cluster config supplies
// a default EKSAccessEntriesConfig when the block is absent. Net semantics:
// nil block → true; block present with enabled unset → true; enabled: true → true;
// only an explicit enabled: false selects legacy aws-auth.
func (s AWSWorkloadClusterSpec) UsesEksAccessEntries() bool {
	return s.EksAccessEntries.IsEnabled()
}

// Taint mirrors python-pulumi/src/ptd/__init__.py Taint.
type Taint struct {
	Effect string `json:"effect" yaml:"effect"`
	Key    string `json:"key" yaml:"key"`
	Value  string `json:"value,omitempty" yaml:"value,omitempty"`
}

// NodeGroupConfig mirrors python-pulumi/src/ptd/__init__.py NodeGroupConfig
// (an additional managed node group on a workload cluster). Pointer-typed
// fields have non-zero Python defaults; resolve with the NG* helpers.
type NodeGroupConfig struct {
	// InstanceType is the node group instance type (Python default "t3.large").
	InstanceType *string `json:"instance_type" yaml:"instance_type"`
	// MinSize is the node group min size (Python default 1).
	MinSize *int `json:"min_size" yaml:"min_size"`
	// MaxSize is the node group max size (Python default 1).
	MaxSize *int `json:"max_size" yaml:"max_size"`
	// AdditionalSecurityGroupIDs are extra SGs attached to this node group's launch template.
	AdditionalSecurityGroupIDs []string `json:"additional_security_group_ids" yaml:"additional_security_group_ids"`
	// AdditionalRootDiskSize is the root disk size in GB (Python default 200).
	AdditionalRootDiskSize *int `json:"additional_root_disk_size" yaml:"additional_root_disk_size"`
	// Taints applied to this node group's nodes.
	Taints []Taint `json:"taints" yaml:"taints"`
	// Labels applied as tags on this node group.
	Labels map[string]string `json:"labels" yaml:"labels"`
	// AmiType overrides the cluster default AMI type when set (Python default None).
	AmiType *string `json:"ami_type" yaml:"ami_type"`
	// DesiredSize defaults to MinSize when nil (Python default None).
	DesiredSize *int `json:"desired_size" yaml:"desired_size"`
	// SystemNodes, when true, labels this node group's Kubernetes nodes with
	// posit.team/node-role=system. System workloads can target these nodes and
	// the image prepull daemonset can be kept off them via node affinity.
	SystemNodes bool `json:"system_nodes" yaml:"system_nodes"`
}

// NGInstanceType resolves the node group instance type (Python default "t3.large").
func (n NodeGroupConfig) NGInstanceType() string { return resolveString(n.InstanceType, "t3.large") }

// NGMinSize resolves the node group min size (Python default 1).
func (n NodeGroupConfig) NGMinSize() int { return resolveInt(n.MinSize, 1) }

// NGMaxSize resolves the node group max size (Python default 1).
func (n NodeGroupConfig) NGMaxSize() int { return resolveInt(n.MaxSize, 1) }

// NGRootDiskSize resolves the node group root disk size in GB (Python default 200).
func (n NodeGroupConfig) NGRootDiskSize() int { return resolveInt(n.AdditionalRootDiskSize, 200) }

// NGDesiredSize resolves the desired size, falling back to the resolved min size
// when unset (mirrors Python `ng_config.desired_size or ng_config.min_size`).
func (n NodeGroupConfig) NGDesiredSize() int {
	if n.DesiredSize != nil {
		return *n.DesiredSize
	}
	return n.NGMinSize()
}

// NGAmiType resolves the node group AMI type, falling back to clusterDefault when
// unset (mirrors Python `ng_config.ami_type or cluster_cfg.ami_type`).
func (n NodeGroupConfig) NGAmiType(clusterDefault string) string {
	if n.AmiType != nil && *n.AmiType != "" {
		return *n.AmiType
	}
	return clusterDefault
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
	TraefikDeploymentReplicas *int    `json:"traefik_deployment_replicas" yaml:"traefik_deployment_replicas"`
	// TigeraOperatorVersion pins the Calico/Tigera operator chart version. Consumed by
	// the eks step (Calico CNI). Mirrors Python WorkloadClusterComponentConfig.tigera_operator_version
	// (default "3.31.4"). Resolve via TigeraOperatorVersionOrDefault.
	TigeraOperatorVersion *string `json:"tigera_operator_version" yaml:"tigera_operator_version"`
}

// TigeraOperatorVersionOrDefault resolves the tigera operator version (Python default "3.31.4").
func (c *AWSWorkloadClusterComponents) TigeraOperatorVersionOrDefault() string {
	if c == nil {
		return "3.31.4"
	}
	return resolveString(c.TigeraOperatorVersion, "3.31.4")
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
	TraefikDeploymentReplicas              int
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
		TraefikDeploymentReplicas:              resolveInt(c.TraefikDeploymentReplicas, 3),
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
	// MountTargetsManaged is false for BYO-EFS scenarios where mount targets are
	// in a different VPC. Mirrors Python EFSConfig.mount_targets_managed
	// (default True). Pointer so unset → true; resolve via IsMountTargetsManaged.
	MountTargetsManaged *bool `json:"mount_targets_managed" yaml:"mount_targets_managed"`
}

// IsMountTargetsManaged resolves MountTargetsManaged (Python default True).
func (c *EFSConfig) IsMountTargetsManaged() bool {
	if c == nil || c.MountTargetsManaged == nil {
		return true
	}
	return *c.MountTargetsManaged
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
	SystemNodes                   bool                     `json:"system_nodes" yaml:"system_nodes"`
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
	ExternalDNSEnabled                      *bool      `json:"external_dns_enabled" yaml:"external_dns_enabled"`
	ExternalID                              *uuid.UUID `json:"external_id" yaml:"external_id"`
	ExtraClusterOidcUrls                    []string   `json:"extra_cluster_oidc_urls" yaml:"extra_cluster_oidc_urls"`
	ExtraPostgresDbs                        []string   `json:"extra_postgres_dbs" yaml:"extra_postgres_dbs"`
	FsxOpenzfsDailyAutomaticBackupStartTime string     `json:"fsx_openzfs_daily_automatic_backup_start_time" yaml:"fsx_openzfs_daily_automatic_backup_start_time"`
	// FsxOpenzfsMultiAz selects the FSx OpenZFS deployment type. Pointer so an absent field can be
	// distinguished from an explicit false: when nil the persistent step applies the Python default
	// of True (→ MULTI_AZ_1). A plain bool would resolve absent→false→SINGLE_AZ and REPLACE live FSx
	// filesystems (deployment_type is ForceNew). Mirrors Python AWSWorkloadConfig.fsx_openzfs_multi_az
	// (default True). Nil-checked via boolPtrOrDefault(p, true) in the deploy code.
	FsxOpenzfsMultiAz                *bool              `json:"fsx_openzfs_multi_az" yaml:"fsx_openzfs_multi_az"`
	FsxOpenzfsOverrideDeploymentType *string            `json:"fsx_openzfs_override_deployment_type" yaml:"fsx_openzfs_override_deployment_type"`
	FsxOpenzfsStorageCapacity        int                `json:"fsx_openzfs_storage_capacity" yaml:"fsx_openzfs_storage_capacity"`
	FsxOpenzfsThroughputCapacity     int                `json:"fsx_openzfs_throughput_capacity" yaml:"fsx_openzfs_throughput_capacity"`
	GrafanaScrapeSystemLogs          bool               `json:"grafana_scrape_system_logs" yaml:"grafana_scrape_system_logs"`
	LoadBalancerPerSite              bool               `json:"load_balancer_per_site" yaml:"load_balancer_per_site"`
	ProtectPersistentResources       bool               `json:"protect_persistent_resources" yaml:"protect_persistent_resources"`
	Profile                          string             `json:"profile" yaml:"profile"`
	CustomRole                       *CustomRoleConfig  `json:"custom_role" yaml:"custom_role"`
	CreateAdminPolicyAsResource      bool               `json:"create_admin_policy_as_resource" yaml:"create_admin_policy_as_resource"`
	ProvisionedVpc                   *AWSProvisionedVpc `json:"provisioned_vpc" yaml:"provisioned_vpc"`
	PublicLoadBalancer               *bool              `json:"public_load_balancer" yaml:"public_load_balancer"`
	Region                           string             `json:"region" yaml:"region"`
	ResourceTags                     map[string]string  `json:"resource_tags" yaml:"resource_tags"`
	RoleArn                          *string            `json:"role_arn" yaml:"role_arn"`
	TailscaleEnabled                 bool               `json:"tailscale_enabled" yaml:"tailscale_enabled"`
	SecretsStoreAddonEnabled         *bool              `json:"secrets_store_addon_enabled,omitempty" yaml:"secrets_store_addon_enabled,omitempty"`
	TrustedPrincipals                []string           `json:"trusted_principals" yaml:"trusted_principals"`
	HostedZoneID                     *string            `json:"hosted_zone_id" yaml:"hosted_zone_id"`
	HostedZoneManagementEnabled      *bool              `json:"hosted_zone_management_enabled,omitempty" yaml:"hosted_zone_management_enabled,omitempty"`
	VpcAzCount                       int                `json:"vpc_az_count" yaml:"vpc_az_count"`
	VpcCidr                          string             `json:"vpc_cidr" yaml:"vpc_cidr"`
	ThirdPartyTelemetryEnabled       *bool              `json:"third_party_telemetry_enabled,omitempty" yaml:"third_party_telemetry_enabled,omitempty"`
	NetworkTrust                     string             `json:"network_trust" yaml:"network_trust"`
	NvidiaGpuEnabled                 bool               `json:"nvidia_gpu_enabled" yaml:"nvidia_gpu_enabled"`
	// FilterControlRoomMetrics enables the per-workload metric filter before forwarding to the
	// control room Mimir remote_write. When true, only metrics referenced by grafana_alerts and
	// grafana_dashboards are forwarded. Defaults to false so rollout can be done per-workload.
	FilterControlRoomMetrics bool `json:"filter_control_room_metrics" yaml:"filter_control_room_metrics"`
	// ExistingFlowLogTargetARNs is a list of pre-existing VPC Flow Log destination ARNs to attach
	// in addition to the PTD-managed CloudWatch LogGroup. Read by the persistent step and passed to
	// the VPC builder's with_flow_log. Mirrors Python AWSWorkloadConfig.existing_flow_log_target_arns
	// (default None / empty).
	ExistingFlowLogTargetARNs []string `json:"existing_flow_log_target_arns" yaml:"existing_flow_log_target_arns"`
	// VPCEndpoints controls which interface/gateway VPC endpoints the persistent step creates.
	// Pointer so an absent field can be distinguished from an explicit one: when nil, the persistent
	// step must apply the Python default of VPCEndpointsConfig() — i.e. all STANDARD_VPC_ENDPOINT_SERVICES
	// enabled with no exclusions. Mirrors Python AWSWorkloadConfig.vpc_endpoints (default None).
	VPCEndpoints *VPCEndpointsConfig `json:"vpc_endpoints" yaml:"vpc_endpoints"`
}

// VPCEndpointsConfig controls creation of VPC endpoints in the workload VPC.
// Mirrors Python ptd.aws_workload.VPCEndpointsConfig.
//
// Python default semantics: when the workload's vpc_endpoints field is absent (nil here), the
// persistent step substitutes VPCEndpointsConfig() — Enabled=True with no excluded services — which
// creates an endpoint for every service in STANDARD_VPC_ENDPOINT_SERVICES.
//
// STANDARD_VPC_ENDPOINT_SERVICES (python-pulumi/src/ptd/aws_workload.py) is the tuple:
//
//	("ec2", "ec2messages", "kms", "s3", "ssm", "ssmmessages")
//
// (note: "fsx" is intentionally NOT in STANDARD_VPC_ENDPOINT_SERVICES — the persistent step adds the
// fsx endpoint separately, gated on Enabled && "fsx" not in ExcludedServices.)
//
// VALID_VPC_ENDPOINT_SERVICES (the set ExcludedServices may contain) is:
//
//	{"ec2", "ec2messages", "fsx", "kms", "s3", "ssm", "ssmmessages"}
type VPCEndpointsConfig struct {
	// Enabled controls whether VPC endpoints are created at all. Python default is True; when the
	// outer pointer is nil the step uses Enabled=true. (A present-but-without-enabled YAML block
	// yields the Go zero value false, matching Python only if the user explicitly sets enabled: false.)
	Enabled bool `json:"enabled" yaml:"enabled"`
	// ExcludedServices lists service names to skip even when Enabled. Must be a subset of
	// VALID_VPC_ENDPOINT_SERVICES. Python default is an empty list.
	ExcludedServices []string `json:"excluded_services" yaml:"excluded_services"`
}

// IsSecretsStoreAddonEnabled returns whether the EKS-managed secrets-store
// CSI driver provider addon should be used instead of the helm-installed
// driver + provider releases. Defaults to true when unset.
func (c *AWSWorkloadConfig) IsSecretsStoreAddonEnabled() bool {
	if c.SecretsStoreAddonEnabled == nil {
		return true
	}
	return *c.SecretsStoreAddonEnabled
}

// IsThirdPartyTelemetryEnabled returns whether third-party telemetry (e.g. the
// Tigera/Calico Felix usage reporting) is enabled. Mirrors Python
// third_party_telemetry_enabled (default True).
func (c *AWSWorkloadConfig) IsThirdPartyTelemetryEnabled() bool {
	if c.ThirdPartyTelemetryEnabled == nil {
		return true
	}
	return *c.ThirdPartyTelemetryEnabled
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
	// FilterControlRoomMetrics enables the per-workload metric filter before forwarding to the
	// control room Mimir remote_write. When true, only metrics referenced by grafana_alerts and
	// grafana_dashboards are forwarded. Defaults to false so rollout can be done per-workload.
	FilterControlRoomMetrics bool `yaml:"filter_control_room_metrics"`
}

type NetworkConfig struct {
	VnetCidr                  string                   `yaml:"vnet_cidr"`
	PublicSubnetCidr          string                   `yaml:"public_subnet_cidr"`
	PrivateSubnetCidr         string                   `yaml:"private_subnet_cidr"`
	PrivateSubnetRouteTableID string                   `yaml:"private_subnet_route_table_id"`
	DbSubnetCidr              string                   `yaml:"db_subnet_cidr"`
	NetAppSubnetCidr          string                   `yaml:"netapp_subnet_cidr"`
	AppGatewaySubnetCidr      string                   `yaml:"app_gateway_subnet_cidr"`
	BastionSubnetCidr         string                   `yaml:"bastion_subnet_cidr"`
	ProvisionedVnetID         string                   `yaml:"provisioned_vnet_id"`
	ProvisionedVnetName       string                   `yaml:"provisioned_vnet_name"`
	VnetRsgName               string                   `yaml:"vnet_rsg_name"`
	DnsForwardDomains         []DNSForwardDomainConfig `yaml:"dns_forward_domains"`
	// CustomerManagedNetwork indicates the customer's landing zone owns subnet
	// address allocation (e.g. via Azure Virtual Network Manager / IPAM pools).
	// When true, the persistent step ignores subnet addressPrefix/addressPrefixes/
	// ipamPoolPrefixAllocations so Pulumi does not fight the customer's IPAM on
	// every deploy.
	CustomerManagedNetwork bool `yaml:"customer_managed_network"`
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
	// TraefikDeploymentReplicas sets the number of workload Traefik ingress replicas.
	// Defaults to 3 for high availability (resolve via ResolveAzureComponents).
	TraefikDeploymentReplicas *int `yaml:"traefik_deployment_replicas"`
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
	TraefikDeploymentReplicas int
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
		TraefikDeploymentReplicas: resolveInt(c.TraefikDeploymentReplicas, 3),
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
	// Route53 hosted-zone controls (persistent step). Mirror AWSSiteConfig.
	// PrivateZone defaults false; AutoAssociateProvisionedVpc and
	// CertificateValidationEnabled default TRUE in Python, so they are pointers
	// (nil => true).
	PrivateZone                  bool     `json:"private_zone" yaml:"private_zone"`
	VpcAssociations              []string `json:"vpc_associations" yaml:"vpc_associations"`
	AutoAssociateProvisionedVpc  *bool    `json:"auto_associate_provisioned_vpc" yaml:"auto_associate_provisioned_vpc"`
	CertificateValidationEnabled *bool    `json:"certificate_validation_enabled" yaml:"certificate_validation_enabled"`
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
