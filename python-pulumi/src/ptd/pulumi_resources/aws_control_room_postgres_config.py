import json

import pulumi
import pulumi_aws as aws
import pulumi_postgresql
import pulumi_random

import ptd.aws_control_room


class AWSControlRoomPostgresConfig(pulumi.ComponentResource):
    control_room: ptd.aws_control_room.AWSControlRoom
    grafana_db: pulumi_postgresql.Database
    grant: pulumi_postgresql.Grant
    db_host_output: pulumi.Output

    @classmethod
    def autoload(cls) -> "AWSControlRoomPostgresConfig":
        return cls(ptd.aws_control_room.AWSControlRoom(pulumi.get_stack()))

    def __init__(self, control_room: ptd.aws_control_room.AWSControlRoom, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            control_room.compound_name,
            *args,
            **kwargs,
        )
        self.control_room = control_room
        self.name = self.control_room.compound_name
        self.required_tags = self.control_room.required_tags | {str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__}

        self._define_grafana()

        outputs = {
            "db_grafana_connection": pulumi.Output.format(
                "postgres://grafana:{0}@{1}/grafana",
                self.db_grafana_pw.result,
                self.db_host_output,
            ),
            "db_grafana_pw": self.db_grafana_pw.result,
            "grafana_db_name": self.grafana_db.name,
            "grant_id": self.grant.id,
        }

        for key, value in outputs.items():
            pulumi.export(key, value)

        self.register_outputs(outputs)

    def _define_grafana(self) -> None:
        persistent_stack = pulumi.StackReference(
            f"organization/ptd-aws-control-room-persistent/{self.control_room.compound_name}"
        )
        self.db_host_output = persistent_stack.require_output("db_address")
        db_port = 5432  # probably shouldn't hardcode this.
        db_secret_arn_output = persistent_stack.require_output("db_secret_arn")
        secret_version = aws.secretsmanager.get_secret_version(secret_id=db_secret_arn_output.apply(lambda x: x))

        pw = json.loads(secret_version.secret_string).get("password", "")

        self.db_grafana_pw = pulumi_random.RandomPassword(
            f"{self.name}-db-grafana-pw",
            special=True,
            override_special="-_",
            length=36,
            opts=pulumi.ResourceOptions(parent=self),
        )

        postgres = pulumi_postgresql.provider.Provider(
            f"{self.name}-postgres-provider",
            host=self.db_host_output.apply(lambda x: x),
            port=db_port,
            sslmode="require",
            username="postgres",
            password=pw,
            superuser=False,
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        grafana_role = pulumi_postgresql.Role(
            f"{self.name}-grafana-role",
            login=True,
            name="grafana",
            password=self.db_grafana_pw.result,
            opts=pulumi.ResourceOptions(
                provider=postgres,
                parent=self,
            ),
        )

        self.grafana_db = pulumi_postgresql.Database(
            f"{self.name}-grafana-db",
            name="grafana",
            owner="grafana",
            opts=pulumi.ResourceOptions(
                provider=postgres,
                parent=self,
                depends_on=[grafana_role],
            ),
        )

        self.grant = pulumi_postgresql.Grant(
            f"{self.name}-grafana-grant",
            database="grafana",
            role="grafana",
            schema="public",
            object_type="schema",
            privileges=[
                "CREATE",
                "USAGE",
            ],
            opts=pulumi.ResourceOptions(
                provider=postgres,
                parent=self,
                depends_on=[self.grafana_db, grafana_role],
                protect=False,
            ),
        )
