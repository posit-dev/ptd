import typing

import pulumi
import pulumi_kubernetes as kubernetes

import ptd.aws_workload
import ptd.pulumi_resources


class KeycloakOperator(pulumi.ComponentResource):
    workload: ptd.aws_workload.AWSWorkload
    release: str

    transformations: list[ptd.pulumi_resources.KustomizeTransformationFunc]

    def __init__(
        self,
        workload: ptd.aws_workload.AWSWorkload,
        release: str,
        transformations: list[ptd.pulumi_resources.KustomizeTransformationFunc],
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

        self.transformations = transformations
        self._define_kustomization()

    @property
    def keycloak_realm_imports_rbac(self):
        return {
            "apiGroups": ["k8s.keycloak.org"],
            "resources": [
                "keycloakrealmimports",
                "keycloakrealmimports/status",
                "keycloakrealmimports/finalizers",
            ],
            "verbs": ["get", "list", "watch", "patch", "update", "create", "delete"],
        }

    @property
    def keycloaks_rbac(self):
        return {
            "apiGroups": ["k8s.keycloak.org"],
            "resources": [
                "keycloaks",
                "keycloaks/status",
                "keycloaks/finalizers",
            ],
            "verbs": ["get", "list", "watch", "patch", "update", "create", "delete"],
        }

    def _define_kustomization(self):
        def set_deployment_namespace(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
            obj.setdefault("metadata", {})

            if (
                obj["kind"] in ("Deployment", "ServiceAccount", "Service")
                and obj["metadata"].get("name") == "keycloak-operator"
            ):
                obj["metadata"]["namespace"] = ptd.POSIT_TEAM_SYSTEM_NAMESPACE

                for container in obj.get("spec", {}).get("template", {}).get("spec", {}).get("containers", []):
                    for env_var in container.get("env", []):
                        if env_var.get("name") != "KUBERNETES_NAMESPACE":
                            continue

                        if "valueFrom" in env_var:
                            env_var.pop("valueFrom")

                        env_var["value"] = ptd.POSIT_TEAM_NAMESPACE

            if obj["kind"] == "RoleBinding" and (
                "name" in obj["metadata"]
                and obj["metadata"]["name"] == "keycloak-operator-role-binding"
                and "subjects" in obj
                and len(obj["subjects"]) > 0
            ):
                obj["subjects"][0]["namespace"] = ptd.POSIT_TEAM_SYSTEM_NAMESPACE

        def update_operator_role(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
            obj.setdefault("metadata", {})

            if obj["kind"] == "Role" and obj["metadata"].get("name") == "keycloak-operator-role":
                obj["metadata"]["namespace"] = ptd.POSIT_TEAM_NAMESPACE

                obj.setdefault("rules", [])
                obj["rules"].append(self.keycloaks_rbac)
                obj["rules"].append(self.keycloak_realm_imports_rbac)

            if obj["kind"] == "RoleBinding" and obj["metadata"].get("name") == "keycloak-operator-role-binding":
                obj["metadata"]["namespace"] = ptd.POSIT_TEAM_NAMESPACE

        def remove_cluster_roles(obj: dict[str, typing.Any], _: pulumi.ResourceOptions) -> None:
            if obj["kind"] in ("ClusterRole", "ClusterRoleBinding") or (
                obj["kind"] == "RoleBinding" and obj.get("roleRef", {}).get("kind") == "ClusterRole"
            ):
                # NOTE: turning objects into `List` is the "official" way to omit
                # objects, apparently:
                # https://www.pulumi.com/registry/packages/kubernetes/api-docs/kustomize/directory/
                for key in sorted(obj.keys()):
                    del obj[key]

                obj["kind"] = "List"
                obj["apiVersion"] = "v1"

        def set_labels(obj: dict[str, typing.Any], _: pulumi.ResourceOptions):
            obj.setdefault("metadata", {})
            obj["metadata"].setdefault("labels", {})
            obj["metadata"]["labels"] |= {
                k: v
                for k, v in (
                    self.workload.required_tags
                    | {
                        str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
                    }
                ).items()
                if ":" not in k
            }

        cluster_components = self.workload.cfg.clusters[self.release].components

        if cluster_components is None:
            msg = f"missing components for cluster {self.release!r}"
            raise ValueError(msg)

        self.kustomization = kubernetes.kustomize.Directory(
            f"{self.workload.compound_name}-{self.release}-keycloak",
            directory=str(ptd.paths.top() / "keycloak" / "kustomization"),
            transformations=(
                [
                    remove_cluster_roles,
                    update_operator_role,
                    set_deployment_namespace,
                    set_labels,
                    *list(self.transformations),
                ]
            ),
            resource_prefix=f"{self.workload.compound_name}-{self.release}",
            opts=pulumi.ResourceOptions(parent=self),
        )
