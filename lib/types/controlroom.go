package types

// EKSAccessEntriesConfig holds configuration for EKS Access Entries
type EKSAccessEntriesConfig struct {
	Enabled                    bool                     `json:"enabled" yaml:"enabled"`
	AdditionalEntries          []map[string]interface{} `json:"additional_entries" yaml:"additional_entries"`
	IncludeSameAccountPoweruser bool                     `json:"include_same_account_poweruser" yaml:"include_same_account_poweruser"`
}

// TrustedUserIpAddress represents an IP address for a trusted user
type TrustedUserIpAddress struct {
	Ip      string `json:"ip" yaml:"ip"`
	Comment string `json:"comment" yaml:"comment"`
}

// TrustedUser represents a trusted user with contact info and IP addresses
type TrustedUser struct {
	Email       string                 `json:"email" yaml:"email"`
	GivenName   string                 `json:"given_name" yaml:"given_name"`
	FamilyName  string                 `json:"family_name" yaml:"family_name"`
	IpAddresses []TrustedUserIpAddress `json:"ip_addresses" yaml:"ip_addresses"`
}

type AWSControlRoomConfig struct {
	AccountID                        string                  `json:"account_id" yaml:"account_id"`
	PowerUserARN                     string                  `json:"power_user_arn" yaml:"power_user_arn"`
	Domain                           string                  `json:"domain" yaml:"domain"`
	Environment                      string                  `json:"environment" yaml:"environment"`
	TrueName                         string                  `json:"true_name" yaml:"true_name"`
	EksAccessEntries                 *EKSAccessEntriesConfig `json:"eks_access_entries" yaml:"eks_access_entries"`
	DBAllocatedStorage               int                     `json:"db_allocated_storage" yaml:"db_allocated_storage"`
	DBEngineVersion                  string            `json:"db_engine_version" yaml:"db_engine_version"`
	DBInstanceClass                  string            `json:"db_instance_class" yaml:"db_instance_class"`
	EksK8sVersion                    *string           `json:"eks_k8s_version" yaml:"eks_k8s_version"`
	EksNodeGroupMax                  int               `json:"eks_node_group_max" yaml:"eks_node_group_max"`
	EksNodeGroupMin                  int               `json:"eks_node_group_min" yaml:"eks_node_group_min"`
	EksNodeInstanceType              string            `json:"eks_node_instance_type" yaml:"eks_node_instance_type"`
	HostedZoneID                     *string           `json:"hosted_zone_id" yaml:"hosted_zone_id"`
	ManageEcrRepositories            bool              `json:"manage_ecr_repositories" yaml:"manage_ecr_repositories"`
	ProtectPersistentResources       bool              `json:"protect_persistent_resources" yaml:"protect_persistent_resources"`
	Region                           string            `json:"region" yaml:"region"`
	ResourceTags                     map[string]string `json:"resource_tags" yaml:"resource_tags"`
	TraefikDeploymentReplicas        int           `json:"traefik_deployment_replicas" yaml:"traefik_deployment_replicas"`
	TrustedUsers                     []TrustedUser `json:"trusted_users" yaml:"trusted_users"`
	FrontDoor                        *string       `json:"front_door" yaml:"front_door"`
	AwsFsxOpenzfsCsiVersion          string            `json:"aws_fsx_openzfs_csi_version" yaml:"aws_fsx_openzfs_csi_version"`
	AwsLbcVersion                    string            `json:"aws_lbc_version" yaml:"aws_lbc_version"`
	ExternalDnsVersion               string            `json:"external_dns_version" yaml:"external_dns_version"`
	GrafanaVersion                   string            `json:"grafana_version" yaml:"grafana_version"`
	KubeStateMetricsVersion          string            `json:"kube_state_metrics_version" yaml:"kube_state_metrics_version"`
	MetricsServerVersion             string            `json:"metrics_server_version" yaml:"metrics_server_version"`
	MimirVersion                     string            `json:"mimir_version" yaml:"mimir_version"`
	SecretStoreCsiAwsProviderVersion string            `json:"secret_store_csi_aws_provider_version" yaml:"secret_store_csi_aws_provider_version"`
	SecretStoreCsiVersion            string            `json:"secret_store_csi_version" yaml:"secret_store_csi_version"`
	TailscaleEnabled                 bool              `json:"tailscale_enabled" yaml:"tailscale_enabled"`
	TigeraOperatorVersion            string            `json:"tigera_operator_version" yaml:"tigera_operator_version"`
	TraefikForwardAuthVersion        string            `json:"traefik_forward_auth_version" yaml:"traefik_forward_auth_version"`
	TraefikVersion                   string            `json:"traefik_version" yaml:"traefik_version"`
}
