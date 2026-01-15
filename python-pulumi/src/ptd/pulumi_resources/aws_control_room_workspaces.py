from __future__ import annotations

import ipaddress

import pulumi
import pulumi_aws as aws
import pulumi_random as random

import ptd
import ptd.aws_control_room
import ptd.aws_workload
import ptd.junkdrawer
import ptd.pulumi_resources.aws_bastion
import ptd.pulumi_resources.aws_directoryservice
import ptd.pulumi_resources.aws_directoryservicedata
import ptd.pulumi_resources.aws_eks_cluster
import ptd.pulumi_resources.aws_vpc
import ptd.pulumi_resources.traefik


class AWSControlRoomWorkspaces(pulumi.ComponentResource):
    required_tags: dict[str, str]
    cidr_block: ipaddress.IPv4Network
    control_room: ptd.aws_control_room.AWSControlRoom
    name: str

    vpc: ptd.pulumi_resources.aws_vpc.AWSVpc
    private_subnet_ids: list[pulumi.Output[str]] | list[str]

    @classmethod
    def autoload(cls) -> AWSControlRoomWorkspaces:
        return cls(ptd.aws_control_room.AWSControlRoom(pulumi.get_stack()))

    def __init__(self, control_room: ptd.aws_control_room.AWSControlRoom, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            control_room.compound_name,
            *args,
            **kwargs,
        )

        self.control_room = control_room
        self.name = self.control_room.compound_name + "-workspaces"

        self.required_tags = self.control_room.required_tags | {str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__}

        self.use1_provider = aws.Provider("use1", region="us-east-1")

        # ensure we use a CIDR block that is not overlapping with aws_control_room_persistent 10.x.x.x/16
        # in case we ever want to peer the VPCs
        self.cidr_block = ipaddress.IPv4Network("172.16.0.0/20")

        self._define_vpc()
        self._define_workspaces()

    def _define_vpc(self):
        self.vpc = ptd.pulumi_resources.aws_vpc.AWSVpc(
            name=self.name,
            cidr_block=str(self.cidr_block),
            azs=["use1-az4", "use1-az6"],
            network_access_tags={
                "public": {
                    str(ptd.TagKeys.POSIT_TEAM_NETWORK_ACCESS): "public",
                },
                "private": {
                    str(ptd.TagKeys.POSIT_TEAM_NETWORK_ACCESS): "private",
                },
            },
            tags=self.required_tags
            | {
                "Name": self.name,
            },
            opts=pulumi.ResourceOptions(parent=self, provider=self.use1_provider),
        )

        self.vpc.with_secure_default_security_group()
        self.vpc.with_secure_default_nacl()
        self.vpc.with_nacl_rule(port_range=0, cidr_blocks=[str(self.cidr_block)], protocol="all")
        self.vpc.with_nacl_rule(port_range=0, cidr_blocks=[str(self.cidr_block)], protocol="all", egress=True)
        self.vpc.with_nacl_rule(port_range=22, cidr_blocks=["0.0.0.0/0"], protocol="tcp", egress=True)
        self.vpc.with_endpoint("sts")

        # allow outbound non-https traffic to the internet
        # TODO: shift workspaces apt archives to use https-only
        self.vpc.with_nacl_rule(port_range=80, cidr_blocks=["0.0.0.0/0"], protocol="tcp", egress=True)

        aws.ec2.VpcDhcpOptions(
            f"{self.name}-resolver",
            domain_name="ec2.internal",
            domain_name_servers=["AmazonProvidedDNS"],
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self.vpc, provider=self.use1_provider),
        )

    def _define_workspaces(self):
        assume_role_policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["sts:AssumeRole"],
                    principals=[
                        aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                            type="Service",
                            identifiers=["workspaces.amazonaws.com"],
                        )
                    ],
                )
            ]
        )

        default_role = aws.iam.Role(
            f"{self.name}-default-role",
            # NOTE: This role name is special and must be exactly this value.
            # See https://docs.aws.amazon.com/workspaces/latest/adminguide/workspaces-access-control.html#create-default-role
            name="workspaces_DefaultRole",
            assume_role_policy=assume_role_policy.json,
            max_session_duration=3600,
            force_detach_policies=False,
            path="/",
            description="",
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self, provider=self.use1_provider),
        )

        aws.iam.RolePolicy(
            f"{self.name}-default-role-policy-skylight-self",
            role=default_role.id,
            name="SkyLightSelfServiceAccess",
            policy=aws.iam.get_policy_document(
                statements=[
                    aws.iam.GetPolicyDocumentStatementArgs(
                        actions=[
                            "ec2:CreateNetworkInterface",
                            "ec2:DeleteNetworkInterface",
                            "ec2:DescribeNetworkInterfaces",
                            "galaxy:DescribeDomains",
                            "workspaces:RebootWorkspaces",
                            "workspaces:RebuildWorkspaces",
                            "workspaces:ModifyWorkspaceProperties",
                        ],
                        effect="Allow",
                        resources=["*"],
                    ),
                ],
            ).json,
        )

        workspaces_ad_password = random.RandomPassword(
            f"{self.name}-ad-password",
            length=31,
            special=True,
            override_special="!#^*-_",
            opts=pulumi.ResourceOptions(parent=self),
        )

        subnets = self.vpc.subnets["public"][:2]

        self.ad = aws.directoryservice.Directory(
            f"{self.name}-ad",
            name="corp.amazonworkspaces.com",
            password=workspaces_ad_password.result,
            edition="Standard",
            type="MicrosoftAD",
            short_name="corp",
            size="Small",
            vpc_settings=aws.directoryservice.DirectoryVpcSettingsArgs(
                vpc_id=self.vpc.vpc.id,
                subnet_ids=[s.id for s in subnets],
            ),
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self, provider=self.use1_provider),
        )

        data_access_enabled = ptd.pulumi_resources.aws_directoryservice.DirectoryServiceDirectoryDataAccessEnabled(
            f"{self.name}-ad-data-access-enabled",
            ptd.pulumi_resources.aws_directoryservice.DirectoryServiceDirectoryDataAccessEnabledInputs(
                directory_id=self.ad.id,
                region_name=self.use1_provider.region,
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

        ptd_users_ip_rules = [
            aws.workspaces.IpGroupRuleArgs(source=f"{ip.ip}/32", description=f"{u.given_name} - {ip.comment}")
            for u in self.control_room.cfg.trusted_users
            for ip in u.ip_addresses
        ]

        ptd_users_ip_group = aws.workspaces.IpGroup(
            f"{self.name}-ptd-users-ip-group",
            name="ptd-users",
            description="PTD trusted users",
            rules=ptd_users_ip_rules,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self, provider=self.use1_provider),
        )

        workspaces_directory = aws.workspaces.Directory(
            f"{self.name}-workspaces-directory",
            directory_id=self.ad.id,
            ip_group_ids=[ptd_users_ip_group.id],
            subnet_ids=[s.id for s in subnets],
            self_service_permissions=aws.workspaces.DirectorySelfServicePermissionsArgs(
                change_compute_type=True,
                increase_volume_size=True,
                rebuild_workspace=True,
                restart_workspace=True,
                switch_running_mode=True,
            ),
            workspace_access_properties=aws.workspaces.DirectoryWorkspaceAccessPropertiesArgs(
                device_type_android="ALLOW",
                device_type_chromeos="ALLOW",
                device_type_ios="ALLOW",
                device_type_linux="ALLOW",
                device_type_osx="ALLOW",
                device_type_web="ALLOW",
                device_type_windows="ALLOW",
                device_type_zeroclient="ALLOW",
            ),
            workspace_creation_properties=aws.workspaces.DirectoryWorkspaceCreationPropertiesArgs(
                enable_internet_access=True,
                enable_maintenance_mode=True,
                user_enabled_as_local_administrator=True,
            ),
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self, provider=self.use1_provider),
        )

        workspaces_bundle = aws.workspaces.get_bundle(
            name="Power with Ubuntu 22.04",
            owner="AMAZON",
            opts=pulumi.InvokeOptions(provider=self.use1_provider),
        )

        workspaces_kms_key = aws.kms.get_key(
            key_id="alias/aws/workspaces",
            opts=pulumi.InvokeOptions(provider=self.use1_provider),
        )

        for u in self.control_room.cfg.trusted_users:
            account_name = u.given_name.lower()  # how long until we lose our unique first name team?
            user = ptd.pulumi_resources.aws_directoryservicedata.DirectoryServiceDataUser(
                f"{self.name}-ad-user-{account_name}",
                ptd.pulumi_resources.aws_directoryservicedata.DirectoryServiceDataUserInputs(
                    directory_id=self.ad.id,
                    region_name=self.use1_provider.region,
                    email_address=u.email,
                    given_name=u.given_name,
                    surname=u.family_name,
                    sam_account_name=account_name,
                ),
                opts=pulumi.ResourceOptions(parent=self, depends_on=[data_access_enabled]),
            )

            aws.workspaces.Workspace(
                f"{self.name}-workspaces-workspace-{account_name}",
                directory_id=workspaces_directory.id,
                bundle_id=workspaces_bundle.id,
                user_name=account_name,
                root_volume_encryption_enabled=True,
                user_volume_encryption_enabled=True,
                volume_encryption_key=workspaces_kms_key.arn,
                workspace_properties=aws.workspaces.WorkspaceWorkspacePropertiesArgs(
                    compute_type_name="POWER",
                    user_volume_size_gib=500,
                    root_volume_size_gib=200,
                    running_mode="AUTO_STOP",
                    running_mode_auto_stop_timeout_in_minutes=60,
                ),
                tags=self.required_tags | {"rs:owner": u.email},
                opts=pulumi.ResourceOptions(parent=self, provider=self.use1_provider, depends_on=[user]),
            )
