import pulumi
import pulumi_postgresql
import pulumi_random

import ptd.workload


class GrafanaPostgresResources(pulumi.ComponentResource):
    provider: pulumi_postgresql.provider.Provider
    workload: ptd.workload.AbstractWorkload
    release: str

    role: str
    database: str
    password: pulumi_random.RandomPassword

    def __init__(
        self,
        workload: ptd.workload.AbstractWorkload,
        release: str,
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
        self.release = release

        self._define_grafana()

        outputs = {
            "db_grafana_pw": self.password.result,
        }

        for key, value in outputs.items():
            pulumi.export(key, value)

        self.register_outputs(outputs)

    def _define_grafana(self) -> None:
        name = f"{self.workload.compound_name}-{self.release}"
        self.password = pulumi_random.RandomPassword(
            f"{name}-db-grafana-pw",
            special=True,
            override_special="-_",
            length=36,
            opts=pulumi.ResourceOptions(parent=self),
        )

        self.role = self.database = f"grafana-{self.workload.compound_name}-{self.release}"

        grafana_role = pulumi_postgresql.Role(
            f"{name}-grafana-role",
            login=True,
            name=self.role,
            password=self.password.result,
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        self.grafana_db = pulumi_postgresql.Database(
            f"{name}-grafana-db",
            name=self.database,
            owner=self.role,
            opts=pulumi.ResourceOptions(
                parent=self,
                depends_on=[grafana_role],
                protect=True,
            ),
        )

        self.grant = pulumi_postgresql.Grant(
            f"{name}-grafana-grant",
            database=self.database,
            role=self.role,
            schema="public",
            object_type="schema",
            privileges=[
                "CREATE",
                "USAGE",
            ],
            opts=pulumi.ResourceOptions(
                parent=self,
                depends_on=[self.grafana_db, grafana_role],
            ),
        )
