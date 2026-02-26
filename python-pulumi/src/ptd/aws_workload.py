from __future__ import annotations

import dataclasses
import ipaddress
import os
import typing
import uuid

import yaml

import ptd
import ptd.aws_iam
import ptd.paths
import ptd.shext
import ptd.workload

if typing.TYPE_CHECKING:
    import collections.abc

# Valid VPC endpoint service names that can be created by PTD
# Used for validation in VPCEndpointsConfig
VALID_VPC_ENDPOINT_SERVICES = frozenset(
    [
        "ec2",
        "ec2messages",
        "fsx",
        "kms",
        "s3",
        "ssm",
        "ssmmessages",
    ]
)

# Standard VPC endpoints created for PTD-managed VPCs (excludes FSX)
STANDARD_VPC_ENDPOINT_SERVICES = (
    "ec2",
    "ec2messages",
    "kms",
    "s3",
    "ssm",
    "ssmmessages",
)


@dataclasses.dataclass(frozen=True)
class AWSSiteConfig(ptd.SiteConfig):
    certificate_arn: str | None = None
    zone_id: str | None = None
    private_zone: bool = False
    vpc_associations: list[str] = dataclasses.field(
        default_factory=list
    )  # List of VPC IDs to associate with private zone
    auto_associate_provisioned_vpc: bool = True  # Automatically include provisioned VPC in associations (default: True)
    certificate_validation_enabled: bool = True  # Whether to validate certificates with DNS


def aws_load_workload_cluster_site_dict(
    cluster_site_dict: dict[str, typing.Any],
) -> tuple[AWSSiteConfig | None, bool]:
    site_spec = cluster_site_dict.get("spec", {})
    for key in list(site_spec.keys()):
        site_spec[key.replace("-", "_")] = site_spec.pop(key)

    return AWSSiteConfig(**site_spec), True


@dataclasses.dataclass(frozen=True)
class AWSProvisionedVPC:
    vpc_id: str
    cidr: str
    private_subnets: list[str] = dataclasses.field(default_factory=list)


@dataclasses.dataclass(frozen=True)
class AWSCustomRole:
    role_arn: str
    external_id: str


@dataclasses.dataclass(frozen=True)
class KarpenterRequirement:
    key: str
    operator: str
    values: list[str]


@dataclasses.dataclass(frozen=True)
class KarpenterLimits:
    cpu: str | None = None
    memory: str | None = None
    nvidia_com_gpu: str | None = None


@dataclasses.dataclass(frozen=True)
class KarpenterNodePool:
    name: str
    requirements: list[KarpenterRequirement] = dataclasses.field(default_factory=list)
    limits: KarpenterLimits | None = None
    expire_after: str | None = None
    taints: list[ptd.Taint] = dataclasses.field(default_factory=list)
    weight: int = 100
    root_volume_size: str = "100Gi"
    session_taints: bool = False  # Default False, opt-in for session isolation
    # Disruption configuration
    consolidation_policy: str = "WhenEmptyOrUnderutilized"  # Karpenter consolidation policy
    consolidate_after: str = "5m"  # Duration after which nodes are considered for consolidation
    # Overprovisioning configuration per nodepool
    overprovisioning_replicas: int = 0  # Number of overprovisioning pods for this pool (0 = disabled)
    overprovisioning_cpu_request: str | None = None  # CPU request per overprovisioning pod
    overprovisioning_memory_request: str | None = None  # Memory request per overprovisioning pod
    overprovisioning_nvidia_gpu_request: str | None = None  # GPU request per overprovisioning pod (optional)


@dataclasses.dataclass(frozen=True)
class KarpenterConfig:
    node_pools: list[KarpenterNodePool] = dataclasses.field(default_factory=list)


