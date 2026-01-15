import pulumi
import pulumi_azure_native as pulumi_az
import pulumi_kubernetes as k8s

import ptd.azure_workload
from ptd.azure_roles import STORAGE_ACCOUNT_CONTRIBUTOR_ROLE_DEFINITION_ID


class AzureFilesCSI(pulumi.ComponentResource):
    """
    Creates a Kubernetes StorageClass for Azure Files CSI driver that uses a pre-created storage account.
    This enables dynamic file share provisioning using the Azure Files CSI driver with private endpoints.
    """

    storage_class_name: str

    def __init__(
        self,
        workload: ptd.azure_workload.AzureWorkload,
        release: str,
        storage_account_name: str,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}-azure-files-csi",
            *args,
            **kwargs,
        )

        self.workload = workload
        self.release = release
        self.storage_account_name = storage_account_name

        self._configure_rbac()
        self._define_storage_class()

        self.register_outputs({})

    def _configure_rbac(self):
        # cluster kubelet identity needs Storage Account Contributor role on the storage account in order to create Azure Files shares
        cluster = pulumi_az.containerservice.get_managed_cluster(
            resource_name=self.workload.cluster_name(self.release),
            resource_group_name=self.workload.resource_group_name,
        )

        kubelet_identity = cluster.identity.principal_id

        pulumi_az.authorization.RoleAssignment(
            f"{self.workload.compound_name}-{self.release}-files-csi-role",
            principal_id=kubelet_identity,
            principal_type="ServicePrincipal",
            role_definition_id=f"/subscriptions/{self.workload.cfg.subscription_id}/providers/Microsoft.Authorization/roleDefinitions/{STORAGE_ACCOUNT_CONTRIBUTOR_ROLE_DEFINITION_ID}",
            scope=f"/subscriptions/{self.workload.cfg.subscription_id}/resourceGroups/{self.workload.resource_group_name}/providers/Microsoft.Storage/storageAccounts/{self.storage_account_name}",
            opts=pulumi.ResourceOptions(parent=self),
        )

    def _define_storage_class(self):
        k8s.storage.v1.StorageClass(
            f"{self.workload.compound_name}-{self.release}-azure-files-csi",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=self.workload.azure_files_csi_storage_class_name,
            ),
            provisioner="file.csi.azure.com",
            parameters={
                "resourceGroup": self.workload.resource_group_name,
                "storageAccount": self.storage_account_name,
                "server": f"{self.storage_account_name}.file.core.windows.net",
                "shareNamePrefix": "ppm-",
                "protocol": "nfs",
            },
            mount_options=[
                "nconnect=4",
                "noresvport",
                "actimeo=30",
                "lookupcache=pos",
            ],
            allow_volume_expansion=True,
            reclaim_policy="Retain",
            volume_binding_mode="Immediate",
            opts=pulumi.ResourceOptions(parent=self),
        )
