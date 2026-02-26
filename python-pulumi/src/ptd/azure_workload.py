from __future__ import annotations

import dataclasses
import functools
import ipaddress
import json
import os
import re
import tempfile
import typing

import yaml

import ptd
import ptd.azure_sdk
import ptd.shext
import ptd.workload

if typing.TYPE_CHECKING:
    import pathlib


@dataclasses.dataclass(frozen=True)
class NetworkConfig:
    private_subnet_cidr: str
    db_subnet_cidr: str
    netapp_subnet_cidr: str
    app_gateway_subnet_cidr: str
    bastion_subnet_cidr: str
    vnet_cidr: str | None = None
    provisioned_vnet_name: str | None = None
    public_subnet_cidr: str | None = None
    vnet_rsg_name: str | None = None
    dns_forward_domains: list[dict[str, str]] = dataclasses.field(default_factory=list)
    private_subnet_route_table_id: str | None = None

    def __post_init__(self):
        """Validate DNS forward domain entries if configured."""
        if not self.dns_forward_domains:
            return

        for domain_obj in self.dns_forward_domains:
            host = domain_obj["host"]
            ip = domain_obj["ip"]

            try:
                ipaddress.ip_address(ip)
            except ValueError as e:
                msg = f"DNS forward domain for {host} has invalid IP address '{ip}': {e}"
                raise ValueError(msg) from e


@dataclasses.dataclass(frozen=True)
class AzureWorkloadConfig(ptd.WorkloadConfig):
    clusters: dict[str, AzureWorkloadClusterConfig]
    subscription_id: str
    tenant_id: str
    client_id: str
    secrets_provider_client_id: str
    network: NetworkConfig

    root_domain: str | None = None
    instance_type: str = "Standard_D2_v4"
    control_plane_node_count: int = 1
    worker_node_count: int = 1
    db_storage_size_gb: int = 128
    resource_tags: dict[str, str] | None = None
    protect_persistent_resources: bool = True
    admin_group_id: str | None = None
    bastion_instance_type: str = "Standard_B1s"
    ppm_file_share_size_gib: int = 100  # Minimum size for PPM Azure File Share in GiB


@dataclasses.dataclass(frozen=True)
class AzureUserNodePoolConfig:
    name: str
    vm_size: str
    min_count: int
    max_count: int
    initial_count: int | None = None
    enable_auto_scaling: bool = True
    availability_zones: list[str] | None = None
    node_taints: list[str] | None = None
    node_labels: dict[str, str] | None = None
    max_pods: int | None = None
    root_disk_size: int | None = None


@dataclasses.dataclass(frozen=True)
class AzureWorkloadClusterConfig(ptd.WorkloadClusterConfig):
    components: AzureWorkloadClusterComponentConfig | None = None
    kubernetes_version: str | None = "v1.31.1"
    outbound_type: str = "LoadBalancer"
    public_endpoint_access: bool = True
    system_node_pool_instance_type: str | None = "Standard_D2s_v6"

    # Legacy field - maintained for backward compatibility
    # Used to configure the hardcoded "userpool" in AgentPoolProfiles for legacy clusters
    user_node_pool_instance_type: str | None = "Standard_D2s_v6"

    # defines additional user node pools as separate AgentPool resources
    # Works for both new clusters (all user pools) and legacy clusters (additional pools)
    user_node_pools: list[AzureUserNodePoolConfig] | None = None

    # Optional: explicit flag to control whether to include legacy user pool in agentPoolProfiles
    # Set to True for existing clusters to maintain the hardcoded "userpool" in AgentPoolProfiles
    # Set to False (or omit) for new clusters to have all user pools as separate AgentPool resources
    # Legacy clusters can have BOTH the hardcoded userpool AND additional user_node_pools
    use_legacy_user_pool: bool | None = None

    # Optional: Root disk size for system node pool in GB (defaults to 128)
    system_node_pool_root_disk_size: int | None = None

    use_lets_encrypt: bool = False


@dataclasses.dataclass(frozen=True)
class AzureWorkloadClusterComponentConfig(ptd.WorkloadClusterComponentConfig):
    secret_store_csi_driver_azure_provider_version: str | None = "1.5.6"  # noqa: S105


def load_workload_cluster_site_dict(
    cluster_site_dict: dict[str, typing.Any],
) -> tuple[ptd.SiteConfig | None, bool]:
    site_spec = cluster_site_dict.get("spec", {})
    for key in list(site_spec.keys()):
        site_spec[key.replace("-", "_")] = site_spec.pop(key)

    return ptd.SiteConfig(**site_spec), True