@dataclasses.dataclass(frozen=True)
class VPCEndpointsConfig:
    """Configuration for VPC endpoints creation.

    Controls which VPC endpoints are created in the workload VPC.
    Useful for handling AWS Service Control Policies (SCPs) that restrict
    endpoint creation or customer-specific network architectures.

    Examples:
        # Disable all VPC endpoints (for SCPs that block endpoint creation)
        vpc_endpoints:
          enabled: false

        # Exclude specific services
        vpc_endpoints:
          enabled: true
          excluded_services:
            - fsx
            - kms

        # Default behavior (all endpoints enabled)
        # No configuration needed - defaults to all endpoints enabled
    """

    enabled: bool = True
    excluded_services: collections.abc.Sequence[str] = dataclasses.field(default_factory=list)

    def __post_init__(self):
        """Validate that excluded_services only contains valid service names."""
        invalid_services = set(self.excluded_services) - VALID_VPC_ENDPOINT_SERVICES
        if invalid_services:
            msg = (
                f"Invalid service names in excluded_services: {sorted(invalid_services)}. "
                f"Valid services are: {sorted(VALID_VPC_ENDPOINT_SERVICES)}"
            )
            raise ValueError(msg)


@dataclasses.dataclass(frozen=True)
class AWSWorkloadConfig(ptd.WorkloadConfig):
    account_id: str
    autoscaling_enabled: bool = False
    clusters: dict[str, AWSWorkloadClusterConfig]
    sites: typing.Mapping[str, AWSSiteConfig]

    bastion_instance_type: str = "t4g.nano"
    db_allocated_storage: int = 100
    db_engine_version: str = "15.12"
    db_instance_class: str = "db.t3.small"
    db_performance_insights_enabled: bool = False
    db_deletion_protection: bool = False
    db_max_allocated_storage: int | None = None
    domain_source: ptd.ClusterDomainSource = ptd.ClusterDomainSource.LABEL
    keycloak_enabled: bool = False
    external_dns_enabled: bool = True
    hosted_zone_management_enabled: bool = True
    external_id: uuid.UUID | None = None
    extra_cluster_oidc_urls: list[str] = dataclasses.field(default_factory=list)
    extra_postgres_dbs: list[str] = dataclasses.field(default_factory=list)
    existing_flow_log_target_arns: list[str] | None = None
    fsx_openzfs_daily_automatic_backup_start_time: str = "02:00"
    fsx_openzfs_multi_az: bool = True
    fsx_openzfs_override_deployment_type: str | None = None
    fsx_openzfs_storage_capacity: int = 100
    fsx_openzfs_throughput_capacity: int = 320
    grafana_scrape_system_logs: bool = False  # only enable for debugging, runs agent as root
    load_balancer_per_site: bool = False
    nvidia_gpu_enabled: bool = False
    protect_persistent_resources: bool = True
    profile: str | None = None
    provisioned_vpc: AWSProvisionedVPC | None = None
    public_load_balancer: bool = True
    resource_tags: dict[str, str] = dataclasses.field(default_factory=dict)
    role_arn: str | None = None
    customer_managed_bastion_id: str | None = None
    custom_role: AWSCustomRole | None = None
    # When True, creates the PositTeamDedicatedAdmin IAM policy during bootstrap.
    # Required for workloads using custom_role where the standard PTD admin setup
    # doesn't automatically create the policy. The policy is used as both a
    # permissions boundary and attached managed policy for PTD-created IAM roles.
    create_admin_policy_as_resource: bool = False
    tailscale_enabled: bool = False
    secrets_store_addon_enabled: bool = False
    trusted_principals: list[str] = dataclasses.field(default_factory=list)
    vpc_az_count: int = 3
    vpc_cidr: str | None = None
    vpc_endpoints: VPCEndpointsConfig | None = None

    @property
    def db_multi_az(self) -> bool:
        """
        Determines whether Multi-AZ is enabled for the RDS database.
        Returns True if the environment is production, otherwise False.
        """
        return self.environment == ptd.Environments.production

    @property
    def hosted_zone_id(self) -> str | None:
        return self.sites[ptd.MAIN].zone_id

    def __post_init__(self) -> None:
        """Validate hosted zone configuration consistency."""
        if not self.hosted_zone_management_enabled:
            # When zones are disabled, validate requirements
            for site_name, site in self.sites.items():
                if not site.certificate_arn:
                    error_msg = (
                        f"Site '{site_name}': certificate_arn is required when hosted_zone_management_enabled is False"
                    )
                    raise ValueError(error_msg)
                if site.certificate_validation_enabled:
                    error_msg = (
                        f"Site '{site_name}': certificate_validation_enabled must be False "
                        "when hosted_zone_management_enabled is False"
                    )
                    raise ValueError(error_msg)

            # Require explicit ExternalDNS configuration
            # Note: external_dns_enabled has a default value of True, so we can't check for None
            # Instead, we'll document this requirement and potentially add a warning


