import copy
import typing

import deepmerge  # type: ignore
import pulumi
import pulumi_aws as aws
import pulumi_kubernetes as kubernetes

import ptd
import ptd.aws_workload
import ptd.pulumi_resources.aws_eks_cluster
import ptd.pulumi_resources.team_site
import ptd.secrecy
from ptd.pulumi_resources.aws_workload_helm import CLUSTER_SECRET_STORE_NAME


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


class AWSWorkloadSites(pulumi.ComponentResource):
    workload: ptd.aws_workload.AWSWorkload

    required_tags: dict[str, str]
    kubeconfigs: dict[str, str]
    kube_providers: dict[str, kubernetes.Provider]

    managed_clusters: list[dict[str, typing.Any]]
    managed_clusters_by_release: dict[str, dict[str, typing.Any]]

    team_sites: dict[str, ptd.pulumi_resources.team_site.TeamSite]

    @classmethod
    def autoload(cls) -> "AWSWorkloadSites":
        return cls(workload=ptd.aws_workload.AWSWorkload(pulumi.get_stack()))

    def __init__(self, workload: ptd.aws_workload.AWSWorkload, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        self.workload = workload
        self.required_tags = self.workload.required_tags | {
            str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
        }

        self.managed_clusters = self.workload.managed_clusters(assume_role=False)
        self.managed_clusters_by_release = self.workload.managed_clusters_by_release(assume_role=False)

        # kubeconfigs are passed to the team site resource, which does not use a provider for a resource lookup
        self.kubeconfigs = {
            release: ptd.pulumi_resources.aws_eks_cluster.get_kubeconfig_for_cluster(
                cluster["cluster"]["name"], self.workload.cfg.tailscale_enabled
            )
            for release, cluster in self.managed_clusters_by_release.items()
        }
        self.kube_providers = {
            release: ptd.pulumi_resources.aws_eks_cluster.get_provider_for_cluster(
                cluster["cluster"]["name"], self.workload.cfg.tailscale_enabled
            )
            for release, cluster in self.managed_clusters_by_release.items()
        }

        self.workload_secrets_dict, ok = ptd.secrecy.aws_get_secret_value_json(
            self.workload.secret_name, region=self.workload.cfg.region
        )

        if not ok:
            msg = f"Failed to look up secret {self.workload.secret_name!r}"
            pulumi.error(msg, self)

            raise ValueError(msg)

        self._define_team_sites()
        self._define_external_secrets()

    def _define_team_sites(self):
        self.team_sites = {}

        def set_workload_fields(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
            if obj["kind"] != "Site":
                return

            workload_secrets = typing.cast(
                ptd.secrecy.AWSWorkloadSecret,
                self.workload_secrets_dict,
            )
            main_db = ptd.aws_rds_describe_db_instance(
                workload_secrets.get("main-database-id", ""), region=self.workload.cfg.region
            )

            account_id = aws.get_caller_identity().account_id

            # Check if EFS is enabled for any cluster in this release
            cluster_cfg = self.workload.cfg.clusters.get(release)
            efs_enabled = False
            if cluster_cfg:
                efs_enabled = cluster_cfg.enable_efs_csi_driver or cluster_cfg.efs_config is not None

            site_spec = {
                "awsAccountId": account_id,
                "chronicle": {
                    "s3Bucket": workload_secrets["chronicle-bucket"],
                },
                "domain": self.workload.cfg.domain,
                "mainDatabaseCredentialSecret": {
                    "type": "aws",
                    "vaultName": main_db["MasterUserSecret"]["SecretArn"],
                },
                "networkTrust": self.workload.cfg.network_trust.value,
                "packageManager": {
                    "s3Bucket": workload_secrets["packagemanager-bucket"],
                },
                "secret": {"type": "aws"},
                "secretType": "aws",
                "volumeSource": {
                    "dnsName": workload_secrets["fs-dns-name"],
                    "type": "nfs",
                },
                "workloadSecret": {"type": "aws"},
            }

            # Add EFS configuration if enabled
            if efs_enabled:
                site_spec["efsEnabled"] = True
                if self.workload.cfg.vpc_cidr:
                    site_spec["vpcCIDR"] = self.workload.cfg.vpc_cidr

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
                    cluster_config=self.workload.cfg.clusters[release],
                    opts=pulumi.ResourceOptions(
                        parent=self,
                        providers=[self.kube_providers[release]],
                    ),
                )

    def _define_external_secrets(self) -> None:
        """
        Create ExternalSecret CRs for each site to sync secrets from AWS Secrets Manager to K8s Secrets.

        This creates K8s Secrets that the operator can reference by name instead of calling AWS SDK directly.

        Note: these CRs reference the `aws-secrets-manager` ClusterSecretStore which is created by
        AWSWorkloadHelm. No Pulumi ``depends_on`` is wired here because even if we declared one, it
        would only guarantee the HelmChart CR object exists — not that ESO's CRDs have converged.
        The ClusterSecretStore will retry until ESO is ready (~1-2 reconcile loops).
        """
        for release in self.managed_clusters_by_release:
            if not self.workload.cfg.clusters[release].enable_external_secrets_operator:
                continue
            for site_name in sorted(self.workload.cfg.sites.keys()):
                # Create ExternalSecret for site secrets
                kubernetes.apiextensions.CustomResource(
                    f"{self.workload.compound_name}-{release}-{site_name}-external-secret",
                    metadata=kubernetes.meta.v1.ObjectMetaArgs(
                        name=f"{site_name}-secrets",
                        namespace=ptd.POSIT_TEAM_NAMESPACE,
                        labels=self.required_tags,
                    ),
                    api_version="external-secrets.io/v1beta1",
                    kind="ExternalSecret",
                    spec=_external_secret_spec(site_name, self.workload.site_secret_name(site_name)),
                    opts=pulumi.ResourceOptions(
                        parent=self,
                        provider=self.kube_providers[release],
                        custom_timeouts=pulumi.CustomTimeouts(create="10m"),
                    ),
                )
