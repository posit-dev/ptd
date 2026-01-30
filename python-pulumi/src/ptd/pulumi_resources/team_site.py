import copy
import functools
import os
import typing

import deepmerge  # type: ignore
import pulumi
import pulumi_kubernetes as kubernetes
import yaml

import ptd
import ptd.paths
import ptd.pulumi_resources
import ptd.workload

DEBUG = os.environ.get("PTD_PULUMI_RESOURCES_TEAM_SITE_DEBUG", "") == "on"


class TeamSite(pulumi.ComponentResource):
    workload: ptd.workload.AbstractWorkload
    release: str
    site_name: str
    kubeconfig: str
    transformations: list[ptd.pulumi_resources.KustomizeTransformationFunc]

    site: kubernetes.yaml.ConfigFile

    def __init__(
        self,
        workload: ptd.workload.AbstractWorkload,
        release: str,
        site_name: str,
        kubeconfig: str,
        transformations: list[ptd.pulumi_resources.KustomizeTransformationFunc],
        cluster_config: typing.Any | None = None,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}-{site_name}",
            *args,
            **kwargs,
        )
        self.workload = workload
        self.release = release
        self.site_name = site_name
        self.kubeconfig = kubeconfig
        self.transformations = transformations
        self.cluster_config = cluster_config

        self._define_site()

    @functools.cached_property
    def _config_overrides(self) -> dict[str, typing.Any]:
        site_yaml = self.workload.site_yaml(self.site_name)

        if not site_yaml.exists():
            return {"apiVersion": "v1beta1"}

        return yaml.safe_load(site_yaml.read_text())

    def _define_site(self):
        def set_release_fields(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
            if obj["kind"] != "Site":
                return

            obj.setdefault("spec", {})
            obj["spec"] = deepmerge.always_merger.merge(
                obj["spec"],
                copy.deepcopy(
                    {
                        "clusterDate": self.release,
                        "secret": {"vaultName": self.workload.site_secret_name(self.site_name)},
                        "workloadCompoundName": self.workload.compound_name,
                        "workloadSecret": {"vaultName": self.workload.secret_name},
                    }
                ),
            )

        def set_metadata(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
            if obj["kind"] != "Site":
                return

            obj.setdefault("metadata", {})
            obj["metadata"] = deepmerge.always_merger.merge(
                obj["metadata"],
                copy.deepcopy(
                    {
                        "namespace": ptd.POSIT_TEAM_NAMESPACE,
                        "name": self.site_name,
                        "labels": {
                            k: v
                            for k, v in (
                                self.workload.required_tags
                                | {
                                    str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
                                    str(ptd.TagKeys.POSIT_TEAM_SITE_NAME): self.site_name,
                                }
                            ).items()
                            if ":" not in k
                        }
                        | {
                            "app.kubernetes.io/instance": self.site_name,
                        },
                    }
                ),
            )

        def merge_overrides(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
            if obj["kind"] != "Site":
                return

            if DEBUG:
                with open(
                    f"/tmp/site_{self.site_name}.config_overrides.yaml",  # noqa: S108
                    "w",
                ) as out:
                    yaml.dump(self._config_overrides, out)

            obj["spec"] = deepmerge.always_merger.merge(
                obj.get("spec", {}),
                copy.deepcopy(self._config_overrides.get("spec", {})),
            )

            if DEBUG:
                with open(f"/tmp/site_{self.site_name}.yaml", "w") as out:  # noqa: S108
                    yaml.dump(obj, out)

        def inject_cluster_tolerations(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
            if obj["kind"] != "Site":
                return

            # Compute session tolerations based on Karpenter node pools with session_taints=true
            session_tolerations = []
            if self.cluster_config and hasattr(self.cluster_config, "karpenter_config"):
                karpenter_config = self.cluster_config.karpenter_config
                if karpenter_config and karpenter_config.node_pools:
                    for node_pool in karpenter_config.node_pools:
                        if node_pool.session_taints:
                            toleration = {
                                "key": "workload-type",
                                "operator": "Equal",
                                "value": "session",
                                "effect": "NoSchedule",
                            }
                            if toleration not in session_tolerations:
                                session_tolerations.append(toleration)

            if not session_tolerations:
                return

            # Merge session tolerations into workbench spec
            deepmerge.always_merger.merge(obj, {"spec": {"workbench": {"sessionTolerations": session_tolerations}}})

            # Deduplicate tolerations (deepmerge concatenates lists)
            tolerations = obj["spec"]["workbench"]["sessionTolerations"]
            seen = {}
            for t in tolerations:
                key = (t.get("key"), t.get("operator"), t.get("value"), t.get("effect"))
                if key not in seen:
                    seen[key] = t
            obj["spec"]["workbench"]["sessionTolerations"] = list(seen.values())

        api_version_path = self._config_overrides.get("apiVersion", "").split("/")[-1]

        self.site = kubernetes.yaml.ConfigFile(
            self.site_name,
            file=str(ptd.paths.HERE / "site_templates" / f"core.posit.team_{api_version_path}_site.yaml"),
            transformations=(
                [
                    *list(self.transformations),
                    set_release_fields,
                    set_metadata,
                    inject_cluster_tolerations,
                    merge_overrides,
                ]
            ),
            resource_prefix=f"{self.workload.compound_name}-{self.release}",
            opts=pulumi.ResourceOptions(parent=self),
        )
