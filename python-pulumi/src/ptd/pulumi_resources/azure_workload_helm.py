import base64
import json
import typing

import pulumi
import pulumi_azure_native as azure
import pulumi_kubernetes as kubernetes
import yaml

import ptd.azure_roles
import ptd.azure_workload
from ptd import azure_sdk
from ptd.pulumi_resources.grafana_alloy import AlloyConfig

ALLOY_NAMESPACE = "alloy"
EXTERNAL_DNS_NAMESPACE = "external-dns"
GRAFANA_NAMESPACE = "grafana"
LOKI_NAMESPACE = "loki"
MIMIR_NAMESPACE = "mimir"
ESO_NAMESPACE = "external-secrets"
ESO_SERVICE_ACCOUNT = "external-secrets"
CLUSTER_SECRET_STORE_NAME = "azure-key-vault"  # noqa: S105
# v1beta1 matches external_secrets_operator_version default "0.10.7".
# Update this if ESO is upgraded past the version that drops v1beta1 support.
ESO_API_VERSION = "external-secrets.io/v1beta1"


def _eso_helm_values() -> dict:
    """Build the Helm values dict for external-secrets-operator."""
    return {
        "installCRDs": True,
        "serviceAccount": {
            "create": True,
            "name": ESO_SERVICE_ACCOUNT,
        },
    }


def _cluster_secret_store_spec(tenant_id: str, vault_url: str) -> dict:
    """Build the ClusterSecretStore spec for Azure Key Vault (Workload Identity auth).

    Args:
        tenant_id: Azure tenant ID
        vault_url: Azure Key Vault URL (e.g., https://<vault-name>.vault.azure.net/)

    Returns:
        ClusterSecretStore spec dict for Azure Key Vault provider
    """
    return {
        "provider": {
            "azurekv": {
                "authType": "WorkloadIdentity",
                "tenantId": tenant_id,
                "vaultUrl": vault_url,
                "serviceAccountRef": {
                    "name": ESO_SERVICE_ACCOUNT,
                    "namespace": ESO_NAMESPACE,
                },
            },
        },
        "conditions": [
            {"namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": ptd.POSIT_TEAM_NAMESPACE}}}
        ],
    }


class AzureWorkloadHelm(pulumi.ComponentResource):
    workload: ptd.azure_workload.AzureWorkload

    required_tags: dict[str, str]
    kube_providers: dict[str, kubernetes.Provider]
    managed_clusters_by_release: dict[str, dict[str, typing.Any]]

    @classmethod
    def autoload(cls) -> "AzureWorkloadHelm":
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

        for release in self.managed_clusters_by_release:
            components = self.workload.cfg.clusters[release].components

            # Deploy external-secrets-operator (opt-in via enable_external_secrets_operator)
            if self.workload.cfg.clusters[release].enable_external_secrets_operator:
                self._define_external_secrets_operator(release, components.external_secrets_operator_version)

            self._define_external_dns(release, components.external_dns_version)
            self._define_loki(release, components.loki_version)
            self._define_mimir(release, components.mimir_version)
            self._define_grafana(release, components.grafana_version)
            self._define_alloy(release, components.alloy_version)
            self._define_kube_state_metrics(release, components.kube_state_metrics_version)

    def _define_loki(self, release: str, version: str):
        loki_identity = self._define_blob_storage_managed_identity(
            release=release, component="loki", namespace=LOKI_NAMESPACE, service_account=str(ptd.Roles.LOKI)
        )

        namespace = kubernetes.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-loki-ns",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=LOKI_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        # yes this chart does mix camel and snake case field names in its values, so fun
        kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-loki-helm-release",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="loki",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://grafana.github.io/helm-charts",
                "chart": "loki",
                "targetNamespace": LOKI_NAMESPACE,
                "version": version,
                "valuesContent": loki_identity.client_id.apply(
                    lambda client_id: yaml.dump(
                        {
                            "gateway": {
                                "image": {
                                    "registry": "quay.io",
                                    "repository": "nginx/nginx-unprivileged",
                                }
                            },
                            "loki": {
                                "auth_enabled": False,
                                "podLabels": {
                                    "azure.workload.identity/use": "true",
                                },
                                "compactor": {
                                    "retention_enabled": True,
                                    "delete_request_store": "azure",
                                },
                                "limits_config": {
                                    "retention_period": "30d",
                                },
                                "schemaConfig": {
                                    "configs": [
                                        {
                                            "store": "tsdb",
                                            "object_store": "azure",
                                            "schema": "v13",
                                            "index": {
                                                "prefix": "loki_index_",
                                                "period": "24h",
                                            },
                                        }
                                    ],
                                },
                                "storage_config": {
                                    "azure": {
                                        "account_name": self.workload.storage_account_name,
                                        "container_name": "loki",
                                        "use_federated_token": True,
                                    },
                                },
                                "storage": {
                                    "type": "azure",
                                    "bucketNames": {
                                        "chunks": "loki",
                                    },
                                    "azure": {
                                        "accountName": self.workload.storage_account_name,
                                        "useFederatedToken": True,
                                    },
                                },
                            },
                            "serviceAccount": {
                                "create": True,
                                "name": str(ptd.Roles.LOKI),
                                "annotations": {
                                    "azure.workload.identity/client-id": client_id,
                                },
                                "labels": {
                                    "azure.workload.identity/use": "true",
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
                        }
                    )
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release], depends_on=[namespace]),
        )

    def _define_mimir(self, release: str, version: str):
        mimir_identity = self._define_blob_storage_managed_identity(
            release=release, component="mimir", namespace=MIMIR_NAMESPACE, service_account=str(ptd.Roles.MIMIR)
        )

        namespace = kubernetes.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-mimir-ns",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=MIMIR_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-mimir-helm-release",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="mimir",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://grafana.github.io/helm-charts",
                "chart": "mimir-distributed",
                "targetNamespace": MIMIR_NAMESPACE,
                "version": version,
                "valuesContent": mimir_identity.client_id.apply(
                    lambda client_id: yaml.dump(
                        {
                            "serviceAccount": {
                                "create": True,
                                "name": str(ptd.Roles.MIMIR),
                                "annotations": {
                                    "azure.workload.identity/client-id": client_id,
                                },
                                "labels": {
                                    "azure.workload.identity/use": "true",
                                },
                            },
                            "global": {
                                "podLabels": {
                                    "azure.workload.identity/use": "true",
                                }
                            },
                            "mimir": {
                                "structuredConfig": {
                                    "common": {
                                        "storage": {
                                            "backend": "azure",
                                            "azure": {
                                                "account_name": self.workload.storage_account_name,
                                            },
                                        },
                                    },
                                    "blocks_storage": {
                                        "backend": "azure",
                                        "azure": {
                                            "container_name": "mimir-blocks",
                                        },
                                    },
                                    "limits": {
                                        "max_global_series_per_user": 800000,
                                        "max_label_names_per_series": 45,
                                    },
                                },
                            },
                            "minio": {
                                "enabled": False,
                            },
                            "alertmanager": {"enabled": False},
                            "ruler": {"enabled": False},
                            "ingester": {"persistentVolume": {"size": "20Gi"}},
                            "compactor": {"persistentVolume": {"size": "20Gi"}},
                            "store_gateway": {"persistentVolume": {"size": "20Gi"}},
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
                    )
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release], depends_on=[namespace]),
        )

    def _define_alloy(self, release: str, version: str):
        namespace = kubernetes.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-alloy-ns",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=ALLOY_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        # create configMap used by Alloy deployment
        AlloyConfig(
            "alloy-config",
            workload=self.workload,
            release=release,
            region=self.workload.cfg.region,
            namespace=ALLOY_NAMESPACE,
            provider=self.kube_providers[release],
        )

        # define auth secret mounted into alloy pods for mimir basic auth with control room
        mimir_password = azure_sdk.get_secret(
            secret_name=f"{self.workload.compound_name}-mimir-auth", vault_name=self.workload.key_vault_name
        )
        kubernetes.core.v1.Secret(
            f"{self.workload.compound_name}-{release}-mimir-auth",
            metadata={
                "name": "mimir-auth",
                "namespace": ALLOY_NAMESPACE,
            },
            data={"password": base64.b64encode(mimir_password.encode()).decode()},
            opts=pulumi.ResourceOptions(depends_on=[namespace], provider=self.kube_providers[release]),
        )

        kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-grafana-alloy-release",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="alloy",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://grafana.github.io/helm-charts",
                "chart": "alloy",
                "targetNamespace": ALLOY_NAMESPACE,
                "version": version,
                "valuesContent": yaml.dump(
                    {
                        "serviceAccount": {
                            "create": True,
                            "name": str(ptd.Roles.ALLOY),
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
                                "varlog": True,
                            },
                            "configMap": {"create": False, "name": "alloy-config", "key": "config.alloy"},
                        },
                        "ingress": {
                            "enabled": True,
                            "faroPort": 12347,
                            "hosts": [f"faro.{self.workload.cfg.domain}"],
                        },
                    }
                ),
            },
            opts=pulumi.ResourceOptions(depends_on=[namespace], provider=self.kube_providers[release]),
        )

    def _define_blob_storage_managed_identity(
        self, release: str, component: str, namespace: str, service_account: str
    ) -> azure.managedidentity.UserAssignedIdentity:
        identity = azure.managedidentity.UserAssignedIdentity(
            resource_name=f"id-{self.workload.compound_name}-{release}-{component}",
            resource_group_name=self.workload.resource_group_name,
            location=self.workload.cfg.region,
            tags=self.workload.required_tags,
            opts=pulumi.ResourceOptions(parent=self),
        )

        azure.authorization.RoleAssignment(
            f"{self.workload.compound_name}-{release}-{component}-blob-contributor",
            scope=f"/subscriptions/{self.workload.cfg.subscription_id}/resourceGroups/{self.workload.resource_group_name}/providers/Microsoft.Storage/storageAccounts/{self.workload.storage_account_name}",
            principal_id=identity.principal_id,
            role_definition_id=f"/providers/Microsoft.Authorization/roleDefinitions/{ptd.azure_roles.STORAGE_BLOB_DATA_CONTRIBUTOR_ROLE_DEFINITION_ID}",
            principal_type=azure.authorization.PrincipalType.SERVICE_PRINCIPAL,
            opts=pulumi.ResourceOptions(parent=identity),
        )

        oidc_issuer_url = self.workload.cluster_oidc_issuer_url(release)
        azure.managedidentity.FederatedIdentityCredential(
            resource_name=f"fedid-{self.workload.compound_name}-{release}-{component}",
            resource_name_=identity.name,
            federated_identity_credential_resource_name=f"fedid-{self.workload.compound_name}-{release}-{component}",
            resource_group_name=self.workload.resource_group_name,
            subject=f"system:serviceaccount:{namespace}:{service_account}",
            issuer=oidc_issuer_url,
            audiences=["api://AzureADTokenExchange"],
            opts=pulumi.ResourceOptions(parent=identity),
        )

        return identity

    def _define_external_dns(self, release: str, version: str):
        identity = azure.managedidentity.UserAssignedIdentity(
            resource_name=f"id-{self.workload.compound_name}-{release}-external-dns",
            resource_group_name=self.workload.resource_group_name,
            location=self.workload.cfg.region,
            tags=self.workload.required_tags,
            opts=pulumi.ResourceOptions(parent=self),
        )

        if self.workload.cfg.root_domain:
            azure.authorization.RoleAssignment(
                f"{self.workload.compound_name}-{release}-external-dns-dns-contributor",
                scope=f"/subscriptions/{self.workload.cfg.subscription_id}/resourceGroups/{self.workload.resource_group_name}/providers/Microsoft.Network/dnszones/{self.workload.cfg.root_domain}",
                principal_id=identity.principal_id,
                role_definition_id=f"/providers/Microsoft.Authorization/roleDefinitions/{ptd.azure_roles.DNS_ZONE_CONTRIBUTOR_ROLE_DEFINITION_ID}",
                principal_type=azure.authorization.PrincipalType.SERVICE_PRINCIPAL,
                opts=pulumi.ResourceOptions(parent=identity),
            )
        else:
            for site_name, site in sorted(self.workload.cfg.sites.items()):
                azure.authorization.RoleAssignment(
                    f"{self.workload.compound_name}-{release}-{site_name}-external-dns-dns-contributor",
                    scope=f"/subscriptions/{self.workload.cfg.subscription_id}/resourceGroups/{self.workload.resource_group_name}/providers/Microsoft.Network/dnszones/{site.domain}",
                    principal_id=identity.principal_id,
                    role_definition_id=f"/providers/Microsoft.Authorization/roleDefinitions/{ptd.azure_roles.DNS_ZONE_CONTRIBUTOR_ROLE_DEFINITION_ID}",
                    principal_type=azure.authorization.PrincipalType.SERVICE_PRINCIPAL,
                    opts=pulumi.ResourceOptions(parent=identity),
                )

        service_account = str(ptd.Roles.EXTERNAL_DNS)
        oidc_issuer_url = self.workload.cluster_oidc_issuer_url(release)
        azure.managedidentity.FederatedIdentityCredential(
            resource_name=f"fedid-{self.workload.compound_name}-{release}-external-dns",
            resource_name_=identity.name,
            federated_identity_credential_resource_name=f"fedid-{self.workload.compound_name}-{release}-external-dns",
            resource_group_name=self.workload.resource_group_name,
            subject=f"system:serviceaccount:{EXTERNAL_DNS_NAMESPACE}:{service_account}",
            issuer=oidc_issuer_url,
            audiences=["api://AzureADTokenExchange"],
            opts=pulumi.ResourceOptions(parent=identity),
        )

        namespace = kubernetes.core.v1.Namespace(
            f"{self.workload.compound_name}-{release}-external-dns-ns",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=EXTERNAL_DNS_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        # create azure.json secret to mount into pod
        azure_config = {
            "tenantId": self.workload.cfg.tenant_id,
            "subscriptionId": self.workload.cfg.subscription_id,
            "resourceGroup": self.workload.resource_group_name,
            "useWorkloadIdentityExtension": True,
        }

        kubernetes.core.v1.Secret(
            f"{self.workload.compound_name}-{release}-external-dns-secret",
            metadata={
                "name": "azure-config-file",
                "namespace": EXTERNAL_DNS_NAMESPACE,
            },
            data={
                "azure.json": base64.b64encode(json.dumps(azure_config).encode()).decode(),
            },
            opts=pulumi.ResourceOptions(parent=self, provider=self.kube_providers[release], depends_on=[namespace]),
        )

        kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-external-dns-helm-release",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="external-dns",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://kubernetes-sigs.github.io/external-dns/",
                "chart": "external-dns",
                "targetNamespace": EXTERNAL_DNS_NAMESPACE,
                "version": version,
                "valuesContent": identity.client_id.apply(
                    lambda client_id: yaml.dump(
                        {
                            "provider": "azure",
                            "domainFilters": [*sorted([site.domain for site in self.workload.cfg.sites.values()])],
                            "extraArgs": {
                                "txt-wildcard-replacement": "wildcard",
                            },
                            "extraVolumes": [
                                {"name": "azure-config-file", "secret": {"secretName": "azure-config-file"}}
                            ],
                            "extraVolumeMounts": [
                                {"name": "azure-config-file", "mountPath": "/etc/kubernetes", "readOnly": True}
                            ],
                            "policy": "sync",
                            "txtOwnerId": self.workload.cluster_name(release),
                            "txtPrefix": "_d",
                            "podLabels": {
                                "azure.workload.identity/use": "true",
                            },
                            "serviceAccount": {
                                "create": True,
                                "name": service_account,
                                "annotations": {
                                    "azure.workload.identity/client-id": client_id,
                                },
                                "labels": {
                                    "azure.workload.identity/use": "true",
                                },
                            },
                        }
                    )
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release], depends_on=[namespace]),
        )

    def _define_grafana(self, release: str, version: str):
        self._define_grafana_secret()

        kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-grafana-helm-release",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="grafana",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec={
                "repo": "https://grafana.github.io/helm-charts",
                "chart": "grafana",
                "targetNamespace": GRAFANA_NAMESPACE,
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
                            "database": {
                                "url": '${{ "{" }}PTD_DATABASE_URL{{ "}" }}',  # ${PTD_DATABASE_URL} in the resulting configMap
                                "ssl_mode": "require",
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

    def _define_grafana_secret(self) -> None:
        secret = azure_sdk.get_secret_json(
            secret_name=f"{self.workload.compound_name}-grafana-postgres-admin-secret",
            vault_name=self.workload.key_vault_name,
        )
        fqdn = secret["fqdn"]

        if not fqdn:
            msg = "Grafana admin secret must contain 'fqdn' field."
            raise ValueError(msg)

        # build kubernetes secret containing Grafana database connection string for use by Grafana helm chart
        for release in self.managed_clusters_by_release:
            secret = azure_sdk.get_secret_json(
                secret_name=f"{self.workload.compound_name}-{release}-postgres-grafana-user",
                vault_name=self.workload.key_vault_name,
            )

            role = secret["role"]
            database = secret["database"]
            pw = secret["password"]

            if not role or not database or not pw:
                msg = "Grafana DB secret must contain 'role', 'database' and 'password' fields."
                raise ValueError(msg)

            db_url = pulumi.Output.secret(f"postgres://{role}:{pw}@{fqdn}/{database}")

            kubernetes.core.v1.Namespace(
                f"{self.workload.compound_name}-{release}-grafana-ns",
                metadata={
                    "name": "grafana",
                },
                opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
            )

            kubernetes.core.v1.Secret(
                f"{self.workload.compound_name}-{release}-grafana-db-url",
                metadata={
                    "name": "grafana-db-url",
                    "namespace": GRAFANA_NAMESPACE,
                },
                data={"PTD_DATABASE_URL": db_url.apply(lambda url: base64.b64encode(url.encode()).decode())},
                opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
            )

    def _define_kube_state_metrics(self, release: str, version: str):
        kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-kube-state-metrics-helm-release",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
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
                        "metricLabelsAllowlist": [
                            "pods=[launcher-instance-id]",
                        ]
                    }
                ),
            },
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

    def _define_external_secrets_operator(self, release: str, version: str | None) -> None:
        """Deploy external-secrets-operator and create ClusterSecretStore for Azure Key Vault.

        Note: the ClusterSecretStore is created with ``depends_on=[eso_helm_release]``, which
        ensures Pulumi registers it after the HelmChart CR object exists in the API server.
        However, this does NOT wait for the Helm release to complete and CRDs to be installed.
        On a fresh deploy, the ClusterSecretStore apply will fail until ESO's CRDs converge
        (~1-2 reconcile loops). This is an architectural constraint of using HelmChart CRDs
        rather than ``pulumi_kubernetes.helm.v3.Release``.
        """
        # Create managed identity for ESO with Key Vault Secrets User role
        eso_identity = self._define_key_vault_secrets_managed_identity(
            release=release, component="eso", namespace=ESO_NAMESPACE, service_account=ESO_SERVICE_ACCOUNT
        )

        # Deploy external-secrets-operator Helm chart
        # Note: helm-controller auto-creates the targetNamespace from the HelmChart CR,
        # so the "external-secrets" namespace does not need to be created explicitly here.
        eso_spec: dict = {
            "repo": "https://charts.external-secrets.io",
            "chart": "external-secrets",
            "targetNamespace": ESO_NAMESPACE,
            "valuesContent": eso_identity.client_id.apply(
                lambda client_id: yaml.dump(
                    {
                        **_eso_helm_values(),
                        "serviceAccount": {
                            "create": True,
                            "name": ESO_SERVICE_ACCOUNT,
                            "annotations": {
                                "azure.workload.identity/client-id": client_id,
                            },
                            "labels": {
                                "azure.workload.identity/use": "true",
                            },
                        },
                        "podLabels": {
                            "azure.workload.identity/use": "true",
                        },
                    }
                )
            ),
        }
        if version is not None:
            eso_spec["version"] = version

        eso_helm_release = kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-external-secrets-helm-release",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="external-secrets",
                namespace=ptd.HELM_CONTROLLER_NAMESPACE,
                labels=self.required_tags,
            ),
            api_version="helm.cattle.io/v1",
            kind="HelmChart",
            spec=eso_spec,
            opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
        )

        # Create ClusterSecretStore for Azure Key Vault.
        # depends_on the HelmChart CR so Pulumi applies it after the ESO chart CR is registered.
        # CustomTimeouts makes the eventual-consistency explicit: on a fresh cluster the CRD may not
        # be available immediately; Pulumi will retry for up to 10 minutes before failing.
        vault_url = f"https://{self.workload.key_vault_name}.vault.azure.net/"
        kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{release}-cluster-secret-store",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=CLUSTER_SECRET_STORE_NAME,
                labels=self.required_tags,
            ),
            api_version=ESO_API_VERSION,
            kind="ClusterSecretStore",
            spec=_cluster_secret_store_spec(tenant_id=self.workload.cfg.tenant_id, vault_url=vault_url),
            opts=pulumi.ResourceOptions(
                provider=self.kube_providers[release],
                depends_on=[eso_helm_release],
                custom_timeouts=pulumi.CustomTimeouts(create="10m"),
            ),
        )

    def _define_key_vault_secrets_managed_identity(
        self, release: str, component: str, namespace: str, service_account: str
    ) -> azure.managedidentity.UserAssignedIdentity:
        """Create a managed identity with Key Vault Secrets User role and federated identity credential."""
        identity = azure.managedidentity.UserAssignedIdentity(
            resource_name=f"id-{self.workload.compound_name}-{release}-{component}",
            resource_group_name=self.workload.resource_group_name,
            location=self.workload.cfg.region,
            tags=self.workload.required_tags,
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Grant Key Vault Secrets User role (read-only access to secrets)
        azure.authorization.RoleAssignment(
            f"{self.workload.compound_name}-{release}-{component}-kv-secrets-user",
            scope=f"/subscriptions/{self.workload.cfg.subscription_id}/resourceGroups/{self.workload.resource_group_name}/providers/Microsoft.KeyVault/vaults/{self.workload.key_vault_name}",
            principal_id=identity.principal_id,
            role_definition_id=f"/providers/Microsoft.Authorization/roleDefinitions/{ptd.azure_roles.KEY_VAULT_SECRETS_USER_ROLE_DEFINITION_ID}",
            principal_type=azure.authorization.PrincipalType.SERVICE_PRINCIPAL,
            opts=pulumi.ResourceOptions(parent=identity),
        )

        # Create federated identity credential for Workload Identity
        oidc_issuer_url = self.workload.cluster_oidc_issuer_url(release)
        azure.managedidentity.FederatedIdentityCredential(
            resource_name=f"fedid-{self.workload.compound_name}-{release}-{component}",
            resource_name_=identity.name,
            federated_identity_credential_resource_name=f"fedid-{self.workload.compound_name}-{release}-{component}",
            resource_group_name=self.workload.resource_group_name,
            subject=f"system:serviceaccount:{namespace}:{service_account}",
            issuer=oidc_issuer_url,
            audiences=["api://AzureADTokenExchange"],
            opts=pulumi.ResourceOptions(parent=identity),
        )

        return identity
