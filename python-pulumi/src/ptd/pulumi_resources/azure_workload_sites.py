import copy
import json
import typing

import deepmerge
import pulumi
import pulumi_kubernetes as kubernetes

import ptd
import ptd.azure_sdk
import ptd.azure_workload
import ptd.pulumi_resources.team_site
import ptd.secrecy

# Constants for external-secrets-operator
CLUSTER_SECRET_STORE_NAME = "azure-key-vault"  # noqa: S105
ESO_API_VERSION = "external-secrets.io/v1beta1"


def _external_secret_spec(site_name: str, secret_key: str) -> dict:
    """Build the ExternalSecret spec dict for a site."""
    return {
        "refreshInterval": "1h",
        "secretStoreRef": {
            "name": CLUSTER_SECRET_STORE_NAME,
            "kind": "ClusterSecretStore",
        },
        "target": {
            "name": f"{site_name}-secrets",
            "creationPolicy": "Owner",
        },
        "dataFrom": [
            {
                "extract": {
                    "key": secret_key,
                }
            }
        ],
    }


class AzureWorkloadSites(pulumi.ComponentResource):
    workload: ptd.azure_workload.AzureWorkload

    required_tags: dict[str, str]
    kubeconfigs: dict[str, str]
    kube_providers: dict[str, kubernetes.Provider]

    managed_clusters: list[dict[str, typing.Any]]
    managed_clusters_by_release: dict[str, dict[str, typing.Any]]

    team_sites: dict[str, ptd.pulumi_resources.team_site.TeamSite]

    @classmethod
    def autoload(cls) -> "AzureWorkloadSites":
        return cls(workload=ptd.azure_workload.AzureWorkload(pulumi.get_stack()))

    def __init__(self, workload: ptd.azure_workload.AzureWorkload, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        self.workload = workload
        self.required_tags = self.workload.required_tags | {
            ptd.azure_tag_key_format(str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY)): __name__,
        }

        clusters = self.workload.managed_clusters()
        self.managed_clusters = {(cluster["name"]): cluster for cluster in clusters}

        self.managed_clusters_by_release = {
            (cluster["name"].removeprefix(f"{self.workload.compound_name}-")): cluster for cluster in clusters
        }

        self.kubeconfigs = {
            release: json.dumps(self.workload.cluster_kubeconfig(release))
            for release in self.managed_clusters_by_release
        }

        self.kube_providers = {
            release: kubernetes.Provider(
                self.workload.cluster_name(release),
                kubeconfig=self.kubeconfigs[release],
            )
            for release in self.managed_clusters_by_release
        }

        self._define_team_sites()
        self._define_external_secrets()

    def _define_team_sites(self):
        self.team_sites = {}

        for release in self.managed_clusters_by_release:
            cluster_cfg = self.workload.cfg.clusters.get(release)

            def generate_set_workload_fields(
                _release: str, cluster_cfg: typing.Any
            ) -> ptd.pulumi_resources.KustomizeTransformationFunc:
                def set_workload_fields(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
                    if obj["kind"] != "Site":
                        return

                    site_spec = {
                        # TODO: set chronicle and ppm storage buckets
                        "domain": self.workload.cfg.domain,
                        "networkTrust": self.workload.cfg.network_trust.value,
                        "packageManager": {
                            "azureFiles": {
                                "storageClassName": self.workload.azure_files_csi_storage_class_name,
                                "shareSizeGiB": self.workload.cfg.ppm_file_share_size_gib,
                            },
                        },
                        "secret": {"type": "kubernetes"},
                        "secretType": "kubernetes",
                        "volumeSource": {
                            "type": "azure-netapp",
                        },
                    }

                    # Cloud-agnostic storage (when enabled)
                    if cluster_cfg and cluster_cfg.enable_cloud_agnostic_storage:
                        site_spec["storageClassName"] = "azure-netapp-files"
                        # Package Manager continues to use Azure Files CSI
                        site_spec["packageManagerStorageClassName"] = self.workload.azure_files_csi_storage_class_name

                    # Cloud-agnostic secrets (when external-secrets-operator is enabled)
                    if cluster_cfg and cluster_cfg.enable_external_secrets_operator:
                        # Use K8s Secret names instead of type+vaultName
                        site_name = obj.get("metadata", {}).get("name", "")
                        site_spec["secret"] = {"name": f"{site_name}-secrets"}
                        # Note: Azure doesn't have a workload-level secret like AWS, so we omit workloadSecret

                    # Cloud-agnostic IAM (Azure Workload Identity)
                    # Always set Workload Identity annotations/labels for Azure (they're used by all products)
                    site_name = obj.get("metadata", {}).get("name", "")
                    # Note: In Azure, we need to get the managed identities for each product.
                    # For now, we'll set placeholder annotations that will be filled in by azure_workload_products.py
                    # or we can set them here if we have the managed identity client IDs available.
                    # For this implementation, we'll follow the pattern and set serviceAccountName + annotations/labels.

                    # Set explicit ServiceAccount names
                    site_spec.setdefault("connect", {})["serviceAccountName"] = f"{site_name}-connect"
                    site_spec.setdefault("workbench", {})["serviceAccountName"] = f"{site_name}-workbench"
                    site_spec.setdefault("packageManager", {})["serviceAccountName"] = f"{site_name}-packagemanager"
                    site_spec.setdefault("chronicle", {})["serviceAccountName"] = f"{site_name}-chronicle"
                    site_spec.setdefault("flightdeck", {})["serviceAccountName"] = f"{site_name}-home"

                    # Set Workload Identity pod labels (same for all products)
                    for product in ["connect", "workbench", "packageManager", "chronicle", "flightdeck"]:
                        site_spec.setdefault(product, {})["podLabels"] = {
                            "azure.workload.identity/use": "true",
                        }

                    # ServiceAccount annotations need to be set with the managed identity client IDs
                    # These will be populated by the infrastructure layer that creates the managed identities
                    # For now, we leave them empty as they'll be set by azure_workload_products.py or similar

                    obj["spec"] = deepmerge.always_merger.merge(
                        obj.get("spec", {}),
                        copy.deepcopy(site_spec),
                    )

                return set_workload_fields

            for site_name in sorted(self.workload.cfg.sites.keys()):

                def generate_set_site_fields(
                    site_name: str,
                ) -> ptd.pulumi_resources.KustomizeTransformationFunc:
                    def set_site_fields(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
                        if obj["kind"] != "Site":
                            return

                        site_config = self.workload.cfg.sites[site_name]
                        obj["spec"] = deepmerge.always_merger.merge(
                            obj.get("spec", {}),
                            copy.deepcopy(
                                {
                                    "domain": site_config.domain,
                                }
                            ),
                        )

                    return set_site_fields

                self.team_sites[f"{release}-{site_name}"] = ptd.pulumi_resources.team_site.TeamSite(
                    workload=self.workload,
                    release=release,
                    site_name=site_name,
                    kubeconfig=self.kubeconfigs[release],
                    transformations=[
                        generate_set_workload_fields(release, cluster_cfg),
                        generate_set_site_fields(site_name),
                    ],
                    opts=pulumi.ResourceOptions(
                        parent=self,
                        providers=[self.kube_providers[release]],
                    ),
                )

    def _define_external_secrets(self) -> None:
        """
        Create ExternalSecret CRs for each site to sync secrets from Azure Key Vault to K8s Secrets.

        This creates K8s Secrets that the operator can reference by name instead of using CSI drivers.

        Note: these CRs reference the `azure-key-vault` ClusterSecretStore which is created by
        AzureWorkloadHelm. No Pulumi ``depends_on`` is wired here because even if we declared one, it
        would only guarantee the HelmChart CR object exists — not that ESO's CRDs have converged.
        The ClusterSecretStore will retry until ESO is ready (~1-2 reconcile loops).
        """
        for release in self.managed_clusters_by_release:
            if not self.workload.cfg.clusters[release].enable_external_secrets_operator:
                continue

            # Note: Azure doesn't have a workload-level secret like AWS.
            # Each site has its own secret in Azure Key Vault.

            # Create ExternalSecret for each site
            for site_name in sorted(self.workload.cfg.sites.keys()):
                # Azure site secrets are stored as: <workload>-<site>-secrets
                secret_key = f"{self.workload.compound_name}-{site_name}-secrets"

                kubernetes.apiextensions.CustomResource(
                    f"{self.workload.compound_name}-{release}-{site_name}-external-secret",
                    metadata=kubernetes.meta.v1.ObjectMetaArgs(
                        name=f"{site_name}-secrets",
                        namespace=ptd.POSIT_TEAM_NAMESPACE,
                        labels=self.required_tags,
                    ),
                    api_version=ESO_API_VERSION,
                    kind="ExternalSecret",
                    spec=_external_secret_spec(site_name, secret_key),
                    opts=pulumi.ResourceOptions(
                        parent=self,
                        provider=self.kube_providers[release],
                        custom_timeouts=pulumi.CustomTimeouts(create="10m"),
                    ),
                )
