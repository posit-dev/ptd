import pulumi
import pulumi_kubernetes as kubernetes

import ptd.aws_workload


class KubernetesRoleRule:
    api_groups: list[str]
    resources: list[str]
    verbs: list[str]

    def __init__(self, api_groups: list[str], resources: list[str], verbs: list[str]):
        self.api_groups = api_groups
        self.resources = resources
        self.verbs = verbs


class KubernetesRole(pulumi.ComponentResource):
    workload: ptd.aws_workload.AWSWorkload
    release: str
    service_account_name: str
    namespace: str
    role_name: str
    annotations: dict[str, str]
    rules: list[KubernetesRoleRule]

    role: kubernetes.rbac.v1.Role
    role_binding: kubernetes.rbac.v1.RoleBinding
    service_account: kubernetes.core.v1.ServiceAccount

    def __init__(
        self,
        workload: ptd.aws_workload.AWSWorkload,
        release: str,
        service_account: str,
        role_name: str,
        namespace: str,
        rules: list[KubernetesRoleRule],
        annotations: dict[str, str] | None = None,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}-{role_name}",
            *args,
            **kwargs,
        )

        self.workload = workload
        self.release = release
        self.service_account_name = service_account
        self.namespace = namespace
        self.role_name = role_name
        self.annotations = annotations or {}
        self.rules = rules

        self._define_service_account()
        self._define_role()
        self._define_role_binding()

    def _define_service_account(self) -> None:
        self.service_account = kubernetes.core.v1.ServiceAccount(
            f"{self.workload.compound_name}-{self.release}-{self.service_account_name}",
            kubernetes.core.v1.ServiceAccountInitArgs(
                metadata=kubernetes.meta.v1.ObjectMetaArgs(
                    name=self.service_account_name,
                    namespace=self.namespace,
                    annotations=self.annotations,
                ),
                automount_service_account_token=True,
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_role(self) -> None:
        self.role = kubernetes.rbac.v1.Role(
            f"{self.workload.compound_name}-{self.release}-{self.role_name}",
            kubernetes.rbac.v1.RoleInitArgs(
                metadata=kubernetes.meta.v1.ObjectMetaArgs(
                    name=self.role_name,
                    namespace=self.namespace,
                ),
                rules=[
                    kubernetes.rbac.v1.PolicyRuleArgs(
                        api_groups=rule.api_groups,
                        resources=rule.resources,
                        verbs=rule.verbs,
                    )
                    for rule in self.rules
                ],
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_role_binding(self) -> None:
        self.role_binding = kubernetes.rbac.v1.RoleBinding(
            f"{self.workload.compound_name}-{self.release}-{self.role_name}",
            kubernetes.rbac.v1.RoleBindingInitArgs(
                metadata=kubernetes.meta.v1.ObjectMetaArgs(
                    name=self.role_name,
                    namespace=self.namespace,
                ),
                subjects=[
                    kubernetes.rbac.v1.SubjectArgs(
                        kind="ServiceAccount",
                        name=self.service_account_name,
                        namespace=self.namespace,
                    ),
                ],
                role_ref=kubernetes.rbac.v1.RoleRefArgs(
                    api_group="rbac.authorization.k8s.io",
                    kind="Role",
                    name=self.role_name,
                ),
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )
