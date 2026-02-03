from __future__ import annotations

import ipaddress
import typing

import pulumi
import pulumi_aws as aws

import ptd
import ptd.aws_accounts
import ptd.aws_control_room
import ptd.aws_workload
import ptd.junkdrawer
import ptd.pulumi_resources.aws_eks_cluster
import ptd.pulumi_resources.aws_tailscale
import ptd.pulumi_resources.aws_vpc
import ptd.pulumi_resources.traefik


class AWSControlRoomPersistent(pulumi.ComponentResource):
    required_tags: dict[str, str]
    cidr_block: ipaddress.IPv4Network
    control_room: ptd.aws_control_room.AWSControlRoom
    name: str

    vpc: ptd.pulumi_resources.aws_vpc.AWSVpc
    private_subnet_ids: list[pulumi.Output[str]] | list[str]

    db: aws.rds.Instance
    releases_bucket: aws.s3.Bucket

    @classmethod
    def autoload(cls) -> AWSControlRoomPersistent:
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

        second_octet = ptd.junkdrawer.octet_signature(self.name)

        self.cidr_block = typing.cast(ipaddress.IPv4Network, ipaddress.ip_network(f"10.{second_octet}.0.0/16"))

        self._define_vpc()
        self._define_tailscale()
        self._define_db()
        self._define_releases_bucket()

        outputs: dict[str, typing.Any] = {
            "db": self.db.identifier,
            "db_address": self.db.address.apply(str),
            "db_secret_arn": self.db_secret_arn,
            "db_host": self.db.endpoint,
            "nat_gw_public_ips": self.vpc.nat_gw_public_ips,
            "vpc_name": self.vpc.name,
            "vpc_id": self.vpc.vpc.id,
            "subnet_ids": self.private_subnet_ids,
            "releases_bucket": self.releases_bucket.bucket,
            "releases_bucket_arn": self.releases_bucket.arn,
        }

        for key, value in outputs.items():
            pulumi.export(key, value)

        self.register_outputs(outputs)

    def _define_vpc(self):
        azs = aws.get_availability_zones()

        self.vpc = ptd.pulumi_resources.aws_vpc.AWSVpc(
            name=self.name,
            cidr_block=str(self.cidr_block),
            azs=list(azs.zone_ids)[:3],
            network_access_tags={
                "public": {
                    "kubernetes.io/role/elb": "1",
                    str(ptd.TagKeys.POSIT_TEAM_NETWORK_ACCESS): "public",
                },
                "private": {
                    str(ptd.TagKeys.POSIT_TEAM_NETWORK_ACCESS): "private",
                },
            },
            tags=self.required_tags
            | {
                "Name": self.name,
                f"kubernetes.io/cluster/{self.name}": "shared",
            },
            opts=pulumi.ResourceOptions(parent=self),
        )

        self.vpc.with_nat_gateways()
        self.vpc.with_nacl_rule(port_range=443, cidr_blocks=["0.0.0.0/0"])
        self.vpc.with_nacl_rule(port_range=80, cidr_blocks=["0.0.0.0/0"])
        self.vpc.with_nacl_rule(egress=True, port_range=0, protocol="-1", cidr_blocks=["0.0.0.0/0"])
        self.vpc.with_nacl_rule(
            egress=True,
            port_range=0,
            protocol="-1",
            cidr_blocks=["0.0.0.0/0"],
            privacy="private",
        )
        self.vpc.with_nacl_rule(
            port_range=range(65536),
            protocol="tcp",
            cidr_blocks=[self.cidr_block.with_prefixlen],
        )
        self.vpc.with_nacl_rule(
            port_range=range(65536),
            protocol="udp",
            cidr_blocks=[self.cidr_block.with_prefixlen],
        )
        self.vpc.with_nacl_rule(
            port_range=range(65536),
            protocol="tcp",
            cidr_blocks=[self.cidr_block.with_prefixlen],
            privacy="private",
        )
        self.vpc.with_nacl_rule(
            port_range=range(65536),
            protocol="udp",
            cidr_blocks=[self.cidr_block.with_prefixlen],
            privacy="private",
        )
        self.vpc.with_secure_default_security_group()
        self.vpc.with_secure_default_nacl()

        for service in (
            "ec2",
            "ec2messages",
            "kms",
            "s3",
            "ssm",
            "ssmmessages",
        ):
            self.vpc.with_endpoint(service=service)

        self.vpc.with_flow_log()

        self.private_subnet_ids = [s.id.apply(str) for s in self.vpc.subnets["private"]]

    def _define_db(self):
        dbsg = aws.ec2.SecurityGroup(
            f"{self.name}-allow-postgresql-traffic-vpc",
            description=f"Allow PostgreSQL traffic from VPC for {self.name}",
            vpc_id=self.vpc.vpc.id,
            ingress=[
                aws.ec2.SecurityGroupIngressArgs(
                    description="Allow PostgreSQL traffic on port 5432",
                    from_port=5432,
                    to_port=5432,
                    protocol="tcp",
                    cidr_blocks=[str(self.cidr_block)],
                ),
            ],
            egress=[
                aws.ec2.SecurityGroupEgressArgs(
                    from_port=0,
                    to_port=0,
                    protocol="-1",
                    cidr_blocks=[str(self.cidr_block)],
                )
            ],
            tags=self.required_tags | {"Name": f"{self.name}-allow-postgresql-traffic-vpc"},
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        dbsng = aws.rds.SubnetGroup(
            f"{self.name}-main-database-subnet-group",
            subnet_ids=self.private_subnet_ids,
            tags=self.required_tags | {"Name": f"{self.name}-main-database-subnet-group"},
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        dbpg = aws.rds.ParameterGroup(
            f"{self.name}-main-database-parameter-group",
            family="postgres16",
            parameters=[
                aws.rds.ParameterGroupParameterArgs(
                    name="auto_explain.log_min_duration",
                    value="5000",
                ),
                aws.rds.ParameterGroupParameterArgs(
                    name="log_min_duration_statement",
                    value="1500",
                ),
                aws.rds.ParameterGroupParameterArgs(
                    name="log_lock_waits",
                    value="1",
                ),
            ],
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        self.db = aws.rds.Instance(
            self.name,
            identifier_prefix=f"{self.name}-",
            allocated_storage=self.control_room.cfg.db_allocated_storage,
            backup_retention_period=7,
            copy_tags_to_snapshot=True,
            db_name="postgres",
            db_subnet_group_name=dbsng.name,
            engine="postgres",
            engine_version=self.control_room.cfg.db_engine_version,
            final_snapshot_identifier=f"{self.name}-final-snapshot",
            instance_class=self.control_room.cfg.db_instance_class,
            parameter_group_name=dbpg.name,
            manage_master_user_password=True,
            port=5432,
            skip_final_snapshot=(not self.control_room.cfg.protect_persistent_resources),
            storage_encrypted=True,
            storage_type=aws.rds.StorageType.GP3,
            tags=self.required_tags | {"Name": self.name},
            username="postgres",
            vpc_security_group_ids=[dbsg.id],
            opts=pulumi.ResourceOptions(
                ignore_changes=["identifier_prefix"],
                parent=self.vpc,
                protect=self.control_room.cfg.protect_persistent_resources,
            ),
        )

        secret = self.db.master_user_secrets.apply(lambda secrets: secrets[0])
        self.db_secret_arn = secret.apply(lambda s: s.secret_arn)

    def _define_releases_bucket(self):
        """Define an S3 bucket for storing customer release artifacts with signed URL access."""
        self.releases_bucket = aws.s3.Bucket(
            f"{self.name}-releases",
            aws.s3.BucketArgs(
                bucket=f"{self.name}-releases",
                tags=self.required_tags | {"Name": f"{self.name}-releases"},
                server_side_encryption_configuration=aws.s3.BucketServerSideEncryptionConfigurationArgs(
                    rule=aws.s3.BucketServerSideEncryptionConfigurationRuleArgs(
                        apply_server_side_encryption_by_default=aws.s3.BucketServerSideEncryptionConfigurationRuleApplyServerSideEncryptionByDefaultArgs(
                            sse_algorithm="aws:kms",
                        ),
                        bucket_key_enabled=True,
                    ),
                ),
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Block all public access by default - access will be via signed URLs
        aws.s3.BucketPublicAccessBlock(
            f"{self.name}-releases-public-access-block",
            bucket=self.releases_bucket.id,
            block_public_acls=True,
            block_public_policy=True,
            ignore_public_acls=True,
            restrict_public_buckets=True,
            opts=pulumi.ResourceOptions(parent=self.releases_bucket),
        )

        # Enable versioning for releases
        aws.s3.BucketVersioningV2(
            f"{self.name}-releases-versioning",
            bucket=self.releases_bucket.id,
            versioning_configuration=aws.s3.BucketVersioningV2VersioningConfigurationArgs(
                status="Enabled",
            ),
            opts=pulumi.ResourceOptions(parent=self.releases_bucket),
        )

    def _define_tailscale(self):
        self.tailscale = ptd.pulumi_resources.aws_tailscale.SubnetRouter(
            vpc=self.vpc,
            tags=self.required_tags | {"rs:project": "security", "rs:subsystem": "tailscale"},
            ts_extra_args="--advertise-tags=tag:ptd",
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )
