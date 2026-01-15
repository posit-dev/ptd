import typing
from ipaddress import IPv4Network

import pulumi
import pulumi_aws as aws

import ptd


class AwsBastion(pulumi.ComponentResource):
    tags: dict[str, str]
    name: str
    vpc_id: str | pulumi.Output[str]
    egress_cidr: IPv4Network
    subnet_id: str | pulumi.Output[str]

    bastion_role: aws.iam.Role
    bastion: aws.ec2.Instance
    prev_parent: typing.Any

    def __init__(
        self,
        name: str,
        vpc_id: str | pulumi.Output[str],
        subnet_id: str | pulumi.Output[str],
        instance_type: str | pulumi.Output[str],
        tags: dict[str, str],
        permissions_boundary: str | None = None,
        prev_parent: typing.Any = None,
        *args,
        **kwargs,
    ):
        # TODO: temporary alias for backwards compat
        kwargs["opts"] = pulumi.ResourceOptions.merge(
            kwargs["opts"],
            pulumi.ResourceOptions(aliases=[pulumi.Alias(name=name, type_="ptd:Bastion")]),
        )
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{name}",
            *args,
            **kwargs,
        )

        self.tags = tags
        self.name = name
        self.vpc_id = vpc_id
        self.subnet_id = subnet_id
        self.instance_type = instance_type
        self.permissions_boundary = permissions_boundary
        self.prev_parent = prev_parent

        self._define_iam()
        self._define_instance()

    def _define_iam(self):
        assume_role_policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["sts:AssumeRole"],
                    principals=[
                        aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                            type="Service",
                            identifiers=["ec2.amazonaws.com"],
                        )
                    ],
                )
            ]
        )

        self.bastion_role = aws.iam.Role(
            f"{self.name}-{ptd.Roles.BASTION}",
            name=f"{self.name}-{ptd.Roles.BASTION}",
            assume_role_policy=assume_role_policy.json,
            permissions_boundary=self.permissions_boundary,
            tags=self.tags,
            opts=pulumi.ResourceOptions(
                parent=self,
                delete_before_replace=True,
                aliases=[pulumi.Alias(parent=self.prev_parent)],
            ),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.name}-bastion-ssm",
            role=self.bastion_role.name,
            policy_arn="arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
            opts=pulumi.ResourceOptions(parent=self.bastion_role, delete_before_replace=True),
        )

    def _define_instance(self):
        sg = aws.ec2.SecurityGroup(
            f"{self.name}-bastion",
            vpc_id=self.vpc_id,
            egress=[
                aws.ec2.SecurityGroupEgressArgs(
                    from_port=0,
                    to_port=0,
                    protocol="-1",
                    cidr_blocks=["0.0.0.0/0"],
                )
            ],
            tags=self.tags | {"Name": f"{self.name}-bastion"},
            opts=pulumi.ResourceOptions(parent=self),
        )
        profile = aws.iam.InstanceProfile(
            f"{self.name}-bastion-profile",
            name=f"{self.name}-bastion-profile.posit.team",
            role=self.bastion_role.name,
            opts=pulumi.ResourceOptions(parent=self, delete_before_replace=True),
        )
        ami = aws.ec2.get_ami(
            most_recent=True,
            name_regex="al2023-ami-202*",
            filters=[
                aws.ec2.GetAmiFilterArgs(
                    name="owner-id",
                    values=[ptd.AMAZON_ACCOUNT_ID],
                ),
                aws.ec2.GetAmiFilterArgs(
                    name="architecture",
                    values=["arm64"],
                ),
            ],
        )

        self.bastion = aws.ec2.Instance(
            f"{self.name}-bastion",
            aws.ec2.InstanceArgs(
                iam_instance_profile=profile.name,
                instance_type=self.instance_type,
                ami=ami.id,
                subnet_id=self.subnet_id,
                vpc_security_group_ids=[sg.id],
                tags=self.tags | {"Name": f"{self.name}-bastion"},
            ),
            opts=pulumi.ResourceOptions(parent=self, depends_on=[profile]),
        )