@dataclasses.dataclass(frozen=True)
class AWSWorkloadClusterConfig(ptd.WorkloadClusterConfig):
    vpc_id: str | None = None
    ami_type: str = "AL2023_x86_64_STANDARD"
    cert: uuid.UUID | None = None
    cluster_version: str | None = "1.33.0"
    flavor: str = "eks-managedmachinepool"
    mp_instance_type: str = "t3.large"
    mp_min_size: int = 4
    mp_max_size: int = 10
    root_disk_size: int = 200  # Root disk size for managed node pool in GB
    routing_weight: str | None = "100"
    subnets: list[str] = dataclasses.field(default_factory=list)
    components: AWSWorkloadClusterComponentConfig | None = None
    additional_node_groups: dict[str, ptd.NodeGroupConfig] = dataclasses.field(default_factory=dict)
    public_endpoint_access: bool = True
    ebs_csi_addon_version: str = "v1.41.0-eksbuild.1"
    pod_identity_agent_version: str | None = None
    enable_pod_identity_agent: bool = False
    enable_external_secrets_operator: bool = False
    enable_nfs_subdir_provisioner: bool = False  # PVCs must carry the nfs.io/storage-path annotation; the storageClass pathPattern uses it to derive subdirectory paths
    enable_efs_csi_driver: bool = False
    efs_config: ptd.EFSConfig | None = None
    karpenter_config: KarpenterConfig | None = None

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.enable_external_secrets_operator and not self.enable_pod_identity_agent:
            msg = (
                "enable_external_secrets_operator requires enable_pod_identity_agent=True "
                "(ClusterSecretStore uses no auth block and relies on Pod Identity for credentials)."
            )
            raise ValueError(msg)


@dataclasses.dataclass(frozen=True)
class AWSWorkloadClusterComponentConfig(ptd.WorkloadClusterComponentConfig):
    aws_ebs_csi_driver_version: str | None = "2.31.0"
    aws_fsx_openzfs_csi_driver_version: str | None = "v1.0.0"
    aws_load_balancer_controller_version: str | None = "1.6.0"
    secret_store_csi_driver_aws_provider_version: str | None = "0.3.5"  # noqa: S105
    nvidia_device_plugin_version: str | None = "0.17.1"
    karpenter_version: str | None = "1.6.0"
    nfs_subdir_provisioner_version: str | None = "4.0.18"
    external_secrets_operator_version: str | None = "0.10.7"


