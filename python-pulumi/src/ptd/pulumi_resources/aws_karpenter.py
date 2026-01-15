import typing

import pulumi
import pulumi_aws as aws
import pulumi_kubernetes as kubernetes
import yaml

import ptd
import ptd.aws_workload


class AWSKarpenter(pulumi.ComponentResource):
    """
    AWS Karpenter component resource for EKS cluster autoscaling.
    """

    karpenter_node_roles: dict[str, aws.iam.Role]
    karpenter_node_instance_profiles: dict[str, aws.iam.InstanceProfile]
    karpenter_controller_roles: dict[str, aws.iam.Role]
    autoscaling_queues: dict[str, aws.sqs.Queue]

    def __init__(
        self,
        workload: ptd.aws_workload.AWSWorkload,
        managed_clusters: list[dict[str, typing.Any]],
        managed_clusters_by_release: dict[str, dict[str, typing.Any]],
        kube_providers: dict[str, kubernetes.Provider],
        required_tags: dict[str, str],
        define_k8s_iam_role_func: typing.Callable,
        parent: pulumi.ComponentResource,
        *,
        use_eks_access_entries: bool = False,
        use_eks_access_entries_by_release: dict[str, bool] | None = None,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-karpenter",
            **kwargs,
        )

        self.workload = workload
        self.managed_clusters = managed_clusters
        self.managed_clusters_by_release = managed_clusters_by_release
        self.kube_providers = kube_providers
        self.required_tags = required_tags
        self._define_k8s_iam_role = define_k8s_iam_role_func
        self.parent_resource = parent
        self.use_eks_access_entries = use_eks_access_entries
        # Store per-release configuration if provided
        self.use_eks_access_entries_by_release = use_eks_access_entries_by_release or {}

        # Initialize the dictionaries
        self.karpenter_node_roles = {}
        self.karpenter_node_instance_profiles = {}
        self.karpenter_controller_roles = {}
        self.autoscaling_queues = {}

        # Set up Karpenter for all clusters
        self._define_karpenter_iam()
        self._define_autoscaling_sqs_queues()

        for release in self.managed_clusters_by_release:
            cluster_name = f"{self.workload.compound_name}-{release}"
            # Check per-release configuration first, then fall back to global setting
            use_access_entries = self.use_eks_access_entries_by_release.get(release, self.use_eks_access_entries)
            if use_access_entries:
                self._define_karpenter_access_entry(cluster_name, release)
            else:
                self._define_karpenter_aws_auth(cluster_name, release)

    def _define_karpenter_iam(self) -> None:
        """
        Create IAM roles and policies required for Karpenter operation.
        """
        for release in self.managed_clusters_by_release:
            cluster_name = f"{self.workload.compound_name}-{release}"

            # Create KarpenterNodeRole for EC2 instances
            # This role needs to be assumable by EC2 service, not IRSA, so we create it directly
            node_role_name = f"KarpenterNodeRole-{cluster_name}.posit.team"

            node_assume_role_policy = aws.iam.get_policy_document(
                statements=[
                    aws.iam.GetPolicyDocumentStatementArgs(
                        effect="Allow",
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

            self.karpenter_node_roles[release] = aws.iam.Role(
                f"{node_role_name}-{release}",
                aws.iam.RoleArgs(
                    name=node_role_name,
                    assume_role_policy=node_assume_role_policy.json,
                    permissions_boundary=self.workload.iam_permissions_boundary,
                    tags=self.required_tags,
                ),
                opts=pulumi.ResourceOptions(parent=self, delete_before_replace=True),
            )

            # Attach required policies to KarpenterNodeRole
            for idx, policy_arn in enumerate(
                [
                    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
                    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
                    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryPullOnly",
                    "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
                ]
            ):
                aws.iam.RolePolicyAttachment(
                    f"{node_role_name}-policy-{idx}",
                    role=self.karpenter_node_roles[release].name,
                    policy_arn=policy_arn,
                    opts=pulumi.ResourceOptions(
                        parent=self.karpenter_node_roles[release],
                        delete_before_replace=True,
                    ),
                )

            # Create instance profile for the KarpenterNodeRole
            instance_profile_name = f"KarpenterNodeInstanceProfile-{cluster_name}.posit.team"
            self.karpenter_node_instance_profiles[release] = aws.iam.InstanceProfile(
                f"{instance_profile_name}-{release}",
                aws.iam.InstanceProfileArgs(
                    name=instance_profile_name,
                    role=self.karpenter_node_roles[release].name,
                    tags=self.required_tags
                    | {
                        f"kubernetes.io/cluster/{cluster_name}": "owned",
                        "topology.kubernetes.io/region": self.workload.cfg.region,
                        "karpenter.k8s.aws/ec2nodeclass": cluster_name,
                    },
                ),
                opts=pulumi.ResourceOptions(
                    parent=self.karpenter_node_roles[release],
                    delete_before_replace=True,
                ),
            )

            # Create KarpenterControllerRole for IRSA using the standard method
            controller_role_name = f"KarpenterControllerRole-{cluster_name}.posit.team"

            # Get the OIDC URL for this cluster to determine if we can create the controller role
            cluster_oidc_url = None
            for cluster in self.managed_clusters:
                if cluster["cluster"]["name"] == cluster_name:
                    cluster_oidc_url = ptd.get_oidc_url(cluster)
                    break

            if cluster_oidc_url:
                # Create the Karpenter controller policy inline
                controller_policy_json = self._define_karpenter_controller_policy(cluster_name)

                self.karpenter_controller_roles[release] = self._define_k8s_iam_role(
                    name=controller_role_name,
                    release=release,
                    namespace=ptd.KARPENTER_NAMESPACE,
                    service_accounts=["karpenter"],
                    role_policies=[controller_policy_json],
                )

    def _define_autoscaling_sqs_queues(self) -> None:
        """
        Create SQS queues for autoscaling events, one per release.
        Includes EventBridge rules for various EC2 interruption events.
        """
        for release in self.managed_clusters_by_release:
            cluster_name = f"{self.workload.compound_name}-{release}"
            # Queue name should match cluster name for Karpenter auto-discovery
            queue_name = cluster_name

            # Create the SQS queue with SSE enabled
            self.autoscaling_queues[release] = aws.sqs.Queue(
                f"{queue_name}-interruption-queue",
                name=queue_name,
                message_retention_seconds=300,  # 5 minutes as per CloudFormation template
                sqs_managed_sse_enabled=True,  # Enable SSE
                tags=self.required_tags | {"Name": queue_name},
                opts=pulumi.ResourceOptions(
                    parent=self,
                    protect=self.workload.cfg.protect_persistent_resources,
                ),
            )

            queue_arn = self.autoscaling_queues[release].arn

            # Create SQS queue policy matching CloudFormation template
            queue_policy = aws.iam.get_policy_document(
                statements=[
                    # Allow EventBridge and SQS services to send messages
                    aws.iam.GetPolicyDocumentStatementArgs(
                        effect="Allow",
                        principals=[
                            aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                                type="Service",
                                identifiers=["events.amazonaws.com", "sqs.amazonaws.com"],
                            )
                        ],
                        actions=["sqs:SendMessage"],
                        resources=[queue_arn],
                    ),
                    # Deny HTTP requests (enforce HTTPS)
                    aws.iam.GetPolicyDocumentStatementArgs(
                        sid="DenyHTTP",
                        effect="Deny",
                        principals=[
                            aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                                type="*",
                                identifiers=["*"],
                            )
                        ],
                        actions=["sqs:*"],
                        resources=[queue_arn],
                        conditions=[
                            aws.iam.GetPolicyDocumentStatementConditionArgs(
                                test="Bool",
                                variable="aws:SecureTransport",
                                values=["false"],
                            ),
                        ],
                    ),
                ]
            )

            # Attach the policy to the queue
            aws.sqs.QueuePolicy(
                f"{queue_name}-interruption-queue-policy",
                queue_url=self.autoscaling_queues[release].url,
                policy=queue_policy.json,
                opts=pulumi.ResourceOptions(
                    parent=self.autoscaling_queues[release],
                    protect=self.workload.cfg.protect_persistent_resources,
                ),
            )

            # Create EventBridge rules for different interruption events
            self._create_eventbridge_rules(cluster_name, queue_arn)

    def _create_eventbridge_rules(self, cluster_name: str, queue_arn: pulumi.Output[str]) -> None:
        """
        Create EventBridge rules for various EC2 interruption events.
        Based on the official AWS CloudFormation template for Karpenter.
        """
        # Scheduled Change Rule - AWS Health Events
        scheduled_change_rule = aws.cloudwatch.EventRule(
            f"{cluster_name}-scheduled-change-rule",
            event_pattern=pulumi.Output.json_dumps({"source": ["aws.health"], "detail-type": ["AWS Health Event"]}),
            opts=pulumi.ResourceOptions(parent=self),
        )

        aws.cloudwatch.EventTarget(
            f"{cluster_name}-scheduled-change-target",
            rule=scheduled_change_rule.name,
            target_id="KarpenterInterruptionQueueTarget",
            arn=queue_arn,
            opts=pulumi.ResourceOptions(parent=scheduled_change_rule),
        )

        # Spot Interruption Rule
        spot_interruption_rule = aws.cloudwatch.EventRule(
            f"{cluster_name}-spot-interruption-rule",
            event_pattern=pulumi.Output.json_dumps(
                {"source": ["aws.ec2"], "detail-type": ["EC2 Spot Instance Interruption Warning"]}
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

        aws.cloudwatch.EventTarget(
            f"{cluster_name}-spot-interruption-target",
            rule=spot_interruption_rule.name,
            target_id="KarpenterInterruptionQueueTarget",
            arn=queue_arn,
            opts=pulumi.ResourceOptions(parent=spot_interruption_rule),
        )

        # Rebalance Rule
        rebalance_rule = aws.cloudwatch.EventRule(
            f"{cluster_name}-rebalance-rule",
            event_pattern=pulumi.Output.json_dumps(
                {"source": ["aws.ec2"], "detail-type": ["EC2 Instance Rebalance Recommendation"]}
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

        aws.cloudwatch.EventTarget(
            f"{cluster_name}-rebalance-target",
            rule=rebalance_rule.name,
            target_id="KarpenterInterruptionQueueTarget",
            arn=queue_arn,
            opts=pulumi.ResourceOptions(parent=rebalance_rule),
        )

        # Instance State Change Rule
        instance_state_change_rule = aws.cloudwatch.EventRule(
            f"{cluster_name}-instance-state-change-rule",
            event_pattern=pulumi.Output.json_dumps(
                {"source": ["aws.ec2"], "detail-type": ["EC2 Instance State-change Notification"]}
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

        aws.cloudwatch.EventTarget(
            f"{cluster_name}-instance-state-change-target",
            rule=instance_state_change_rule.name,
            target_id="KarpenterInterruptionQueueTarget",
            arn=queue_arn,
            opts=pulumi.ResourceOptions(parent=instance_state_change_rule),
        )

    def _define_karpenter_controller_policy(self, cluster_name: str) -> str:
        """
        Generate the IAM policy document for the Karpenter controller role.
        Based on the official AWS CloudFormation template for Karpenter.

        :param cluster_name: The name of the EKS cluster
        :return: JSON string of the policy document
        """
        release = cluster_name.replace(f"{self.workload.compound_name}-", "")
        queue_arn = (
            self.autoscaling_queues[release].arn
            if release in self.autoscaling_queues
            else f"arn:aws:sqs:{self.workload.cfg.region}:{self.workload.cfg.account_id}:{cluster_name}"
        )

        return aws.iam.get_policy_document(
            statements=[
                # AllowScopedEC2InstanceAccessActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedEC2InstanceAccessActions",
                    effect="Allow",
                    resources=[
                        f"arn:aws:ec2:{self.workload.cfg.region}::image/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}::snapshot/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:security-group/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:subnet/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:capacity-reservation/*",
                    ],
                    actions=[
                        "ec2:RunInstances",
                        "ec2:CreateFleet",
                    ],
                ),
                # AllowScopedEC2LaunchTemplateAccessActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedEC2LaunchTemplateAccessActions",
                    effect="Allow",
                    resources=[f"arn:aws:ec2:{self.workload.cfg.region}:*:launch-template/*"],
                    actions=[
                        "ec2:RunInstances",
                        "ec2:CreateFleet",
                    ],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:ResourceTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:ResourceTag/karpenter.sh/nodepool",
                            values=["*"],
                        ),
                    ],
                ),
                # AllowScopedEC2InstanceActionsWithTags
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedEC2InstanceActionsWithTags",
                    effect="Allow",
                    resources=[
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:fleet/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:instance/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:volume/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:network-interface/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:launch-template/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:spot-instances-request/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:capacity-reservation/*",
                    ],
                    actions=[
                        "ec2:RunInstances",
                        "ec2:CreateFleet",
                        "ec2:CreateLaunchTemplate",
                    ],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:RequestTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:RequestTag/eks:eks-cluster-name",
                            values=[cluster_name],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:RequestTag/karpenter.sh/nodepool",
                            values=["*"],
                        ),
                    ],
                ),
                # AllowScopedResourceCreationTagging
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedResourceCreationTagging",
                    effect="Allow",
                    resources=[
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:fleet/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:instance/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:volume/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:network-interface/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:launch-template/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:spot-instances-request/*",
                    ],
                    actions=["ec2:CreateTags"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:RequestTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:RequestTag/eks:eks-cluster-name",
                            values=[cluster_name],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="ec2:CreateAction",
                            values=["RunInstances", "CreateFleet", "CreateLaunchTemplate"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:RequestTag/karpenter.sh/nodepool",
                            values=["*"],
                        ),
                    ],
                ),
                # AllowScopedResourceTagging
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedResourceTagging",
                    effect="Allow",
                    resources=[f"arn:aws:ec2:{self.workload.cfg.region}:*:instance/*"],
                    actions=["ec2:CreateTags"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:ResourceTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:ResourceTag/karpenter.sh/nodepool",
                            values=["*"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEqualsIfExists",
                            variable="aws:RequestTag/eks:eks-cluster-name",
                            values=[cluster_name],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="ForAllValues:StringEquals",
                            variable="aws:TagKeys",
                            values=["eks:eks-cluster-name", "karpenter.sh/nodeclaim", "Name"],
                        ),
                    ],
                ),
                # AllowScopedDeletion
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedDeletion",
                    effect="Allow",
                    resources=[
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:instance/*",
                        f"arn:aws:ec2:{self.workload.cfg.region}:*:launch-template/*",
                    ],
                    actions=[
                        "ec2:TerminateInstances",
                        "ec2:DeleteLaunchTemplate",
                    ],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:ResourceTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:ResourceTag/karpenter.sh/nodepool",
                            values=["*"],
                        ),
                    ],
                ),
                # AllowRegionalReadActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowRegionalReadActions",
                    effect="Allow",
                    resources=["*"],
                    actions=[
                        "ec2:DescribeCapacityReservations",
                        "ec2:DescribeImages",
                        "ec2:DescribeInstances",
                        "ec2:DescribeInstanceTypeOfferings",
                        "ec2:DescribeInstanceTypes",
                        "ec2:DescribeLaunchTemplates",
                        "ec2:DescribeSecurityGroups",
                        "ec2:DescribeSpotPriceHistory",
                        "ec2:DescribeSubnets",
                    ],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:RequestedRegion",
                            values=[self.workload.cfg.region],
                        ),
                    ],
                ),
                # AllowSSMReadActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowSSMReadActions",
                    effect="Allow",
                    resources=[f"arn:aws:ssm:{self.workload.cfg.region}::parameter/aws/service/*"],
                    actions=["ssm:GetParameter"],
                ),
                # AllowPricingReadActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowPricingReadActions",
                    effect="Allow",
                    resources=["*"],
                    actions=["pricing:GetProducts"],
                ),
                # AllowInterruptionQueueActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowInterruptionQueueActions",
                    effect="Allow",
                    resources=[queue_arn],
                    actions=[
                        "sqs:DeleteMessage",
                        "sqs:GetQueueUrl",
                        "sqs:ReceiveMessage",
                    ],
                ),
                # AllowPassingInstanceRole
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowPassingInstanceRole",
                    effect="Allow",
                    resources=[
                        f"arn:aws:iam::{self.workload.cfg.account_id}:role/KarpenterNodeRole-{cluster_name}.posit.team"
                    ],
                    actions=["iam:PassRole"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="iam:PassedToService",
                            values=["ec2.amazonaws.com", "ec2.amazonaws.com.cn"],
                        ),
                    ],
                ),
                # AllowScopedInstanceProfileCreationActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedInstanceProfileCreationActions",
                    effect="Allow",
                    resources=[f"arn:aws:iam::{self.workload.cfg.account_id}:instance-profile/*"],
                    actions=["iam:CreateInstanceProfile"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:RequestTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:RequestTag/eks:eks-cluster-name",
                            values=[cluster_name],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:RequestTag/topology.kubernetes.io/region",
                            values=[self.workload.cfg.region],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:RequestTag/karpenter.k8s.aws/ec2nodeclass",
                            values=["*"],
                        ),
                    ],
                ),
                # AllowScopedInstanceProfileTagActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedInstanceProfileTagActions",
                    effect="Allow",
                    resources=[f"arn:aws:iam::{self.workload.cfg.account_id}:instance-profile/*"],
                    actions=["iam:TagInstanceProfile"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:ResourceTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:ResourceTag/topology.kubernetes.io/region",
                            values=[self.workload.cfg.region],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:RequestTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:RequestTag/eks:eks-cluster-name",
                            values=[cluster_name],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:RequestTag/topology.kubernetes.io/region",
                            values=[self.workload.cfg.region],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:ResourceTag/karpenter.k8s.aws/ec2nodeclass",
                            values=["*"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:RequestTag/karpenter.k8s.aws/ec2nodeclass",
                            values=["*"],
                        ),
                    ],
                ),
                # AllowScopedInstanceProfileActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowScopedInstanceProfileActions",
                    effect="Allow",
                    resources=[f"arn:aws:iam::{self.workload.cfg.account_id}:instance-profile/*"],
                    actions=[
                        "iam:AddRoleToInstanceProfile",
                        "iam:RemoveRoleFromInstanceProfile",
                        "iam:DeleteInstanceProfile",
                    ],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable=f"aws:ResourceTag/kubernetes.io/cluster/{cluster_name}",
                            values=["owned"],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            variable="aws:ResourceTag/topology.kubernetes.io/region",
                            values=[self.workload.cfg.region],
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringLike",
                            variable="aws:ResourceTag/karpenter.k8s.aws/ec2nodeclass",
                            values=["*"],
                        ),
                    ],
                ),
                # AllowInstanceProfileReadActions
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowInstanceProfileReadActions",
                    effect="Allow",
                    resources=[f"arn:aws:iam::{self.workload.cfg.account_id}:instance-profile/*"],
                    actions=["iam:GetInstanceProfile"],
                ),
                # AllowAPIServerEndpointDiscovery
                aws.iam.GetPolicyDocumentStatementArgs(
                    sid="AllowAPIServerEndpointDiscovery",
                    effect="Allow",
                    resources=[
                        f"arn:aws:eks:{self.workload.cfg.region}:{self.workload.cfg.account_id}:cluster/{cluster_name}"
                    ],
                    actions=["eks:DescribeCluster"],
                ),
            ]
        ).json

    def _define_karpenter_aws_auth(self, cluster_name: str, release: str) -> None:
        """
        Add Karpenter node role to the AWS auth ConfigMap for the cluster.
        This allows Karpenter-launched nodes to join the cluster.
        """
        try:
            # Get AWS account ID
            account_id = self.workload.cfg.account_id

            # Build the Karpenter node role ARN
            karpenter_node_role_arn = f"arn:aws:iam::{account_id}:role/KarpenterNodeRole-{cluster_name}.posit.team"

            # Create the aws-auth ConfigMap entry for Karpenter nodes
            karpenter_node_map_entry = {
                "groups": ["system:bootstrappers", "system:nodes"],
                "rolearn": karpenter_node_role_arn,
                "username": "system:node:{{EC2PrivateDNSName}}",
            }

            # Get the existing aws-auth ConfigMap
            try:
                existing_configmap = kubernetes.core.v1.ConfigMap.get(
                    f"{cluster_name}-aws-auth-configmap",
                    "kube-system/aws-auth",
                    opts=pulumi.ResourceOptions(provider=self.kube_providers[release]),
                )

                # Parse existing mapRoles data
                def update_map_roles(existing_data):
                    import yaml

                    current_data = existing_data or {}
                    map_roles_yaml = current_data.get("mapRoles", "")

                    # Parse existing mapRoles
                    try:
                        existing_roles = yaml.safe_load(map_roles_yaml) or [] if map_roles_yaml else []
                    except yaml.YAMLError:
                        existing_roles = []

                    # Check if Karpenter role already exists
                    karpenter_exists = any(role.get("rolearn") == karpenter_node_role_arn for role in existing_roles)

                    if not karpenter_exists:
                        # Add Karpenter role to the list
                        existing_roles.append(karpenter_node_map_entry)
                    else:
                        pass

                    # Convert back to YAML
                    updated_map_roles = yaml.dump(existing_roles, default_flow_style=False)

                    # Update the data
                    updated_data = current_data.copy()
                    updated_data["mapRoles"] = updated_map_roles

                    return updated_data

                # Update the ConfigMap with the new mapRoles
                kubernetes.core.v1.ConfigMapPatch(
                    f"{cluster_name}-aws-auth-patch",
                    metadata={"name": "aws-auth", "namespace": ptd.KARPENTER_NAMESPACE},
                    data=existing_configmap.data.apply(update_map_roles),
                    opts=pulumi.ResourceOptions(
                        provider=self.kube_providers[release], parent=self, depends_on=[existing_configmap]
                    ),
                )

            except Exception:
                # Create new ConfigMap with just the Karpenter role
                map_roles_yaml = yaml.dump([karpenter_node_map_entry], default_flow_style=False)

                kubernetes.core.v1.ConfigMap(
                    f"{cluster_name}-aws-auth-new",
                    metadata={"name": "aws-auth", "namespace": ptd.KARPENTER_NAMESPACE},
                    data={"mapRoles": map_roles_yaml},
                    opts=pulumi.ResourceOptions(provider=self.kube_providers[release], parent=self),
                )

        except Exception as e:
            pulumi.log.warn(f"Failed to update AWS auth ConfigMap for cluster {cluster_name}: {e}")

    def _define_karpenter_access_entry(self, cluster_name: str, release: str) -> None:  # noqa: ARG002
        """
        Add Karpenter node role to EKS cluster using Access Entries (modern approach).
        This allows Karpenter-launched nodes to join the cluster using EKS Access Entries
        instead of the aws-auth ConfigMap.
        """
        try:
            # Get AWS account ID
            account_id = self.workload.cfg.account_id

            # Build the Karpenter node role ARN
            karpenter_node_role_arn = f"arn:aws:iam::{account_id}:role/KarpenterNodeRole-{cluster_name}.posit.team"

            # Create Access Entry for Karpenter node role
            aws.eks.AccessEntry(
                f"{cluster_name}-karpenter-node-access-entry",
                cluster_name=cluster_name,
                principal_arn=karpenter_node_role_arn,
                type="EC2_LINUX",
                opts=pulumi.ResourceOptions(parent=self),
            )

        except Exception as e:
            pulumi.log.warn(f"Failed to create Karpenter EKS Access Entry for cluster {cluster_name}: {e}")
