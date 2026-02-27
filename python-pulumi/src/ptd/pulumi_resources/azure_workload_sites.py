import copy
import json
import typing

import deepmerge
import pulumi
import pulumi_kubernetes as kubernetes

import ptd.azure_sdk
import ptd.azure_workload
import ptd.pulumi_resources.team_site
import ptd.secrecy


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

    def _define_team_sites(self):
        self.team_sites = {}

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

            # Cloud-agnostic ingress (when Gateway API is enabled)
            # Note: Azure workload sites don't have per-site cluster_cfg, so check all releases
            for release in self.managed_clusters_by_release:
                if release in self.workload.cfg.clusters and self.workload.cfg.clusters[release].enable_gateway_api:
                    site_spec["gatewayRef"] = {
                        "name": "posit-team",
                        "namespace": "traefik",
                    }
                    break  # Only need to set once if any cluster has it enabled

            obj["spec"] = deepmerge.always_merger.merge(
                obj.get("spec", {}),
                copy.deepcopy(site_spec),
            )

        for release in self.managed_clusters_by_release:
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
                        set_workload_fields,
                        generate_set_site_fields(site_name),
                    ],
                    opts=pulumi.ResourceOptions(
                        parent=self,
                        providers=[self.kube_providers[release]],
                    ),
                )