class AzureWorkload(ptd.workload.AbstractWorkload):
    cfg: AzureWorkloadConfig

    def load_unique_config(self) -> None:
        cfg_dict = yaml.safe_load(self.ptd_yaml.read_text())
        if cfg_dict["kind"] != AzureWorkloadConfig.__name__ or cfg_dict["apiVersion"] != "posit.team/v1":
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
        site_domains = []
        for site_name, site_spec in sorted(spec.pop("sites", {}).items()):
            site_cfg, ok = load_workload_cluster_site_dict(site_spec)
            if not ok or site_cfg is None:
                msg = f"failed to load config for site {site_name!r}"
                raise ValueError(msg)
            if site_cfg.domain in site_domains:
                msg = f"config for sites suggests a duplicate domain '{site_cfg.domain}' at site '{site_name}'"
                raise ValueError(msg)

            site_domains.append(site_cfg.domain)
            sites[site_name] = site_cfg

        spec["sites"] = sites

        if isinstance(spec.get("network"), dict):
            spec["network"] = NetworkConfig(**spec["network"])

        self.cfg = AzureWorkloadConfig(**spec)

    def _load_workload_cluster_config_dict(
        self,
        cluster_spec: dict[str, typing.Any],
    ) -> tuple[AzureWorkloadClusterConfig | None, bool]:
        cluster_spec["components"] = AzureWorkloadClusterComponentConfig(**cluster_spec.pop("components", {}))

        for key in list(cluster_spec.keys()):
            cluster_spec[key.replace("-", "_")] = cluster_spec.pop(key)

        team_operator_image = cluster_spec.pop("team_operator_image", "latest").strip().lower()
        cluster_spec["team_operator_image"] = {"": "latest"}.get(team_operator_image, team_operator_image)

        # Handle user_node_pools if present
        if cluster_spec.get("user_node_pools"):
            user_node_pools = []
            for pool_dict in cluster_spec["user_node_pools"]:
                pool_spec = {}
                for key, value in pool_dict.items():
                    pool_spec[key.replace("-", "_")] = value
                user_node_pools.append(AzureUserNodePoolConfig(**pool_spec))
            cluster_spec["user_node_pools"] = user_node_pools

        return AzureWorkloadClusterConfig(**cluster_spec), True

    def role_env(self) -> dict[str, str]:
        env = os.environ.copy()
        env.setdefault(
            "PULUMI_BACKEND_URL",
            self.state_backend_url,
        )

    def cluster_kubeconfig(self, release: str):
        kubeconfig_str = ptd.shext.sh(
            [
                "az",
                "aks",
                "get-credentials",
                "--name",
                self.cluster_name(release),
                "--resource-group",
                self.resource_group_name,
                "-f",
                "-",
            ],
            env=self.role_env(),
        ).stdout

        kubeconfig = yaml.safe_load(kubeconfig_str)

        # assume azure workloads do not support tailscale, enforce socks5 proxy for kube interactions
        kubeconfig["clusters"][0]["cluster"]["proxy-url"] = "socks5://localhost:1080"

        # Save kubeconfig to a temporary file to pass to kubelogin command
        with tempfile.NamedTemporaryFile(delete=False) as temp_kubeconfig:
            temp_kubeconfig.write(yaml.dump(kubeconfig).encode())
            temp_kubeconfig_path = temp_kubeconfig.name
        try:
            ptd.shext.sh(
                [
                    "kubelogin",
                    "convert-kubeconfig",
                    "-l",
                    "azurecli",
                    "--kubeconfig",
                    temp_kubeconfig_path,
                ],
                env=self.role_env(),
            )

            with open(temp_kubeconfig_path) as f:
                converted_kubeconfig = f.read()
                return yaml.safe_load(converted_kubeconfig)
        finally:
            if os.path.exists(temp_kubeconfig_path):
                os.unlink(temp_kubeconfig_path)

    def cluster_name(self, release: str) -> str:
        return f"{self.compound_name}-{release}"

    def managed_clusters(self) -> list[dict[str, typing.Any]]:
        clusters = json.loads(
            ptd.shext.sh(
                [
                    "az",
                    "aks",
                    "list",
                ],
                env=self.role_env(),
            ).stdout
        )

        return [c for c in clusters if c["name"].startswith(self.compound_name)]

    def cluster_oidc_issuer_url(self, release: str) -> str:
        clusters = self.managed_clusters()

        cluster_name = self.cluster_name(release)
        for cluster in clusters:
            if cluster["name"] == cluster_name:
                url = cluster["oidcIssuerProfile"]["issuerUrl"]
                if not url:
                    msg = f"Cluster {cluster_name} does not have an OIDC issuer URL."
                    raise ValueError(msg)
                return url

        msg = f"Cluster {cluster_name} not found in managed clusters."
        raise ValueError(msg)

    @property
    def ptd_yaml(self) -> pathlib.Path:
        return self.d / "ptd.yaml"

    @property
    def required_tags(self) -> dict[str, str]:
        return (self.cfg.resource_tags or {}) | {
            ptd.azure_tag_key_format(str(ptd.TagKeys.POSIT_TEAM_TRUE_NAME)): self.cfg.true_name,
            ptd.azure_tag_key_format(str(ptd.TagKeys.POSIT_TEAM_ENVIRONMENT)): self.cfg.environment,
        }

    @property
    def acr_registry(self) -> str:
        parts = self.compound_name.split("-")
        return "crptd" + parts[0] + "".join(word.lower() for word in parts[1:])

    @property
    def cloud_provider(self) -> ptd.CloudProvider.AWS:
        return ptd.CloudProvider.AZURE

    @property
    def image_registry_hostname(self) -> str:
        return "docker.io/posit"

    # Replicates VaultName logic from azure/target.go
    @functools.cached_property
    def key_vault_name(self) -> str:
        name = self.compound_name.lower()
        name = re.sub(r"[^a-z0-9-]", "-", name)
        name = name[:17]
        return f"kv-ptd-{name}"

    @property
    def secret_name(self) -> str:
        return f"{self.compound_name}-posit-team"

    def site_secret_name(self, site: str) -> str:
        return f"{self.compound_name}-{site}-posit-team"

    # Replicates StateBucketName logic from azure/target.go
    @property
    def storage_account_name(self) -> str:
        name = self.compound_name.lower()
        name = re.sub(r"[^a-z0-9-]", "-", name)
        name = name.replace("-", "")
        name = name[:19]
        return f"stptd{name}"

    @property
    def netapp_account_name(self) -> str:
        return f"naa-ptd-{self.compound_name}"

    @property
    def netapp_pool_name(self) -> str:
        return f"nap-ptd-{self.compound_name}"

    # Replicates ResourceGroupName logic from azure/target.go
    @property
    def resource_group_name(self) -> str:
        name = self.compound_name.lower()
        name = re.sub(r"[^a-z0-9-]", "-", name)
        return f"rsg-ptd-{name}"

    @property
    def secrets_provider_url(self) -> str:
        return f"azurekeyvault://{self.key_vault_name}.vault.azure.net/keys/{ptd.MGMT_AZ_KEY_NAME}"

    @property
    def state_backend_url(self) -> str:
        return f"azblob://{self.state_container}?storage_account={self.storage_account_name}"

    # Replicates BlobStorageName logic from azure/target.go
    @property
    def state_container(self) -> str:
        name = self.compound_name.lower()
        name = re.sub(r"[^a-z0-9-]", "-", name)
        return f"blob-ptd-{name}"

    @property
    def compound_name(self) -> str:
        return f"{self.cfg.true_name}-{self.cfg.environment}"

    @property
    def vnet_name(self) -> str:
        return f"vnet-ptd-{self.compound_name}"

    @property
    def netapp_subnet_name(self) -> str:
        return f"snet-ptd-{self.compound_name}-netapp"

    @property
    def app_gateway_subnet_name(self) -> str:
        return f"snet-ptd-{self.compound_name}-agw"

    @property
    def azure_files_storage_account_name(self) -> str:
        # Storage account names must be lowercase alphanumerics, 3-24 chars
        return f"stptdfiles{self.compound_name.lower().replace('-', '')[0:14]}"

    @property
    def azure_files_csi_storage_class_name(self) -> str:
        return f"{self.compound_name}-azure-files-csi"

    def fully_qualified_name(self, release: str = ptd.ZERO) -> str:
        if release == ptd.ZERO:
            return self.compound_name

        return f"{self.compound_name}-{release}"

    def resolve_image_digest(self, _repository: ptd.ComponentImages, tag: str = ptd.LATEST) -> tuple[str, bool]:
        # Simply return the tag as-is - no digest resolution needed for public Docker Hub
        if not tag or tag == ptd.LATEST:
            return "latest", True
        return tag, True
