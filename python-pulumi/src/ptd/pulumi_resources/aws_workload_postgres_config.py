import json

import pulumi
import pulumi_aws as aws
import pulumi_postgresql
import pulumi_random

import ptd
import ptd.aws_workload
import ptd.paths
import ptd.pulumi_resources.aws_fsx_openzfs_multi
import ptd.pulumi_resources.aws_vpc


class AWSWorkloadPostgresConfig(pulumi.ComponentResource):
    postgres: pulumi_postgresql.provider.Provider
    workload: ptd.aws_workload.AWSWorkload
    extra_db_pws: dict[str, pulumi_random.RandomPassword]

    @classmethod
    def autoload(cls) -> "AWSWorkloadPostgresConfig":
        return cls(workload=ptd.aws_workload.AWSWorkload(pulumi.get_stack()))

    def __init__(
        self,
        workload: ptd.aws_workload.AWSWorkload,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        self.extra_db_pws = {}
        self.workload = workload

        self._define_provider()
        self._define_grafana()
        self._define_extra_dbs()

        outputs = {
            "db_grafana_pw": self.db_grafana_pw.result,
            "grafana_db_name": self.grafana_db.name,
            "grant_id": self.grant.id,
        }

        for db_name, pw in self.extra_db_pws.items():
            outputs[f"{db_name}_pw"] = pw.result

        for key, value in outputs.items():
            pulumi.export(key, value)

        self.register_outputs(outputs)

    def _define_provider(self) -> None:
        persistent_stack = pulumi.StackReference(
            f"organization/ptd-aws-workload-persistent/{self.workload.compound_name}"
        )
        db_host_output = persistent_stack.require_output("db_address")
        db_port = 5432  # probably shouldn't hardcode this.
        db_secret_arn_output = persistent_stack.require_output("db_secret_arn")
        secret_version = aws.secretsmanager.get_secret_version(secret_id=db_secret_arn_output.apply(lambda x: x))

        pw = json.loads(secret_version.secret_string).get("password", "")

        self.postgres = pulumi_postgresql.provider.Provider(
            f"{self.workload.compound_name}-postgres-provider",
            host=db_host_output.apply(lambda x: x),
            port=db_port,
            sslmode="require",
            username="postgres",
            password=pw,
            superuser=False,
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

    def _define_grafana(self) -> None:
        self.db_grafana_pw = pulumi_random.RandomPassword(
            f"{self.workload.compound_name}-db-grafana-pw",
            special=True,
            override_special="-_",
            length=36,
            opts=pulumi.ResourceOptions(parent=self),
        )

        role = database = f"grafana-{self.workload.compound_name}"

        grafana_role = pulumi_postgresql.Role(
            f"{self.workload.compound_name}-grafana-role",
            login=True,
            name=role,
            password=self.db_grafana_pw.result,
            opts=pulumi.ResourceOptions(
                provider=self.postgres,
                parent=self,
            ),
        )

        self.grafana_db = pulumi_postgresql.Database(
            f"{self.workload.compound_name}-grafana-db",
            name=database,
            owner=role,
            opts=pulumi.ResourceOptions(
                provider=self.postgres,
                parent=self,
                depends_on=[grafana_role],
                protect=True,
            ),
        )

        self.grant = pulumi_postgresql.Grant(
            f"{self.workload.compound_name}-grafana-grant",
            database=database,
            role=role,
            schema="public",
            object_type="schema",
            privileges=[
                "CREATE",
                "USAGE",
            ],
            opts=pulumi.ResourceOptions(
                provider=self.postgres,
                parent=self,
                depends_on=[self.grafana_db, grafana_role],
            ),
        )

    def _define_extra_dbs(self) -> None:
        for dbn in self.workload.cfg.extra_postgres_dbs:
            db_name = dbn.replace("-", "_")

            pw = pulumi_random.RandomPassword(
                f"{self.workload.compound_name}-db-{db_name}-pw",
                special=True,
                override_special="-_",
                length=36,
                opts=pulumi.ResourceOptions(parent=self),
            )

            self.extra_db_pws[db_name] = pw

            role = pulumi_postgresql.Role(
                f"{self.workload.compound_name}-{db_name}-role",
                login=True,
                name=db_name,
                password=pw.result,
                opts=pulumi.ResourceOptions(
                    provider=self.postgres,
                    parent=self,
                ),
            )

            db = pulumi_postgresql.Database(
                f"{self.workload.compound_name}-{db_name}-db",
                name=db_name,
                owner=role,
                opts=pulumi.ResourceOptions(
                    provider=self.postgres,
                    parent=self,
                    depends_on=[role],
                    protect=True,
                ),
            )

            pulumi_postgresql.Grant(
                f"{self.workload.compound_name}-{db_name}-grant",
                database=db_name,
                role=role,
                schema="public",
                object_type="schema",
                privileges=["CREATE", "USAGE"],
                opts=pulumi.ResourceOptions(
                    provider=self.postgres,
                    parent=self,
                    depends_on=[db, role],
                ),
            )
