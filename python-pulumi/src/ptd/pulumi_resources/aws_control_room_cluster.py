from __future__ import annotations

import typing

import pulumi
import pulumi_aws as aws

import ptd
import ptd.aws_control_room
import ptd.aws_workload
import ptd.azure_workload
import ptd.pulumi_resources.aws_eks_cluster
import ptd.pulumi_resources.aws_vpc
import ptd.pulumi_resources.traefik
import ptd.secrecy


class AWSControlRoomCluster(pulumi.ComponentResource):
    required_tags: dict[str, str]
    control_room: ptd.aws_control_room.AWSControlRoom
    name: str
    control_room_secret: ptd.secrecy.AWSControlRoomSecret

    eks: ptd.pulumi_resources.aws_eks_cluster.AWSEKSCluster

    @classmethod
    def autoload(cls) -> AWSControlRoomCluster:
        return cls(ptd.aws_control_room.AWSControlRoom(pulumi.get_stack()))

    def __init__(
        self,
        control_room: ptd.aws_control_room.AWSControlRoom,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            control_room.compound_name,
            *args,
            **kwargs,
        )

        self.control_room = control_room
        self.name = self.control_room.compound_name

        self.required_tags = self.control_room.required_tags | {
            str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
        }

        control_room_secret, ok = ptd.secrecy.aws_get_secret_value_json(
            f"{self.name}.ctrl.posit.team", region=self.control_room.cfg.region
        )

        if not ok:
            msg = "Failed to fetch control room secret"
            pulumi.error(msg)
            raise ValueError(msg)

        self.control_room_secret = typing.cast(
            ptd.secrecy.AWSControlRoomSecret,
            control_room_secret,
        )

        self._define_eks()

    def _define_eks(self) -> None:
        self.eks = ptd.pulumi_resources.aws_eks_cluster.AWSEKSCluster(
            name=self.name,
            enabled_cluster_log_types={
                "api",
                "audit",
                "authenticator",
                "controllerManager",
                "scheduler",
            },
            sg_prefix=self.name,
            subnet_ids=[
                s["SubnetId"]
                for s in ptd.aws_subnets_for_vpc(
                    self.name,
                    network_access="private",
                    region=self.control_room.cfg.region,
                )
            ],
            version=self.control_room.cfg.eks_k8s_version,
            tags=self.required_tags | {"Name": self.name},
            tailscale_enabled=self.control_room.cfg.tailscale_enabled,
            protect_persistent_resources=self.control_room.cfg.protect_persistent_resources,
            opts=pulumi.ResourceOptions(parent=self),
        )

        launch_template = aws.ec2.LaunchTemplate(
            self.name,
            tags=self.required_tags | {"Name": self.name},
            tag_specifications=[
                {
                    "resourceType": "instance",
                    "tags": self.required_tags,
                },
                {
                    "resourceType": "volume",
                    "tags": self.required_tags,
                },
            ],
            instance_type=self.control_room.cfg.eks_node_instance_type,
            metadata_options=aws.ec2.LaunchTemplateMetadataOptionsArgs(
                http_endpoint="enabled",
                http_tokens="required",
                http_put_response_hop_limit=2,
            ),
            block_device_mappings=[
                aws.ec2.LaunchTemplateBlockDeviceMappingArgs(
                    device_name="/dev/xvda",
                    ebs=aws.ec2.LaunchTemplateBlockDeviceMappingEbsArgs(
                        volume_type="gp3",
                    ),
                ),
            ],
            opts=pulumi.ResourceOptions(parent=self.eks.eks),
        )

        self.eks.with_node_role()
        self._define_node_iam()

        self.eks.with_node_group(
            name=self.name,
            launch_template=launch_template,
            tags=self.required_tags | {"Name": self.name},
            desired=self.control_room.cfg.eks_node_group_max,
            min_nodes=self.control_room.cfg.eks_node_group_min,
            max_nodes=self.control_room.cfg.eks_node_group_max,
            version=self.control_room.cfg.eks_k8s_version,
            ami_type="AL2023_x86_64_STANDARD",
        )

        self.eks.with_aws_auth(
            use_eks_access_entries=self.control_room.cfg.eks_access_entries.enabled,
            additional_access_entries=self.control_room.cfg.eks_access_entries.additional_entries,
            include_poweruser=self.control_room.cfg.eks_access_entries.include_same_account_poweruser,
        )

        self.eks.with_gp3()

        self.eks.with_oidc_provider()

        domain = self.control_room.cfg.domain
        wildcard_domain = f"*.{domain}"
        front_door_domain = self.control_room.cfg.front_door or ""
        wildcard_front_door_domain = f"*.{front_door_domain}"
        parent_zone: aws.route53.AwaitableGetZoneResult

        if self.control_room.cfg.hosted_zone_id is not None and self.control_room.cfg.hosted_zone_id != "":
            parent_zone = aws.route53.get_zone(
                zone_id=self.control_room.cfg.hosted_zone_id,
                private_zone=False,
            )
        else:
            parent_zone = aws.route53.get_zone(
                name=(domain.split(".", 1)[-1].removesuffix(".") + "."),
                private_zone=False,
            )

        alt_domains = [wildcard_domain]
        if front_door_domain != "":
            alt_domains.extend([front_door_domain, wildcard_front_door_domain])

        cert = aws.acm.Certificate(
            f"{self.name}-{domain}",
            domain_name=domain,
            subject_alternative_names=alt_domains,
            validation_method="DNS",
            tags=self.required_tags | {"Name": f"{self.name}-{domain}"},
            opts=pulumi.ResourceOptions(parent=self),
        )

        # NOTE: The input type here should be
        # `pulumi.Output[list[aws.acm.CertificateDomainValidationOption]]` but the
        # innermost type is seemingly not defined at static analysis time (?)
        def extract_dvo_records(dvos) -> list[aws.route53.Record]:
            cert_dvos = set()

            records: list[aws.route53.Record] = []

            for dvo in dvos:
                if dvo.resource_record_name in cert_dvos:
                    continue

                records.append(
                    aws.route53.Record(
                        f"{self.name}-{dvo.resource_record_name}",
                        name=dvo.resource_record_name,
                        records=[dvo.resource_record_value],
                        ttl=300,
                        type=dvo.resource_record_type,
                        zone_id=parent_zone.zone_id,
                        opts=pulumi.ResourceOptions(
                            parent=cert,
                        ),
                    )
                )

                cert_dvos.add(dvo.resource_record_name)

            return records

        records = cert.domain_validation_options.apply(extract_dvo_records)

        aws.acm.CertificateValidation(
            self.name,
            certificate_arn=cert.arn,
            validation_record_fqdns=records.apply(lambda r: [record.fqdn for record in r]),
            opts=pulumi.ResourceOptions(parent=cert),
        )

        traefik = ptd.pulumi_resources.traefik.Traefik(
            self.eks,
            "default",
            "",
            cert,
            deployment_replicas=self.control_room.cfg.traefik_deployment_replicas,
            opts=pulumi.ResourceOptions(
                parent=self.eks,
                provider=self.eks.provider,
                protect=self.control_room.cfg.protect_persistent_resources,
            ),
        )

        def define_domains_for_zone(
            zones: list[aws.route53.Zone | aws.route53.GetZoneResult],
        ) -> None:
            domains_to_cnames: dict[str, str] = {domain: "", wildcard_domain: ""}
            if front_door_domain != "":
                domains_to_cnames[domain] = front_door_domain
                domains_to_cnames[wildcard_domain] = wildcard_front_door_domain

            # NOTE: This fails in an unrecoverable way when `svc/traefik` is not
            # available, even during preview, even though `depends_on` is in use.
            # Maybe this bit should be moved to a separate stack that runs later?
            traefik.define_domains(
                domains_to_cnames,
                zones[0],
            )

        pulumi.Output.all(parent_zone).apply(define_domains_for_zone)  # type: ignore

        self.eks.with_ebs_csi_driver(version=self.control_room.cfg.ebs_csi_addon_version)

        self.eks.with_aws_lbc()

        self.eks.with_metrics_server(version=self.control_room.cfg.metrics_server_version)

        self.eks.with_secret_store_csi(version=self.control_room.cfg.secret_store_csi_version)

        self.eks.with_secret_store_csi_aws_provider(version=self.control_room.cfg.secret_store_csi_aws_provider_version)

        # use front_door_domain if set to serve from a domain behind Okta auth
        desired_domain = front_door_domain if front_door_domain != "" else domain
        self.eks.with_traefik_forward_auth(
            domain=desired_domain,
            version=self.control_room.cfg.traefik_forward_auth_version,
            opts=pulumi.ResourceOptions(depends_on=traefik),
        )

        postgres_config_stack = pulumi.StackReference(
            f"organization/ptd-aws-control-room-postgres-config/{self.control_room.compound_name}"
        )
        db_connection_output = postgres_config_stack.require_output("db_grafana_connection")

        wl_account_ids = set()
        wls = self.control_room.workloads_index()

        # Collect account IDS and tenant IDs from AWS and Azure workloads respectively
        for k in wls:
            kind = wls[k]["kind"]
            if kind == ptd.aws_workload.AWSWorkloadConfig.__name__:
                wl = ptd.aws_workload.AWSWorkload(k)
                wl_account_ids.add(wl.cfg.account_id)
            elif kind == ptd.azure_workload.AzureWorkloadConfig.__name__:
                wl = ptd.azure_workload.AzureWorkload(k)
                wl_account_ids.add(wl.cfg.tenant_id)

        # sort the account IDs to ensure consistent ordering
        wl_account_ids = sorted(wl_account_ids)

        opsgenie_key = self.control_room_secret["opsgenie-api-key"]
        if not opsgenie_key:
            msg = "opsgenie-api-key secret not found in control room secret"
            pulumi.error(msg)
            raise ValueError(msg)

        self.eks.with_grafana(
            domain=desired_domain,
            db_connection_output=db_connection_output,
            opsgenie_key=opsgenie_key,
            wl_account_ids=wl_account_ids,
            version=self.control_room.cfg.grafana_version,
        )

        mimir_auth_secret, ok = ptd.secrecy.aws_get_secret_value_json(
            self.control_room.mimir_auth_secret, region=self.control_room.cfg.region
        )

        if not ok:
            msg = f"Failed to look up secret {self.control_room.mimir_auth_secret!r}"

            pulumi.error(msg, self)

            raise ValueError(msg)

        salt = self.control_room_secret["mimir-password-salt"]
        if not salt:
            msg = "mimir-password-salt secret not found in control room secret"
            pulumi.error(msg)
            raise ValueError(msg)

        self.eks.with_mimir(
            bucket_prefix=self.control_room.mimir_ruler_storage_bucket_prefix,
            domain=desired_domain,
            mimir_creds=typing.cast(dict[str, str], mimir_auth_secret),
            salt=salt,
            tags=self.required_tags,
            version=self.control_room.cfg.mimir_version,
        )

    def _define_node_iam(self):
        if self.eks.default_node_role is None:
            msg = "no default node role available"
            raise ValueError(msg)

        aws.iam.RolePolicyAttachment(
            f"{self.name}-eks-nodegroup-ssm",
            role=self.eks.default_node_role.name,
            policy_arn="arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
            opts=pulumi.ResourceOptions(parent=self.eks.default_node_role),
        )
