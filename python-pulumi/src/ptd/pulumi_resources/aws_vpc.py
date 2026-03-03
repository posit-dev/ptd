import collections
import ipaddress
import typing
import warnings

import pulumi
import pulumi_aws as aws

import ptd

AWS_UTILITIES_CUTOFF_VERSION_MAJOR = 6
MAX_AZ_COUNT = 3
MIN_CIDR_BLOCK_SIZE = 4096


class AWSVpc(pulumi.ComponentResource):
    """
    Encapsulates a somewhat opinionated VPC lovingly borrowed from the rstudio-pulumi library.
    """

    name: str
    cidr_block: ipaddress.IPv4Network
    subnet_cidr_blocks: ptd.SubnetCIDRBlocks
    azs: list[str]
    network_access_tags: dict[str, dict[str, str]]

    vpc: aws.ec2.Vpc
    subnets: dict[str, list[aws.ec2.Subnet]]
    public_route_table: aws.ec2.RouteTable
    private_route_tables: list[aws.ec2.RouteTable]
    nacls: dict[str, aws.ec2.NetworkAcl]
    nat_gw_public_ips: list[pulumi.Output[str]]
    next_nacl_rule_ids: dict[str, dict[bool, int]]
    vpc_endpoint_sg: aws.ec2.SecurityGroup
    eips_by_az: dict[int, str]

    def __init__(
        self,
        name: str,
        cidr_block: str,
        azs: list[str],
        tags: dict[str, str],
        network_access_tags: dict[str, dict[str, str]] | None = None,
        existing_vpc_id: str | None = None,
        existing_private_subnet_ids: list[str] | None = None,
        *args,
        **kwargs,
    ):
        """
        Create a VPC, Internet Gateway, Public and Private Subnets, Route tables, Public and Private NACLs
        OR use an existing VPC if existing_vpc_id is provided
        :param name: the name of the VPC
        :param cidr_block: the CIDR block of the VPC
        :param azs: the Availability Zone ids (use1-az4 vs. us-east-1c) for the VPC. A warning will be emitted if only
        a single AZ is specified (high-availability cannot be achieved) or if use1-az2 is specified (this AZ is known
        to be at capacity and should be used with caution)
        :param network_access_tags: tags specifically to apply to network entities including subnets and route tables,
        broken down by privacy type.
        :param existing_vpc_id: If provided, use this existing VPC instead of creating a new one
        :param existing_private_subnet_ids: If using existing VPC, provide the private subnet IDs
        :param map_public_ip_on_launch: Whether to assign a public IP address in public subnets on instance launch.
         Defaults to False in order to aid in complying with Security Hub finding
         EC2.15 (https://docs.aws.amazon.com/console/securityhub/EC2.15/remediation)
        :param tags: the tags to apply to all the resources
        :param opts: the options to use for this resource
        """

        self.name: str = name
        self.tags: dict[str, str] = tags
        self.azs = azs

        network_access_tags = network_access_tags or {"public": {}, "private": {}}

        super().__init__(f"ptd:{self.__class__.__name__}", self.name, *args, **kwargs)

        # Handle existing VPC case
        if existing_vpc_id:
            self._init_with_existing_vpc(existing_vpc_id, existing_private_subnet_ids, cidr_block)
            return

        # Original VPC creation logic follows...

        if len(azs) == 0:
            pulumi.error("Using zero availability zones is not supported")

        if len(azs) > MAX_AZ_COUNT:
            pulumi.error("Using more than three availability zones is not supported")

        if len(azs) == 1:
            warnings.warn(
                "Using a single availability zone is not recommended for production workloads",
                stacklevel=2,
            )

        if "use1-az2" in azs:
            warnings.warn(
                "It is recommended to not use use1-az2, it is at capacity and therefore unable to support "
                "new instance families and services",
                stacklevel=2,
            )

        self.cidr_block = typing.cast(
            ipaddress.IPv4Network,
            ipaddress.ip_network(cidr_block),
        )

        if self.cidr_block.num_addresses < MIN_CIDR_BLOCK_SIZE:
            pulumi.error("Using a VPC cidr smaller than /20 is not supported")

        self.subnet_cidr_blocks = ptd.SubnetCIDRBlocks.from_cidr_block(self.cidr_block)

        self.vpc: aws.ec2.Vpc = aws.ec2.Vpc(
            name,
            cidr_block=str(self.cidr_block),
            enable_dns_hostnames=True,
            enable_dns_support=True,
            tags=self.tags | {"Name": name},
            opts=pulumi.ResourceOptions(parent=self),
        )

        ig = aws.ec2.InternetGateway(
            name,
            vpc_id=self.vpc.id,
            tags=self.tags | {"Name": name} | network_access_tags["public"],
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        # Create public and private subnets
        self.subnets: dict[str, list[aws.ec2.Subnet]] = collections.defaultdict(list)

        for _, pp in enumerate(("public", "private")):
            subnet_cidrs = getattr(self.subnet_cidr_blocks, pp)

            for j, az in enumerate(azs):
                number = j + 1
                subnet = aws.ec2.Subnet(
                    f"{self.name}-{pp}-az{number}",
                    vpc_id=self.vpc.id,
                    cidr_block=str(subnet_cidrs[j]),
                    availability_zone_id=az,
                    map_public_ip_on_launch=False,
                    tags=self.tags | {"Name": f"{self.name}-{pp}-az{number}"} | network_access_tags[pp],
                    opts=pulumi.ResourceOptions(
                        parent=self.vpc,
                    ),
                )
                self.subnets[pp].append(subnet)

        self.public_route_table: aws.ec2.RouteTable = aws.ec2.RouteTable(
            f"{self.name}-public",
            vpc_id=self.vpc.id,
            tags=self.tags | {"Name": f"{self.name}-public"} | network_access_tags["public"],
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        aws.ec2.Route(
            f"{self.name}-public",
            route_table_id=self.public_route_table.id,
            gateway_id=ig.id,
            destination_cidr_block="0.0.0.0/0",
            opts=pulumi.ResourceOptions(parent=self.public_route_table),
        )

        for i, subnet in enumerate(self.subnets["public"]):
            number = i + 1

            aws.ec2.RouteTableAssociation(
                f"{self.name}-public-az{number}",
                subnet_id=subnet.id,
                route_table_id=self.public_route_table.id,
                opts=pulumi.ResourceOptions(
                    parent=self.public_route_table,
                ),
            )

        # Create private route tables per AZ
        self.private_route_tables: list[aws.ec2.RouteTable] = []
        for i, subnet in enumerate(self.subnets["private"]):
            number = i + 1

            private_rt = aws.ec2.RouteTable(
                f"{self.name}-private-az{number}",
                vpc_id=self.vpc.id,
                tags=self.tags | {"Name": f"{self.name}-private-az{number}"} | network_access_tags["private"],
                opts=pulumi.ResourceOptions(parent=self.vpc),
            )

            aws.ec2.RouteTableAssociation(
                f"{self.name}-private-az{number}",
                subnet_id=subnet.id,
                route_table_id=private_rt.id,
                opts=pulumi.ResourceOptions(parent=private_rt),
            )

            self.private_route_tables.append(private_rt)

        # https://docs.aws.amazon.com/vpc/latest/userguide/VPC_Scenario2.html#nacl-rules-scenario-2
        self.nacls: dict[str, aws.ec2.NetworkAcl] = {}
        # Privacy -> Egress? -> rule id
        self.next_nacl_rule_ids: dict[str, dict[bool, int]] = {}

        self.nacls["public"] = aws.ec2.NetworkAcl(
            f"{self.name}-public",
            vpc_id=self.vpc.id,
            subnet_ids=[subnet.id for subnet in self.subnets["public"]],
            tags=self.tags | {"Name": f"{self.name}-public"} | network_access_tags["public"],
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )
        # Allow traffic to VPC endpoints from within VPC
        aws.ec2.NetworkAclRule(
            f"{self.name}-public-internal-https",
            network_acl_id=self.nacls["public"].id,
            rule_number=1000,
            protocol="6",
            from_port=443,
            to_port=443,
            cidr_block=str(self.cidr_block),
            rule_action="allow",
            opts=pulumi.ResourceOptions(parent=self.nacls["public"]),
        )
        # Deny inbound traffic to SSH port
        aws.ec2.NetworkAclRule(
            f"{self.name}-public-ssh-deny",
            network_acl_id=self.nacls["public"].id,
            rule_number=9000,
            protocol="6",
            from_port=22,
            to_port=22,
            cidr_block="0.0.0.0/0",
            rule_action="deny",
            opts=pulumi.ResourceOptions(parent=self.nacls["public"]),
        )
        # Deny inbound traffic to RDP port
        aws.ec2.NetworkAclRule(
            f"{self.name}-public-rdp-deny",
            network_acl_id=self.nacls["public"].id,
            rule_number=9001,
            protocol="6",
            from_port=3389,
            to_port=3389,
            cidr_block="0.0.0.0/0",
            rule_action="deny",
            opts=pulumi.ResourceOptions(parent=self.nacls["public"]),
        )
        # Allows inbound return traffic from hosts on the internet that are responding to requests
        # originating in the subnet.
        # NB: this includes AWS API endpoints that cannot be created within the VPC, e.g. LakeFormation
        aws.ec2.NetworkAclRule(
            f"{self.name}-public-internal-ephemeral",
            network_acl_id=self.nacls["public"].id,
            rule_number=10000,
            protocol="6",
            from_port=1024,
            to_port=65535,
            cidr_block="0.0.0.0/0",
            rule_action="allow",
            opts=pulumi.ResourceOptions(parent=self.nacls["public"]),
        )
        # Allow communication to VPC endpoints and AWS endpoints that need to traverse the public internet
        # such as LakeFormation
        aws.ec2.NetworkAclRule(
            f"{self.name}-public-https-egress",
            network_acl_id=self.nacls["public"].id,
            egress=True,
            rule_number=1000,
            protocol="6",
            from_port=443,
            to_port=443,
            cidr_block="0.0.0.0/0",
            rule_action="allow",
            opts=pulumi.ResourceOptions(parent=self.nacls["public"]),
        )
        aws.ec2.NetworkAclRule(
            f"{self.name}-public-ephemeral-egress",
            network_acl_id=self.nacls["public"].id,
            egress=True,
            rule_number=10000,
            protocol="6",
            from_port=1024,
            to_port=65535,
            cidr_block="0.0.0.0/0",
            rule_action="allow",
            opts=pulumi.ResourceOptions(parent=self.nacls["public"]),
        )
        self.next_nacl_rule_ids["public"] = {True: 2000, False: 2000}

        self.nacls["private"] = aws.ec2.NetworkAcl(
            f"{self.name}-private",
            vpc_id=self.vpc.id,
            subnet_ids=[subnet.id for subnet in self.subnets["private"]],
            tags=self.tags | {"Name": f"{self.name}-private"} | network_access_tags["private"],
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )
        # Allows inbound return traffic through the NAT device in the public subnet for requests
        # originating in the private subnet. Needs to use the public internet because the NAT device
        # passes the external IP as the source address.
        aws.ec2.NetworkAclRule(
            f"{self.name}-private-ephemeral",
            network_acl_id=self.nacls["private"].id,
            rule_number=10000,  # Use a high rule number to allow for earlier deny rules
            protocol="6",
            from_port=1024,
            to_port=65535,
            cidr_block="0.0.0.0/0",
            rule_action="allow",
            opts=pulumi.ResourceOptions(parent=self.nacls["private"]),
        )
        # Allow communication to VPC endpoints and AWS endpoints that need to traverse the public internet
        # such as LakeFormation
        aws.ec2.NetworkAclRule(
            f"{self.name}-private-https-egress",
            network_acl_id=self.nacls["private"].id,
            egress=True,
            rule_number=1000,
            protocol="6",
            from_port=443,
            to_port=443,
            cidr_block="0.0.0.0/0",
            rule_action="allow",
            opts=pulumi.ResourceOptions(parent=self.nacls["private"]),
        )
        # Deny inbound traffic to SSH port
        aws.ec2.NetworkAclRule(
            f"{self.name}-private-ssh-deny",
            network_acl_id=self.nacls["private"].id,
            rule_number=9000,
            protocol="6",
            from_port=22,
            to_port=22,
            cidr_block="0.0.0.0/0",
            rule_action="deny",
            opts=pulumi.ResourceOptions(parent=self.nacls["private"]),
        )
        # Deny inbound traffic to RDP port
        aws.ec2.NetworkAclRule(
            f"{self.name}-private-rdp-deny",
            network_acl_id=self.nacls["private"].id,
            rule_number=9001,
            protocol="6",
            from_port=3389,
            to_port=3389,
            cidr_block="0.0.0.0/0",
            rule_action="deny",
            opts=pulumi.ResourceOptions(parent=self.nacls["private"]),
        )
        # Allows outbound responses to the public subnet (for example, responses to web servers in the
        # public subnet that are communicating with DB servers in the private subnet).
        aws.ec2.NetworkAclRule(
            f"{self.name}-private-internal-ephemeral-egress",
            network_acl_id=self.nacls["private"].id,
            egress=True,
            rule_number=10000,
            protocol="6",
            from_port=1024,
            to_port=65535,
            cidr_block=str(self.cidr_block),
            rule_action="allow",
            opts=pulumi.ResourceOptions(parent=self.nacls["private"]),
        )
        self.next_nacl_rule_ids["private"] = {True: 2000, False: 2000}

        self.vpc_endpoint_sg = aws.ec2.SecurityGroup(
            f"{self.name}-vpc-endpoint",
            description=f"{self.name} VPC endpoint",
            name_prefix=f"{self.name}-vpc-endpoint-",
            ingress=[
                aws.ec2.SecurityGroupIngressArgs(
                    from_port=443,
                    to_port=443,
                    protocol="tcp",
                    cidr_blocks=[str(self.cidr_block)],
                )
            ],
            vpc_id=self.vpc.id,
            tags=tags | {"Name": f"{self.name}-vpc-endpoint"},
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        self.register_outputs(
            {
                "cidr_block": str(self.cidr_block),
                "subnet_cidr_blocks": {k: [str(n) for n in v] for k, v in vars(self.subnet_cidr_blocks).items()},
                "azs": self.azs,
                "vpc_id": self.vpc.id,
                "public_subnet_ids": [sn.id.apply(str) for sn in self.subnets["public"]],
                "private_subnet_ids": [sn.id.apply(str) for sn in self.subnets["private"]],
                "public_network_acl": self.nacls["public"].id,
                "private_network_acl": self.nacls["private"].id,
                "vpc_endpoint_sg": self.vpc_endpoint_sg.id,
            }
        )

    @staticmethod
    def create_flow_logs_role(
        name: str = "FlowLogs", permissions_boundary: str | None = None, opts: pulumi.ResourceOptions | None = None
    ):
        if opts is None:
            opts = pulumi.ResourceOptions()

        assume_role_policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["sts:AssumeRole"],
                    principals=[
                        aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                            type="Service", identifiers=["vpc-flow-logs.amazonaws.com"]
                        )
                    ],
                )
            ]
        )
        policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "logs:CreateLogGroup",
                        "logs:CreateLogStream",
                        "logs:PutLogEvents",
                        "logs:DescribeLogGroups",
                        "logs:DescribeLogStreams",
                    ],
                    resources=["*"],
                )
            ]
        )
        role = aws.iam.Role(
            name,
            name=name,
            assume_role_policy=assume_role_policy.json,
            permissions_boundary=permissions_boundary,
            opts=pulumi.ResourceOptions.merge(
                opts,
                pulumi.ResourceOptions(aliases=[pulumi.Alias(name="flow-logs-role")]),
            ),
        )

        aws.iam.RolePolicy(f"{name}-role-policy", name=f"{name}-role-policy", role=role.id, policy=policy.json)

        return role

    def with_flow_log(
        self,
        permissions_boundary: str | None = None,
        role_arn: str | None = None,
        existing_flow_log_target_arns: list[str] | None = None,
    ):
        """
        Enable VPC Flow Logs

        :param role_arn: The arn of the flow logs role to use
        If role_arn is None, a new FlowLogs role will be created automatically

        :return:
        """
        # TODO: consider extracting the LogGroup and Role to a StackSet so that they may be global-ish

        flow_logs_group = aws.cloudwatch.LogGroup(
            f"{self.name}-flow-logs-group",
            name=f"{self.name}-VPCFlowLogs",
            # TODO: add KMS key id
            retention_in_days=30,
            tags=self.tags,
            opts=pulumi.ResourceOptions(parent=self),
        )

        fields = [
            "version",
            "account-id",
            "vpc-id",
            "subnet-id",
            "interface-id",
            "flow-direction",
            "action",
            "srcaddr",
            "srcport",
            "dstaddr",
            "dstport",
            "protocol",
            "packets",
            "bytes",
            "start",
            "end",
            "log-status",
        ]

        # We want to handle the case where an existing flow log target ARN is provided.
        # If it is provided, we will use it as an additional log destination.
        # If not, we will only use the newly created flow logs group.
        # This allows for flexibility in the use of existing flow log targets.
        log_destinations = []
        if existing_flow_log_target_arns:
            log_destinations.extend(existing_flow_log_target_arns)

        log_destinations.append(flow_logs_group.arn)
        # If no role_arn is provided, create a new FlowLogs role
        if not role_arn:
            role_arn = AWSVpc.create_flow_logs_role(
                name=f"{self.name}-flow-logs-iam-role.posit.team",
                permissions_boundary=permissions_boundary,
                opts=pulumi.ResourceOptions(parent=self),
            ).arn

        for log_destination in log_destinations:
            aws.ec2.FlowLog(
                f"{self.name}-flow-log",
                iam_role_arn=role_arn,
                log_destination=log_destination,
                traffic_type="ALL",
                max_aggregation_interval=60,
                log_format=" ".join([f"${{{field}}}" for field in fields]),
                vpc_id=self.vpc.id,
                tags=self.tags | {"Name": f"{self.name}"},
                opts=pulumi.ResourceOptions(parent=self.vpc),
            )

        return self

    def with_endpoint(
        self,
        service: str,
        security_group_ids: list[pulumi.Output[str]] | list[str] | None = None,
    ):
        """
        All the endpoint services:
         https://docs.aws.amazon.com/vpc/latest/privatelink/aws-services-privatelink-support.html

        Security Hub recommends always creating the ec2 endpoint:
         https://docs.aws.amazon.com/securityhub/latest/userguide/securityhub-standards-fsbp-controls.html#ec2-10-remediation

        Note: in order for Systems Manager and Session Manager to properly function, endpoints for ssm, ec2messages,
        ssmmessages and kms ought to be created.
        See https://docs.aws.amazon.com/systems-manager/latest/userguide/setup-create-vpc.html#sysman-setting-up-vpc-create

        :param service:
        :param security_group_ids: An optional list of security group ids to associate with an interface endpoint.
        defaults to the vpc_endpoint_sg created in __init__.  This opens it on port 443 to all IPs in the cidr_block
        :return:
        """
        endpoint_type = "Gateway" if service in ("s3", "dynamodb") else "Interface"
        svc = aws.ec2.get_vpc_endpoint_service(
            service=service,
            service_type=endpoint_type,
            opts=pulumi.InvokeOptions(parent=self.vpc),
        )

        args = aws.ec2.VpcEndpointArgs(
            service_name=svc.service_name,
            vpc_endpoint_type=svc.service_type,
            vpc_id=self.vpc.id,
            tags=self.tags | {"Name": f"{self.name}-{service}"},
        )

        if svc.service_type == "Gateway":
            route_table_ids = [rt.id for rt in self.private_route_tables]
            route_table_ids.append(self.public_route_table.id)
            args.route_table_ids = route_table_ids
        else:
            args.private_dns_enabled = True

            args.security_group_ids = [self.vpc_endpoint_sg.id] if security_group_ids is None else security_group_ids

            args.subnet_ids = [subnet.id for subnet in self.subnets["public"]]

        aws.ec2.VpcEndpoint(
            f"{self.name}-{service}",
            args,
            opts=pulumi.ResourceOptions(parent=self.vpc),
        )

        return self

    def with_nat_gateways(self):
        self.eips_by_az = {}
        self.nat_gw_public_ips = []

        for i, subnet in enumerate(self.subnets["public"]):
            number = i + 1

            if (
                aws._utilities._version.major  # noqa: SLF001
                >= AWS_UTILITIES_CUTOFF_VERSION_MAJOR
            ):
                args = aws.ec2.EipArgs(
                    domain="vpc",
                    tags=self.tags | {"Name": f"{self.name}-az{number}"},
                )
            else:
                args = aws.ec2.EipArgs(
                    vpc=True,
                    tags=self.tags | {"Name": f"{self.name}-az{number}"},
                )
            eip = aws.ec2.Eip(
                f"{self.name}-az{number}",
                args,
                opts=pulumi.ResourceOptions(parent=self.vpc),
            )

            def generate_set_eip_by_az(i: int) -> typing.Callable:
                def set_eip_by_az(eip_public_ip: str):
                    self.eips_by_az[i] = eip_public_ip

                return set_eip_by_az

            eip.public_ip.apply(generate_set_eip_by_az(i))

            ng = aws.ec2.NatGateway(
                f"{self.name}-az{number}",
                subnet_id=subnet.id,
                allocation_id=eip.id,
                tags=self.tags | {"Name": f"{self.name}-az{number}"},
                opts=pulumi.ResourceOptions(
                    parent=self.vpc,
                    delete_before_replace=True,
                ),
            )

            self.nat_gw_public_ips.append(ng.public_ip)

            aws.ec2.Route(
                f"{self.name}-nat-az{number}",
                route_table_id=self.private_route_tables[i].id,
                nat_gateway_id=ng.id,
                destination_cidr_block="0.0.0.0/0",
                opts=pulumi.ResourceOptions(parent=self.private_route_tables[i]),
            )

        return self

    def with_nacl_rule(
        self,
        port_range: int | range | tuple[int, int],
        cidr_blocks: list[str],
        privacy: str = "public",
        protocol: str = "tcp",
        rule_action: str = "allow",
        *,
        egress: bool = False,
        include_deny: bool = False,
    ):
        """
        Add NACL rules allowing a port range for a number of CIDR blocks to either ingress or egress.
        Optionally add a rule for the port range to/from other destinations/sources to deny ingress after the allows.
        :param port_range: a single port number or either a range or tuple representing the from and to (inclusive)
        :param cidr_blocks: a sequence of CIDR blocks to create NACL rules
        :param privacy: which NACL to modify
        :param egress: True if the rule is an egress rule, otherwise it is an ingress rule. defaults to False.
        :param protocol: 'tcp' or 'udp'. defaults to 'tcp'.
        :param rule_action: Whether to allow or deny traffic. defaults to 'allow'
        :param include_deny: Whether to add a rule to deny ingress for destinations/sources other than the CIDR blocks.
        :return:
        """
        next_rule_ids = self.next_nacl_rule_ids[privacy]
        rule_id = next_rule_ids[egress]

        nacl = self.nacls[privacy]

        if protocol == "tcp":
            protocol = "6"
        elif protocol == "udp":
            protocol = "17"
        elif protocol == "all":
            protocol = "-1"

        if isinstance(port_range, int):
            from_port = to_port = port_range
        else:
            from_port = port_range[0]
            to_port = port_range[-1]

        name_prefix = f"{'public' if privacy == 'public' else 'private'}-{'egress' if egress else 'ingress'}-rule"
        for cidr_block in cidr_blocks:
            aws.ec2.NetworkAclRule(
                f"{self.name}-{name_prefix}-{rule_id}",
                network_acl_id=nacl.id,
                rule_number=rule_id,
                egress=egress,
                protocol=protocol,
                rule_action=rule_action,
                cidr_block=cidr_block,
                from_port=from_port,
                to_port=to_port,
                opts=pulumi.ResourceOptions(parent=nacl, delete_before_replace=True),
            )
            rule_id = rule_id + 1

        if include_deny and not egress:
            aws.ec2.NetworkAclRule(
                f"{self.name}-{name_prefix}-{rule_id}",
                network_acl_id=nacl.id,
                rule_number=rule_id,
                egress=False,
                protocol=protocol,
                rule_action="deny",
                cidr_block="0.0.0.0/0",
                from_port=from_port,
                to_port=to_port,
                opts=pulumi.ResourceOptions(parent=nacl, delete_before_replace=True),
            )

        self.next_nacl_rule_ids[privacy][egress] = self.next_nacl_rule_ids[privacy][egress] + 500

        return self

    def with_secure_default_security_group(self):
        """
        Manage the default security group by removing its ingress and egress rules in order to comply with
        Security Hub control EC2.2

        https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-2
        :return:
        """
        aws.ec2.DefaultSecurityGroup(
            f"{self.name}-default",
            vpc_id=self.vpc.id,
            opts=pulumi.ResourceOptions(parent=self),
        )

        return self

    def with_secure_default_nacl(self):
        """
        Manage the default network acl by removing its ingress and egress rules in order to comply with
        Security Hub control EC2.21

        https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-21
        :return:
        """
        aws.ec2.DefaultNetworkAcl(
            f"{self.name}-default",
            default_network_acl_id=self.vpc.default_network_acl_id,
            opts=pulumi.ResourceOptions(parent=self),
        )

        return self

    @staticmethod
    def lookup_route_tables_for_subnets(vpc_id: str, subnet_ids: list[str]) -> list[str]:
        """
        Look up route tables associated with the given subnet IDs in a VPC.

        :param vpc_id: The VPC ID to search within
        :param subnet_ids: List of subnet IDs to find route tables for
        :return: List of route table IDs associated with the subnets
        """
        route_table_ids = []
        route_tables = aws.ec2.get_route_tables(vpc_id=vpc_id)

        for table_id in route_tables.ids:
            table = aws.ec2.get_route_table(route_table_id=table_id)
            for assoc in table.associations:
                if assoc.subnet_id in subnet_ids:
                    route_table_ids.append(table.id)
                    break

        return route_table_ids

    def _init_with_existing_vpc(
        self,
        existing_vpc_id: str,
        existing_private_subnet_ids: list[str],
        cidr_block: str,
    ):
        """Initialize the VPC object using existing AWS resources."""

        self.cidr_block = typing.cast(ipaddress.IPv4Network, ipaddress.ip_network(cidr_block))
        self.subnet_cidr_blocks = ptd.SubnetCIDRBlocks.from_cidr_block(self.cidr_block)

        # Look up the default NACL for this VPC
        default_nacls = aws.ec2.get_network_acls(
            filters=[
                aws.ec2.GetNetworkAclsFilterArgs(name="vpc-id", values=[existing_vpc_id]),
                aws.ec2.GetNetworkAclsFilterArgs(name="default", values=["true"]),
            ]
        )

        # Create a reference to the existing VPC (not a new resource)
        class ExistingVpcReference(pulumi.ComponentResource):
            def __init__(self, vpc_id, default_nacl_id, parent):
                super().__init__(
                    "ptd:ExistingVpcReference", f"existing-vpc-{vpc_id}", None, pulumi.ResourceOptions(parent=parent)
                )
                self.id = vpc_id
                self.default_network_acl_id = default_nacl_id

        default_nacl_id = default_nacls.ids[0] if default_nacls.ids else None
        self.vpc = ExistingVpcReference(existing_vpc_id, default_nacl_id, self)

        # Look up existing subnets
        self.subnets = {"private": [], "public": []}

        # Create subnet references for private subnets
        for subnet_id in existing_private_subnet_ids:

            class ExistingSubnetReference:
                def __init__(self, subnet_id):
                    self.id = subnet_id

            self.subnets["private"].append(ExistingSubnetReference(subnet_id))

        # Look up existing route tables associated with private subnets using the static method
        self.private_route_tables = []
        route_table_ids = self.lookup_route_tables_for_subnets(existing_vpc_id, existing_private_subnet_ids)

        for route_table_id in route_table_ids:

            class ExistingRouteTableReference:
                def __init__(self, route_table_id):
                    self.id = route_table_id

            self.private_route_tables.append(ExistingRouteTableReference(route_table_id))

        # Create VPC endpoint security group for endpoints
        self.vpc_endpoint_sg = aws.ec2.SecurityGroup(
            f"{self.name}-vpc-endpoint",
            description=f"{self.name} VPC endpoint",
            name_prefix=f"{self.name}-vpc-endpoint-",
            ingress=[
                aws.ec2.SecurityGroupIngressArgs(
                    from_port=443,
                    to_port=443,
                    protocol="tcp",
                    cidr_blocks=[str(self.cidr_block)],
                )
            ],
            vpc_id=existing_vpc_id,
            tags=self.tags | {"Name": f"{self.name}-vpc-endpoint"},
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Initialize attributes that other methods expect
        self.nacls = {}
        self.next_nacl_rule_ids = {}
        self.public_route_table = None
        self.nat_gw_public_ips = []
        self.eips_by_az = {}

        self.register_outputs(
            {
                "cidr_block": str(self.cidr_block),
                "vpc_id": existing_vpc_id,
                "private_subnet_ids": existing_private_subnet_ids,
                "vpc_endpoint_sg": self.vpc_endpoint_sg.id,
            }
        )
