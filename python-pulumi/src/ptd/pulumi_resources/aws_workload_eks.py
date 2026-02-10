import pulumi
import pulumi_aws

import ptd
import ptd.aws_workload
import ptd.pulumi_resources.aws_eks_cluster
import ptd.pulumi_resources.tigera_operator


class AWSWorkloadEKS(pulumi.ComponentResource):
    eks_clusters: dict[str, ptd.pulumi_resources.aws_eks_cluster.AWSEKSCluster]
    launch_templates: dict[str, dict[str, pulumi_aws.ec2.LaunchTemplate]]

    @classmethod
    def autoload(cls) -> "AWSWorkloadEKS":
        return cls(workload=ptd.aws_workload.AWSWorkload(pulumi.get_stack()))

    def __init__(self, workload: ptd.aws_workload.AWSWorkload, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        self.eks_clusters = {}
        self.launch_templates = {}
        self.workload = workload
        self.required_tags = self.workload.required_tags | {
            str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
        }

        for release in self.workload.cfg.clusters:
            self._define_eks_cluster(release)

    def _define_eks_cluster(self, cluster_release: str):
        cluster_cfg = self.workload.cfg.clusters[cluster_release]

        full_name = f"{self.workload.compound_name}-{cluster_release}"
        subnets = self.workload.subnets("private")

        eks_cluster = ptd.pulumi_resources.aws_eks_cluster.AWSEKSCluster(
            name=full_name,
            default_addons_to_remove=["vpc-cni"],
            enabled_cluster_log_types={
                "api",
                "audit",
                "authenticator",
                "controllerManager",
                "scheduler",
            },
            sg_prefix=self.workload.compound_name,
            subnet_ids=[s["SubnetId"] for s in subnets],
            tags=self.required_tags,
            tailscale_enabled=self.workload.cfg.tailscale_enabled,
            customer_managed_bastion_id=self.workload.cfg.customer_managed_bastion_id,
            version=cluster_cfg.cluster_version,
            protect_persistent_resources=self.workload.cfg.protect_persistent_resources,
            eks_role_name=f"{full_name}-eks.posit.team",
            iam_permissions_boundary=self.workload.iam_permissions_boundary,
            opts=pulumi.ResourceOptions(parent=self),
        )
        self.eks_clusters[cluster_release] = eks_cluster

        eks_cluster.eks.vpc_config.apply(
            lambda config: self._build_with_vpc_config(
                full_name=full_name, vpc_config=config, cluster_release=cluster_release
            )
        )

    def _build_with_vpc_config(
        self, full_name: str, vpc_config: pulumi_aws.eks.outputs.ClusterVpcConfig, cluster_release: str
    ):
        fsx_sg_id, ok = ptd.aws_fsx_nfs_sg_id(vpc_config.vpc_id, region=self.workload.cfg.region)
        security_group_ids = [fsx_sg_id]

        # Add EFS security group if EFS is enabled for this cluster
        cluster_cfg = self.workload.cfg.clusters[cluster_release]
        if cluster_cfg.enable_efs_csi_driver or cluster_cfg.efs_config is not None:
            efs_sg_id, efs_ok = ptd.aws_efs_nfs_sg_id(vpc_config.vpc_id, region=self.workload.cfg.region)
            if efs_ok:
                security_group_ids.append(efs_sg_id)

        if vpc_config.cluster_security_group_id:
            security_group_ids.append(vpc_config.cluster_security_group_id)

        if vpc_config.security_group_ids:
            security_group_ids.extend(vpc_config.security_group_ids)

        # Initialize launch templates for this cluster release
        self.launch_templates[cluster_release] = {}

        # Create the main/default node group FIRST - nodes register with API server
        # even without CNI (kubelet→API is direct, not through pod network)
        self._create_node_group(
            cluster_release=cluster_release,
            node_group_name="default",
            full_name=full_name,
            security_group_ids=security_group_ids,
            instance_type=cluster_cfg.mp_instance_type,
            volume_size=cluster_cfg.root_disk_size,
            ami_type=cluster_cfg.ami_type,
            min_size=cluster_cfg.mp_min_size,
            max_size=cluster_cfg.mp_max_size,
            desired_size=cluster_cfg.mp_min_size,
        )

        # Create additional node groups if configured
        if cluster_cfg.additional_node_groups:
            for ng_name, ng_config in cluster_cfg.additional_node_groups.items():
                self._create_node_group(
                    cluster_release=cluster_release,
                    node_group_name=ng_name,
                    full_name=f"{full_name}-{ng_name}",
                    security_group_ids=security_group_ids + ng_config.additional_security_group_ids,
                    instance_type=ng_config.instance_type,
                    volume_size=ng_config.additional_root_disk_size,
                    ami_type=ng_config.ami_type
                    or cluster_cfg.ami_type,  # Use node group AMI type if specified, otherwise cluster default
                    min_size=ng_config.min_size,
                    max_size=ng_config.max_size,
                    desired_size=ng_config.desired_size
                    or ng_config.min_size,  # Use desired_size if specified, otherwise min_size
                    taints=ng_config.taints,
                    labels=ng_config.labels,
                )

        # Install Tigera/Calico CNI - runs in parallel with node group creation
        # The operator uses hostNetwork so it can schedule on NotReady nodes
        self._define_tigera_operator(cluster_release)

        eks_cluster = self.eks_clusters[cluster_release]
        eks_cluster.with_aws_auth(
            use_eks_access_entries=cluster_cfg.eks_access_entries.enabled,
            additional_access_entries=cluster_cfg.eks_access_entries.additional_entries,
            include_poweruser=cluster_cfg.eks_access_entries.include_same_account_poweruser,
        )
        eks_cluster.with_ebs_csi_driver(
            role_name=f"{full_name}-ebs-csi-driver.posit.team",
            version=cluster_cfg.ebs_csi_addon_version,
        )

        if cluster_cfg.enable_efs_csi_driver:
            eks_cluster.with_efs_csi_driver(role_name=f"{full_name}-efs-csi-driver.posit.team")

            # Attach security group to EFS mount targets if configured
            if cluster_cfg.efs_config is not None:
                efs_sg_id, efs_sg_ok = ptd.aws_efs_nfs_sg_id(vpc_config.vpc_id, region=self.workload.cfg.region)
                if efs_sg_ok:
                    eks_cluster.attach_efs_security_group(
                        efs_file_system_id=cluster_cfg.efs_config.file_system_id,
                        security_group_id=efs_sg_id,
                        mount_targets_managed=cluster_cfg.efs_config.mount_targets_managed,
                        region=self.workload.cfg.region,
                    )

        if self.workload.cfg.secrets_store_addon_enabled:
            eks_cluster.with_aws_secrets_store_csi_driver_provider()

        eks_cluster.with_gp3()
        eks_cluster.with_encrypted_ebs_storage_class()
        eks_cluster.with_oidc_provider()

    def _create_node_group(
        self,
        cluster_release: str,
        node_group_name: str,
        full_name: str,
        security_group_ids: list[str],
        instance_type: str,
        volume_size: int,
        ami_type: str,
        min_size: int,
        max_size: int,
        desired_size: int,
        taints: list[ptd.Taint] | None = None,
        labels: dict[str, str] | None = None,
        depends_on: list[pulumi.Resource] | None = None,
    ):
        """Create a launch template and node group with the specified configuration."""
        cluster_cfg = self.workload.cfg.clusters[cluster_release]

        # Create launch template for this node group
        launch_template = pulumi_aws.ec2.LaunchTemplate(
            full_name,
            metadata_options=pulumi_aws.ec2.LaunchTemplateMetadataOptionsArgs(  # enforce IMDSv2
                http_endpoint="enabled",
                http_put_response_hop_limit=2,
                http_tokens="required",
                instance_metadata_tags="disabled",
            ),
            block_device_mappings=[
                pulumi_aws.ec2.LaunchTemplateBlockDeviceMappingArgs(
                    device_name="/dev/xvda",
                    ebs=pulumi_aws.ec2.LaunchTemplateBlockDeviceMappingEbsArgs(
                        volume_size=volume_size, volume_type="gp3", delete_on_termination=True
                    ),
                )
            ],
            tags=self.required_tags | {"Name": full_name},
            tag_specifications=[
                {
                    "resourceType": "instance",
                    "tags": self.required_tags | {"Name": full_name},
                },
                {
                    "resourceType": "volume",
                    "tags": self.required_tags | {"Name": full_name},
                },
            ],
            instance_type=instance_type,
            vpc_security_group_ids=security_group_ids,
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Store the launch template
        self.launch_templates[cluster_release][node_group_name] = launch_template

        # Create the node group
        eks_cluster = self.eks_clusters[cluster_release]

        # Convert ptd.Taint objects to AWS EKS taint format if provided
        eks_taints = None
        if taints:
            eks_taints = []
            for taint in taints:
                eks_taints.append(
                    {
                        "key": taint["key"],
                        "value": taint["value"],
                        "effect": taint["effect"],
                    }
                )

        # Set up node role if it's the first node group (default)
        if node_group_name == "default":
            eks_cluster.with_node_role(role_name=f"{self.workload.compound_name}-{cluster_release}-eks-node.posit.team")

        # Add the node group to the cluster
        eks_cluster.with_node_group(
            name=full_name,
            launch_template=launch_template,
            tags=self.required_tags | (labels or {}),
            ami_type=ami_type,
            desired=desired_size,
            min_nodes=min_size,
            max_nodes=max_size,
            version=cluster_cfg.cluster_version,
            taints=eks_taints,
            depends_on=depends_on,
        )

    def _define_tigera_operator(
        self,
        release: str,
        depends_on: list[pulumi.Resource] | None = None,
    ) -> ptd.pulumi_resources.tigera_operator.TigeraOperator:
        """Create Tigera/Calico CNI operator.

        Args:
            release: The cluster release identifier.
            depends_on: Resources that must be created before the operator (e.g., node groups).

        Returns:
            The TigeraOperator so it can be used as a dependency.
        """
        kube_provider = self.eks_clusters[release].provider
        return ptd.pulumi_resources.tigera_operator.TigeraOperator(
            name=self.workload.compound_name,
            release=release,
            opts=pulumi.ResourceOptions(
                parent=self,
                provider=kube_provider,
                depends_on=depends_on,
            ),
        )
