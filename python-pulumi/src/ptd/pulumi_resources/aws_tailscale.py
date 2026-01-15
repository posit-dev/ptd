import json

import pulumi
import pulumi_aws as aws
import pulumi_tailscale as tailscale

from ptd.pulumi_resources.aws_vpc import AWSVpc


class SubnetRouter(pulumi.ComponentResource):
    def __init__(
        self,
        vpc: AWSVpc,
        tags: dict[str, str],
        version: str = "stable",
        cpu: str = "256",
        memory: str = "512",
        site_id: int | None = None,
        ts_extra_args: str | None = None,
        permissions_boundary: str | None = None,
        opts: pulumi.ResourceOptions | None = None,
    ):
        """
        Tailscale AWS Fargate class

        Primarily lifted from https://github.com/rstudio/rstudio-pulumi/blob/main/src/rstudio_pulumi/tailscale/fargate.py

        Create an ECS Fargate cluster, an ECS Task Definition, and an ECS Service in a given VPC in which to run
        the Tailscale subnet router.

        If the auth key is an OAuth key with a tag and the auth_key scope, then passing ts_extra_args with
        '--advertise-tags=tag:<tag>' is required in order to register the subnet router.
        https://tailscale.com/kb/1282/docker#ts_authkey

        It is possible to debug the container using ECS Exec.

        https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-exec.html#ecs-exec-enabling-and-using

        aws ecs execute-command --cluster cluster-name --task task-id --container tailscale --interactive --command "/bin/sh"

        :param vpc: rstudio-pulumi VPC object
        :param tags: Tags to apply to resources
        :param version: Tailscale version to use
        :param cpu: CPU units for the Fargate task
        :param memory: Memory for the Fargate task
        :param site_id: Site ID to be used for Tailscale 4via6 subnet routing. See https://tailscale.com/kb/1201/4via6-subnets?q=4via6
        :param permissions_boundary: Permission boundary to use for the IAM roles
        :param ts_extra_args: Extra arguments to pass to the Tailscale subnet router as TS_EXTRA_ARGS
        :param opts:
        """
        self.name = f"{vpc.name}-tailscale"

        if opts is None:
            opts = pulumi.ResourceOptions()

        super().__init__("rstudio:tailscale/Fargate", self.name, None, opts)

        self.sg = aws.ec2.SecurityGroup(
            f"{self.name}-sg",
            aws.ec2.SecurityGroupArgs(
                name=self.name,
                description="Tailscale Fargate Security Group",
                vpc_id=vpc.vpc.id,
                tags={"Name": self.name},
            ),
            opts=opts.merge(pulumi.ResourceOptions(parent=self)),
        )
        aws.vpc.SecurityGroupEgressRule(
            f"{self.name}-sg-egress",
            aws.vpc.SecurityGroupEgressRuleArgs(security_group_id=self.sg.id, cidr_ipv4="0.0.0.0/0", ip_protocol="-1"),
            opts=opts.merge(pulumi.ResourceOptions(parent=self)),
        )

        self.cluster = aws.ecs.Cluster(
            f"{self.name}-fargate",
            aws.ecs.ClusterArgs(name=self.name, tags=tags),
            opts=opts.merge(pulumi.ResourceOptions(parent=self)),
        )

        tailscale_secret = aws.secretsmanager.get_secret(name="tailscale-authkey")

        self.log_group_name = f"/aws/ecs/{self.name}"
        self.log_group = aws.cloudwatch.LogGroup(
            f"{self.name}-log-group",
            aws.cloudwatch.LogGroupArgs(name=self.log_group_name, retention_in_days=60, tags=tags),
            opts=opts.merge(pulumi.ResourceOptions(parent=self)),
        )

        account_id = aws.get_caller_identity().account_id
        region = aws.get_region(opts=pulumi.InvokeOptions(provider=opts.provider)).name
        self.ssm_parameter_arn = f"arn:aws:ssm:{region}:{account_id}:parameter/{self.name}/ts-state"

        container_definitions = pulumi.Output.all(cidr_block=vpc.vpc.cidr_block).apply(
            lambda args: json.dumps(
                [
                    {
                        "name": "tailscale",
                        "image": f"tailscale/tailscale:{version}",
                        "essential": True,
                        "environment": [
                            {"name": "TS_HOSTNAME", "value": f"{vpc.name}-{region}-{account_id}"},
                            {
                                "name": "TS_ROUTES",
                                "value": args["cidr_block"]
                                if site_id is None
                                else tailscale.get4_via6(site=site_id, cidr=args["cidr_block"]).ipv6,
                            },
                            {"name": "TS_EXTRA_ARGS", "value": ts_extra_args if ts_extra_args else ""},
                        ],
                        "secrets": [{"name": "TS_AUTHKEY", "valueFrom": tailscale_secret.arn}],
                        "logConfiguration": {
                            "logDriver": "awslogs",
                            "options": {
                                "awslogs-create-group": "true",
                                "awslogs-group": self.log_group_name,
                                "awslogs-region": region,
                                "awslogs-stream-prefix": self.name,
                                "mode": "non-blocking",
                                "max-buffer-size": "25m",
                            },
                        },
                        "healthcheck": {
                            "command": ["tailscale", "status"],
                            "interval": 30,
                            "timeout": 5,
                            "retries": 3,
                            "startPeriod": 0,
                        },
                    }
                ]
            )
        )

        self.permissions_boundary = permissions_boundary
        self.execution_role = self._create_execution_role(tailscale_secret, opts)
        self.task_role = self._create_task_role(opts)

        self.task = aws.ecs.TaskDefinition(
            f"{self.name}-task",
            aws.ecs.TaskDefinitionArgs(
                family=self.name,
                requires_compatibilities=["FARGATE"],
                network_mode="awsvpc",
                runtime_platform=aws.ecs.TaskDefinitionRuntimePlatformArgs(
                    cpu_architecture="ARM64", operating_system_family="LINUX"
                ),
                cpu=cpu,
                memory=memory,
                container_definitions=container_definitions,
                execution_role_arn=self.execution_role.arn,
                task_role_arn=self.task_role.arn,
                tags=tags,
            ),
            opts=opts.merge(pulumi.ResourceOptions(parent=self)),
        )

        self.service = aws.ecs.Service(
            f"{self.name}-service",
            aws.ecs.ServiceArgs(
                name=self.name,
                cluster=self.cluster.arn,
                task_definition=self.task.arn,
                launch_type="FARGATE",
                desired_count=1,
                enable_ecs_managed_tags=True,
                enable_execute_command=True,  # This should make it easier to debug
                wait_for_steady_state=True,
                network_configuration=aws.ecs.ServiceNetworkConfigurationArgs(
                    # This will violate a Security Hub finding, but is necessary for Tailscale to perform optimally
                    assign_public_ip=True,
                    subnets=vpc.subnets["public"],
                    security_groups=[self.sg.id],
                ),
                propagate_tags="SERVICE",
                tags=tags,
            ),
            opts=opts.merge(pulumi.ResourceOptions(parent=self)),
        )

    def _create_execution_role(self, tailscale_secret, opts):
        assume_role_policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["sts:AssumeRole"],
                    principals=[
                        aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                            type="Service", identifiers=["ecs-tasks.amazonaws.com"]
                        )
                    ],
                )
            ]
        )

        policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["secretsmanager:GetSecretValue"], resources=[tailscale_secret.arn]
                )
            ]
        ).json

        return aws.iam.Role(
            f"{self.name}-ecs-task-execution-role.posit.team",
            aws.iam.RoleArgs(
                name=f"{self.name}-TaskExecution.posit.team",
                description=f"Role for {self.name} Fargate Task Execution",
                assume_role_policy=assume_role_policy.json,
                permissions_boundary=self.permissions_boundary,
                managed_policy_arns=["arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"],
                inline_policies=[aws.iam.RoleInlinePolicyArgs(name="tailscale-secrets-access", policy=policy)],
            ),
            opts=opts.merge(pulumi.ResourceOptions(parent=self)),
        )

    def _create_task_role(self, opts):
        assume_role_policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["sts:AssumeRole"],
                    principals=[
                        aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                            type="Service", identifiers=["ecs-tasks.amazonaws.com"]
                        )
                    ],
                )
            ]
        )

        policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["ssm:GetParameter", "ssm:PutParameter"], resources=[self.ssm_parameter_arn]
                )
            ]
        ).json

        return aws.iam.Role(
            f"{self.name}-ecs-task-role.posit.team",
            aws.iam.RoleArgs(
                name=f"{self.name}-Task.posit.team",
                description=f"Role for {self.name} Fargate Task",
                assume_role_policy=assume_role_policy.json,
                permissions_boundary=self.permissions_boundary,
                managed_policy_arns=["arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"],
                inline_policies=[aws.iam.RoleInlinePolicyArgs(name="tailscale-ssm-parameter-access", policy=policy)],
            ),
            opts=opts.merge(pulumi.ResourceOptions(parent=self)),
        )
