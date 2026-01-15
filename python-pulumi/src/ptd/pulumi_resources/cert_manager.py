import pulumi
import pulumi_azure_native as azure
import pulumi_kubernetes as k8s

import ptd
import ptd.azure_roles
import ptd.azure_workload

CERT_MANAGER_NAMESPACE = "cert-manager"


class CertManager(pulumi.ComponentResource):
    release: str
    workload: ptd.azure_workload.AzureWorkload
    domains: list[str]

    managed_identity: azure.managedidentity.UserAssignedIdentity
    service_account: k8s.core.v1.ServiceAccount

    def __init__(
        self,
        release: str,
        workload: ptd.azure_workload.AzureWorkload,
        domains: list[str],
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-cert-manager",
            None,
            *args,
            **kwargs,
        )

        self.release = release
        self.workload = workload
        self.domains = domains

        self._define_namespace()
        self._define_managed_identity()
        self._define_helm_release()
        self._define_cluster_issuers()

        self.register_outputs({})

    def _define_managed_identity(self):
        self.managed_identity = azure.managedidentity.UserAssignedIdentity(
            resource_name=f"id-{self.workload.compound_name}-{self.release}-cert-manager-sa",
            resource_group_name=self.workload.resource_group_name,
            location=self.workload.cfg.region,
            tags=self.workload.required_tags,
            opts=pulumi.ResourceOptions(parent=self),
        )

        azure.authorization.RoleAssignment(
            f"{self.workload.compound_name}-{self.release}-dns-contributor-cert-manager",
            scope=f"/subscriptions/{self.workload.cfg.subscription_id}/resourceGroups/{self.workload.resource_group_name}",
            principal_id=self.managed_identity.principal_id,
            role_definition_id=f"/providers/Microsoft.Authorization/roleDefinitions/{ptd.azure_roles.DNS_ZONE_CONTRIBUTOR_ROLE_DEFINITION_ID}",
            principal_type=azure.authorization.PrincipalType.SERVICE_PRINCIPAL,
            opts=pulumi.ResourceOptions(parent=self.managed_identity),
        )

        self.service_account = k8s.core.v1.ServiceAccount(
            f"{self.workload.compound_name}-{self.release}-cert-manager-sa",
            metadata={
                "name": "cert-manager",
                "namespace": CERT_MANAGER_NAMESPACE,
                "annotations": {
                    "azure.workload.identity/client-id": self.managed_identity.client_id,
                },
                "labels": {
                    "azure.workload.identity/use": "true",
                },
            },
            opts=pulumi.ResourceOptions(parent=self, depends_on=[self.namespace, self.managed_identity]),
        )

        oidc_issuer_url = self.workload.cluster_oidc_issuer_url(self.release)
        azure.managedidentity.FederatedIdentityCredential(
            resource_name=f"fedid-{self.workload.compound_name}-{self.release}-cert-manager",
            resource_name_=self.managed_identity.name,
            federated_identity_credential_resource_name=f"fedid-{self.workload.compound_name}-{self.release}-cert-manager",
            resource_group_name=self.workload.resource_group_name,
            subject=f"system:serviceaccount:{CERT_MANAGER_NAMESPACE}:cert-manager",
            issuer=oidc_issuer_url,
            audiences=["api://AzureADTokenExchange"],
            opts=pulumi.ResourceOptions(parent=self.managed_identity),
        )

    def _define_namespace(self):
        self.namespace = k8s.core.v1.Namespace(
            f"{self.workload.compound_name}-{self.release}-cert-manager-namespace",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=CERT_MANAGER_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_helm_release(self):
        self.helm_release = k8s.helm.v3.Release(
            f"{self.workload.compound_name}-{self.release}-cert-manager",
            chart="cert-manager",
            version="v1.18.1",
            namespace=CERT_MANAGER_NAMESPACE,
            name="cert-manager",
            repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                repo="https://charts.jetstack.io",
            ),
            atomic=True,
            values={
                "installCRDs": True,
                "serviceAccount": {"create": False, "name": "cert-manager"},
                "podLabels": {"azure.workload.identity/use": "true"},
            },
            opts=pulumi.ResourceOptions(parent=self, depends_on=[self.namespace, self.service_account]),
        )

    def _define_cluster_issuers(self):
        for domain in self.domains:
            k8s.apiextensions.CustomResource(
                f"{self.workload.compound_name}-{self.release}-{domain}-cluster-issuer",
                api_version="cert-manager.io/v1",
                kind="ClusterIssuer",
                metadata=k8s.meta.v1.ObjectMetaArgs(name=f"letsencrypt-{domain}"),
                spec={
                    "acme": {
                        "email": "posit-dev@posit.co",
                        "server": "https://acme-v02.api.letsencrypt.org/directory",
                        "privateKeySecretRef": {
                            "name": f"{self.workload.compound_name}-{self.release}-{domain}-letsencrypt-account-key",
                        },
                        "solvers": [
                            {
                                "dns01": {
                                    "azureDNS": {
                                        "managedIdentity": {"clientID": self.managed_identity.client_id},
                                        "environment": "AzurePublicCloud",
                                        "hostedZoneName": domain,
                                        "resourceGroupName": self.workload.resource_group_name,
                                        "subscriptionID": self.workload.cfg.subscription_id,
                                    }
                                }
                            }
                        ],
                    }
                },
                opts=pulumi.ResourceOptions(parent=self, depends_on=self.helm_release),
            )