class AWSWorkload(ptd.workload.AbstractWorkload):
    cfg: AWSWorkloadConfig

    @property
    def has_config(self):
        return hasattr(self, "cfg") and self.cfg.account_id is not None and self.cfg.account_id.strip() != ""

    def load_unique_config(self) -> None:
        cfg_dict = yaml.safe_load(self.ptd_yaml.read_text())
        if cfg_dict["kind"] != AWSWorkloadConfig.__name__ or cfg_dict["apiVersion"] != "posit.team/v1":
            msg = (
                f"mismatched workload config kind={cfg_dict['kind']!r} "
                f"apiVersion={cfg_dict['apiVersion']!r} in {str(self.ptd_yaml)!r}"
            )
            raise ValueError(msg)

        spec = self.spec

        clusters = {}
        for cluster_name, cluster_cfg_spec in sorted(spec.pop("clusters", {}).items()):
            cluster_cfg, ok = self._load_workload_cluster_config_dict(cluster_cfg_spec)
            if not ok:
                msg = f"failed to load config for cluster {cluster_name!r}"
                raise ValueError(msg)

            clusters[cluster_name] = cluster_cfg

        spec["clusters"] = clusters

        sites = {}
        for site_name, site_spec in sorted(spec.pop("sites", {}).items()):
            site_cfg, ok = aws_load_workload_cluster_site_dict(site_spec)
            if not ok or site_cfg is None:
                msg = f"failed to load config for site {site_name!r}"
                raise ValueError(msg)

            sites[site_name] = site_cfg

        spec["sites"] = sites

        if "external_id" in spec:
            spec["external_id"] = uuid.UUID(spec.pop("external_id", b"bad"))

        if "domain_source" in spec:
            spec["domain_source"] = ptd.ClusterDomainSource[str(spec["domain_source"])]

        if not hasattr(spec["network_trust"], "name"):
            spec["network_trust"] = ptd.NetworkTrust.__members__[str(spec["network_trust"]).upper()]

        if "provisioned_vpc" in spec:
            spec["provisioned_vpc"] = AWSProvisionedVPC(**spec["provisioned_vpc"])

        if "custom_role" in spec:
            spec["custom_role"] = AWSCustomRole(**spec["custom_role"])

        if "vpc_endpoints" in spec:
            vpc_endpoints_spec = spec.pop("vpc_endpoints")
            spec["vpc_endpoints"] = VPCEndpointsConfig(**vpc_endpoints_spec)

        self.cfg = AWSWorkloadConfig(**spec)

    def _load_workload_cluster_config_dict(
        self,
        cluster_cfg_dict: dict[str, typing.Any],
    ) -> tuple[AWSWorkloadClusterConfig | None, bool]:
        cluster_spec = cluster_cfg_dict.get("spec", {})
        cluster_spec["components"] = AWSWorkloadClusterComponentConfig(**cluster_spec.pop("components", {}))

        adl_ng = cluster_spec.pop("additional_node_groups", {})
        cluster_spec["additional_node_groups"] = {}
        for k, v in adl_ng.items():
            cluster_spec["additional_node_groups"][k] = ptd.NodeGroupConfig(**v)

        # Handle karpenter_config
        if "karpenter_config" in cluster_spec:
            karpenter_config_spec = cluster_spec.pop("karpenter_config")
            node_pools = []

            for pool_spec in karpenter_config_spec.get("node_pools", []):
                requirements = [KarpenterRequirement(**req_spec) for req_spec in pool_spec.get("requirements", [])]

                limits = None
                if "limits" in pool_spec:
                    limits_spec = pool_spec["limits"].copy()
                    # Handle the nvidia.com/gpu field which has a dot and slash in the name
                    nvidia_gpu = limits_spec.pop("nvidia.com/gpu", None)
                    limits = KarpenterLimits(
                        cpu=limits_spec.get("cpu"), memory=limits_spec.get("memory"), nvidia_com_gpu=nvidia_gpu
                    )

                # Parse taints if present
                taints = [ptd.Taint(**taint_spec) for taint_spec in pool_spec.get("taints", [])]

                # Handle session_taints field
                session_taints = pool_spec.get("session_taints", False)
                if session_taints:
                    session_taint = ptd.Taint(key="workload-type", value="session", effect="NoSchedule")
                    # Avoid duplicates - check if this taint already exists
                    if not any(t.key == "workload-type" and t.effect == "NoSchedule" for t in taints):
                        taints.append(session_taint)

                node_pools.append(
                    KarpenterNodePool(
                        name=pool_spec["name"],
                        requirements=requirements,
                        limits=limits,
                        expire_after=pool_spec.get("expire_after"),
                        taints=taints,
                        weight=pool_spec.get("weight", 100),
                        root_volume_size=pool_spec.get("root_volume_size", "100Gi"),
                        session_taints=session_taints,
                        consolidation_policy=pool_spec.get("consolidation_policy", "WhenEmptyOrUnderutilized"),
                        consolidate_after=pool_spec.get("consolidate_after", "5m"),
                        overprovisioning_replicas=pool_spec.get("overprovisioning_replicas", 0),
                        overprovisioning_cpu_request=pool_spec.get("overprovisioning_cpu_request"),
                        overprovisioning_memory_request=pool_spec.get("overprovisioning_memory_request"),
                        overprovisioning_nvidia_gpu_request=pool_spec.get("overprovisioning_nvidia_gpu_request"),
                    )
                )

            # Convert snake_case keys to match dataclass fields
            karpenter_config_dict = {}
            for key, value in karpenter_config_spec.items():
                if key == "node_pools":
                    karpenter_config_dict["node_pools"] = node_pools
                else:
                    karpenter_config_dict[key.replace("-", "_")] = value

            cluster_spec["karpenter_config"] = KarpenterConfig(**karpenter_config_dict)

        # Handle efs_config
        if "efs_config" in cluster_spec:
            efs_config_spec = cluster_spec.pop("efs_config")
            cluster_spec["efs_config"] = ptd.EFSConfig(**efs_config_spec)

        for key in list(cluster_spec.keys()):
            cluster_spec[key.replace("-", "_")] = cluster_spec.pop(key)

        team_operator_image = cluster_spec.pop("team_operator_image", "latest").strip().lower()
        cluster_spec["team_operator_image"] = {"": "latest"}.get(team_operator_image, team_operator_image)

        ptd_controller_image = cluster_spec.pop("ptd_controller_image", "latest").strip().lower()

        cluster_spec["ptd_controller_image"] = {"": "latest"}.get(ptd_controller_image, ptd_controller_image)

        # Handle eks_access_entries configuration if present
        if "eks_access_entries" in cluster_spec:
            eks_access_entries_dict = cluster_spec.pop("eks_access_entries")
            if isinstance(eks_access_entries_dict, dict):
                cluster_spec["eks_access_entries"] = ptd.EKSAccessEntriesConfig(**eks_access_entries_dict)
            else:
                cluster_spec["eks_access_entries"] = eks_access_entries_dict

        # Handle team_operator_tolerations configuration if present
        if "team_operator_tolerations" in cluster_spec:
            tolerations_list = cluster_spec.pop("team_operator_tolerations")
            cluster_spec["team_operator_tolerations"] = tuple(ptd.Toleration(**t) for t in tolerations_list)

        return AWSWorkloadClusterConfig(**cluster_spec), True

    def role_env(self) -> dict[str, str]:
        env = os.environ.copy()
        env.setdefault(
            "PULUMI_BACKEND_URL",
            self.state_backend_url,
        )

        return (
            env
            | ptd.aws_env_from_session_credentials(self.aws_assume_role()["Credentials"])
            | {"AWS_REGION": self.cfg.region}
        )

    @property
    def secrets_provider_url(self) -> str:
        return f"awskms://{ptd.MGMT_KMS_KEY_ALIAS}?region={self.cfg.region}"

    @property
    def state_bucket(self) -> str:
        return f"{self.prefix}-{self.compound_name}"

    @property
    def state_backend_url(self) -> str:
        return f"s3://{self.state_bucket}?region={self.cfg.region}"

    @property
    def role_arn(self) -> str:
        if self.cfg.custom_role is not None:
            return self.cfg.custom_role.role_arn

        if self.cfg.role_arn is not None:
            return self.cfg.role_arn

        return f"arn:aws:iam::{self.cfg.account_id}:role/{ptd.Roles.POSIT_TEAM_ADMIN}"

    @property
    def cloud_provider(self) -> ptd.CloudProvider.AWS:
        return ptd.CloudProvider.AWS

    @property
    def image_registry_hostname(self) -> str:
        return "docker.io/posit"

    @property
    def team_operator_policy_name(self) -> str:
        return f"team-operator.{self.compound_name}.posit.team"

    def ppm_s3_bucket_policy_name_site(self, release: str, site: str) -> str:
        return f"ppm-s3-bucket.{release}.{site}.{self.compound_name}.posit.team"

    def chronicle_s3_bucket_policy_name(self, release: str, site: str) -> str:
        return f"chronicle-s3-bucket.{release}.{site}.{self.compound_name}.posit.team"

    def chronicle_read_only_s3_bucket_policy_name(self, release: str, site: str) -> str:
        return f"chronicle-s3-bucket-read-only.{release}.{site}.{self.compound_name}.posit.team"

    @property
    def external_dns_role_name(self) -> str:
        return f"external-dns.{self.compound_name}.posit.team"

    @property
    def grafana_agent_role_name(self) -> str:
        """
        DEPRECATED: This property is maintained for backward compatibility only.
        Use alloy_role_name instead for new implementations.

        Will be removed in a future release.
        """
        return f"grafana-agent.{self.compound_name}.posit.team"

    @property
    def alloy_role_name(self) -> str:
        """
        Returns the IAM role name for the Grafana Alloy monitoring system.
        This is the preferred property to use for all new implementations.
        """
        return f"alloy.{self.compound_name}.posit.team"

    @property
    def loki_s3_bucket_name(self) -> str:
        return f"{self.compound_name}-loki"

    @property
    def loki_s3_bucket_policy_name(self) -> str:
        return f"loki-s3-bucket.{self.compound_name}.posit.team"

    @property
    def loki_role_name(self) -> str:
        return f"loki.{self.compound_name}.posit.team"

    @property
    def lbc_role_name(self) -> str:
        return f"aws-load-balancer-controller.{self.compound_name}.posit.team"

    @property
    def lbc_policy_name(self) -> str:
        return f"lbc.{self.compound_name}.posit.team"

    @property
    def mimir_s3_bucket_name(self) -> str:
        return f"{self.compound_name}-mimir"

    @property
    def mimir_s3_bucket_policy_name(self) -> str:
        return f"mimir-s3-bucket.{self.compound_name}.posit.team"

    @property
    def mimir_role_name(self) -> str:
        return f"mimir.{self.compound_name}.posit.team"

    @property
    def dns_update_policy_name(self) -> str:
        return f"dns-update.{self.compound_name}.posit.team"

    @property
    def grafana_agent_policy_name(self) -> str:
        """
        DEPRECATED: This property is maintained for backward compatibility only.
        Use alloy_policy_name instead for new implementations.

        Will be removed in a future release.
        """
        return f"grafana-agent.{self.compound_name}.posit.team"

    @property
    def alloy_policy_name(self) -> str:
        """
        Returns the IAM policy name for the Grafana Alloy monitoring system.
        This is the preferred property to use for all new implementations.
        """
        return f"alloy.{self.compound_name}.posit.team"

    @property
    def traefik_forward_auth_read_secrets_policy_name(self) -> str:
        return f"traefik-forward-auth-read-secrets.{self.compound_name}.posit.team"

    @property
    def traefik_forward_auth_role_name(self) -> str:
        return f"traefik-forward-auth.{self.compound_name}.posit.team"

    def okta_oidc_client_creds_secret(self, site: str) -> str:
        return f"okta-oidc-client-creds.{self.compound_name}-{site}.posit.team"

    @property
    def fsx_nfs_sg_name(self) -> str:
        return f"fsx-nfs.{self.compound_name}.posit.team"

    @property
    def ebs_csi_role_name(self) -> str:
        return f"aws-ebs-csi.{self.compound_name}.posit.team"

    @property
    def fsx_openzfs_role_name(self) -> str:
        return f"aws-fsx-openzfs-csi-driver.{self.compound_name}.posit.team"

    def external_secrets_role_name(self, release: str) -> str:
        return f"external-secrets.{release}.{self.compound_name}.posit.team"

    def cluster_home_role_name(self, release: str) -> str:
        return f"home.{release}.{self.compound_name}.posit.team"

    def cluster_connect_role_name(self, release: str) -> str:
        return f"pub.{release}.{self.compound_name}.posit.team"

    def cluster_connect_session_role_name(self, release: str, site_name: str) -> str:
        return f"pub-ses.{release}.{site_name}.{self.compound_name}.posit.team"

    def cluster_workbench_role_name(self, release: str) -> str:
        return f"dev.{release}.{self.compound_name}.posit.team"

    def cluster_workbench_session_role_name(self, release: str, site_name: str) -> str:
        return f"dev-ses.{release}.{site_name}.{self.compound_name}.posit.team"

    def cluster_packagemanager_role_name(self, release: str, site_name: str) -> str:
        return f"pkg.{release}.{site_name}.{self.compound_name}.posit.team"

    def cluster_chronicle_role_name(self, release: str, site_name: str) -> str:
        return f"chr.{release}.{site_name}.{self.compound_name}.posit.team"

    def cluster_chronicle_read_only_role_name(self, release: str, site_name: str) -> str:
        return f"chr-ro.{release}.{site_name}.{self.compound_name}.posit.team"

    def cluster_keycloak_role_name(self, release: str) -> str:
        return f"keycloak.{release}.{self.compound_name}.posit.team"

    def cluster_ptd_controller_role_name(self, release: str) -> str:
        return f"ptd-controller.{release}.{self.compound_name}.posit.team"

    def cluster_team_operator_role_name(self, release: str) -> str:
        return f"team-operator.{release}.{self.compound_name}.posit.team"

    def cluster_loki_role_name(self, release: str) -> str:
        return f"loki.{release}.{self.compound_name}.posit.team"

    def sessions_role_name(self, release: str, site_name: str) -> str:
        return f"ses.{release}.{site_name}.{self.compound_name}.posit.team"

    def sessions_role_arn(self, release: str, site_name: str) -> str:
        return f"arn:aws:iam::{self.cfg.account_id}:role/{self.sessions_role_name(release, site_name)}"

    @property
    def required_tags(self) -> dict[str, str]:
        return (self.cfg.resource_tags or {}) | {
            str(ptd.TagKeys.POSIT_TEAM_TRUE_NAME): self.cfg.true_name,
            str(ptd.TagKeys.POSIT_TEAM_ENVIRONMENT): self.cfg.environment,
        }

    def vpc_cidr(self, release: str = ptd.ZERO) -> ipaddress.IPv4Network:
        if self.cfg.provisioned_vpc is not None:
            return ipaddress.IPv4Network(self.cfg.provisioned_vpc.cidr)
        if self.cfg.vpc_cidr is not None:
            return ipaddress.IPv4Network(self.cfg.vpc_cidr)
        second_octet = sum([ord(c) for c in self.fully_qualified_name(release)]) % 255
        return ipaddress.IPv4Network(f"10.{second_octet}.0.0/16")

    def vpc(self) -> dict[str, typing.Any]:
        vpcs = ptd.shext.shj(
            [
                "aws",
                "ec2",
                "describe-vpcs",
                "--filters",
                f"Name=tag:Name,Values={self.compound_name}",
                f"Name=tag-key,Values={ptd.TagKeys.POSIT_TEAM_MANAGED_BY}",
            ],
        ).get("Vpcs", [])

        if len(vpcs) == 0:
            return {}

        return vpcs[0]

    def subnets(self, subnet_type: str) -> list[dict[str, typing.Any]]:
        vpc_id = None
        tag_filters = None
        if self.cfg.provisioned_vpc is not None:
            if subnet_type != "private":
                msg = f"currently only private subnets are supported in pre-provisioned VPCs, not {subnet_type!r}"
                raise ValueError(msg)

            vpc_id = self.cfg.provisioned_vpc.vpc_id
            tag_filters = [{"Name": "tag:Name", "Values": self.cfg.provisioned_vpc.private_subnets}]

        return ptd.aws_subnets_for_vpc(
            name=self.compound_name,
            network_access=subnet_type,
            tag_filters=tag_filters,
            vpc_id=vpc_id,
            region=self.cfg.region,
        )

    def managed_clusters(self, *, assume_role: bool = True) -> list[dict[str, typing.Any]]:
        exe_env = os.environ.copy()
        if assume_role:
            exe_env |= self.role_env()

        return ptd.aws_eks_clusters(self.compound_name, exe_env=exe_env, region=self.cfg.region)

    def managed_clusters_by_release(self, *, assume_role: bool = True) -> dict[str, dict[str, typing.Any]]:
        clusters_by_release = {}
        clusters = self.managed_clusters(assume_role=assume_role)
        for cluster in clusters:
            name: str = cluster["cluster"]["name"]
            if name.startswith(f"default_{self.compound_name}-") and name.endswith("-control-plane"):
                name = name.removeprefix(f"default_{self.compound_name}-").removesuffix("-control-plane")
            else:
                name = name.removeprefix(f"{self.compound_name}-")

            clusters_by_release[name] = cluster
        return clusters_by_release

    def federated_endpoints(self, *, assume_role: bool = True) -> dict[str, list[str]]:
        exe_env = os.environ.copy()
        if assume_role:
            exe_env |= self.role_env()

        return ptd.aws_iam.aws_iam_roles_federated(exe_env=exe_env)

    def domain_hosted_zone(self) -> dict[str, typing.Any]:
        if self.cfg.hosted_zone_id is not None:
            return ptd.shext.shj(
                [
                    "aws",
                    "route53",
                    "get-hosted-zone",
                    "--id",
                    self.cfg.hosted_zone_id,
                    "--output",
                    "json",
                ]
            ).get("HostedZone", {})

        for hosted_zone in ptd.shext.shj(
            [
                "aws",
                "route53",
                "list-hosted-zones",
                "--output",
                "json",
                "--no-paginate",
            ],
        ).get("HostedZones", []):
            # Check if we should consider private zones based on site configuration
            site_config = self.cfg.sites.get(self.cfg.default_site, AWSSiteConfig())
            # Skip if zone privacy doesn't match site configuration
            is_private_mismatch = hosted_zone["Config"]["PrivateZone"] != site_config.private_zone
            if is_private_mismatch:
                continue

            # TODO: fetch this hosted zone in a more rigorous/reliable way
            comment = hosted_zone["Config"]["Comment"].lower()
            if comment.startswith(f"hosted zone for the posit team dedicated service in {self.compound_name}."):
                return hosted_zone

        return {}

    def cluster_kubeconfig(self, release: str, *, assume_role: bool = False) -> dict[str, typing.Any]:
        exe_env = os.environ.copy()
        if assume_role:
            exe_env |= self.role_env()

        cluster_name = f"{self.compound_name}-{release}"
        kubeconfig = yaml.safe_load(
            ptd.aws_eks_kubeconfig(
                cluster_name,
                exe_env,
                region=self.cfg.region,
            )
        )

        if not self.cfg.tailscale_enabled:
            kubeconfig["clusters"][0]["cluster"]["proxy-url"] = "socks5://localhost:1080"
        return kubeconfig

    def aws_assume_role(self, aws_region: str | None = None) -> ptd.AWSSession:
        external_id: str | uuid.UUID | None = self.cfg.external_id
        if self.cfg.custom_role is not None:
            external_id = self.cfg.custom_role.external_id

        return ptd.aws_assume_workload_account_role(
            role_arn=self.role_arn,
            external_id=external_id,
            region=(aws_region or self.cfg.region),
        )

    def annotations(self, release: str) -> dict[str, str]:
        return {
            "release": release,
            "awsAccountID": str(self.cfg.account_id),
        }

    def labels(self, release: str) -> dict[str, str]:
        return {
            "trueName": self.cfg.true_name,
            "environment": self.cfg.environment,
            "release": release,
            "awsAccountID": str(self.cfg.account_id),
            "region": self.cfg.region,
            "domain": self.cfg.domain,
            "clusterName": self.eks_cluster_name(release),
        }

    def eks_cluster_name(self, release: str = ptd.ZERO) -> str:
        fqn = self.fully_qualified_name(release)
        return f"default_{fqn}-control-plane"

    @property
    def secret_name(self) -> str:
        return f"{self.compound_name}.posit.team"

    def site_secret_name(self, site_name: str) -> str:
        return f"{self.compound_name}-{site_name}.posit.team"

    def site_sessions_vault_name(self, site_name: str) -> str:
        return f"{self.compound_name}-{site_name}.sessions.posit.team"

    @property
    def iam_permissions_boundary(self) -> str:
        return f"arn:aws:iam::{self.cfg.account_id}:policy/{ptd.aws_iam.POSIT_TEAM_IAM_PERMISSIONS_BOUNDARY}"

    def resolve_image_digest(self, _repository: ptd.ComponentImages, tag: str = ptd.LATEST) -> tuple[str, bool]:
        # Simply return the tag as-is - no digest resolution needed for public Docker Hub
        if not tag or tag == ptd.LATEST:
            return "latest", True
        return tag, True
