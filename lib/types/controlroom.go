package types

// EKSAccessEntriesConfig holds configuration for EKS Access Entries.
//
// Enabled is a pointer so an absent field can be distinguished from an explicit
// false. The Python default is True (ptd.EKSAccessEntriesConfig.enabled = True),
// so an unset Enabled (nil) must resolve to true — i.e. access entries are the
// default auth path. A plain bool would resolve absent→false→legacy aws-auth,
// which mismatches Python and would DELETE the live EKS access entries. Resolve
// via IsEnabled / the spec-level UsesEksAccessEntries helpers (nil → true).
type EKSAccessEntriesConfig struct {
	Enabled                     *bool                    `json:"enabled" yaml:"enabled"`
	AdditionalEntries           []map[string]interface{} `json:"additional_entries" yaml:"additional_entries"`
	IncludeSameAccountPoweruser bool                     `json:"include_same_account_poweruser" yaml:"include_same_account_poweruser"`
}

// IsEnabled resolves the access-entries Enabled flag (Python default True).
// Returns true when the config is nil, when Enabled is unset (nil), or when
// Enabled is explicitly true; returns false only when Enabled is explicitly
// set to false.
func (c *EKSAccessEntriesConfig) IsEnabled() bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
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
	AccountID                  string                  `json:"account_id" yaml:"account_id"`
	PowerUserARN               string                  `json:"power_user_arn" yaml:"power_user_arn"`
	Domain                     string                  `json:"domain" yaml:"domain"`
	Environment                string                  `json:"environment" yaml:"environment"`
	TrueName                   string                  `json:"true_name" yaml:"true_name"`
	EksAccessEntries           *EKSAccessEntriesConfig `json:"eks_access_entries" yaml:"eks_access_entries"`
	DBAllocatedStorage         int                     `json:"db_allocated_storage" yaml:"db_allocated_storage"`
	DBEngineVersion            string                  `json:"db_engine_version" yaml:"db_engine_version"`
	DBInstanceClass            string                  `json:"db_instance_class" yaml:"db_instance_class"`
	EksK8sVersion              *string                 `json:"eks_k8s_version" yaml:"eks_k8s_version"`
	EksNodeGroupMax            int                     `json:"eks_node_group_max" yaml:"eks_node_group_max"`
	EksNodeGroupMin            int                     `json:"eks_node_group_min" yaml:"eks_node_group_min"`
	EksNodeInstanceType        string                  `json:"eks_node_instance_type" yaml:"eks_node_instance_type"`
	HostedZoneID               *string                 `json:"hosted_zone_id" yaml:"hosted_zone_id"`
	ManageEcrRepositories      bool                    `json:"manage_ecr_repositories" yaml:"manage_ecr_repositories"`
	ProtectPersistentResources bool                    `json:"protect_persistent_resources" yaml:"protect_persistent_resources"`
	Region                     string                  `json:"region" yaml:"region"`
	ResourceTags               map[string]string       `json:"resource_tags" yaml:"resource_tags"`
	// IgnoreTags is a flat list of exact AWS tag keys that the Pulumi AWS provider should
	// never add or remove on managed resources. Used so customer-applied tags are left
	// untouched by our IaC. AWS-only; wired into the provider's ignoreTags.keys.
	IgnoreTags                       []string      `json:"ignore_tags" yaml:"ignore_tags"`
	TraefikDeploymentReplicas        int           `json:"traefik_deployment_replicas" yaml:"traefik_deployment_replicas"`
	TrustedUsers                     []TrustedUser `json:"trusted_users" yaml:"trusted_users"`
	FrontDoor                        *string       `json:"front_door" yaml:"front_door"`
	AwsFsxOpenzfsCsiVersion          string        `json:"aws_fsx_openzfs_csi_version" yaml:"aws_fsx_openzfs_csi_version"`
	AwsLbcVersion                    string        `json:"aws_lbc_version" yaml:"aws_lbc_version"`
	ExternalDnsVersion               string        `json:"external_dns_version" yaml:"external_dns_version"`
	GrafanaVersion                   string        `json:"grafana_version" yaml:"grafana_version"`
	KubeStateMetricsVersion          string        `json:"kube_state_metrics_version" yaml:"kube_state_metrics_version"`
	MetricsServerVersion             string        `json:"metrics_server_version" yaml:"metrics_server_version"`
	MimirVersion                     string        `json:"mimir_version" yaml:"mimir_version"`
	SecretStoreCsiAwsProviderVersion string        `json:"secret_store_csi_aws_provider_version" yaml:"secret_store_csi_aws_provider_version"`
	SecretStoreCsiVersion            string        `json:"secret_store_csi_version" yaml:"secret_store_csi_version"`
	TailscaleEnabled                 bool          `json:"tailscale_enabled" yaml:"tailscale_enabled"`
	TigeraOperatorVersion            string        `json:"tigera_operator_version" yaml:"tigera_operator_version"`
	TraefikForwardAuthVersion        string        `json:"traefik_forward_auth_version" yaml:"traefik_forward_auth_version"`
	TraefikVersion                   string        `json:"traefik_version" yaml:"traefik_version"`
	EbsCsiAddonVersion               string        `json:"ebs_csi_addon_version" yaml:"ebs_csi_addon_version"`
}

