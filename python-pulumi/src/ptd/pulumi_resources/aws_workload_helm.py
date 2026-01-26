import json

import pulumi
import pulumi_aws as aws
import pulumi_kubernetes as k8s
import yaml

import ptd
import ptd.aws_workload
import ptd.pulumi_resources.aws_eks_cluster
from ptd.pulumi_resources.grafana_alloy import AlloyConfig

ALLOY_NAMESPACE = "alloy"


class AWSWorkloadHelm(pulumi.ComponentResource):
    workload: ptd.aws_workload.AWSWorkload

    required_tags: dict[str, str]
    kube_providers: dict[str, k8s.Provider]

    @classmethod
    def autoload(cls) -> "AWSWorkloadHelm":
        return cls(workload=ptd.aws_workload.AWSWorkload(pulumi.get_stack()))

    def __init__(self, workload: ptd.aws_workload.AWSWorkload, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        self.workload = workload
        self.required_tags = {
            str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
        }

        self.managed_clusters_by_release = self.workload.managed_clusters_by_release(assume_role=False)
        self.kube_providers = {
            release: ptd.pulumi_resources.aws_eks_cluster.get_provider_for_cluster(
                cluster["cluster"]["name"], self.workload.cfg.tailscale_enabled
            )
            for release, cluster in self.managed_clusters_by_release.items()
        }

        persistent_stack = pulumi.StackReference(
            f"organization/ptd-aws-workload-persistent/{self.workload.compound_name}"
        )
        cert_arns_output = persistent_stack.require_output("cert_arns")

        for release in self.managed_clusters_by_release:
            components = self.workload.cfg.clusters[release].components
            weight = self.workload.cfg.clusters[release].routing_weight

            self._define_aws_lbc(release, components.aws_load_balancer_controller_version)
            self._define_aws_fsx_openzfs_csi(release, components.aws_fsx_openzfs_csi_driver_version)
            self._define_secret_store_csi(release, components.secret_store_csi_driver_version)
            self._define_secret_store_csi_aws(release, components.secret_store_csi_driver_aws_provider_version)
            self._define_traefik(release, components.traefik_version, weight, cert_arns_output)
            self._define_metrics_server(release, components.metrics_server_version)
            self._define_loki(release, components.loki_version, components)
            self._define_grafana(release, components.grafana_version)
            self._define_mimir(release, components.mimir_version, components)
            self._define_kube_state_metrics(release, components.kube_state_metrics_version)

            if components.alloy_version:
                self._define_alloy(release, components.alloy_version)

            if self.workload.cfg.nvidia_gpu_enabled:
                self._define_nvidia_device_plugin(release, components.nvidia_device_plugin_version)

            if self.workload.cfg.autoscaling_enabled:
                self._define_karpenter(release, components.karpenter_version)

    def _define_aws_fsx_openzfs_csi(self, release: str, version: str):
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-aws-fsx-openzfs-csi-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="aws-fsx-openzfs-csi",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://kubernetes-sigs.github.io/aws-fsx-openzfs-csi-driver",
                "chart": "aws-fsx-openzfs-csi-driver",
                "targetNamespace": ptd.KUBE_SYSTEM_NAMESPACE,
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "controller": {
                            "resources": {
                                "requests": {
                                    "cpu": "10m",
                                    "memory": "40Mi",
                                },
                                "limits": {
                                    "memory": "40Mi",
                                },
                            },
                            "serviceAccount": {
                                "create": True,
                                "name": f"controller.{ptd.Roles.AWS_FSX_OPENZFS_CSI_DRIVER}",
                                "annotations": {
                                    "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}:role/"
                                    + self.workload.fsx_openzfs_role_name,
                                },
                            },
                            "tolerations": [
                                # Preserve defaults: CriticalAddonsOnly and NoExecute
                                {
                                    "key": "CriticalAddonsOnly",
                                    "operator": "Exists",
                                },
                                {
                                    "effect": "NoExecute",
                                    "operator": "Exists",
                                    "tolerationSeconds": 300,
                                },
                                # Add session taint toleration
                                {
                                    "key": "workload-type",
                                    "operator": "Equal",
                                    "value": "session",
                                    "effect": "NoSchedule",
                                },
                            ],
                        },
                        "node": {
                            "serviceAccount": {
                                "create": True,
                                "name": f"nodes.{ptd.Roles.AWS_FSX_OPENZFS_CSI_DRIVER}",
                                "annotations": {
                                    "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}:role/"
                                    + self.workload.fsx_openzfs_role_name,
                                },
                            },
                            "tolerations": [
                                # Preserve default: broad NoExecute toleration
                                {
                                    "operator": "Exists",
                                    "effect": "NoExecute",
                                    "tolerationSeconds": 300,
                                },
                                # Add session taint toleration
                                {
                                    "key": "workload-type",
                                    "operator": "Equal",
                                    "value": "session",
                                    "effect": "NoSchedule",
                                },
                            ],
                        },
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_secret_store_csi(self, release: str, version: str):
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-secret-store-csi-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="secret-store-csi",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts",
                "chart": "secrets-store-csi-driver",
                "targetNamespace": ptd.KUBE_SYSTEM_NAMESPACE,
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "resources": {
                            "requests": {
                                "cpu": "30m",
                                "memory": "128Mi",
                            },
                            "limits": {
                                "memory": "128Mi",
                            },
                        },
                        "rotationPollInterval": "15s",
                        "enableSecretRotation": True,
                        "syncSecret": {
                            "enabled": True,
                        },
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_secret_store_csi_aws(self, release: str, version: str):
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-secret-store-csi-provider-aws-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="secrets-store-csi-driver-provider-aws",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://aws.github.io/secrets-store-csi-driver-provider-aws",
                "chart": "secrets-store-csi-driver-provider-aws",
                "targetNamespace": ptd.KUBE_SYSTEM_NAMESPACE,
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "resources": {
                            "requests": {
                                "cpu": "10m",
                                "memory": "50Mi",
                            },
                            "limits": {
                                "memory": "50Mi",
                            },
                        },
                        "tolerations": [
                            {
                                "key": "workload-type",
                                "operator": "Equal",
                                "value": "session",
                                "effect": "NoSchedule",
                            },
                        ],
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_aws_lbc(self, release: str, version: str):
        cluster_name = f"{self.workload.compound_name}-{release}"
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-aws-lbc-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="aws-load-balancer-controller",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://aws.github.io/eks-charts",
                "chart": "aws-load-balancer-controller",
                "targetNamespace": ptd.KUBE_SYSTEM_NAMESPACE,
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "resources": {
                            "requests": {
                                "cpu": "100m",
                                "memory": "256Mi",
                            },
                            "limits": {
                                "memory": "256Mi",
                            },
                        },
                        "clusterName": cluster_name,
                        "serviceAccount": {
                            "create": True,
                            "name": str(ptd.Roles.AWS_LOAD_BALANCER_CONTROLLER),
                            "annotations": {
                                "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}:role/"
                                + str(
                                    ptd.DynamicRoles.aws_lbc_name_env(
                                        self.workload.cfg.true_name, self.workload.cfg.environment
                                    )
                                ),
                            },
                        },
                        "hostNetwork": True,
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_metrics_server(self, release: str, version: str):
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-metrics-server-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="metrics-server",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://kubernetes-sigs.github.io/metrics-server/",
                "chart": "metrics-server",
                "targetNamespace": ptd.KUBE_SYSTEM_NAMESPACE,
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "resources": {
                            "requests": {
                                "cpu": "100m",
                                "memory": "200Mi",
                            },
                            "limits": {
                                "memory": "200Mi",
                            },
                        },
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_loki(self, release: str, version: str, components):
        k8s.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-loki-ns",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="loki",
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-loki-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="loki",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://grafana.github.io/helm-charts",
                "chart": "loki",
                "targetNamespace": "loki",
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "gateway": {
                            "image": {
                                "registry": "quay.io",
                                "repository": "nginx/nginx-unprivileged",
                            }
                        },
                        "loki": {
                            "auth_enabled": False,
                            "storage": {
                                "bucketNames": {
                                    "chunks": f"{self.workload.prefix}-{self.workload.loki_s3_bucket_name}",
                                    "ruler": f"{self.workload.prefix}-{self.workload.loki_s3_bucket_name}",
                                    "admin": f"{self.workload.prefix}-{self.workload.loki_s3_bucket_name}",
                                },
                                "type": "s3",
                                "s3": {
                                    "region": self.workload.cfg.region,
                                    "insecure": False,
                                    "s3ForcePathStyle": False,
                                    # Critical: Add S3 retry configuration to prevent runaway costs
                                    "backoff_config": {
                                        "min_period": "100ms",  # Start with 100ms backoff
                                        "max_period": "10s",  # Maximum 10s between retries
                                        "max_retries": 5,  # Limit to 5 retry attempts (prevents infinite loops)
                                    },
                                    "http_config": {
                                        "idle_conn_timeout": "90s",
                                        "response_header_timeout": "30s",  # Timeout for S3 responses
                                        "insecure_skip_verify": False,
                                    },
                                },
                            },
                            # Valid limits_config keys for Helm chart
                            "limits_config": {
                                "max_cache_freshness_per_query": "10m",
                                "query_timeout": "300s",
                                "reject_old_samples": True,
                                "reject_old_samples_max_age": "168h",  # 7 days
                                "split_queries_by_interval": "15m",
                                "volume_enabled": True,
                            },
                            # Valid storage_config keys for Helm chart
                            "storage_config": {
                                "hedging": {
                                    "at": "250ms",
                                    "max_per_second": 20,
                                    "up_to": 3,
                                },
                            },
                        },
                        "serviceAccount": {
                            "create": True,
                            "name": str(ptd.Roles.LOKI),
                            "annotations": {
                                "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}:role/"
                                f"loki.{self.workload.cfg.true_name}-{self.workload.cfg.environment}.posit.team"
                            },
                        },
                        "sidecar": {
                            "image": {
                                "repository": "quay.io/kiwigrid/k8s-sidecar",
                            },
                        },
                        "monitoring": {
                            "dashboards": {"enabled": False},
                            "serviceMonitor": {"enabled": False},
                            "selfMonitoring": {
                                "enabled": False,
                                "grafanaAgent": {
                                    "installOperator": False,
                                },
                            },
                        },
                        "test": {
                            "enabled": False,
                        },
                        "backend": {
                            "replicas": components.loki_replicas,
                            "persistence": {
                                "enableStatefulSetAutoDeletePVC": True,
                            },
                        },
                        "read": {
                            "replicas": components.loki_replicas,
                            "persistence": {
                                "enableStatefulSetAutoDeletePVC": True,
                            },
                        },
                        "write": {
                            "replicas": components.loki_replicas,
                            "persistence": {
                                "enableStatefulSetAutoDeletePVC": True,
                            },
                        },
                    },
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_grafana(self, release: str, version: str):
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-grafana-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="grafana",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://grafana.github.io/helm-charts",
                "chart": "grafana",
                "targetNamespace": "grafana",
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "envFromSecret": "grafana-db-url",
                        "grafana.ini": {
                            "server": {
                                "domain": f"{self.workload.cfg.domain}",
                                "root_url": f"https://grafana.{self.workload.cfg.domain}",
                                "serve_from_sub_path": False,
                            },
                            "auth.proxy": {
                                "enabled": True,
                                "header_name": "X-Forwarded-User",
                                "header_property": "username",
                                "auto_sign_up": True,
                            },
                            "auth": {
                                "disable_signout_menu": True,
                            },
                            "database": {
                                "url": '${{ "{" }}PTD_DATABASE_URL{{ "}" }}',  # ${PTD_DATABASE_URL} in the resulting configMap
                                "ssl_mode": "require",
                            },
                            "users": {
                                "auto_assign_org_role": "Editor",
                            },
                        },
                        "ingress": {
                            "enabled": True,
                            "annotations": {
                                "traefik.ingress.kubernetes.io/router.middlewares": "kube-system-traefik-forward-auth-add-forwarded-headers@kubernetescrd,kube-system-traefik-forward-auth-main@kubernetescrd",
                            },
                            "hosts": [f"grafana.{self.workload.cfg.domain}"],
                            "path": "/",
                        },
                        "datasources": {
                            "datasources.yaml": {
                                "apiVersion": 1,
                                "datasources": [
                                    {
                                        "name": "Loki",
                                        "type": "loki",
                                        "access": "proxy",
                                        "editable": False,
                                        "url": "http://loki-gateway.loki.svc.cluster.local",
                                        "isDefault": True,
                                    },
                                    {
                                        "name": "Mimir",
                                        "type": "prometheus",
                                        "access": "proxy",
                                        "editable": False,
                                        "url": "http://mimir-gateway.mimir.svc.cluster.local/prometheus",
                                        "isDefault": False,
                                    },
                                ],
                            },
                        },
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_mimir(self, release: str, version: str, components):
        k8s.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-mimir-ns",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="mimir",
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-mimir-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="mimir",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://grafana.github.io/helm-charts",
                "chart": "mimir-distributed",
                "targetNamespace": "mimir",
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "serviceAccount": {
                            "create": True,
                            "name": str(ptd.Roles.MIMIR),
                            "annotations": {
                                "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}:role/"
                                f"mimir.{self.workload.cfg.true_name}-{self.workload.cfg.environment}.posit.team"
                            },
                        },
                        "minio": {
                            "enabled": False,
                        },
                        "mimir": {
                            "structuredConfig": {
                                "blocks_storage": {
                                    "backend": "s3",
                                    "storage_prefix": "blocks",
                                    "s3": {
                                        "bucket_name": f"{self.workload.prefix}-{self.workload.mimir_s3_bucket_name}",
                                        "endpoint": f"s3.{self.workload.cfg.region}.amazonaws.com",
                                        "insecure": False,
                                    },
                                },
                                "limits": {
                                    "max_global_series_per_user": 800000,
                                    "max_label_names_per_series": 45,
                                },
                            }
                        },
                        "alertmanager": {"enabled": False},
                        "ruler": {"enabled": False},
                        "ingester": {
                            "persistentVolume": {"size": "20Gi"},
                            "replicas": components.mimir_replicas,
                            "zoneAwareReplication": {"enabled": False},
                            "affinity": {
                                "nodeAffinity": {
                                    "requiredDuringSchedulingIgnoredDuringExecution": {
                                        "nodeSelectorTerms": [
                                            {
                                                "matchExpressions": [
                                                    {
                                                        "key": "karpenter.sh/nodepool",
                                                        "operator": "DoesNotExist",
                                                    }
                                                ]
                                            }
                                        ]
                                    }
                                }
                            },
                        },
                        "compactor": {
                            "persistentVolume": {"size": "20Gi"},
                            "replicas": components.mimir_replicas,
                            "affinity": {
                                "nodeAffinity": {
                                    "requiredDuringSchedulingIgnoredDuringExecution": {
                                        "nodeSelectorTerms": [
                                            {
                                                "matchExpressions": [
                                                    {
                                                        "key": "karpenter.sh/nodepool",
                                                        "operator": "DoesNotExist",
                                                    }
                                                ]
                                            }
                                        ]
                                    }
                                }
                            },
                        },
                        "store_gateway": {
                            "persistentVolume": {"size": "20Gi"},
                            "replicas": components.mimir_replicas,
                            "zoneAwareReplication": {"enabled": False},
                            "affinity": {
                                "nodeAffinity": {
                                    "requiredDuringSchedulingIgnoredDuringExecution": {
                                        "nodeSelectorTerms": [
                                            {
                                                "matchExpressions": [
                                                    {
                                                        "key": "karpenter.sh/nodepool",
                                                        "operator": "DoesNotExist",
                                                    }
                                                ]
                                            }
                                        ]
                                    }
                                }
                            },
                        },
                        "gateway": {
                            "enabledNonEnterprise": True,
                            "nginx": {
                                "image": {
                                    "registry": "quay.io",
                                    "repository": "nginx/nginx-unprivileged",
                                },
                            },
                        },
                        "nginx": {"enabled": False},
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_kube_state_metrics(self, release: str, version: str):
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-kube-state-metrics-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="kube-state-metrics",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://prometheus-community.github.io/helm-charts",
                "chart": "kube-state-metrics",
                "targetNamespace": ptd.KUBE_SYSTEM_NAMESPACE,
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "resources": {
                            "requests": {
                                "cpu": "10m",
                                "memory": "64Mi",
                            },
                            "limits": {
                                "memory": "64Mi",
                            },
                        },
                        "metricLabelsAllowlist": [
                            "pods=[launcher-instance-id]",
                        ],
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_traefik(self, release: str, version: str, weight: str, cert_arns_output: pulumi.Output):
        ns = k8s.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-traefik-ns",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="traefik",
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        chart = k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-traefik-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="traefik",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://traefik.github.io/charts",
                "chart": "traefik",
                "helmVersion": "v3",
                "targetNamespace": ptd.TRAEFIK_NAMESPACE,
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "resources": {
                            "requests": {
                                "cpu": "100m",
                                "memory": "128Mi",
                            },
                            "limits": {
                                "memory": "128Mi",
                            },
                        },
                        "image": {
                            "registry": "ghcr.io/traefik",
                        },
                        "deployment": {
                            "kind": "Deployment",
                        },
                        "logs": {
                            "access": {"enabled": True},
                            "general": {"level": "DEBUG"},
                        },
                        "ingressClass": {
                            "enabled": True,
                            "isDefaultClass": True,
                        },
                        "ingressRoute": {
                            "dashboard": {
                                "enabled": True,
                            },
                        },
                        "providers": {
                            "kubernetesCRD": {
                                "allowCrossNamespace": True,
                                "enabled": True,
                            },
                            "kubernetesIngress": {
                                "enabled": True,
                            },
                        },
                        "service": {"type": "NodePort"},
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release], depends_on=ns),
        )

        if self.workload.cfg.load_balancer_per_site:
            for site_name, site_config in sorted(self.workload.cfg.sites.items()):
                # Handle both external certificates and PTD-managed certificates
                if site_config.certificate_arn:
                    # External certificate provided directly in site config
                    cert_arns_for_site = [site_config.certificate_arn]
                    annotations = self._define_per_site_ingress_annotations(
                        release, weight, cert_arns_for_site, site_name, site_config
                    )
                else:
                    # Use all certs for PTD-managed certificates
                    annotations = cert_arns_output.apply(
                        lambda cert_arns, sn=site_name, sc=site_config: self._define_per_site_ingress_annotations(
                            release, weight, cert_arns, sn, sc
                        )
                    )

                k8s.apiextensions.CustomResource(
                    f"{self.workload.compound_name}-{release}-{site_name}-traefik-ingress",
                    metadata=k8s.meta.v1.ObjectMetaArgs(
                        name=f"traefik-{site_name}",
                        namespace=ptd.TRAEFIK_NAMESPACE,
                        annotations=annotations,
                        labels=self.required_tags | {"app": "traefik", "site": site_name},
                    ),
                    api_version="networking.k8s.io/v1",
                    kind="Ingress",
                    spec={
                        "ingressClassName": "alb",
                        "rules": [
                            {
                                "http": {
                                    "paths": [
                                        {
                                            "path": "/*",
                                            "pathType": "ImplementationSpecific",
                                            "backend": {
                                                "service": {
                                                    "name": "traefik",
                                                    "port": {
                                                        "number": 80,
                                                    },
                                                }
                                            },
                                        },
                                    ]
                                },
                            }
                        ],
                    },
                    opts=pulumi.ResourceOptions(provider=self.kube_providers[release], depends_on=chart),
                )
        else:
            k8s.apiextensions.CustomResource(
                f"{self.workload.compound_name}-{release}-traefik-ingress",
                metadata=k8s.meta.v1.ObjectMetaArgs(
                    name="traefik",
                    namespace=ptd.TRAEFIK_NAMESPACE,
                    annotations=cert_arns_output.apply(
                        lambda cert_arns: self._define_shared_ingress_annotations(release, weight, cert_arns)
                    ),
                    labels=self.required_tags | {"app": "traefik"},
                ),
                api_version="networking.k8s.io/v1",
                kind="Ingress",
                spec={
                    "ingressClassName": "alb",
                    "rules": [
                        {
                            "http": {
                                "paths": [
                                    {
                                        "path": "/*",
                                        "pathType": "ImplementationSpecific",
                                        "backend": {
                                            "service": {
                                                "name": "traefik",
                                                "port": {
                                                    "number": 80,
                                                },
                                            }
                                        },
                                    },
                                ]
                            },
                        }
                    ],
                },
                opts=pulumi.ResourceOptions(provider=self.kube_providers[release], depends_on=chart),
            )

    def _define_shared_ingress_annotations(self, release: str, weight: str, cert_arns: list[str]) -> dict[str, str]:
        annotations = self._define_ingress_alb_annotations(cert_arns)

        # Use a set to deduplicate domains in case multiple sites share the same domain
        unique_dns_hosts = set()
        for _, site in sorted(self.workload.cfg.sites.items()):
            unique_dns_hosts.add(site.domain)
            unique_dns_hosts.add(f"*.{site.domain}")
        dns_hosts = ",".join(sorted(unique_dns_hosts))

        annotations.update(
            {
                "external-dns.alpha.kubernetes.io/hostname": dns_hosts,
                "external-dns.alpha.kubernetes.io/set-identifier": f"{self.workload.compound_name}-{release}",
                "external-dns.alpha.kubernetes.io/aws-weight": weight,
            }
        )

        return annotations

    def _define_per_site_ingress_annotations(
        self, release: str, weight: str, cert_arns: list[str], site_name: str, site_config: ptd.SiteConfig
    ) -> dict[str, str]:
        annotations = self._define_ingress_alb_annotations(cert_arns)

        dns_hosts = f"{site_config.domain},*.{site_config.domain}"
        annotations.update(
            {
                "external-dns.alpha.kubernetes.io/hostname": dns_hosts,
                "external-dns.alpha.kubernetes.io/set-identifier": f"{self.workload.compound_name}-{release}-{site_name}",
                "external-dns.alpha.kubernetes.io/aws-weight": weight,
            }
        )

        return annotations

    def _define_ingress_alb_annotations(self, cert_arns: list[str]) -> dict[str, str]:
        annotations = {
            "alb.ingress.kubernetes.io/ssl-redirect": "443",
            "alb.ingress.kubernetes.io/listen-ports": json.dumps([{"HTTP": 80}, {"HTTPS": 443}]),
            "alb.ingress.kubernetes.io/backend-protocol": "HTTP",
            "alb.ingress.kubernetes.io/certificate-arn": ",".join(cert_arns),
            "alb.ingress.kubernetes.io/healthcheck-protocol": "HTTP",
            "alb.ingress.kubernetes.io/ssl-policy": "ELBSecurityPolicy-FS-1-2-2019-08",
            "alb.ingress.kubernetes.io/healthcheck-path": "/ping",
            "alb.ingress.kubernetes.io/healthcheck-port": "9000",
            "alb.ingress.kubernetes.io/load-balancer-attributes": "routing.http.drop_invalid_header_fields.enabled=true",
        }

        if self.workload.cfg.provisioned_vpc:
            annotations["alb.ingress.kubernetes.io/subnets"] = ",".join(
                self.workload.cfg.provisioned_vpc.private_subnets
            )

        if self.workload.cfg.public_load_balancer:
            annotations["alb.ingress.kubernetes.io/scheme"] = "internet-facing"
        else:
            annotations["alb.ingress.kubernetes.io/scheme"] = "internal"

        return annotations

    def _define_nvidia_device_plugin(self, release: str, version: str):
        k8s.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-nvidia-device-plugin-ns",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="nvidia-device-plugin",
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-nvidia-device-plugin-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="nvidia-device-plugin",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://nvidia.github.io/k8s-device-plugin",
                "chart": "nvidia-device-plugin",
                "targetNamespace": "nvidia-device-plugin",
                "valuesContent": yaml.dump(
                    {
                        "nfd": {
                            "enabled": True,
                            "worker": {
                                "tolerations": [
                                    {
                                        "key": "workload-type",
                                        "operator": "Equal",
                                        "value": "session",
                                        "effect": "NoSchedule",
                                    },
                                    {
                                        "key": "nvidia.com/gpu",
                                        "operator": "Equal",
                                        "value": "present",
                                        "effect": "NoSchedule",
                                    },
                                    {
                                        "key": "node-role.kubernetes.io/master",
                                        "operator": "Equal",
                                        "value": "",
                                        "effect": "NoSchedule",
                                    },
                                ],
                            },
                        },
                        "migStrategy": "none",
                        "failOnInitError": True,
                        "nvidiaDriverRoot": "/",
                        "plugin": {
                            "passDeviceSpecs": False,
                            "deviceListStrategy": "envvar",
                            "deviceIDStrategy": "uuid",
                        },
                        "tolerations": [
                            {
                                "key": "workload-type",
                                "operator": "Equal",
                                "value": "session",
                                "effect": "NoSchedule",
                            },
                            {
                                "key": "nvidia.com/gpu",
                                "operator": "Exists",
                                "effect": "NoSchedule",
                            },
                            {
                                "key": "CriticalAddonsOnly",
                                "operator": "Exists",
                            },
                        ],
                    },
                ),
                "version": version,
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_karpenter(self, release: str, version: str):
        cluster_name = f"{self.workload.compound_name}-{release}"

        # Get nodegroup names for this cluster to use in affinity rules
        def get_nodegroup_names():
            try:
                cluster_nodegroups = aws.eks.get_node_groups(cluster_name=cluster_name)
                return list(cluster_nodegroups.names)
            except Exception as e:
                pulumi.log.warn(f"Could not get nodegroup names for cluster {cluster_name}: {e}")
                # Fallback to common architecture names
                return ["amd64", "arm64"]

        nodegroup_names = get_nodegroup_names()
        pulumi.log.info(f"Using nodegroup names for Karpenter affinity in cluster {cluster_name}: {nodegroup_names}")

        # Deploy Karpenter Helm chart
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-karpenter-helm-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="karpenter",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "chart": "oci://public.ecr.aws/karpenter/karpenter",
                "targetNamespace": "kube-system",
                "valuesContent": yaml.dump(
                    {
                        "controller": {
                            "resources": {
                                "limits": {
                                    "cpu": "1",
                                    "memory": "1Gi",
                                },
                                "requests": {
                                    "cpu": "1",
                                    "memory": "1Gi",
                                },
                            },
                            "env": [
                                {
                                    "name": "AWS_REGION",
                                    "value": self.workload.cfg.region,
                                },
                            ],
                        },
                        "affinity": {
                            "nodeAffinity": {
                                "requiredDuringSchedulingIgnoredDuringExecution": {
                                    "nodeSelectorTerms": [
                                        {
                                            "matchExpressions": [
                                                {
                                                    "key": "karpenter.sh/nodepool",
                                                    "operator": "DoesNotExist",
                                                },
                                                {
                                                    "key": "eks.amazonaws.com/nodegroup",
                                                    "operator": "In",
                                                    "values": nodegroup_names,
                                                },
                                            ]
                                        }
                                    ]
                                }
                            },
                            "podAntiAffinity": {
                                "requiredDuringSchedulingIgnoredDuringExecution": [
                                    {"topologyKey": "kubernetes.io/hostname"}
                                ]
                            },
                        },
                        "serviceAccount": {
                            "annotations": {
                                "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}:role/KarpenterControllerRole-{cluster_name}.posit.team"
                            },
                        },
                        "settings": {
                            "clusterName": cluster_name,
                            "interruptionQueue": cluster_name,
                        },
                    }
                ),
                "version": version,
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        # Deploy NodePools and EC2NodeClasses from configuration
        cluster_config = self.workload.cfg.clusters[release]
        if cluster_config.karpenter_config.node_pools:
            for node_pool in cluster_config.karpenter_config.node_pools:
                # Convert KarpenterRequirement objects to dictionaries
                requirements = [
                    {"key": req.key, "operator": req.operator, "values": req.values} for req in node_pool.requirements
                ]

                # Build the NodePool spec
                nodepool_spec = {
                    "template": {
                        "spec": {
                            "requirements": requirements,
                            "nodeClassRef": {
                                "group": "karpenter.k8s.aws",
                                "kind": "EC2NodeClass",
                                "name": node_pool.name,
                            },
                        }
                    },
                    "disruption": {"consolidationPolicy": "WhenEmptyOrUnderutilized", "consolidateAfter": "5m"},
                }

                # Add weight for NodePool priority
                nodepool_spec["weight"] = node_pool.weight

                # Add expireAfter if specified
                if node_pool.expire_after:
                    nodepool_spec["template"]["spec"]["expireAfter"] = node_pool.expire_after

                # Add taints if specified
                if node_pool.taints:
                    nodepool_spec["template"]["spec"]["taints"] = [
                        {"key": taint.key, "value": taint.value, "effect": taint.effect} for taint in node_pool.taints
                    ]

                # Add limits if specified
                if node_pool.limits:
                    limits = {}
                    if node_pool.limits.cpu:
                        limits["cpu"] = node_pool.limits.cpu
                    if node_pool.limits.memory:
                        limits["memory"] = node_pool.limits.memory
                    if node_pool.limits.nvidia_com_gpu:
                        limits["nvidia.com/gpu"] = node_pool.limits.nvidia_com_gpu

                    if limits:
                        nodepool_spec["limits"] = limits

                # Deploy the NodePool
                k8s.apiextensions.CustomResource(
                    f"{self.workload.compound_name}-{release}-karpenter-nodepool-{node_pool.name}",
                    metadata=k8s.meta.v1.ObjectMetaArgs(
                        name=node_pool.name,
                        labels=self.required_tags,
                    ),
                    api_version="karpenter.sh/v1",
                    kind="NodePool",
                    spec=nodepool_spec,
                    opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
                )

                # Deploy EC2NodeClass for this NodePool
                cluster_data = self.managed_clusters_by_release[release]
                vpc_config = cluster_data["cluster"]["resourcesVpcConfig"]
                subnet_ids = vpc_config["subnetIds"]
                security_group_ids = vpc_config.get("securityGroupIds", [])
                if vpc_config.get("clusterSecurityGroupId"):
                    security_group_ids = [*security_group_ids, vpc_config["clusterSecurityGroupId"]]

                # Add FSX NFS security group
                fsx_sg_id, fsx_ok = ptd.aws_fsx_nfs_sg_id(vpc_config["vpcId"], region=self.workload.cfg.region)
                if fsx_ok:
                    security_group_ids.append(fsx_sg_id)

                # Add EFS NFS security group if EFS is enabled
                if cluster_config.enable_efs_csi_driver or cluster_config.efs_config is not None:
                    efs_sg_id, efs_ok = ptd.aws_efs_nfs_sg_id(vpc_config["vpcId"], region=self.workload.cfg.region)
                    if efs_ok:
                        security_group_ids.append(efs_sg_id)

                k8s.apiextensions.CustomResource(
                    f"{self.workload.compound_name}-{release}-karpenter-ec2nodeclass-{node_pool.name}",
                    metadata=k8s.meta.v1.ObjectMetaArgs(
                        name=node_pool.name,
                        labels=self.required_tags,
                    ),
                    api_version="karpenter.k8s.aws/v1",
                    kind="EC2NodeClass",
                    spec={
                        "instanceProfile": f"KarpenterNodeInstanceProfile-{cluster_name}.posit.team",
                        "amiSelectorTerms": [{"alias": "al2023@latest"}],
                        "subnetSelectorTerms": [{"id": subnet_id} for subnet_id in subnet_ids],
                        "securityGroupSelectorTerms": [{"id": sg_id} for sg_id in security_group_ids],
                        "blockDeviceMappings": [
                            {
                                "deviceName": "/dev/xvda",
                                "ebs": {
                                    "volumeSize": node_pool.root_volume_size,
                                    "volumeType": "gp3",
                                },
                            }
                        ],
                    },
                    opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
                )

        # Deploy per-nodepool overprovisioning deployments (only creates deployments for pools with replicas > 0)
        self._define_karpenter_overprovisioning_pool(release, cluster_config.karpenter_config)

    def _define_alloy(self, release: str, version: str):
        namespace = k8s.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-alloy-ns",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=ALLOY_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        # create configMap used by Alloy deployment
        config_map_name = f"{self.workload.compound_name}-{release}-alloy-config"
        AlloyConfig(
            config_map_name,
            workload=self.workload,
            release=release,
            region=self.workload.cfg.region,
            namespace=ALLOY_NAMESPACE,
            provider=self.kube_providers[release],
            should_scrape_system_logs=self.workload.cfg.grafana_scrape_system_logs,
        )

        # Get mimir password from workload secrets
        workload_secrets, ok = ptd.secrecy.aws_get_secret_value_json(
            self.workload.secret_name, region=self.workload.cfg.region
        )
        if not ok:
            msg = f"Failed to retrieve workload secrets from {self.workload.secret_name} in region {self.workload.cfg.region}"
            raise ValueError(msg)

        if "mimir-password" not in workload_secrets:
            msg = f"mimir-password key missing from workload secrets in {self.workload.secret_name}"
            raise ValueError(msg)

        mimir_password = workload_secrets["mimir-password"]

        k8s.core.v1.Secret(
            f"{self.workload.compound_name}-{release}-alloy-mimir-auth",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="mimir-auth",
                namespace="alloy",
            ),
            string_data={"password": mimir_password},
            opts=pulumi.ResourceOptions(parent=namespace, providers=[self.kube_providers[release]]),
        )

        # Deploy the Helm chart
        k8s.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-grafana-alloy-release",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="alloy",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://grafana.github.io/helm-charts",
                "chart": "alloy",
                "targetNamespace": "alloy",
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "serviceAccount": {
                            "create": True,
                            "name": str(ptd.Roles.ALLOY),
                            "annotations": {
                                "eks.amazonaws.com/role-arn": f"arn:aws:iam::{self.workload.cfg.account_id}:role/{self.workload.alloy_role_name}",
                            },
                        },
                        "controller": {
                            "volumes": {
                                "extra": [
                                    {
                                        "name": "mimir-auth",
                                        "secret": {
                                            "secretName": "mimir-auth",
                                            "items": [
                                                {
                                                    "key": "password",
                                                    "path": "password",
                                                }
                                            ],
                                        },
                                    }
                                ]
                            }
                        },
                        "alloy": {
                            "clustering": {"enabled": True},
                            "extraPorts": [
                                {
                                    "name": "faro",
                                    "port": 12347,
                                    "targetPort": 12347,
                                    "protocol": "TCP",
                                }
                            ],
                            "mounts": {
                                "extra": [
                                    {
                                        "name": "mimir-auth",
                                        "mountPath": "/etc/mimir/",
                                        "readOnly": True,
                                    }
                                ],
                                "varlog": self.workload.cfg.grafana_scrape_system_logs,
                            },
                            "securityContext": {
                                "privileged": self.workload.cfg.grafana_scrape_system_logs,
                                "runAsUser": 0 if self.workload.cfg.grafana_scrape_system_logs else None,
                            },
                            "configMap": {"create": False, "name": config_map_name, "key": "config.alloy"},
                        },
                        "ingress": {
                            "enabled": True,
                            "faroPort": 12347,
                            "hosts": [f"faro.{self.workload.cfg.domain}"],
                        },
                        # Alloy is a DaemonSet, needs to run on all nodes including Karpenter session nodes
                        "tolerations": [
                            {
                                "key": "workload-type",
                                "operator": "Equal",
                                "value": "session",
                                "effect": "NoSchedule",
                            },
                        ],
                    }
                ),
            },
            opts=pulumi.ResourceOptions(depends_on=[namespace], provider=self.kube_providers[release]),
        )

    def _define_karpenter_overprovisioning_pool(self, release: str, karpenter_config):
        """Deploy per-nodepool deployments to trigger Karpenter node provisioning for overprovisioning."""
        cluster_name = f"{self.workload.compound_name}-{release}"

        # Create a low priority class for overprovisioning pool pods (shared across all pools)
        k8s.scheduling.v1.PriorityClass(
            f"{cluster_name}-karpenter-overprovisioning-pool-priority",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="karpenter-overprovisioning-pool-priority",
                labels=self.required_tags,
            ),
            value=-100,  # Low priority to ensure eviction for real workloads
            global_default=False,
            description="Low priority for Karpenter overprovisioning pool pods that should be evicted first",
            preemption_policy="PreemptLowerPriority",
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        # Create a deployment for each nodepool with overprovisioning enabled
        for node_pool in karpenter_config.node_pools:
            if node_pool.overprovisioning_replicas <= 0:
                continue

            # Build resource requests/limits
            requests = {}
            limits = {}
            if node_pool.overprovisioning_cpu_request:
                requests["cpu"] = node_pool.overprovisioning_cpu_request
                limits["cpu"] = node_pool.overprovisioning_cpu_request
            if node_pool.overprovisioning_memory_request:
                requests["memory"] = node_pool.overprovisioning_memory_request
                limits["memory"] = node_pool.overprovisioning_memory_request
            if node_pool.overprovisioning_nvidia_gpu_request:
                requests["nvidia.com/gpu"] = node_pool.overprovisioning_nvidia_gpu_request
                limits["nvidia.com/gpu"] = node_pool.overprovisioning_nvidia_gpu_request

            # Build node affinity match expressions to target this specific nodepool
            # and match its requirements
            match_expressions = [
                k8s.core.v1.NodeSelectorRequirementArgs(
                    key="karpenter.sh/nodepool",
                    operator="In",
                    values=[node_pool.name],
                ),
            ]

            # Add tolerations from the nodepool config
            tolerations = [
                k8s.core.v1.TolerationArgs(
                    key=taint.key,
                    operator="Equal",
                    value=taint.value,
                    effect=taint.effect,
                )
                for taint in node_pool.taints
            ]

            deployment_name = f"{node_pool.name}-overprovisioning"
            app_label = f"{node_pool.name}-overprovisioning"

            k8s.apps.v1.Deployment(
                f"{cluster_name}-{deployment_name}",
                metadata=k8s.meta.v1.ObjectMetaArgs(
                    name=deployment_name,
                    namespace="kube-system",
                    labels=self.required_tags | {"app": app_label, "nodepool": node_pool.name},
                ),
                spec=k8s.apps.v1.DeploymentSpecArgs(
                    replicas=node_pool.overprovisioning_replicas,
                    selector=k8s.meta.v1.LabelSelectorArgs(
                        match_labels={"app": app_label},
                    ),
                    template=k8s.core.v1.PodTemplateSpecArgs(
                        metadata=k8s.meta.v1.ObjectMetaArgs(
                            labels={"app": app_label, "nodepool": node_pool.name},
                        ),
                        spec=k8s.core.v1.PodSpecArgs(
                            priority_class_name="karpenter-overprovisioning-pool-priority",
                            termination_grace_period_seconds=0,  # Immediate termination for quick eviction
                            containers=[
                                k8s.core.v1.ContainerArgs(
                                    name="pause",
                                    image="registry.k8s.io/pause:3.9",
                                    resources=k8s.core.v1.ResourceRequirementsArgs(
                                        requests=requests,
                                        limits=limits,
                                    ),
                                )
                            ],
                            tolerations=tolerations,
                            affinity=k8s.core.v1.AffinityArgs(
                                # REQUIRE scheduling on this specific Karpenter nodepool
                                node_affinity=k8s.core.v1.NodeAffinityArgs(
                                    required_during_scheduling_ignored_during_execution=k8s.core.v1.NodeSelectorArgs(
                                        node_selector_terms=[
                                            k8s.core.v1.NodeSelectorTermArgs(match_expressions=match_expressions)
                                        ]
                                    )
                                ),
                                # Spread overprovisioning pods across different nodes
                                pod_anti_affinity=k8s.core.v1.PodAntiAffinityArgs(
                                    preferred_during_scheduling_ignored_during_execution=[
                                        k8s.core.v1.WeightedPodAffinityTermArgs(
                                            weight=100,
                                            pod_affinity_term=k8s.core.v1.PodAffinityTermArgs(
                                                label_selector=k8s.meta.v1.LabelSelectorArgs(
                                                    match_labels={"app": app_label},
                                                ),
                                                topology_key="kubernetes.io/hostname",
                                            ),
                                        )
                                    ],
                                ),
                            ),
                        ),
                    ),
                ),
                opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
            )
