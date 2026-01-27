import pulumi
import pulumi_azure_native as azure
import pulumi_kubernetes as kubernetes

import ptd.azure_roles
import ptd.azure_workload

TRIDENT_NAMESPACE = "trident"


class TridentOperator(pulumi.ComponentResource):
    workload: ptd.azure_workload.AzureWorkload
    release: str

    namespace: kubernetes.core.v1.Namespace
    helm_release: kubernetes.helm.v3.Release
    backend: kubernetes.apiextensions.CustomResource
    managed_identity: azure.managedidentity.UserAssignedIdentity

    def __init__(
        self,
        workload: ptd.azure_workload.AzureWorkload,
        release: str,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}-trident-operator",
            *args,
            **kwargs,
        )

        self.release = release
        self.workload = workload

        # commented out until we resolve: https://github.com/posit-dev/ptd/issues/45
        # self._define_managed_identity()
        # self._define_helm_release()
        # self._define_netapp_backend()
        self._define_storage_class()

        self.register_outputs({})

    def _define_managed_identity(self):
        self.managed_identity = azure.managedidentity.UserAssignedIdentity(
            resource_name=f"id-ptd-{self.workload.compound_name}-{self.release}-trident-sa",
            resource_group_name=self.workload.resource_group_name,
            location=self.workload.cfg.region,
            tags=self.workload.required_tags,
            opts=pulumi.ResourceOptions(parent=self),
        )

        self.namespace = kubernetes.core.v1.Namespace(
            f"{self.workload.compound_name}-{self.release}-trident-namespace",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=TRIDENT_NAMESPACE,
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

        azure.authorization.RoleAssignment(
            f"{self.workload.compound_name}-contributor-role-assignment",
            scope=f"/subscriptions/{self.workload.cfg.subscription_id}/resourceGroups/{self.workload.resource_group_name}",
            principal_id=self.managed_identity.principal_id,
            role_definition_id=f"/providers/Microsoft.Authorization/roleDefinitions/{ptd.azure_roles.CONTRIBUTOR_ROLE_DEFINITION_ID}",
            principal_type=azure.authorization.PrincipalType.SERVICE_PRINCIPAL,
            opts=pulumi.ResourceOptions(parent=self.managed_identity, delete_before_replace=True),
        )

        oidc_issuer_url = self.workload.cluster_oidc_issuer_url(self.release)

        for service_account in ["trident-controller", "trident-operator", "trident-node-linux"]:
            azure.managedidentity.FederatedIdentityCredential(
                resource_name=f"fedid-{self.workload.compound_name}-{self.release}-{service_account}",
                resource_name_=self.managed_identity.name,
                federated_identity_credential_resource_name=f"fedid-{self.workload.compound_name}-{self.release}-{service_account}",
                resource_group_name=self.workload.resource_group_name,
                subject=f"system:serviceaccount:{TRIDENT_NAMESPACE}:{service_account}",
                issuer=oidc_issuer_url,
                audiences=["api://AzureADTokenExchange"],
                opts=pulumi.ResourceOptions(parent=self.managed_identity),
            )

    def _define_helm_release(self):
        self.helm_release = kubernetes.helm.v3.Release(
            f"{self.workload.compound_name}-{self.release}-trident-operator",
            chart="trident-operator",
            version="100.2502.1",
            repository_opts=kubernetes.helm.v3.RepositoryOptsArgs(
                repo="https://netapp.github.io/trident-helm-chart",
            ),
            namespace=TRIDENT_NAMESPACE,
            create_namespace=True,
            values={
                "operatorDebug": True,
                "tridentDebug": True,
                "cloudProvider": "Azure",
                "cloudIdentity": self.managed_identity.client_id.apply(
                    lambda client_id: f"'azure.workload.identity/client-id: {client_id}'"
                ),
                "serviceAccount": {"create": False, "name": "trident-operator"},
                "podLabels": {"azure.workload.identity/use": "true"},
            },
            opts=pulumi.ResourceOptions(parent=self, depends_on=[self.namespace, self.managed_identity]),
        )

    def _define_netapp_backend(self):
        resource_group = self.workload.resource_group_name

        self.backend = kubernetes.apiextensions.CustomResource(
            f"{self.workload.compound_name}-{self.release}-netapp-backend",
            api_version="trident.netapp.io/v1",
            kind="TridentBackendConfig",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=f"{self.workload.compound_name}-{self.release}-netapp",
                namespace=TRIDENT_NAMESPACE,
            ),
            spec={
                "version": 1,
                "storageDriverName": "azure-netapp-files",
                "subscriptionID": self.workload.cfg.subscription_id,
                "location": self.workload.cfg.region,
                "capacityPools": [
                    f"{resource_group}/{self.workload.netapp_account_name}/{self.workload.netapp_pool_name}"
                ],
                "networkFeatures": "Standard",
                "virtualNetwork": f"{resource_group}/{self.workload.vnet_name}",
                "subnet": f"{resource_group}/{self.workload.vnet_name}/{self.workload.netapp_subnet_name}",
                "serviceLevel": "Premium",
            },
            opts=pulumi.ResourceOptions(
                parent=self,
                depends_on=[self.helm_release],
            ),
        )

    def _define_storage_class(self):
        kubernetes.storage.v1.StorageClass(
            f"{self.workload.compound_name}-{self.release}-netapp-storage-class",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="azure-netapp-files",
                namespace=TRIDENT_NAMESPACE,
            ),
            provisioner="csi.trident.netapp.io",
            parameters={
                "backendType": "azure-netapp-files",
            },
            allow_volume_expansion=True,
            opts=pulumi.ResourceOptions(
                # depends_on=[self.backend], # uncomment when backend is re-enabled
                parent=self,
            ),
        )
