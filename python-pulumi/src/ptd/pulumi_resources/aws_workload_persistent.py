import ipaddress
import json
import typing

import pulumi
import pulumi_aws as aws
import pulumi_random

import ptd
import ptd.aws_iam
import ptd.aws_workload
import ptd.paths
import ptd.pulumi_resources.aws_bastion
import ptd.pulumi_resources.aws_bucket
import ptd.pulumi_resources.aws_fsx_openzfs_multi
import ptd.pulumi_resources.aws_tailscale
import ptd.pulumi_resources.aws_vpc
import ptd.secrecy

# AWS VPC ID format constants
# This is due to an annoying linter error that doesn't allow generating some values on the fly
# PLR2004: https://docs.astral.sh/ruff/rules/magic-value-comparison/
AWS_VPC_ID_PREFIX = "vpc-"
AWS_VPC_ID_LENGTH = 21


class InternalSiteConfig:
    zone: aws.route53.Zone | None
    zone_id: str | None | pulumi.Output[str]
    domain: str

    def __init__(self, domain: str, zone_id: str | None) -> None:
        self.zone = None
        self.domain = domain
        self.zone_id = zone_id


class AWSWorkloadPersistent(pulumi.ComponentResource):
    workload: ptd.aws_workload.AWSWorkload
    vpc_net: ipaddress.IPv4Network
    required_tags: dict[str, str]
    managed_clusters: list[dict[str, typing.Any]]
    federated_endpoints: dict[str, list[str]]
    oidc_urls: list[str]

    vpc: ptd.pulumi_resources.aws_vpc.AWSVpc | None
    vpc_id: str
    private_subnet_ids: list[pulumi.Output[str]] | list[str]
    ecrs: dict[str, aws.ecr.Repository | None]
    ecr_lifecycle_policies: dict[str, aws.ecr.LifecyclePolicy | None]

    db: aws.rds.Instance

    packagemanager_bucket: aws.s3.Bucket

    chronicle_bucket: aws.s3.Bucket

    loki_bucket: aws.s3.Bucket
    loki_bucket_policy: aws.iam.Policy
    loki_role: aws.iam.Role

    mimir_bucket: aws.s3.Bucket
    mimir_bucket_policy: aws.iam.Policy
    mimir_role: aws.iam.Role

    team_operator_policy: aws.iam.Policy

    domain_certs: dict[str, aws.acm.Certificate]
    cert_arns: list[pulumi.Output[str]]
    cert_validation_records: dict[str, list[aws.route53.Record]]  # Track validation records for each domain

    fsx_openzfs_fs: aws.fsx.OpenZfsFileSystem | ptd.pulumi_resources.aws_fsx_openzfs_multi.AWSFsxOpenZfsMulti
    fsx_openzfs_role: aws.iam.Role
    fsx_openzfs_sg: aws.ec2.SecurityGroup

    fsx_nfs_sg: aws.ec2.SecurityGroup

    efs_nfs_sg: aws.ec2.SecurityGroup | None

    lbc_role: aws.iam.Role

    externaldns_role: aws.iam.Role

    traefik_forward_auth_role: aws.iam.Role

    mimir_password: pulumi_random.RandomPassword

    ebs_csi_role: aws.iam.Role
    bastion: ptd.pulumi_resources.aws_bastion.AwsBastion

    internal_sites: dict[str, InternalSiteConfig]

    @classmethod
    def autoload(cls) -> "AWSWorkloadPersistent":
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

        outputs = {}

        self.workload = workload

        main_site = InternalSiteConfig(domain=workload.cfg.domain, zone_id=workload.cfg.hosted_zone_id)
        self.internal_sites = {"main": main_site}

        for site_name, site in sorted(workload.cfg.sites.items()):
            if site_name == "main":
                continue

            self.internal_sites[site_name] = InternalSiteConfig(domain=site.domain, zone_id=site.zone_id)

        self.required_tags = self.workload.required_tags | {
            str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
        }

        self.managed_clusters = self.workload.managed_clusters(assume_role=False)
        self.federated_endpoints = self.workload.federated_endpoints(assume_role=False)
        self.oidc_urls = [
            u for u in [ptd.get_oidc_url(c) for c in self.managed_clusters] if u is not None
        ] + self.workload.cfg.extra_cluster_oidc_urls

        self.vpc = None
        self._define_vpc()

        if self.workload.cfg.tailscale_enabled:
            self._define_tailscale()
            bastion_id = None
        # support behavior where customer requires one-off instances to be created via external automation.
        elif self.workload.cfg.customer_managed_bastion_id:
            bastion_id = self.workload.cfg.customer_managed_bastion_id
        else:
            self._define_bastion()
            bastion_id = self.bastion.bastion.id

        self._define_db()
        self._define_ppm_bucket()
        self._define_chronicle_bucket()
        self._define_team_operator_iam()
        self.cert_validation_records = {}  # Initialize the dict to track validation records
        self._define_zones_and_domain_certs()

        self.ecrs = dict.fromkeys([c.value for c in ptd.ComponentImages], None)
        self.ecr_lifecycle_policies = {}
        self._define_ecr()

        self._define_fsx_openzfs()
        self._define_fsx_nfs_sg()
        self._define_efs_nfs_sg()
        self._define_lbc_iam()
        if self.workload.cfg.external_dns_enabled:
            self._define_externaldns_iam()
        self._define_traefik_forward_auth_iam()
        self._define_mimir()
        self._define_loki_bucket()
        self._define_loki_iam()
        self._define_ebs_csi_iam()
        self._define_alloy_iam()

        outputs = outputs | {
            "bastion_id": bastion_id,
            "chronicle_bucket": self.chronicle_bucket.bucket,
            "db": self.db.identifier,
            "db_address": self.db.address.apply(str),
            "db_secret_arn": self.db_secret_arn,
            "db_url": self.db.address.apply(lambda a: f"postgres://{a}/postgres?sslmode=require"),
            "cert_arns": self.cert_arns,
            "fs_dns_name": self.fsx_openzfs_fs.dns_name.apply(str),
            "fs_root_volume_id": self.fsx_openzfs_fs.root_volume_id.apply(str),
        }

        # Add hosted zone outputs only if zone management is enabled
        if self.workload.cfg.hosted_zone_management_enabled:
            outputs = outputs | {
                "domain_ns_map": {
                    v.domain: v.zone.name_servers for v in self.internal_sites.values() if v.zone is not None
                },
                # Add hosted zone name servers as individual outputs for each domain
                "hosted_zone_name_servers": {
                    site_name: {
                        "domain": site.domain,
                        "name_servers": site.zone.name_servers if site.zone else None,
                        "zone_id": site.zone.zone_id if site.zone else site.zone_id,
                    }
                    for site_name, site in self.internal_sites.items()
                },
                # Add certificate validation records (CNAME records needed for DNS validation)
                "certificate_validation_records": {
                    domain: records.apply(
                        lambda recs: [
                            {
                                "name": rec.name,
                                "type": rec.type,
                                "value": rec.records[0] if rec.records else None,
                            }
                            for rec in recs
                        ]
                    )
                    for domain, records in self.cert_validation_records.items()
                },
            }
        else:
            # Indicate zones are externally managed
            outputs = outputs | {
                "domain_ns_map": {},
                "hosted_zone_name_servers": [],
                "hosted_zone_info": "Hosted zones are externally managed",
                "certificate_validation_records": {},
            }

        outputs = outputs | {
            "mimir_bucket": self.mimir_bucket.bucket,
            "mimir_password": self.mimir_password.result,
            "packagemanager_bucket": self.packagemanager_bucket.bucket,
            "private_subnet_ids": self.private_subnet_ids,
            "rds_host": self.db.address,
            "vpc": self.vpc_id,
            "subnet_ids": [s["SubnetId"] for s in self.workload.subnets("private")],
        }

        for key, value in outputs.items():
            pulumi.export(key, value)

        self.register_outputs(outputs)

    @property
    def _oidc_url_tails(self):
        return [u.split("//")[1] for u in self.oidc_urls]

    def _define_vpc(self) -> None:
        if self.workload.cfg.provisioned_vpc is not None:
            self._lookup_existing_vpc_resources()
            return

        # Create new VPC
        self.vpc_net = self.workload.vpc_cidr()
        azs = aws.get_availability_zones()

        self.vpc = ptd.pulumi_resources.aws_vpc.AWSVpc(
            self.workload.compound_name,
            cidr_block=str(self.vpc_net),
            azs=list(azs.zone_ids)[: self.workload.cfg.vpc_az_count],
            network_access_tags={
                "public": {
                    "kubernetes.io/role/elb": "1",
                    str(ptd.TagKeys.POSIT_TEAM_NETWORK_ACCESS): "public",
                    str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
                },
                "private": {
                    "kubernetes.io/role/internal-elb": "1",
                    str(ptd.TagKeys.POSIT_TEAM_NETWORK_ACCESS): "private",
                    str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
                },
            },
            tags=self.required_tags | {"Name": self.workload.compound_name},
            opts=pulumi.ResourceOptions(parent=self),
        )

        self.vpc_id = self.vpc.vpc.id

        self.vpc.with_nat_gateways()
        self.vpc.with_nacl_rule(port_range=443, cidr_blocks=["0.0.0.0/0"])
        self.vpc.with_nacl_rule(port_range=80, cidr_blocks=["0.0.0.0/0"])
        self.vpc.with_secure_default_security_group()
        self.vpc.with_secure_default_nacl()

        for port_range, protocol, privacy in [
            (port_range, protocol, privacy)
            for port_range in (111, 2049, range(20001, 20003), range(65536))
            for protocol in ("tcp", "udp")
            for privacy in ("public", "private")
        ]:
            self.vpc.with_nacl_rule(
                port_range=typing.cast(int | range | tuple[int, int], port_range),
                protocol=protocol,
                privacy=privacy,
                cidr_blocks=[str(self.vpc_net)],
            )

        self.vpc.with_nacl_rule(
            egress=True,
            port_range=0,
            protocol="-1",
            cidr_blocks=["0.0.0.0/0"],
        )

        self.vpc.with_nacl_rule(
            egress=True,
            port_range=0,
            protocol="-1",
            cidr_blocks=["0.0.0.0/0"],
            privacy="private",
        )

        # Check feature flag for VPC endpoints
        # Default to enabled with all services if not configured
        vpc_endpoints_config = self.workload.cfg.vpc_endpoints
        if vpc_endpoints_config is None:
            vpc_endpoints_config = ptd.aws_workload.VPCEndpointsConfig()

        if vpc_endpoints_config.enabled:
            for service in ptd.aws_workload.STANDARD_VPC_ENDPOINT_SERVICES:
                if service not in vpc_endpoints_config.excluded_services:
                    self.vpc.with_endpoint(service=service)

        # FIXME: the admin role can't do this :sob:, e.g.:
        # > creating Flow Log (vpc-0c8af691fbfe5cc88): UnauthorizedOperation: You are not
        # authorized to perform this operation. User:
        # arn:aws:sts::123456789012:assumed-role/admin.example.com/user@example.com is
        # not authorized to perform: iam:PassRole on resource:
        # arn:aws:iam::123456789012:role/FlowLogs because no identity-based policy allows
        # the iam:PassRole action.
        #
        self.vpc.with_flow_log(
            permissions_boundary=self.workload.iam_permissions_boundary,
            existing_flow_log_target_arns=self.workload.cfg.existing_flow_log_target_arns,
        )

        self.private_subnet_ids = [s.id.apply(str) for s in self.vpc.subnets["private"]]

    def _define_db(self):
        dbsg = aws.ec2.SecurityGroup(
            f"{self.workload.compound_name}-allow-postgresql-traffic-vpc",
            description=f"Allow PostgreSQL traffic from VPC for {self.workload.compound_name}",
            vpc_id=self.vpc_id,
            ingress=[
                aws.ec2.SecurityGroupIngressArgs(
                    description="Allow PostgreSQL traffic on port 5432",
                    from_port=5432,
                    to_port=5432,
                    protocol="tcp",
                    cidr_blocks=[str(self.vpc_net)],
                ),
            ],
            egress=[
                aws.ec2.SecurityGroupEgressArgs(
                    from_port=0,
                    to_port=0,
                    protocol="-1",
                    cidr_blocks=[str(self.vpc_net)],
                )
            ],
            tags=self.required_tags | {"Name": f"{self.workload.compound_name}-allow-postgresql-traffic-vpc"},
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        dbsng = aws.rds.SubnetGroup(
            f"{self.workload.compound_name}-main-database-subnet-group",
            subnet_ids=self.private_subnet_ids,
            tags=self.required_tags | {"Name": f"{self.workload.compound_name}-main-database-subnet-group"},
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        dbpg = aws.rds.ParameterGroup(
            f"{self.workload.compound_name}-main-database-parameter-group",
            family="postgres15",
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
            self.workload.compound_name,
            identifier_prefix=f"{self.workload.compound_name}-",
            allocated_storage=self.workload.cfg.db_allocated_storage,
            max_allocated_storage=self.workload.cfg.db_max_allocated_storage,
            backup_retention_period=7,
            copy_tags_to_snapshot=True,
            db_name="postgres",
            db_subnet_group_name=dbsng.name,
            engine="postgres",
            engine_version=self.workload.cfg.db_engine_version,
            final_snapshot_identifier=f"{self.workload.compound_name}-final-snapshot",
            instance_class=self.workload.cfg.db_instance_class,
            manage_master_user_password=True,
            parameter_group_name=dbpg.name,
            skip_final_snapshot=(not self.workload.cfg.protect_persistent_resources),
            storage_encrypted=True,
            storage_type=aws.rds.StorageType.GP3,
            tags=self.required_tags | {"Name": self.workload.compound_name},
            username="postgres",
            vpc_security_group_ids=[dbsg.id],
            performance_insights_enabled=self.workload.cfg.db_performance_insights_enabled,
            deletion_protection=self.workload.cfg.db_deletion_protection,
            multi_az=self.workload.cfg.db_multi_az,
            opts=pulumi.ResourceOptions(
                parent=self.vpc,
                protect=self.workload.cfg.protect_persistent_resources,
                ignore_changes=["identifier_prefix"],
            ),
        )

        secret = self.db.master_user_secrets.apply(lambda secrets: secrets[0])
        self.db_secret_arn = secret.apply(lambda s: s.secret_arn)

    def _define_bastion(self):
        self.bastion = ptd.pulumi_resources.aws_bastion.AwsBastion(
            name=self.workload.compound_name,
            vpc_id=self.vpc_id,
            subnet_id=self.private_subnet_ids[0],
            instance_type=self.workload.cfg.bastion_instance_type,
            tags=self.required_tags,
            permissions_boundary=self.workload.iam_permissions_boundary,
            opts=pulumi.ResourceOptions(parent=self.vpc),
            prev_parent=self,
        )

    def _define_named_bucket(self, name: str, opts: pulumi.ResourceOptions | None = None) -> aws.s3.Bucket:
        if opts is None:
            opts = pulumi.ResourceOptions()
        return aws.s3.Bucket(
            f"{self.workload.compound_name}-{name}-bucket",
            aws.s3.BucketArgs(
                bucket=f"{self.workload.prefix}-{name}",
                acl="private",
                tags=self.required_tags,
                server_side_encryption_configuration=aws.s3.BucketServerSideEncryptionConfigurationArgs(
                    rule=aws.s3.BucketServerSideEncryptionConfigurationRuleArgs(
                        apply_server_side_encryption_by_default=aws.s3.BucketServerSideEncryptionConfigurationRuleApplyServerSideEncryptionByDefaultArgs(
                            sse_algorithm="aws:kms",
                        ),
                        bucket_key_enabled=True,
                    ),
                ),
            ),
            opts=pulumi.ResourceOptions.merge(
                opts,
                pulumi.ResourceOptions(
                    parent=self,
                    protect=self.workload.cfg.protect_persistent_resources,
                    retain_on_delete=True,
                ),
            ),
        )

    def _define_prefixed_bucket(self, name: str) -> aws.s3.Bucket:
        return aws.s3.Bucket(
            f"{self.workload.compound_name}-{name}-bucket",
            aws.s3.BucketArgs(
                bucket_prefix=f"{self.workload.prefix}-{self.workload.compound_name}-{name}-",
                acl="private",
                tags=self.required_tags,
                server_side_encryption_configuration=aws.s3.BucketServerSideEncryptionConfigurationArgs(
                    rule=aws.s3.BucketServerSideEncryptionConfigurationRuleArgs(
                        apply_server_side_encryption_by_default=aws.s3.BucketServerSideEncryptionConfigurationRuleApplyServerSideEncryptionByDefaultArgs(
                            sse_algorithm="aws:kms",
                        ),
                        bucket_key_enabled=True,
                    ),
                ),
            ),
            opts=pulumi.ResourceOptions(
                parent=self,
                protect=self.workload.cfg.protect_persistent_resources,
                retain_on_delete=True,
            ),
        )

    def _define_ppm_bucket(self):
        # NOTE: policies are created in the `cluster` step, since they are scoped to each site
        self.packagemanager_bucket = self._define_prefixed_bucket("ppm")

    def _define_named_bucket_and_policy(
        self,
        name: str,
        policy_name: str,
        policy_description: str,
        read_policy_name: str = "",
        read_policy_description: str = "",
    ) -> aws.s3.Bucket:
        bucket = self._define_named_bucket(name)

        ptd.pulumi_resources.aws_bucket.define_bucket_policy(
            name=name,
            compound_name=self.workload.compound_name,
            bucket=bucket,
            policy_name=policy_name,
            policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ_WRITE,
            policy_description=policy_description,
            required_tags=self.required_tags,
        )

        if read_policy_name != "":
            ptd.pulumi_resources.aws_bucket.define_bucket_policy(
                name=name,
                compound_name=self.workload.compound_name,
                bucket=bucket,
                policy_name=read_policy_name,
                policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ,
                policy_description=read_policy_description,
                required_tags=self.required_tags,
            )

        return bucket

    def _define_prefixed_bucket_and_policy(
        self,
        name: str,
        policy_name: str,
        policy_description: str = "",
        read_policy_name: str = "",
        read_policy_description: str = "",
    ) -> aws.s3.Bucket:
        bucket = self._define_prefixed_bucket(name)

        ptd.pulumi_resources.aws_bucket.define_bucket_policy(
            name=name,
            compound_name=self.workload.compound_name,
            bucket=bucket,
            policy_name=policy_name,
            policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ_WRITE,
            policy_description=policy_description,
            required_tags=self.required_tags,
        )

        if read_policy_name != "":
            ptd.pulumi_resources.aws_bucket.define_bucket_policy(
                name=name,
                compound_name=self.workload.compound_name,
                bucket=bucket,
                policy_name=read_policy_name,
                policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ,
                policy_description=read_policy_description,
                required_tags=self.required_tags,
            )

        return bucket

    def _define_chronicle_bucket(self):
        # NOTE: policies are created in the `cluster` step, since they are scoped to each site
        self.chronicle_bucket = self._define_prefixed_bucket("chronicle")

    def _define_team_operator_iam(self):
        team_operator_policy_doc = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["secretsmanager:GetSecretValue"],
                    resources=["*"],
                ),
            ]
        )

        self.team_operator_policy = aws.iam.Policy(
            self.workload.team_operator_policy_name,
            name=self.workload.team_operator_policy_name,
            description=f"Posit Team Dedicated policy for {self.workload.compound_name} Team Operator",
            policy=team_operator_policy_doc.json,
            tags=self.required_tags | {"Name": f"{self.workload.compound_name}-team-operator-policy"},
            opts=pulumi.ResourceOptions(parent=self, aliases=[pulumi.Alias(parent=self.chronicle_bucket)]),
        )

    def _define_hosted_zone(
        self,
        zone_id: str,
        domain: str,
        alias: str,
        site_config: ptd.aws_workload.AWSSiteConfig | None = None,
    ) -> aws.route53.Zone | None:
        # Return None if hosted zone management is disabled
        if not self.workload.cfg.hosted_zone_management_enabled:
            return None

        name = f"{domain}-zone"
        if zone_id is not None:
            return aws.route53.Zone.get(name, id=zone_id)

        # Determine if this should be a private zone based on site configuration
        is_private = site_config.private_zone if site_config else False
        vpc_associations = site_config.vpc_associations if site_config else []
        auto_associate = site_config.auto_associate_provisioned_vpc if site_config else True

        # Automatically include the provisioned VPC if enabled and we're creating a private zone
        if (
            is_private
            and auto_associate
            and self.workload.cfg.provisioned_vpc
            and self.workload.cfg.provisioned_vpc.vpc_id
        ):
            provisioned_vpc_id = self.workload.cfg.provisioned_vpc.vpc_id
            # Add the provisioned VPC if it's not already in the list
            if provisioned_vpc_id not in vpc_associations:
                vpc_associations = [provisioned_vpc_id, *vpc_associations]

        # Validate VPC IDs format before using them
        for vpc_id in vpc_associations:
            if not vpc_id.startswith(AWS_VPC_ID_PREFIX) or len(vpc_id) != AWS_VPC_ID_LENGTH:
                error_msg = (
                    f"Invalid VPC ID format: {vpc_id}. "
                    f"VPC IDs must start with '{AWS_VPC_ID_PREFIX}' and be {AWS_VPC_ID_LENGTH} "
                    f"characters long (e.g., vpc-0123456789abcdef0)"
                )
                raise ValueError(error_msg)

        # Prepare VPC configuration for private zones
        vpcs = []
        if is_private and vpc_associations:
            vpcs.extend(
                aws.route53.ZoneVpcArgs(
                    vpc_id=vpc_id,
                    vpc_region=self.workload.cfg.region,
                )
                for vpc_id in vpc_associations
            )

        zone_type = "Private" if is_private else "Publicly accessible"
        return aws.route53.Zone(
            name,
            name=domain,
            comment=f"Hosted Zone for the Posit Team Dedicated service in {self.workload.compound_name}. {zone_type}",
            vpcs=vpcs if vpcs else None,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                parent=self,
                protect=self.workload.cfg.protect_persistent_resources,
                aliases=[pulumi.Alias(name=alias)],
                ignore_changes=[
                    "comment"
                ],  # TO-DO: We don't have route53:UpdateHostedZoneComment permission, so we ignore changes to the comment field
            ),
        )

    def _return_build_validation_function(self, site_name: str, suffix: str, zone: aws.route53.Zone):
        def _build_validation_records(domain_validation_options):
            return [
                aws.route53.Record(
                    f"{self.workload.compound_name}-cert-validation-record-{suffix}-{i}",
                    name=dvo.resource_record_name,
                    records=[dvo.resource_record_value],
                    ttl=60,
                    type=dvo.resource_record_type,
                    zone_id=zone.zone_id,
                    opts=pulumi.ResourceOptions(
                        parent=zone,
                        aliases=(
                            [pulumi.Alias(name=f"{self.workload.compound_name}-cert-validation-record-{i}")]
                            if site_name == "main"
                            else []
                        ),
                        delete_before_replace=True,
                    ),
                )
                for i, dvo in enumerate({dvo.resource_record_value: dvo for dvo in domain_validation_options}.values())
            ]

        return _build_validation_records

    def _define_zones_and_domain_certs(self):
        self.cert_arns = []

        # Handle disabled zone management
        if not self.workload.cfg.hosted_zone_management_enabled:
            # Process only certificates (zones must exist externally)
            for site_name, site in self.workload.cfg.sites.items():
                # Validation already done in config validation
                # Just use the provided certificate
                self.internal_sites[site_name].certificate = site.certificate_arn
                self.internal_sites[site_name].zone = None
                self.internal_sites[site_name].zone_id = site.zone_id  # May be None if not provided
                # Add certificate ARN to list for use by other resources
                if site.certificate_arn:
                    self.cert_arns.append(site.certificate_arn)
            return

        # Group sites by domain to avoid duplicate resources for the same domain
        domains_to_sites = {}
        for site_name, site in sorted(self.workload.cfg.sites.items()):
            domain = site.domain
            if domain not in domains_to_sites:
                domains_to_sites[domain] = []
            domains_to_sites[domain].append((site_name, site))

        # Process each unique domain once
        for domain, sites_for_domain in domains_to_sites.items():
            # Use the first site for this domain to determine zone settings
            primary_site_name, primary_site = sites_for_domain[0]

            zone = self._define_hosted_zone(
                typing.cast(str, primary_site.zone_id),
                domain,
                self.workload.compound_name if primary_site_name == "main" else f"{self.workload.compound_name}-other",
                site_config=primary_site,
            )

            # Update all sites that use this domain
            for site_name, _site in sites_for_domain:
                self.internal_sites[site_name].zone_id = zone.zone_id
                self.internal_sites[site_name].zone = zone

            if primary_site.zone_id is None and zone is None:
                pulumi.info(f"skipping domain cert for domain {domain!r} because zone_id and zone are both None")
                continue

            # If a certificate ARN is provided, use that and skip creation of new certificate
            if primary_site.certificate_arn:
                self.cert_arns.append(primary_site.certificate_arn)
                continue

            dashify_domain = domain.replace(".", "-")
            cert = aws.acm.Certificate(
                f"{self.workload.compound_name}-domain-cert-{dashify_domain}",
                domain_name=domain,
                subject_alternative_names=[
                    f"*.{domain}",
                ],
                validation_method="DNS",
                tags=self.required_tags,
                opts=pulumi.ResourceOptions(
                    parent=self,
                ),
            )
            self.cert_arns.append(cert.arn)

            # yes. this is really gross. basically what happens is that any using a lambda to
            # pass multiple values or using "site_name" / "dashify_domain" from the global scope
            # will cause some type of weird cache that does not get invalidated...😱
            #
            # but this wildness seems to work just fine...
            tmp_func = self._return_build_validation_function(primary_site_name, dashify_domain, zone)

            # Always generate validation records for output (needed for manual validation)
            cert_validation_records = cert.domain_validation_options.apply(tmp_func)

            # Store the validation records for this domain (for stack outputs)
            self.cert_validation_records[domain] = cert_validation_records

            # Only create AWS Certificate Validation resource if enabled
            if primary_site.certificate_validation_enabled:
                aws.acm.CertificateValidation(
                    f"{self.workload.compound_name}-cert-validation-{dashify_domain}",
                    certificate_arn=cert.arn,
                    validation_record_fqdns=cert_validation_records.apply(
                        lambda cvr: sorted([rec.fqdn for rec in cvr])
                    ),  # type: ignore
                    opts=pulumi.ResourceOptions(
                        parent=cert,
                    ),
                )

    # TODO: this is sort-of duplicated from aws_control_room_persistent...
    # NOTE: ECR repositories are deprecated - images now come from public Docker Hub.
    # force_delete=True is set to allow cleanup in a follow-up PR.
    def _define_ecr(self):
        for repo_name in list(self.ecrs.keys()):
            self.ecrs[repo_name] = aws.ecr.Repository(
                f"{self.workload.compound_name}-{repo_name}",
                name=repo_name,
                force_delete=True,  # Allow deletion even with images present
                image_scanning_configuration=aws.ecr.RepositoryImageScanningConfigurationArgs(
                    scan_on_push=True,
                ),
                image_tag_mutability="IMMUTABLE",
                tags=self.required_tags | {"Name": f"{repo_name}-{self.workload.compound_name}"},
                opts=pulumi.ResourceOptions(parent=self),
            )

            self.ecr_lifecycle_policies[repo_name] = aws.ecr.LifecyclePolicy(
                f"{repo_name}-ecr-expire-untagged-images",
                repository=repo_name,
                policy=json.dumps(
                    {
                        "rules": [
                            {
                                "rulePriority": 1,
                                "description": "Expire images older than 30 days",
                                "selection": {
                                    "tagStatus": "untagged",
                                    "countType": "sinceImagePushed",
                                    "countUnit": "days",
                                    "countNumber": 30,
                                },
                                "action": {"type": "expire"},
                            }
                        ]
                    }
                ),
                opts=pulumi.ResourceOptions(parent=self.ecrs[repo_name]),
            )

        # Handle deprecated ECR repos that need to be force-deleted
        # These repos are no longer in ComponentImages but may exist in existing deployments
        for deprecated_repo_name in ptd.DEPRECATED_ECR_REPOS:
            aws.ecr.Repository(
                f"{self.workload.compound_name}-{deprecated_repo_name}",
                name=deprecated_repo_name,
                force_delete=True,
                opts=pulumi.ResourceOptions(parent=self),
            )

    def _define_fsx_openzfs(self) -> None:
        self.fsx_openzfs_role = aws.iam.Role(
            str(ptd.Roles.AWS_FSX_OPENZFS_CSI_DRIVER),
            name=self.workload.fsx_openzfs_role_name,
            assume_role_policy=json.dumps(
                (
                    ptd.aws_iam.build_irsa_role_assume_role_policy(
                        service_accounts=[
                            f"controller.{ptd.Roles.AWS_FSX_OPENZFS_CSI_DRIVER!s}",
                            f"nodes.{ptd.Roles.AWS_FSX_OPENZFS_CSI_DRIVER!s}",
                        ],
                        namespace="kube-system",
                        managed_account_id=self.workload.cfg.account_id,
                        oidc_url_tails=[u.split("//")[1] for u in self.oidc_urls],
                    )
                    if len(self.oidc_urls) > 0
                    else {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {
                                    "AWS": aws.get_caller_identity().arn,
                                },
                            },
                        ],
                    }
                ),
            ),
            permissions_boundary=self.workload.iam_permissions_boundary,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.workload.compound_name}-fsx-openzfs",
            role=self.fsx_openzfs_role.name,
            policy_arn="arn:aws:iam::aws:policy/AmazonFSxFullAccess",
            opts=pulumi.ResourceOptions(parent=self.fsx_openzfs_role),
        )

        if len(self.private_subnet_ids) <= 0:
            msg = "no private subnet ids available"
            raise ValueError(msg)

        subnet_ids = [self.private_subnet_ids[0]]
        deployment_type = "SINGLE_AZ_HA_2"

        if self.workload.cfg.fsx_openzfs_multi_az:
            deployment_type = "MULTI_AZ_1"

        if self.workload.cfg.fsx_openzfs_override_deployment_type is not None:
            deployment_type = self.workload.cfg.fsx_openzfs_override_deployment_type

        self.fsx_openzfs_sg = aws.ec2.SecurityGroup(
            self.workload.fsx_nfs_sg_name,
            name_prefix=f"{self.workload.fsx_nfs_sg_name}-",
            description=f"Allow FSx NFS traffic for {self.workload.compound_name}",
            vpc_id=self.vpc_id,
            ingress=[
                aws.ec2.SecurityGroupIngressArgs(
                    description=f"Allow {protocol.upper()} on ports {from_port}-{to_port}",
                    from_port=from_port,
                    to_port=to_port,
                    protocol=protocol,
                    cidr_blocks=[str(self.vpc_net)],
                )
                for from_port, to_port in ((111, 111), (2049, 2049), (20001, 20003))
                for protocol in ("tcp", "udp")
            ],
            egress=[],
            tags=self.required_tags
            | {
                "Name": self.workload.fsx_nfs_sg_name,
            },
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        if deployment_type.startswith("MULTI"):
            self.fsx_openzfs_fs = ptd.pulumi_resources.aws_fsx_openzfs_multi.AWSFsxOpenZfsMulti(
                self.workload.compound_name,
                props=ptd.pulumi_resources.aws_fsx_openzfs_multi.AWSFsxOpenZfsMultiArgs(
                    subnet_ids=self.private_subnet_ids[:2],
                    daily_automatic_backup_start_time=self.workload.cfg.fsx_openzfs_daily_automatic_backup_start_time,
                    deployment_type=deployment_type,
                    route_table_ids=[t.id.apply(str) for t in self.vpc.private_route_tables],
                    security_group_ids=[self.fsx_openzfs_sg.id],
                    storage_capacity=self.workload.cfg.fsx_openzfs_storage_capacity,
                    throughput_capacity=self.workload.cfg.fsx_openzfs_throughput_capacity,
                    copy_tags_to_backups=True,
                    copy_tags_to_volumes=True,
                    root_volume_configuration=ptd.pulumi_resources.aws_fsx_openzfs_multi.AWSFsxOpenZfsMultiRootVolumeConfigurationArgs(
                        copy_tags_to_snapshots=True,
                        data_compression_type="NONE",
                        nfs_exports=ptd.pulumi_resources.aws_fsx_openzfs_multi.AWSFsxOpenZfsMultiRootVolumeConfigurationNfsExportsArgs(
                            client_configurations=[
                                ptd.pulumi_resources.aws_fsx_openzfs_multi.AWSFsxOpenZfsMultiRootVolumeConfigurationNfsExportsClientConfigurationArgs(
                                    clients="*",
                                    options=["rw", "no_root_squash", "crossmnt"],
                                ),
                            ]
                        ),
                    ),
                    tags=self.required_tags | {"Name": self.workload.compound_name},
                ),
                opts=pulumi.ResourceOptions(
                    parent=self.vpc,
                    protect=self.workload.cfg.protect_persistent_resources,
                ),
            )

        else:
            self.fsx_openzfs_fs = aws.fsx.OpenZfsFileSystem(
                self.workload.compound_name,
                preferred_subnet_id=(subnet_ids[0] if deployment_type.startswith("MULTI") else None),
                subnet_ids=subnet_ids,
                deployment_type=deployment_type,
                security_group_ids=[self.fsx_openzfs_sg.id],
                storage_capacity=self.workload.cfg.fsx_openzfs_storage_capacity,
                throughput_capacity=self.workload.cfg.fsx_openzfs_throughput_capacity,
                copy_tags_to_backups=True,
                copy_tags_to_volumes=True,
                root_volume_configuration=aws.fsx.OpenZfsFileSystemRootVolumeConfigurationArgs(
                    copy_tags_to_snapshots=True,
                    data_compression_type="NONE",
                    nfs_exports=aws.fsx.OpenZfsFileSystemRootVolumeConfigurationNfsExportsArgs(
                        client_configurations=[
                            aws.fsx.OpenZfsFileSystemRootVolumeConfigurationNfsExportsClientConfigurationArgs(
                                clients="*",
                                options=["rw", "no_root_squash"],
                            ),
                        ]
                    ),
                ),
                tags=self.required_tags | {"Name": self.workload.compound_name},
                opts=pulumi.ResourceOptions(
                    parent=self.vpc,
                    protect=self.workload.cfg.protect_persistent_resources,
                ),
            )

    def _define_fsx_nfs_sg(self):
        self.fsx_nfs_sg = aws.ec2.SecurityGroup(
            str(ptd.SecurityGroupPrefixes.EKS_NODES_FSX_NFS),
            name_prefix=f"{ptd.SecurityGroupPrefixes.EKS_NODES_FSX_NFS}-",
            description=f"Allow NFS traffic for {self.workload.compound_name}",
            vpc_id=self.vpc_id,
            ingress=[
                aws.ec2.SecurityGroupIngressArgs(
                    description=f"Allow {protocol.upper()} on ports {from_port}-{to_port}",
                    from_port=from_port,
                    to_port=to_port,
                    protocol=protocol,
                    cidr_blocks=[str(self.vpc_net)],
                )
                for from_port, to_port in ((111, 111), (2049, 2049), (20001, 20003))
                for protocol in ("tcp", "udp")
            ],
            egress=[
                aws.ec2.SecurityGroupEgressArgs(
                    description="Allow all TCP and UDP egress",
                    from_port=0,
                    to_port=0,
                    protocol="-1",
                    cidr_blocks=[str(self.vpc_net)],
                )
            ],
            tags=self.required_tags
            | {
                "Name": str(ptd.SecurityGroupPrefixes.EKS_NODES_FSX_NFS),
            },
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        # Check feature flag for FSX VPC endpoint
        # Default to enabled with all services if not configured
        vpc_endpoints_config = self.workload.cfg.vpc_endpoints
        if vpc_endpoints_config is None:
            vpc_endpoints_config = ptd.aws_workload.VPCEndpointsConfig()

        if vpc_endpoints_config.enabled and "fsx" not in vpc_endpoints_config.excluded_services:
            self.vpc.with_endpoint(service="fsx", security_group_ids=[self.fsx_nfs_sg.id])

    def _define_efs_nfs_sg(self):
        # Check if any cluster has EFS enabled
        efs_enabled = any(
            cluster.enable_efs_csi_driver or cluster.efs_config is not None
            for cluster in self.workload.cfg.clusters.values()
        )

        if not efs_enabled:
            self.efs_nfs_sg = None
            return

        self.efs_nfs_sg = aws.ec2.SecurityGroup(
            str(ptd.SecurityGroupPrefixes.EKS_NODES_EFS_NFS),
            name_prefix=f"{ptd.SecurityGroupPrefixes.EKS_NODES_EFS_NFS}-",
            description=f"Allow EFS NFS traffic for {self.workload.compound_name}",
            vpc_id=self.vpc_id,
            ingress=[
                aws.ec2.SecurityGroupIngressArgs(
                    description="Allow NFS (TCP port 2049)",
                    from_port=2049,
                    to_port=2049,
                    protocol="tcp",
                    cidr_blocks=[str(self.vpc_net)],
                ),
            ],
            egress=[
                aws.ec2.SecurityGroupEgressArgs(
                    description="Allow NFS egress within VPC",
                    from_port=2049,
                    to_port=2049,
                    protocol="tcp",
                    cidr_blocks=[str(self.vpc_net)],
                )
            ],
            tags=self.required_tags
            | {
                "Name": str(ptd.SecurityGroupPrefixes.EKS_NODES_EFS_NFS),
            },
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

    def _define_lbc_iam(self):
        self.lbc_role = aws.iam.Role(
            self.workload.lbc_role_name,
            name=self.workload.lbc_role_name,
            assume_role_policy=json.dumps(
                (
                    ptd.aws_iam.build_irsa_role_assume_role_policy(
                        service_accounts=[
                            str(ptd.Roles.AWS_LOAD_BALANCER_CONTROLLER),
                        ],
                        namespace="kube-system",
                        managed_account_id=self.workload.cfg.account_id,
                        oidc_url_tails=self._oidc_url_tails,
                    )
                    if len(self._oidc_url_tails) > 0
                    else {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {
                                    "AWS": aws.get_caller_identity().arn,
                                },
                            },
                        ],
                    }
                ),
            ),
            permissions_boundary=self.workload.iam_permissions_boundary,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                parent=self,
                delete_before_replace=True,
                aliases=[pulumi.Alias(name=ptd.Roles.AWS_LOAD_BALANCER_CONTROLLER)],
            ),
        )

        lbc_policy = aws.iam.Policy(
            self.workload.lbc_policy_name,
            name=self.workload.lbc_policy_name,
            policy=(ptd.paths.HERE / "iam" / "aws-load-balancer-controller.policy.json").read_text(),
            opts=pulumi.ResourceOptions(parent=self.lbc_role, delete_before_replace=True),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.workload.lbc_policy_name}-att",
            role=self.lbc_role.name,
            policy_arn=lbc_policy.arn,
            opts=pulumi.ResourceOptions(parent=self.lbc_role, delete_before_replace=True),
        )

    def _define_externaldns_iam(self):
        self.externaldns_role = aws.iam.Role(
            self.workload.external_dns_role_name,
            name=self.workload.external_dns_role_name,
            assume_role_policy=json.dumps(
                (
                    ptd.aws_iam.build_irsa_role_assume_role_policy(
                        service_accounts=[
                            str(ptd.Roles.EXTERNAL_DNS),
                        ],
                        namespace="kube-system",
                        managed_account_id=self.workload.cfg.account_id,
                        oidc_url_tails=self._oidc_url_tails,
                    )
                    if len(self._oidc_url_tails) > 0
                    else {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {
                                    "AWS": aws.get_caller_identity().arn,
                                },
                            },
                        ],
                    }
                ),
            ),
            permissions_boundary=self.workload.iam_permissions_boundary,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                parent=self,
                delete_before_replace=True,
                aliases=[pulumi.Alias(name=ptd.Roles.EXTERNAL_DNS)],
            ),
        )

        dns_update_policy = aws.iam.Policy(
            self.workload.dns_update_policy_name,
            name=self.workload.dns_update_policy_name,
            policy=pulumi.Output.json_dumps(
                {
                    "Version": "2012-10-17",
                    "Statement": [
                        {
                            "Effect": "Allow",
                            "Action": ["route53:ChangeResourceRecordSets"],
                            "Resource": [
                                typing.cast(aws.route53.Zone, site_cfg.zone).arn
                                for _, site_cfg in sorted(self.internal_sites.items())
                                if site_cfg.zone is not None
                            ],
                        },
                        {
                            "Effect": "Allow",
                            "Action": [
                                "route53:ListHostedZones",
                                "route53:ListResourceRecordSets",
                                "route53:ListTagsForResource",
                            ],
                            "Resource": ["*"],
                        },
                    ],
                }
            ),
            opts=pulumi.ResourceOptions(
                parent=self.externaldns_role,
                delete_before_replace=True,
            ),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.workload.dns_update_policy_name}-att",
            role=self.externaldns_role.name,
            policy_arn=dns_update_policy.arn,
            opts=pulumi.ResourceOptions(parent=self.externaldns_role, delete_before_replace=True),
        )

    def _define_traefik_forward_auth_iam(self):
        self.traefik_forward_auth_role = aws.iam.Role(
            self.workload.traefik_forward_auth_role_name,
            name=self.workload.traefik_forward_auth_role_name,
            assume_role_policy=json.dumps(
                (
                    ptd.aws_iam.build_irsa_role_assume_role_policy(
                        service_accounts=[
                            str(ptd.Roles.TRAEFIK_FORWARD_AUTH),
                        ],
                        namespace="kube-system",
                        managed_account_id=self.workload.cfg.account_id,
                        oidc_url_tails=self._oidc_url_tails,
                    )
                    if len(self._oidc_url_tails) > 0
                    else {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {
                                    "AWS": aws.get_caller_identity().arn,
                                },
                            },
                        ],
                    }
                ),
            ),
            permissions_boundary=self.workload.iam_permissions_boundary,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self, delete_before_replace=True),
        )

        secrets_policy = aws.iam.Policy(
            self.workload.traefik_forward_auth_read_secrets_policy_name,
            name=self.workload.traefik_forward_auth_read_secrets_policy_name,
            policy=json.dumps(
                ptd.aws_traefik_forward_auth_secrets_policy(
                    self.workload.cfg.region,
                    self.workload.cfg.account_id,
                )
            ),
            opts=pulumi.ResourceOptions(parent=self.traefik_forward_auth_role),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.workload.traefik_forward_auth_read_secrets_policy_name}-att",
            role=self.traefik_forward_auth_role.name,
            policy_arn=secrets_policy.arn,
            opts=pulumi.ResourceOptions(parent=self.traefik_forward_auth_role),
        )

    def _define_mimir(self):
        self.mimir_password = pulumi_random.RandomPassword(
            f"{self.workload.compound_name}-mimir",
            special=True,
            override_special="-/_",
            length=36,
            opts=pulumi.ResourceOptions(parent=self),
        )

        self.mimir_bucket = self._define_named_bucket(
            self.workload.mimir_s3_bucket_name,
            pulumi.ResourceOptions(
                aliases=[
                    pulumi.Alias(
                        name=f"{self.workload.compound_name}-mimir-storage",
                    )
                ]
            ),
        )

        self.mimir_bucket_policy = ptd.pulumi_resources.aws_bucket.define_bucket_policy(
            name=self.workload.mimir_s3_bucket_name,
            compound_name=self.workload.compound_name,
            bucket=self.mimir_bucket,
            policy_name=self.workload.mimir_s3_bucket_policy_name,
            policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ_WRITE,
            policy_description=f"Posit Team Dedicated policy for {self.workload.compound_name} to read the Mimir S3 bucket",
            required_tags=self.required_tags,
        )

        self.mimir_role = aws.iam.Role(
            self.workload.mimir_role_name,
            name=self.workload.mimir_role_name,
            assume_role_policy=json.dumps(
                (
                    ptd.aws_iam.build_irsa_role_assume_role_policy(
                        service_accounts=[
                            str(ptd.Roles.MIMIR),
                        ],
                        namespace="mimir",
                        managed_account_id=self.workload.cfg.account_id,
                        oidc_url_tails=self._oidc_url_tails,
                    )
                    if len(self._oidc_url_tails) > 0
                    else {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {
                                    "AWS": aws.get_caller_identity().arn,
                                },
                            },
                        ],
                    }
                ),
            ),
            permissions_boundary=self.workload.iam_permissions_boundary,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self, delete_before_replace=True),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.workload.mimir_s3_bucket_policy_name}-att",
            role=self.mimir_role.name,
            policy_arn=self.mimir_bucket_policy.arn,
            opts=pulumi.ResourceOptions(parent=self.mimir_role, delete_before_replace=True),
        )

    def _define_loki_bucket(self):
        self.loki_bucket = self._define_named_bucket(
            self.workload.loki_s3_bucket_name,
            opts=pulumi.ResourceOptions(aliases=[pulumi.Alias(name=f"{self.workload.compound_name}-loki-bucket")]),
        )
        self.loki_bucket_policy = ptd.pulumi_resources.aws_bucket.define_bucket_policy(
            name=self.workload.loki_s3_bucket_name,
            compound_name=self.workload.compound_name,
            bucket=self.loki_bucket,
            policy_name=self.workload.loki_s3_bucket_policy_name,
            policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ_WRITE,
            policy_description=f"Posit Team Dedicated policy for {self.workload.compound_name} to read the Loki S3 bucket",
            required_tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                aliases=[pulumi.Alias(name=self.workload.loki_s3_bucket_policy_name)],
            ),
        )

    def _define_loki_iam(self):
        self.loki_role = aws.iam.Role(
            self.workload.loki_role_name,
            name=self.workload.loki_role_name,
            assume_role_policy=json.dumps(
                (
                    ptd.aws_iam.build_irsa_role_assume_role_policy(
                        service_accounts=[
                            str(ptd.Roles.LOKI),
                        ],
                        namespace="loki",
                        managed_account_id=self.workload.cfg.account_id,
                        oidc_url_tails=self._oidc_url_tails,
                    )
                    if len(self._oidc_url_tails) > 0
                    else {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {
                                    "AWS": aws.get_caller_identity().arn,
                                },
                            },
                        ],
                    }
                ),
            ),
            permissions_boundary=self.workload.iam_permissions_boundary,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self, delete_before_replace=True),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.workload.loki_s3_bucket_policy_name}-att",
            role=self.loki_role.name,
            policy_arn=self.loki_bucket_policy.arn,
            opts=pulumi.ResourceOptions(parent=self.loki_role, delete_before_replace=True),
        )

    def _define_ebs_csi_iam(self):
        self.ebs_csi_role = aws.iam.Role(
            self.workload.ebs_csi_role_name,
            name=self.workload.ebs_csi_role_name,
            assume_role_policy=json.dumps(
                (
                    ptd.aws_iam.build_irsa_role_assume_role_policy(
                        service_accounts=[
                            str(ptd.Roles.AWS_EBS_CSI_DRIVER),
                        ],
                        namespace="kube-system",
                        managed_account_id=self.workload.cfg.account_id,
                        oidc_url_tails=self._oidc_url_tails,
                    )
                    if len(self._oidc_url_tails) > 0
                    else {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {
                                    "AWS": aws.get_caller_identity().arn,
                                },
                            },
                        ],
                    }
                ),
            ),
            permissions_boundary=self.workload.iam_permissions_boundary,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(parent=self, delete_before_replace=True),
        )

        aws.iam.RolePolicyAttachment(
            "ebs-csi-driver-policy-att",
            role=self.ebs_csi_role.name,
            policy_arn="arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy",
            opts=pulumi.ResourceOptions(parent=self.ebs_csi_role, delete_before_replace=True),
        )

    def _define_alloy_iam(self):
        # Create a role for Grafana Alloy with the same permissions as Grafana Agent
        self.alloy_role = aws.iam.Role(
            self.workload.alloy_role_name,
            name=self.workload.alloy_role_name,
            assume_role_policy=json.dumps(
                (
                    ptd.aws_iam.build_irsa_role_assume_role_policy(
                        service_accounts=[
                            str(ptd.Roles.ALLOY),
                        ],
                        namespace="alloy",
                        managed_account_id=self.workload.cfg.account_id,
                        oidc_url_tails=self._oidc_url_tails,
                    )
                    if len(self._oidc_url_tails) > 0
                    else {
                        "Version": "2012-10-17",
                        "Statement": [
                            {
                                "Action": "sts:AssumeRole",
                                "Effect": "Allow",
                                "Principal": {
                                    "AWS": aws.get_caller_identity().arn,
                                },
                            },
                        ],
                    }
                ),
            ),
            permissions_boundary=self.workload.iam_permissions_boundary,
            tags=self.required_tags,
            opts=pulumi.ResourceOptions(
                parent=self,
                delete_before_replace=True,
                aliases=[pulumi.Alias(name=ptd.Roles.ALLOY)],
            ),
        )

        alloy_policy = aws.iam.Policy(
            self.workload.alloy_policy_name,
            name=self.workload.alloy_policy_name,
            policy=pulumi.Output.json_dumps(
                {
                    "Version": "2012-10-17",
                    "Statement": [
                        {
                            "Effect": "Allow",
                            "Action": [
                                "tag:GetResources",
                                "cloudwatch:GetMetricData",
                                "cloudwatch:GetMetricStatistics",
                                "cloudwatch:ListMetrics",
                            ],
                            "Resource": ["*"],
                        },
                    ],
                }
            ),
            opts=pulumi.ResourceOptions(
                parent=self.alloy_role,
                delete_before_replace=True,
            ),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.workload.alloy_policy_name}-att",
            role=self.alloy_role.name,
            policy_arn=alloy_policy.arn,
            opts=pulumi.ResourceOptions(parent=self.alloy_role, delete_before_replace=True),
        )

    def _define_tailscale(self):
        self.tailscale = ptd.pulumi_resources.aws_tailscale.SubnetRouter(
            vpc=self.vpc,
            tags=self.required_tags | {"rs:project": "security", "rs:subsystem": "tailscale"},
            permissions_boundary=self.workload.iam_permissions_boundary,
            ts_extra_args="--advertise-tags=tag:ptd",
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

    def _lookup_existing_vpc_resources(self):
        """Look up and configure existing VPC resources."""
        # Use existing VPC
        self.vpc_id = self.workload.cfg.provisioned_vpc.vpc_id
        self.vpc_net = self.workload.cfg.provisioned_vpc.cidr

        # Get existing private subnet IDs
        private_subnet_ids = []
        subnets = self.workload.subnets("private")
        private_subnet_ids.extend([s["SubnetId"] for s in subnets])

        self.private_subnet_ids = private_subnet_ids

        # Get availability zones for the existing subnets
        azs = aws.get_availability_zones()

        # Create AWSVpc with existing resources
        self.vpc = ptd.pulumi_resources.aws_vpc.AWSVpc(
            self.workload.compound_name,
            cidr_block=str(self.vpc_net),
            azs=list(azs.zone_ids)[: self.workload.cfg.vpc_az_count],
            network_access_tags={
                "private": {
                    "kubernetes.io/role/internal-elb": "1",
                    str(ptd.TagKeys.POSIT_TEAM_NETWORK_ACCESS): "private",
                    str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
                },
            },
            tags=self.required_tags | {"Name": self.workload.compound_name},
            existing_vpc_id=self.vpc_id,
            existing_private_subnet_ids=self.private_subnet_ids,
            opts=pulumi.ResourceOptions(parent=self),
        )
