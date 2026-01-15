import pulumi
import pulumi_kubernetes as k8s

import ptd
import ptd.workload


# See: https://github.com/k3s-io/helm-controller
class HelmController(pulumi.ComponentResource):
    namespace: k8s.core.v1.Namespace
    deployment: k8s.apps.v1.Deployment

    def __init__(
        self,
        workload: ptd.workload.AbstractWorkload,
        release: str,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}-helm-controller",
            *args,
            **kwargs,
        )

        self.workload = workload
        self.release = release

        self._define_namespace()
        self._define_crds()
        self._define_rbac()
        self._define_deployment()
        self.register_outputs({})

    def _define_namespace(self):
        self.namespace = k8s.core.v1.Namespace(
            f"{self.workload.compound_name}-{self.release}-helm-controller-namespace",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=ptd.HELM_CONTROLLER_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_crds(self):
        k8s.apiextensions.v1.CustomResourceDefinition(
            f"{self.workload.compound_name}-{self.release}-helmcharts-crd",
            metadata=k8s.meta.v1.ObjectMetaArgs(name="helmcharts.helm.cattle.io"),
            opts=pulumi.ResourceOptions(parent=self),
            spec={
                "group": "helm.cattle.io",
                "names": {"kind": "HelmChart", "plural": "helmcharts", "singular": "helmchart"},
                "preserveUnknownFields": False,
                "scope": "Namespaced",
                "versions": [
                    {
                        "name": "v1",
                        "served": True,
                        "storage": True,
                        "schema": {
                            "openAPIV3Schema": {
                                "properties": {
                                    "spec": {
                                        "properties": {
                                            "authPassCredentials": {"type": "boolean"},
                                            "authSecret": {
                                                "nullable": True,
                                                "properties": {"name": {"nullable": True, "type": "string"}},
                                                "type": "object",
                                            },
                                            "backOffLimit": {"nullable": True, "type": "integer"},
                                            "bootstrap": {"type": "boolean"},
                                            "chart": {"nullable": True, "type": "string"},
                                            "chartContent": {"nullable": True, "type": "string"},
                                            "createNamespace": {"type": "boolean"},
                                            "dockerRegistrySecret": {
                                                "nullable": True,
                                                "properties": {"name": {"nullable": True, "type": "string"}},
                                                "type": "object",
                                            },
                                            "failurePolicy": {"nullable": True, "type": "string"},
                                            "helmVersion": {"nullable": True, "type": "string"},
                                            "insecureSkipTLSVerify": {"type": "boolean"},
                                            "jobImage": {"nullable": True, "type": "string"},
                                            "plainHTTP": {"type": "boolean"},
                                            "podSecurityContext": {
                                                "nullable": True,
                                                "properties": {
                                                    "fsGroup": {"nullable": True, "type": "integer"},
                                                    "fsGroupChangePolicy": {"nullable": True, "type": "string"},
                                                    "runAsGroup": {"nullable": True, "type": "integer"},
                                                    "runAsNonRoot": {"nullable": True, "type": "boolean"},
                                                    "runAsUser": {"nullable": True, "type": "integer"},
                                                    "seLinuxOptions": {
                                                        "nullable": True,
                                                        "properties": {
                                                            "level": {"nullable": True, "type": "string"},
                                                            "role": {"nullable": True, "type": "string"},
                                                            "type": {"nullable": True, "type": "string"},
                                                            "user": {"nullable": True, "type": "string"},
                                                        },
                                                        "type": "object",
                                                    },
                                                    "seccompProfile": {
                                                        "nullable": True,
                                                        "properties": {
                                                            "localhostProfile": {"nullable": True, "type": "string"},
                                                            "type": {"nullable": True, "type": "string"},
                                                        },
                                                        "type": "object",
                                                    },
                                                    "supplementalGroups": {
                                                        "items": {"type": "integer"},
                                                        "nullable": True,
                                                        "type": "array",
                                                    },
                                                    "sysctls": {
                                                        "items": {
                                                            "properties": {
                                                                "name": {"nullable": True, "type": "string"},
                                                                "value": {"nullable": True, "type": "string"},
                                                            },
                                                            "type": "object",
                                                        },
                                                        "nullable": True,
                                                        "type": "array",
                                                    },
                                                    "windowsOptions": {
                                                        "nullable": True,
                                                        "properties": {
                                                            "gmsaCredentialSpec": {"nullable": True, "type": "string"},
                                                            "gmsaCredentialSpecName": {
                                                                "nullable": True,
                                                                "type": "string",
                                                            },
                                                            "hostProcess": {"nullable": True, "type": "boolean"},
                                                            "runAsUserName": {"nullable": True, "type": "string"},
                                                        },
                                                        "type": "object",
                                                    },
                                                },
                                                "type": "object",
                                            },
                                            "repo": {"nullable": True, "type": "string"},
                                            "repoCA": {"nullable": True, "type": "string"},
                                            "repoCAConfigMap": {
                                                "nullable": True,
                                                "properties": {"name": {"nullable": True, "type": "string"}},
                                                "type": "object",
                                            },
                                            "securityContext": {
                                                "nullable": True,
                                                "properties": {
                                                    "allowPrivilegeEscalation": {"nullable": True, "type": "boolean"},
                                                    "capabilities": {
                                                        "nullable": True,
                                                        "properties": {
                                                            "add": {
                                                                "items": {"nullable": True, "type": "string"},
                                                                "nullable": True,
                                                                "type": "array",
                                                            },
                                                            "drop": {
                                                                "items": {"nullable": True, "type": "string"},
                                                                "nullable": True,
                                                                "type": "array",
                                                            },
                                                        },
                                                        "type": "object",
                                                    },
                                                    "privileged": {"nullable": True, "type": "boolean"},
                                                    "procMount": {"nullable": True, "type": "string"},
                                                    "readOnlyRootFilesystem": {"nullable": True, "type": "boolean"},
                                                    "runAsGroup": {"nullable": True, "type": "integer"},
                                                    "runAsNonRoot": {"nullable": True, "type": "boolean"},
                                                    "runAsUser": {"nullable": True, "type": "integer"},
                                                    "seLinuxOptions": {
                                                        "nullable": True,
                                                        "properties": {
                                                            "level": {"nullable": True, "type": "string"},
                                                            "role": {"nullable": True, "type": "string"},
                                                            "type": {"nullable": True, "type": "string"},
                                                            "user": {"nullable": True, "type": "string"},
                                                        },
                                                        "type": "object",
                                                    },
                                                    "seccompProfile": {
                                                        "nullable": True,
                                                        "properties": {
                                                            "localhostProfile": {"nullable": True, "type": "string"},
                                                            "type": {"nullable": True, "type": "string"},
                                                        },
                                                        "type": "object",
                                                    },
                                                    "windowsOptions": {
                                                        "nullable": True,
                                                        "properties": {
                                                            "gmsaCredentialSpec": {"nullable": True, "type": "string"},
                                                            "gmsaCredentialSpecName": {
                                                                "nullable": True,
                                                                "type": "string",
                                                            },
                                                            "hostProcess": {"nullable": True, "type": "boolean"},
                                                            "runAsUserName": {"nullable": True, "type": "string"},
                                                        },
                                                        "type": "object",
                                                    },
                                                },
                                                "type": "object",
                                            },
                                            "set": {
                                                "additionalProperties": {"x-kubernetes-int-or-string": True},
                                                "nullable": True,
                                                "type": "object",
                                            },
                                            "targetNamespace": {"nullable": True, "type": "string"},
                                            "timeout": {"nullable": True, "type": "string"},
                                            "valuesContent": {"nullable": True, "type": "string"},
                                            "version": {"nullable": True, "type": "string"},
                                        },
                                        "type": "object",
                                    },
                                    "status": {
                                        "properties": {
                                            "conditions": {
                                                "items": {
                                                    "properties": {
                                                        "message": {"nullable": True, "type": "string"},
                                                        "reason": {"nullable": True, "type": "string"},
                                                        "status": {"nullable": True, "type": "string"},
                                                        "type": {"nullable": True, "type": "string"},
                                                    },
                                                    "type": "object",
                                                },
                                                "nullable": True,
                                                "type": "array",
                                            },
                                            "jobName": {"nullable": True, "type": "string"},
                                        },
                                        "type": "object",
                                    },
                                },
                                "type": "object",
                            }
                        },
                        "subresources": {"status": {}},
                        "additionalPrinterColumns": [
                            {"jsonPath": ".status.jobName", "name": "Job", "type": "string"},
                            {"jsonPath": ".spec.chart", "name": "Chart", "type": "string"},
                            {"jsonPath": ".spec.targetNamespace", "name": "TargetNamespace", "type": "string"},
                            {"jsonPath": ".spec.version", "name": "Version", "type": "string"},
                            {"jsonPath": ".spec.repo", "name": "Repo", "type": "string"},
                            {"jsonPath": ".spec.helmVersion", "name": "HelmVersion", "type": "string"},
                            {"jsonPath": ".spec.bootstrap", "name": "Bootstrap", "type": "string"},
                        ],
                    }
                ],
            },
        )

        k8s.apiextensions.v1.CustomResourceDefinition(
            f"{self.workload.compound_name}-{self.release}-helmchartconfigs",
            metadata=k8s.meta.v1.ObjectMetaArgs(name="helmchartconfigs.helm.cattle.io"),
            opts=pulumi.ResourceOptions(parent=self),
            spec={
                "group": "helm.cattle.io",
                "names": {"kind": "HelmChartConfig", "plural": "helmchartconfigs", "singular": "helmchartconfig"},
                "preserveUnknownFields": False,
                "scope": "Namespaced",
                "versions": [
                    {
                        "name": "v1",
                        "served": True,
                        "storage": True,
                        "schema": {
                            "openAPIV3Schema": {
                                "properties": {
                                    "spec": {
                                        "properties": {
                                            "failurePolicy": {"nullable": True, "type": "string"},
                                            "valuesContent": {"nullable": True, "type": "string"},
                                        },
                                        "type": "object",
                                    }
                                },
                                "type": "object",
                            }
                        },
                    }
                ],
            },
        )

    def _define_rbac(self):
        k8s.rbac.v1.ClusterRole(
            f"{self.workload.compound_name}-{self.release}-helm-controller-cluster-role",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="helm-controller",
            ),
            rules=[
                k8s.rbac.v1.PolicyRuleArgs(
                    api_groups=["*"],
                    resources=["*"],
                    verbs=["*"],
                ),
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

        k8s.rbac.v1.ClusterRoleBinding(
            f"{self.workload.compound_name}-{self.release}-helm-controller-cluster-role-binding",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="helm-controller",
            ),
            role_ref=k8s.rbac.v1.RoleRefArgs(
                api_group="rbac.authorization.k8s.io",
                kind="ClusterRole",
                name="helm-controller",
            ),
            subjects=[
                k8s.rbac.v1.SubjectArgs(
                    kind="ServiceAccount",
                    name="default",
                    namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                ),
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_deployment(self):
        self.deployment = k8s.apps.v1.Deployment(
            f"{self.workload.compound_name}-{self.release}-helm-controller-deployment",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                name="helm-controller",
                labels={
                    "app": "helm-controller",
                },
            ),
            spec=k8s.apps.v1.DeploymentSpecArgs(
                replicas=1,
                selector=k8s.meta.v1.LabelSelectorArgs(
                    match_labels={
                        "app": "helm-controller",
                    },
                ),
                template=k8s.core.v1.PodTemplateSpecArgs(
                    metadata=k8s.meta.v1.ObjectMetaArgs(
                        labels={
                            "app": "helm-controller",
                        },
                    ),
                    spec=k8s.core.v1.PodSpecArgs(
                        containers=[
                            k8s.core.v1.ContainerArgs(
                                name="helm-controller",
                                image="ghcr.io/k3s-io/helm-controller:v0.16.10",
                                command=["helm-controller"],
                                args=[
                                    "--namespace",
                                    ptd.HELM_CONTROLLER_NAMESPACE,
                                    "--default-job-image",
                                    "ghcr.io/k3s-io/klipper-helm:latest",
                                ],
                            ),
                        ],
                    ),
                ),
            ),
            opts=pulumi.ResourceOptions(parent=self, depends_on=[self.namespace]),
        )