// The following accessor methods return the value from config, or the Python
// dataclass default (python-pulumi/src/ptd/aws_control_room.py) when the YAML
// omits the field. Control-room ptd.yaml files do NOT pin these versions, so the
// defaults must match Python verbatim or Pulumi would diff every chart/addon.

func crStringDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func crIntDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// EKSK8sVersion resolves the EKS Kubernetes version (Python default "1.30").
func (c AWSControlRoomConfig) EKSK8sVersion() string {
	if c.EksK8sVersion != nil && *c.EksK8sVersion != "" {
		return *c.EksK8sVersion
	}
	return "1.30"
}

// UsesEksAccessEntries reports whether the EKS access-entries auth path should be
// used (as opposed to the legacy aws-auth ConfigMap). Mirrors Python, where
// EKSAccessEntriesConfig.enabled defaults to True and aws_control_room.py supplies
// a default EKSAccessEntriesConfig when the block is absent. Net semantics:
// nil block → true; block present with enabled unset → true; enabled: true → true;
// only an explicit enabled: false selects legacy aws-auth.
func (c AWSControlRoomConfig) UsesEksAccessEntries() bool {
	return c.EksAccessEntries.IsEnabled()
}

// EKSNodeInstanceType resolves the node instance type (Python default "m6a.xlarge").
func (c AWSControlRoomConfig) EKSNodeInstanceType() string {
	return crStringDefault(c.EksNodeInstanceType, "m6a.xlarge")
}

// EKSNodeGroupMin resolves the node group min size (Python default 3).
func (c AWSControlRoomConfig) EKSNodeGroupMin() int { return crIntDefault(c.EksNodeGroupMin, 3) }

// EKSNodeGroupMax resolves the node group max size (Python default 3).
func (c AWSControlRoomConfig) EKSNodeGroupMax() int { return crIntDefault(c.EksNodeGroupMax, 3) }

// TraefikDeploymentReplicasOrDefault resolves the traefik replica count (Python default 3).
func (c AWSControlRoomConfig) TraefikDeploymentReplicasOrDefault() int {
	return crIntDefault(c.TraefikDeploymentReplicas, 3)
}

// EBSCsiAddonVersion resolves the EBS CSI addon version (Python default v1.41.0-eksbuild.1).
func (c AWSControlRoomConfig) EBSCsiAddonVersion() string {
	return crStringDefault(c.EbsCsiAddonVersion, "v1.41.0-eksbuild.1")
}

// AWSLbcVersion resolves the aws-load-balancer-controller version (Python default 1.6.0).
func (c AWSControlRoomConfig) AWSLbcVersion() string {
	return crStringDefault(c.AwsLbcVersion, "1.6.0")
}

// MetricsServerVersion resolves the metrics-server chart version (Python default 3.11.0).
func (c AWSControlRoomConfig) MetricsServerVersionOrDefault() string {
	return crStringDefault(c.MetricsServerVersion, "3.11.0")
}

// SecretStoreCsiVersionOrDefault resolves the secrets-store CSI chart version (Python default 1.3.4).
func (c AWSControlRoomConfig) SecretStoreCsiVersionOrDefault() string {
	return crStringDefault(c.SecretStoreCsiVersion, "1.3.4")
}

// SecretStoreCsiAwsProviderVersionOrDefault resolves the AWS provider chart version (Python default 0.3.5).
func (c AWSControlRoomConfig) SecretStoreCsiAwsProviderVersionOrDefault() string {
	return crStringDefault(c.SecretStoreCsiAwsProviderVersion, "0.3.5")
}

// TraefikForwardAuthVersionOrDefault resolves the traefik-forward-auth chart version (Python default 0.0.14).
func (c AWSControlRoomConfig) TraefikForwardAuthVersionOrDefault() string {
	return crStringDefault(c.TraefikForwardAuthVersion, "0.0.14")
}

// GrafanaVersionOrDefault resolves the grafana chart version (Python default 7.0.14).
func (c AWSControlRoomConfig) GrafanaVersionOrDefault() string {
	return crStringDefault(c.GrafanaVersion, "7.0.14")
}

// MimirVersionOrDefault resolves the mimir-distributed chart version (Python default 5.1.3).
func (c AWSControlRoomConfig) MimirVersionOrDefault() string {
	return crStringDefault(c.MimirVersion, "5.1.3")
}

// TraefikVersionOrDefault resolves the traefik chart version (Python default 24.0.0).
func (c AWSControlRoomConfig) TraefikVersionOrDefault() string {
	return crStringDefault(c.TraefikVersion, "24.0.0")
}
