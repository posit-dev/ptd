import json

import pulumi
import pulumi_azure_native as pulumi_az
import pulumi_postgresql

import ptd.azure_workload
from ptd import azure_sdk
from ptd.pulumi_resources.grafana_postgres_resources import GrafanaPostgresResources


class AzureWorkloadPostgresConfig(pulumi.ComponentResource):
    provider: pulumi_postgresql.provider.Provider
    workload: ptd.azure_workload.AzureWorkload

    @classmethod
    def autoload(cls) -> "AzureWorkloadPostgresConfig":
        return cls(workload=ptd.azure_workload.AzureWorkload(pulumi.get_stack()))

    def __init__(
        self,
        workload: ptd.azure_workload.AzureWorkload,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        self.workload = workload

        self._define_provider()
        self._define_grafana_resources()

    def _define_provider(self) -> None:
        # fetch secret created via persistent step
        secret = azure_sdk.get_secret_json(
            secret_name=f"{self.workload.compound_name}-grafana-postgres-admin-secret",
            vault_name=self.workload.key_vault_name,
        )

        fqdn = secret.get("fqdn")
        user = secret.get("username")
        pw = secret.get("password")

        if not fqdn or not user or not pw:
            msg = "Grafana DB secret must contain 'fqdn', 'username' and 'password' fields."
            raise ValueError(msg)

        self.provider = pulumi_postgresql.provider.Provider(
            f"{self.workload.compound_name}-grafana-postgres-provider",
            host=fqdn,
            port=5432,
            sslmode="require",
            username=user,
            password=pw,
            superuser=False,
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

    def _define_grafana_resources(self):
        for release in self.workload.cfg.clusters:
            postgres = GrafanaPostgresResources(
                workload=self.workload,
                release=release,
                opts=pulumi.ResourceOptions(
                    parent=self,
                    provider=self.provider,
                ),
            )

            secret_val = pulumi.Output.all(
                pw=postgres.password.result, role=postgres.role, database=postgres.database
            ).apply(
                lambda outputs: json.dumps(
                    {
                        "role": outputs["role"],
                        "database": outputs["database"],
                        "password": outputs["pw"],
                    }
                )
            )

            pulumi_az.keyvault.Secret(
                f"{self.workload.compound_name}-{release}-postgres-grafana-user",
                secret_name=f"{self.workload.compound_name}-{release}-postgres-grafana-user",
                resource_group_name=self.workload.resource_group_name,
                properties=pulumi_az.keyvault.SecretPropertiesArgs(value=secret_val),
                vault_name=self.workload.key_vault_name,
                opts=pulumi.ResourceOptions(
                    protect=self.workload.cfg.protect_persistent_resources,
                ),
            )
