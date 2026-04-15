import typing

import pulumi
import pulumi_kubernetes as kubernetes
import yaml

import ptd
import ptd.workload

# Default Helm chart version (OCI charts require explicit version, no "latest")
DEFAULT_CHART_VERSION = "v1.23.1"


class TeamOperator(pulumi.ComponentResource):
    """Deploy Team Operator using Helm chart."""

    workload: ptd.workload.AbstractWorkload
    release: str
    cluster_cfg: ptd.WorkloadClusterConfig
    service_account_annotations: dict[str, typing.Any]

    posit_team_namespace: kubernetes.core.v1.Namespace
    helm_release: kubernetes.helm.v3.Release

    def __init__(
        self,
        workload: ptd.workload.AbstractWorkload,
        release: str,
        service_account_annotations: dict[str, typing.Any] | None = None,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}",
            *args,
            **kwargs,
        )

        self.workload = workload
        self.release = release
        self.service_account_annotations = service_account_annotations or {}

        self.cluster_cfg = self.workload.cfg.clusters[release]
        if self.cluster_cfg is None:
            msg = f"missing config for cluster {release!r}"
            raise ValueError(msg)

        self.helm_release_name = "team-operator"

        self._define_image()
        self._define_posit_team_namespace()
        self._define_helm_release()

    def _define_image(self):
        # Use adhoc_team_operator_image if set, otherwise use team_operator_image if explicitly set.
        # If neither is set (team_operator_image is None), self.image stays None so the Helm chart
        # defaults to its appVersion.
        image_config = self.cluster_cfg.adhoc_team_operator_image or self.cluster_cfg.team_operator_image
        if image_config is None:
            self.image = None
            return
        self.image = ptd.define_component_image(
            image_config=image_config,
            component_image=ptd.ComponentImages.TEAM_OPERATOR,
            image_registry_hostname=self.workload.image_registry_hostname,
        )

    def _define_posit_team_namespace(self):
        self.posit_team_namespace = kubernetes.core.v1.Namespace(
            f"{self.workload.compound_name}-{self.release}-{ptd.POSIT_TEAM_NAMESPACE}",
            metadata={"name": ptd.POSIT_TEAM_NAMESPACE},
            opts=pulumi.ResourceOptions(
                parent=self,
                retain_on_delete=True,
            ),
        )

    def _any_site_has_session_labels(self) -> bool:
        """Return True if any site.yaml in this workload has workbench.sessionLabels set."""
        for site_name in self.workload.cfg.sites:
            site_yaml_path = self.workload.site_yaml(site_name)
            if not site_yaml_path.exists():
                continue
            site_dict = yaml.safe_load(site_yaml_path.read_text())
            if site_dict.get("spec", {}).get("workbench", {}).get("sessionLabels"):
                return True
        return False

    def _define_helm_release(self):
        # Parse self.image (from _define_image) into repository and tag
        # Format is either "repo@sha256:digest" or "repo:tag"
        # If self.image is None, we skip image configuration to let the Helm chart use its default appVersion
        if self.image is not None:
            if "@" in self.image:
                # Image with digest: "hostname/repo@sha256:abc123"
                image_repository, image_tag = self.image.rsplit("@", 1)
                image_tag = f"@{image_tag}"  # Helm needs the @ prefix for digests
            elif ":" in self.image.split("/")[-1]:
                # Image with tag: "hostname/repo:tag"
                image_repository, image_tag = self.image.rsplit(":", 1)
            else:
                # No tag specified, use latest
                image_repository = self.image
                image_tag = "latest"

        # Build environment variables
        env_vars = {
            "WATCH_NAMESPACES": ptd.POSIT_TEAM_NAMESPACE,
        }

        # Add AWS_REGION if we have a region configured
        if self.workload.cfg.region:
            env_vars["AWS_REGION"] = self.workload.cfg.region

        # Build container config - only include image if explicitly set
        container_config = {"env": env_vars}
        if self.image is not None:
            container_config["image"] = {
                "repository": image_repository,
                "tag": image_tag,
            }

        # Helm values for the team-operator chart
        helm_values = {
            "controllerManager": {
                "replicas": 1,
                "container": container_config,
                "serviceAccount": {
                    "annotations": self.service_account_annotations,
                },
                "tolerations": [
                    {
                        "key": t.key,
                        "operator": t.operator,
                        "effect": t.effect,
                        **({"value": t.value} if t.value else {}),
                    }
                    for t in self.cluster_cfg.team_operator_tolerations
                ],
            },
            # CRD configuration: when team_operator_skip_crds=True, crd.enable=False prevents
            # Helm from rendering CRD templates. When False (default): Helm manages CRDs normally.
            # crd.keep=True adds helm.sh/resource-policy: keep as defense-in-depth.
            "crd": {
                "enable": not self.cluster_cfg.team_operator_skip_crds,
                "keep": True,
            },
            # Enable the session group label controller only if at least one site
            # has workbench.sessionLabels configured in its site.yaml.
            "sessionGroupLabels": {
                "enable": self._any_site_has_session_labels(),
            },
        }

        # OCI Helm chart from public repository
        chart = "oci://ghcr.io/posit-dev/charts/team-operator"

        # Chart version (OCI registries require explicit version)
        chart_version = self.cluster_cfg.team_operator_chart_version or DEFAULT_CHART_VERSION

        # Dependencies for the Helm release
        depends = [self.posit_team_namespace]

        release_args = kubernetes.helm.v3.ReleaseArgs(
            name=self.helm_release_name,
            chart=chart,
            version=chart_version,
            namespace=ptd.POSIT_TEAM_SYSTEM_NAMESPACE,
            create_namespace=True,
            values=helm_values,
            # Skip CRDs at Helm level (belt-and-suspenders with crd.enable in values).
            # This tells Helm CLI to skip the crds/ directory. Combined with
            # crd.enable=False, provides complete CRD skip when skip_crds is set.
            skip_crds=self.cluster_cfg.team_operator_skip_crds,
        )

        self.helm_release = kubernetes.helm.v3.Release(
            f"{self.workload.compound_name}-{self.release}-team-operator",
            release_args,
            opts=pulumi.ResourceOptions(
                parent=self,
                depends_on=depends,
            ),
        )
