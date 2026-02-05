import base64
import collections
import dataclasses
import enum
import json
import re
import typing
import warnings

import bcrypt
import boto3
import botocore.exceptions
import pulumi
import pulumi_aws as aws
import pulumi_kubernetes as k8s

import ptd.aws_auth_user
import ptd.aws_iam
import ptd.junkdrawer
import ptd.oidc
import ptd.paths
import ptd.secrecy


class NodeRolePolicy(enum.StrEnum):
    WORKER_POLICY = "worker-policy"
    CNI_POLICY = "cni-policy"
    REGISTRY_POLICY = "registry-policy"
    SSM_POLICY = "ssm-policy"


@dataclasses.dataclass
class ServiceAccount:
    name: str
    namespace: str

    def get_subject(self):
        """
        :return: The value to use in a condition for the sub (subject)
        """
        return f"system:serviceaccount:{self.namespace}:{self.name}"


class AWSEKSCluster(pulumi.ComponentResource):
    """
    Create an EKS cluster. Other useful methods:
      - with_node_role()
      - with_node_group()

    Example usage:
      ```
      lt = ec2.LaunchTemplate('lt1')
      rstudio_eks = eks.AWSEKSCluster(
          name='test-eks',
          subnets=[x.subnet for x in vpc_data.public_subnets],
          version='1.21',
          tags=tags,
      )
        .with_node_role()
        .with_node_group(lt)
      ```

    Then you can create your own node groups, etc. as well

    :param name: The name of the EKS cluster
    :param subnet_ids: The subnet ids used for the EKS control plane. A list of subnet ids as strings. Make sure these
    subnets have a tag: kubernetes.io/cluster/CLUSTER_NAME
    :param version: The version of EKS / Kubernetes to use
    :param tags: Tags to attach to child resources
    :param enabled_cluster_log_types: Optional.  A set of the cluster log types to enable.
    :param opts: Optional. Resource options
    :return A Pulumi component with the eks_role, node_role, eks_cluster_policy, eks_cluster, and several node_role
    policy attachments
    """

    name: str
    protect_persistent_resources: bool

    eks_role: aws.iam.Role
    eks_cluster_policy: aws.iam.RolePolicyAttachment
    eks: aws.eks.Cluster
    cluster_subnet_ids: list[str] | list[pulumi.Output[str]]

    default_node_role: aws.iam.Role | None
    default_node_role_policies: dict[NodeRolePolicy, aws.iam.RolePolicyAttachment]
    node_groups: dict[str, aws.eks.NodeGroup]
    default_fargate_node_role: aws.iam.Role | None
    fargate_profiles: dict[str, aws.eks.FargateProfile]
    grafana_ns: k8s.core.v1.Namespace

    def __init__(
        self,
        name: str,
        sg_prefix: str,
        subnet_ids: list[str] | list[pulumi.Output[str]],
        version: str,
        tags: dict[str, str],
        default_addons_to_remove: list[str] | None = None,
        enabled_cluster_log_types: set[str] | None = None,
        *args,
        tailscale_enabled: bool = False,
        customer_managed_bastion_id: str | None = None,
        protect_persistent_resources: bool = True,
        eks_role_name: str = "",
        iam_permissions_boundary: str = "",
        **kwargs,
    ):
        if version is None:
            warnings.warn(
                "`version` argument is `None`. This could cause the EKS version to change without warning",
                stacklevel=2,
            )

        super().__init__(f"ptd:{self.__class__.__name__}", name, *args, **kwargs)

        self.name = name
        self.sg_prefix = sg_prefix
        self.tags = tags
        self.protect_persistent_resources = protect_persistent_resources
        self.iam_permissions_boundary = iam_permissions_boundary
        self.tailscale_enabled = tailscale_enabled
        self.customer_managed_bastion_id = customer_managed_bastion_id

        # optional variables added later
        self.default_node_role = None
        self.default_node_role_policies = collections.defaultdict()
        self.node_groups = collections.defaultdict()
        self.default_fargate_node_role = None
        self.fargate_profiles = collections.defaultdict()
        self.oidc_provider = None
        self.ebs_csi_addon = None  # Set by with_ebs_csi_driver(), used by with_encrypted_ebs_storage_class()

        # TODO: evaluate whether to create just one of these / etc.
        assume_role_policy = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    actions=["sts:AssumeRole"],
                    principals=[
                        aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                            type="Service",
                            identifiers=["eks.amazonaws.com"],
                        )
                    ],
                )
            ]
        )

        if eks_role_name != "":
            self.eks_role = aws.iam.Role(
                f"{self.name}-eks",
                aws.iam.RoleArgs(
                    name=f"{eks_role_name}",
                    assume_role_policy=assume_role_policy.json,
                    permissions_boundary=iam_permissions_boundary,
                ),
                opts=pulumi.ResourceOptions(parent=self),
            )
        else:  # silly hack to avoid renaming the role on the existing control room clusters which is causing major headaches
            self.eks_role = aws.iam.Role(
                f"{self.name}-eks",
                aws.iam.RoleArgs(
                    assume_role_policy=assume_role_policy.json, permissions_boundary=iam_permissions_boundary
                ),
                opts=pulumi.ResourceOptions(parent=self),
            )

        self.eks_cluster_policy = aws.iam.RolePolicyAttachment(
            f"{self.name}-eks",
            policy_arn="arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
            role=self.eks_role.name,
            opts=pulumi.ResourceOptions(parent=self.eks_role, aliases=[pulumi.Alias(parent=self)]),
        )

        # Try to detect if this is an existing cluster by checking with boto3
        # This approach avoids replacement for existing clusters
        # The eks update-cluster-config command needs to be used to migrate the cluster authentication mode
        cluster_exists = False
        current_auth_mode = None
        region = aws.get_region().name

        try:
            # Use boto3 to check if cluster exists and get its actual auth mode
            eks_client = boto3.client("eks", region_name=region)
            cluster_info = eks_client.describe_cluster(name=name)
            cluster_exists = True

            # Get the actual authentication mode from the API
            current_auth_mode = cluster_info["cluster"].get("accessConfig", {}).get("authenticationMode", "CONFIG_MAP")

            if current_auth_mode not in ["API_AND_CONFIG_MAP", "API"]:
                pulumi.log.warn(
                    f"Cluster {name} exists with authentication mode '{current_auth_mode}'. "
                    f"To avoid replacement, please first run: "
                    f"aws eks update-cluster-config --name {name} --access-config authenticationMode=API_AND_CONFIG_MAP --region {region}"
                )
            else:
                pulumi.log.info(f"Cluster {name} exists with authentication mode '{current_auth_mode}'")

        except Exception as e:
            if "ResourceNotFoundException" in str(e.__class__.__name__):
                # Cluster doesn't exist yet - will be created with correct settings
                pulumi.log.info(f"Cluster {name} not found, will be created with API_AND_CONFIG_MAP mode")
                cluster_exists = False
            else:
                # Some other error - log it but assume cluster doesn't exist
                pulumi.log.warn(f"Could not check cluster {name} status: {e}")
                cluster_exists = False

        # Build cluster arguments
        cluster_args = {
            "name": name,
            "role_arn": self.eks_role.arn,
            "default_addons_to_removes": default_addons_to_remove,
            "enabled_cluster_log_types": (sorted(enabled_cluster_log_types) if enabled_cluster_log_types else None),
            "vpc_config": aws.eks.ClusterVpcConfigArgs(
                subnet_ids=subnet_ids,
                endpoint_private_access=True,
                endpoint_public_access=False,
            ),
            "version": version,
            "tags": {"Name": name} | tags,
        }

        # Configure cluster options based on whether it exists and its auth mode
        cluster_opts = pulumi.ResourceOptions(parent=self)

        if cluster_exists:
            # For ANY existing cluster, don't add access_config to avoid replacement
            # The authentication mode should be set via AWS CLI for existing clusters
            if current_auth_mode in ["API_AND_CONFIG_MAP", "API"]:
                pulumi.log.info(
                    f"Cluster {name} already has authentication mode '{current_auth_mode}' - ready for Access Entries"
                )
            else:
                pulumi.log.warn(
                    f"Cluster {name} has authentication mode '{current_auth_mode}'. "
                    f"To use Access Entries, please run: "
                    f"aws eks update-cluster-config --name {name} --access-config authenticationMode=API_AND_CONFIG_MAP --region {region}"
                )
            # Don't add access_config for existing clusters to prevent replacement
        else:
            # New cluster - set access_config
            cluster_args["access_config"] = aws.eks.ClusterAccessConfigArgs(
                authentication_mode="API_AND_CONFIG_MAP",
            )
            pulumi.log.info(f"Creating new cluster {name} with API_AND_CONFIG_MAP authentication mode")

        # Create or update the cluster
        self.eks = aws.eks.Cluster(
            name,
            **cluster_args,
            opts=cluster_opts,
        )

        if self.tailscale_enabled:
            self.setup_tailscale_access()
        elif not self.customer_managed_bastion_id:
            # NB: prevents bastion sg configuration if customer managed bastion is used.
            self.setup_bastion_access()

        self.cluster_subnet_ids = subnet_ids

    @property
    def provider(self) -> k8s.Provider:
        if self._provider is None:
            self._provider = get_provider_for_cluster(self.name, self.tailscale_enabled)

        return typing.cast(k8s.Provider, self._provider)

    def with_service_account(
        self,
        name: str,
        namespace: str,
    ):
        """
        Add a service account with full access to a namespace, requires with_namespace to have been run

        :param name: Name of the service account
        :param namespace: Namespace to put the account in
        :param opts: Optional. Pulumi resource options
        :return: self
        """
        # Modeled after Service account in https://github.com/rstudio/infra-kubernetes/blob/master/k8s/qa.yaml

        service_account = k8s.core.v1.ServiceAccount(
            f"{self.name}-{name}-{namespace}",
            api_version="v1",
            metadata=k8s.meta.v1.ObjectMetaArgs(name=name, namespace=namespace),
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self),
        )

        k8s.rbac.v1.RoleBinding(
            f"{self.name}-{name}-{namespace}",
            api_version="rbac.authorization.k8s.io/v1",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=name,
                namespace=namespace,
            ),
            role_ref=k8s.rbac.v1.RoleRefArgs(
                api_group="rbac.authorization.k8s.io",
                kind="Role",
                name=f"{namespace}-full-access",
            ),
            subjects=[k8s.rbac.v1.SubjectArgs(kind="ServiceAccount", name=name)],
            opts=pulumi.ResourceOptions(provider=self.provider, parent=service_account),
        )

        return self

    def with_namespace(self, name: str):
        """
        Add a namespace named name

        :param name: Name of the namespace to add
        :param opts: Optional. Pulumi resource options
        :return: self
        """

        # Modelling the namespace/role/rolebinding off of
        # https://github.com/rstudio/infra-kubernetes/blob/master/k8s/dev.yaml
        namespace = k8s.core.v1.Namespace(
            f"{self.name}-{name}",
            api_version="v1",
            metadata=k8s.meta.v1.ObjectMetaArgs(name=name, labels={"name": name}),
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self),
        )

        role = k8s.rbac.v1.Role(
            f"{self.name}-{name}-full-access",
            api_version="rbac.authorization.k8s.io/v1",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                namespace=name,
                name=f"{name}-full-access",
            ),
            rules=[k8s.rbac.v1.PolicyRuleArgs(api_groups=["*"], resources=["*"], verbs=["*"])],
            opts=pulumi.ResourceOptions(provider=self.provider, parent=namespace),
        )

        k8s.rbac.v1.RoleBinding(
            f"{self.name}-{name}-group",
            api_version="rbac.authorization.k8s.io/v1",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name=f"{name}-group",
                namespace=name,
            ),
            role_ref=k8s.rbac.v1.RoleRefArgs(
                api_group="rbac.authorization.k8s.io",
                kind="Role",
                name=f"{name}-full-access",
            ),
            subjects=[k8s.rbac.v1.SubjectArgs(kind="Group", name=name)],
            opts=pulumi.ResourceOptions(provider=self.provider, parent=role),
        )

        return self

    def create_service_account_role(
        self,
        role_name: str,
        service_accounts: list[ServiceAccount],
        managed_policies: (pulumi.Input[typing.Sequence[pulumi.Input[str]]] | None) = None,
        role_policies: (pulumi.Input[typing.Sequence[pulumi.Input[str]]] | None) = None,
        opts: pulumi.ResourceOptions | None = None,
    ) -> aws.iam.Role:
        """
        Create a role that is assumable by one or more service accounts.

        :param role_name: The name of the role to create.
        :param service_accounts: A list of service accounts allowed to assume the role.
        :param managed_policies: Optional managed policy ARNs to attach to the role.
        :param role_policies: Optional role policies to create with the role. (previously known as inline_policies)
        :param opts: Optional resource options.
        :return: The aws.iam.Role
        """
        if opts is None:
            opts = pulumi.ResourceOptions()

        # We will need the account id and oidc_issuer_url to create the iam role policy
        account_id = aws.get_caller_identity().account_id
        oidc_issuer_url = self.eks.identities[0]["oidcs"][0]["issuer"]

        # Modelled after https://docs.aws.amazon.com/eks/latest/userguide/csi-iam-role.html
        assume_role_policy = oidc_issuer_url.apply(
            lambda url: aws.iam.get_policy_document(
                statements=[
                    aws.iam.GetPolicyDocumentStatementArgs(
                        sid="ServiceAccountTrustPolicy",
                        effect="Allow",
                        principals=[
                            aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                                type="Federated",
                                identifiers=[f"arn:aws:iam::{account_id}:oidc-provider/{url.split('//')[1]}"],
                            )
                        ],
                        actions=["sts:AssumeRoleWithWebIdentity"],
                        conditions=[
                            aws.iam.GetPolicyDocumentStatementConditionArgs(
                                test="StringEquals",
                                values=["sts.amazonaws.com"],
                                variable=f"{url.split('//')[1]}:aud",
                            ),
                            aws.iam.GetPolicyDocumentStatementConditionArgs(
                                test="StringEquals",
                                values=[service_account.get_subject() for service_account in service_accounts],
                                variable=f"{url.split('//')[1]}:sub",
                            ),
                        ],
                    )
                ]
            ).json
        )

        role = aws.iam.Role(
            role_name,
            aws.iam.RoleArgs(
                name=role_name,
                assume_role_policy=assume_role_policy,
                managed_policy_arns=managed_policies,
                permissions_boundary=self.iam_permissions_boundary,
            ),
            opts=opts,
        )

        if role_policies:
            for idx, role_policy in enumerate(role_policies):
                aws.iam.RolePolicy(
                    f"{role_name}-role-policy-{idx}",
                    name=f"{role_name}-role-policy-{idx}",
                    policy=role_policy,
                    role=role.id,
                )
        return role

    def with_node_role(self, role: aws.iam.Role | None = None, role_name: str = ""):
        """
        Create the default node role for the EKS cluster.
        This is a minimal node role for EKS cluster nodes to function.

        :param role: Optional. The role that should be used by default for node groups. No policies are created if a
        role is provided. Default is a role with minimal policies for proper EKS function.
        :param opts: Optional. Resource options.
        :return: The AWSEKSCluster component resource
        """
        if role is not None:
            self.default_node_role = role
        else:
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
            if role_name != "":
                self.default_node_role = aws.iam.Role(
                    f"{self.name}-eks-node",
                    name=role_name,
                    assume_role_policy=assume_role_policy.json,
                    permissions_boundary=self.iam_permissions_boundary,
                    opts=pulumi.ResourceOptions(parent=self.eks, aliases=[pulumi.Alias(parent=self)]),
                )
            else:
                self.default_node_role = aws.iam.Role(
                    f"{self.name}-eks-node",
                    assume_role_policy=assume_role_policy.json,
                    permissions_boundary=self.iam_permissions_boundary,
                    opts=pulumi.ResourceOptions(parent=self.eks, aliases=[pulumi.Alias(parent=self)]),
                )

            self.default_node_role_policies[NodeRolePolicy.WORKER_POLICY] = aws.iam.RolePolicyAttachment(
                f"{self.name}-eks-node-worker",
                role=self.default_node_role.id,
                policy_arn="arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
                opts=pulumi.ResourceOptions(
                    parent=self.default_node_role,
                    aliases=[pulumi.Alias(parent=self)],
                ),
            )
            self.default_node_role_policies[NodeRolePolicy.CNI_POLICY] = aws.iam.RolePolicyAttachment(
                f"{self.name}-eks-node-cni",
                role=self.default_node_role.id,
                policy_arn="arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
                opts=pulumi.ResourceOptions(
                    parent=self.default_node_role,
                    aliases=[pulumi.Alias(parent=self)],
                ),
            )
            self.default_node_role_policies[NodeRolePolicy.REGISTRY_POLICY] = aws.iam.RolePolicyAttachment(
                f"{self.name}-eks-node-registry",
                role=self.default_node_role.id,
                policy_arn=aws.iam.ManagedPolicy.AMAZON_EC2_CONTAINER_REGISTRY_READ_ONLY,
                opts=pulumi.ResourceOptions(
                    parent=self.default_node_role,
                    aliases=[pulumi.Alias(parent=self)],
                ),
            )
            self.default_node_role_policies[NodeRolePolicy.SSM_POLICY] = aws.iam.RolePolicyAttachment(
                f"{self.name}-eks-node-ssm",
                role=self.default_node_role.id,
                policy_arn=aws.iam.ManagedPolicy.AMAZON_SSM_MANAGED_INSTANCE_CORE,
                opts=pulumi.ResourceOptions(
                    parent=self.default_node_role,
                    aliases=[pulumi.Alias(parent=self)],
                ),
            )

        return self

    def with_node_group(
        self,
        name: str,
        launch_template: aws.ec2.LaunchTemplate,
        tags: dict[str, str],
        subnet_ids: list[str] | list[pulumi.Output[str]] | None = None,
        node_role: aws.iam.Role | None = None,
        version: str | None = None,
        desired: int = 2,
        min_nodes: int = 2,
        max_nodes: int = 4,
        max_unavailable: int = 1,
        ami_type: str = "AL2_x86_64",
        taints: list[aws.eks.NodeGroupTaintArgs] | None = None,
        depends_on: list[pulumi.Resource] | None = None,
        *,
        use_name: bool = False,
    ):
        # TODO: what typing should we have for subnets? Consistency?
        """
        Add a node group to the EKS cluster
        Node groups are tracked in the `node_groups` dict on the

        :param name: The name of the nodegroup. Should be consistent to avoid unexpected node group deletion.
        :param launch_template: A Pulumi LaunchTemplate object or RStudio ec2.LaunchTemplate
        :param tags: Tags to attach to child resources
        :param subnet_ids: Optional. A list of subnet ids to launch nodes within. Make sure these subnets have a tag:
        kubernetes.io/cluster/CLUSTER_NAME
        :param use_name: Optional. If it is True, then use the name parameter as 'node_group_name', else None.
        :param node_role: Optional. The node role to attach to the node group nodes. Default is the cluster's default
        node role (which must exist if node_role is not provided)
        :param version: Optional. The Kubernetes version for nodes in the node group. Defaults to the EKS version if
        the launch template image_id is None, else None
        :param desired: Optional. The desired number of nodes in the node group. Default 2
        :param min_nodes: Optional. The minimum number of nodes in the node group. Default 2
        :param max_nodes: Optional. The maximum number of nodes in the node group. Default 4
        :param ami_type: Optional. The AMI type. Default 'AL2_x86_64'
        :param max_unavailable: Optional. The maximum number of unavailable nodes during an update. Default 1
        :param taints: Optional. The Kubernetes taints to be applied to the nodes in the node group
        :param depends_on: Optional. Resources that must be created before the node group (e.g., CNI)
        :param opts: Optional. Resource options.
        :return: The AWSEKSCluster component resource
        """
        if subnet_ids is None:
            subnet_ids = self.cluster_subnet_ids

        if node_role is None and self.default_node_role is None:
            msg = "node_role must be defined because default_node_role is not set"
            raise ValueError(msg)

        # If the version is None and the passed in launch template image_id is ALSO None, use the eks version
        # this check must come after `launch_template` is guaranteed to be an `aws.ec2.LaunchTemplate`
        if (version is None) and (launch_template.image_id is None):
            version = self.eks.version  # type: ignore

        def instance_type_check(t: str) -> None:
            inst_regex = re.compile(".*(nano|micro|small|medium)$")
            if inst_regex.match(t):
                pulumi.warn(f"Recommend using at least a large instance for nodes, but got instance type: {t}")

        launch_template.instance_type.apply(instance_type_check)  # type: ignore

        if node_role is None:
            node_role = self.default_node_role

        if node_role is None:
            msg = "node role is None (somehow)"
            raise ValueError(msg)

        node_group = aws.eks.NodeGroup(
            name,
            cluster_name=self.eks.name,
            node_group_name=name if use_name else None,
            node_role_arn=node_role.arn,
            subnet_ids=subnet_ids,  # type: ignore
            version=version,
            scaling_config=aws.eks.NodeGroupScalingConfigArgs(
                desired_size=desired,
                min_size=min_nodes,
                max_size=max_nodes,
            ),
            ami_type=ami_type,
            tags=tags,
            launch_template=aws.eks.NodeGroupLaunchTemplateArgs(
                id=launch_template.id,
                version=launch_template.latest_version,  # type: ignore
            ),
            update_config=aws.eks.NodeGroupUpdateConfigArgs(max_unavailable=max_unavailable),
            taints=taints,
            opts=pulumi.ResourceOptions(parent=self.eks, depends_on=depends_on),
        )

        self.node_groups[name] = node_group

        return self

    def with_aws_auth(
        self,
        node_role: aws.iam.Role | None = None,
        additional_users: list[ptd.aws_auth_user.AWSAuthUser] | None = None,
        *,
        use_eks_access_entries: bool = False,
        additional_access_entries: list[dict] | None = None,
        include_poweruser: bool = False,
    ):
        """
        Add IAM roles to EKS cluster using either aws-auth ConfigMap or EKS Access Entries.

        This method supports both the legacy aws-auth ConfigMap approach and the modern
        EKS Access Entries approach based on the feature flag.

        :param node_role: Optional. The node role attached to the node group nodes. Default is the cluster's default
        node role (which must exist if node_role is not provided)
        :param additional_users: Optional. Additional ptd.aws_auth_user.AWSAuthUser to pass along to the configmap
        (only used when use_eks_access_entries=False)
        :param use_eks_access_entries: Optional. If True, use EKS Access Entries instead of aws-auth ConfigMap. Default False
        :param additional_access_entries: Optional. Additional access entries to create when using EKS Access Entries
        :param include_poweruser: Optional. If True, include PowerUser role access entry when using EKS Access Entries. Default False
        :return: The AWSEKSCluster component resource
        """

        if use_eks_access_entries:
            # Use the modern EKS Access Entries approach
            return self.with_eks_access_entries(
                node_role=node_role,
                additional_access_entries=additional_access_entries,
                include_poweruser=include_poweruser,
            )

        # Use the legacy aws-auth ConfigMap approach
        warnings.warn(
            "aws-auth ConfigMap authentication is deprecated. "
            "Set eks_access_entries.enabled=True in cluster configuration. "
            "ConfigMap support will be removed in a future release.",
            DeprecationWarning,
            stacklevel=2,
        )
        if node_role is None:
            if self.default_node_role is not None:
                node_role = self.default_node_role
            else:
                msg = "node_role must be defined because default_node_role is not set"
                raise ValueError(msg)

        # Get the ARNs for PowerUser and User
        account_id = aws.get_caller_identity().account_id
        # The role created for a permission set is of the form::
        #  arn:aws:iam::954555569365:role/aws-reserved/sso.amazonaws.com/us-east-2/AWSReservedSSO_PowerUser_a87135b953b3eb83
        # For aws-auth ConfigMap (legacy), the path must be removed from the ARN,
        # as suggested by this blogger (how they determine this is unclear):
        #  https://www.powerupcloud.com/aws-eks-authentication-and-authorization-using-aws-single-signon/#:%7E:text=ensure%20to%20remove,role_arn
        poweruser_arn = f"arn:aws:iam::{account_id}:role/{ptd.aws_iam.get_role_name_for_permission_set('PowerUser')}"
        adminrole_arn = f"arn:aws:iam::{account_id}:role/admin.posit.team"

        if additional_users is None:
            additional_users = []
        aws_auth_configmap = node_role.arn.apply(
            lambda arn: ptd.aws_auth_user.AWSAuthUser.create_configmap_contents(  # type: ignore
                [  # type: ignore
                    ptd.aws_auth_user.AWSAuthUser(
                        arn,
                        "system:node:{{EC2PrivateDNSName}}",
                        ["system:bootstrappers", "system:nodes"],
                    ),
                    ptd.aws_auth_user.AWSAuthUser(poweruser_arn, "admin", ["system:masters"]),
                    ptd.aws_auth_user.AWSAuthUser(adminrole_arn, "admin", ["system:masters"]),
                    *additional_users,
                ]
            )
        )

        k8s.core.v1.ConfigMapPatch(
            f"{self.name}-aws-auth",
            data={"mapRoles": aws_auth_configmap},
            metadata=k8s.meta.v1.ObjectMetaPatchArgs(
                name="aws-auth",
                namespace="kube-system",
                annotations={"pulumi.com/patchForce": "true"},
            ),
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self.eks),
        )

        return self

    def _get_associated_policies(self, principal_arn: str) -> set[str]:
        """
        Get the set of policy ARNs associated with an access entry.

        :param principal_arn: The principal ARN to check policies for
        :return: Set of policy ARNs associated with the principal
        """
        try:
            if not hasattr(self, "_eks_client"):
                region = aws.get_region().name
                self._eks_client = boto3.client("eks", region_name=region)

            policies = set()
            response = self._eks_client.list_associated_access_policies(
                clusterName=self.name,
                principalArn=principal_arn,
            )
            for policy in response.get("associatedAccessPolicies", []):
                policies.add(policy.get("policyArn"))

            # Handle pagination
            while "nextToken" in response:
                response = self._eks_client.list_associated_access_policies(
                    clusterName=self.name,
                    principalArn=principal_arn,
                    nextToken=response["nextToken"],
                )
                for policy in response.get("associatedAccessPolicies", []):
                    policies.add(policy.get("policyArn"))
        except botocore.exceptions.ClientError as e:
            error_code = e.response.get("Error", {}).get("Code", "")
            if error_code == "ResourceNotFoundException":
                pulumi.log.info(f"No associated policies found for {principal_arn}")
            else:
                pulumi.log.warn(f"Could not list associated policies for {principal_arn}: {e}")
            return set()
        else:
            return policies

    def with_eks_access_entries(
        self,
        node_role: aws.iam.Role | None = None,
        additional_access_entries: list[dict] | None = None,
        *,
        include_poweruser: bool = False,
    ):
        """
        Add IAM roles to EKS cluster using Access Entries (modern approach).

        This method creates EKS Access Entries instead of using the aws-auth ConfigMap.
        It handles the standard roles (Admin, Node roles) and supports
        additional custom access entries. PowerUser role is optional.

        Note: The cluster must have authentication mode set to API_AND_CONFIG_MAP or API
        for Access Entries to work. For existing clusters, update the mode using AWS CLI first.

        This method always defines Pulumi resources for all required access entries. If an
        entry already exists in AWS (e.g., auto-created during auth mode migration), it will
        be imported into Pulumi state rather than skipped. This ensures consistent state
        management and prevents flip-flopping diffs on subsequent runs.

        :param node_role: Optional. The node role attached to the node group nodes. Default is the cluster's default
        node role (which must exist if node_role is not provided)
        :param additional_access_entries: Optional. Additional access entries to create. Each entry should be a dict
        with keys: 'principalArn', 'type' (default 'STANDARD'), 'accessPolicies' (list of policy ARNs and access scopes)
        :param include_poweruser: Optional. If True, include PowerUser role access entry. Default False
        :return: The AWSEKSCluster component resource
        """
        # We need to check if the cluster has the correct authentication mode after modification of existing clusters.
        try:
            region = aws.get_region().name
            eks_client = boto3.client("eks", region_name=region)

            # Check cluster authentication mode
            cluster_info = eks_client.describe_cluster(name=self.name)
            auth_mode = cluster_info["cluster"].get("accessConfig", {}).get("authenticationMode", "CONFIG_MAP")

            if auth_mode not in ["API_AND_CONFIG_MAP", "API"]:
                pulumi.log.warn(
                    f"Cluster {self.name} has authentication mode '{auth_mode}'. "
                    f"Access Entries require API_AND_CONFIG_MAP or API mode. "
                    f"Skipping Access Entries creation. "
                    f"To enable, run: aws eks update-cluster-config --name {self.name} --access-config authenticationMode=API_AND_CONFIG_MAP --region {region}"
                )
                return self
        except Exception as e:
            # If we can't check, proceed and let AWS return an error if needed
            pulumi.log.info(f"Could not verify cluster authentication mode: {e}")

        if node_role is None:
            if self.default_node_role is not None:
                node_role = self.default_node_role
            else:
                msg = "node_role must be defined because default_node_role is not set"
                raise ValueError(msg)

        # Get the ARNs for Admin (and PowerUser if needed)
        account_id = aws.get_caller_identity().account_id
        adminrole_arn = f"arn:aws:iam::{account_id}:role/admin.posit.team"

        # Check which Access Entries already exist in AWS (for import decisions)
        # AWS auto-migrates some entries when changing auth mode to API_AND_CONFIG_MAP
        existing_entries = set()
        try:
            if not hasattr(self, "_eks_client"):
                region = aws.get_region().name
                self._eks_client = boto3.client("eks", region_name=region)

            response = self._eks_client.list_access_entries(clusterName=self.name)
            existing_entries = set(response.get("accessEntries", []))

            # Handle pagination if needed
            while "nextToken" in response:
                response = self._eks_client.list_access_entries(clusterName=self.name, nextToken=response["nextToken"])
                existing_entries.update(response.get("accessEntries", []))

            if existing_entries:
                pulumi.log.info(f"Found existing Access Entries in AWS for cluster {self.name}: {existing_entries}")
        except Exception as e:
            pulumi.log.info(f"Could not check existing Access Entries: {e}")

        # Helper to build import ID for AccessEntry
        # Import format: {cluster_name}:{principal_arn}
        def access_entry_import_id(principal_arn: str) -> str | None:
            if principal_arn in existing_entries:
                return f"{self.name}:{principal_arn}"
            return None

        # Helper to build import ID for AccessPolicyAssociation
        # Import format: {cluster_name}#{principal_arn}#{policy_arn}
        def policy_association_import_id(
            principal_arn: str, policy_arn: str, existing_policies: set[str]
        ) -> str | None:
            if principal_arn in existing_entries and policy_arn in existing_policies:
                return f"{self.name}#{principal_arn}#{policy_arn}"
            return None

        # --- Admin Role Access Entry (always created) ---
        admin_existing_policies = (
            self._get_associated_policies(adminrole_arn) if adminrole_arn in existing_entries else set()
        )
        admin_policy_arn = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"

        admin_import = access_entry_import_id(adminrole_arn)
        if admin_import:
            pulumi.log.debug(f"Setting import flag for Admin Access Entry: {adminrole_arn}")

        aws.eks.AccessEntry(
            f"{self.name}-admin-access-entry",
            cluster_name=self.eks.name,
            principal_arn=adminrole_arn,
            type="STANDARD",
            opts=pulumi.ResourceOptions(parent=self.eks, import_=admin_import),
        )

        admin_policy_import = policy_association_import_id(adminrole_arn, admin_policy_arn, admin_existing_policies)
        if admin_policy_import:
            pulumi.log.debug(f"Setting import flag for Admin Policy Association: {adminrole_arn}")

        aws.eks.AccessPolicyAssociation(
            f"{self.name}-admin-policy-association",
            cluster_name=self.eks.name,
            principal_arn=adminrole_arn,
            policy_arn=admin_policy_arn,
            access_scope=aws.eks.AccessPolicyAssociationAccessScopeArgs(
                type="cluster",
            ),
            opts=pulumi.ResourceOptions(parent=self.eks, import_=admin_policy_import),
        )

        # --- PowerUser Role Access Entry (optional) ---
        if include_poweruser:
            poweruser_arn = ptd.aws_iam.get_role_arn_for_permission_set("PowerUser")

            if poweruser_arn is None:
                pulumi.log.warn("PowerUser role not found, skipping Access Entry creation")
            else:
                poweruser_existing_policies = (
                    self._get_associated_policies(poweruser_arn) if poweruser_arn in existing_entries else set()
                )
                poweruser_policy_arn = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"

                poweruser_import = access_entry_import_id(poweruser_arn)
                if poweruser_import:
                    pulumi.log.debug(f"Setting import flag for PowerUser Access Entry: {poweruser_arn}")

                aws.eks.AccessEntry(
                    f"{self.name}-poweruser-access-entry",
                    cluster_name=self.eks.name,
                    principal_arn=poweruser_arn,
                    type="STANDARD",
                    opts=pulumi.ResourceOptions(parent=self.eks, import_=poweruser_import),
                )

                poweruser_policy_import = policy_association_import_id(
                    poweruser_arn, poweruser_policy_arn, poweruser_existing_policies
                )
                if poweruser_policy_import:
                    pulumi.log.debug(f"Setting import flag for PowerUser Policy Association: {poweruser_arn}")

                aws.eks.AccessPolicyAssociation(
                    f"{self.name}-poweruser-policy-association",
                    cluster_name=self.eks.name,
                    principal_arn=poweruser_arn,
                    policy_arn=poweruser_policy_arn,
                    access_scope=aws.eks.AccessPolicyAssociationAccessScopeArgs(
                        type="cluster",
                    ),
                    opts=pulumi.ResourceOptions(parent=self.eks, import_=poweruser_policy_import),
                )

        # --- Node Role Access Entry ---
        # Node roles are auto-created by AWS when changing auth mode to API_AND_CONFIG_MAP
        # We need to handle the case where AWS created a node entry with a different ARN
        # Check if the exact node_role.arn exists, or if there's any node entry we should adopt

        # Find existing node entries using "eks-node" pattern matching.
        # AWS auto-creates node entries with ARNs containing "eks-node" (e.g., eks-node-group-role).
        # Note: This pattern is based on AWS naming conventions and may need adjustment if AWS changes.
        node_entries = [entry for entry in existing_entries if "eks-node" in entry]

        if len(node_entries) > 1:
            pulumi.log.warn(
                f"Found multiple node entries matching 'eks-node' pattern: {node_entries}. "
                f"Using first match. If this causes issues, manually import the correct entry."
            )

        existing_node_entry_arn = node_entries[0] if node_entries else None

        # If we found an existing node entry, use that ARN for import
        # Otherwise, create a new one with node_role.arn
        if existing_node_entry_arn:
            # AWS already created a node entry - import it
            # Note: We use the existing ARN, not node_role.arn, since AWS may have created
            # this with a slightly different naming. The imported resource will use the
            # existing ARN going forward. If you need to use a different node role,
            # delete the access entry from AWS first.
            pulumi.log.debug(f"Setting import flag for Node Access Entry: {existing_node_entry_arn}")
            node_import_id = f"{self.name}:{existing_node_entry_arn}"

            # Warn if the imported ARN differs from the configured node_role.arn
            # This helps users understand when the managed resource differs from config
            def check_arn_mismatch(configured_arn: str) -> None:
                if configured_arn != existing_node_entry_arn:
                    pulumi.log.warn(
                        f"Importing node entry with ARN '{existing_node_entry_arn}' which differs from "
                        f"configured node_role ARN '{configured_arn}'. The imported ARN will be used. "
                        f"To use the configured role instead, delete the existing access entry from AWS."
                    )

            node_role.arn.apply(check_arn_mismatch)

            aws.eks.AccessEntry(
                f"{self.name}-node-access-entry",
                cluster_name=self.eks.name,
                principal_arn=existing_node_entry_arn,
                type="EC2_LINUX",
                opts=pulumi.ResourceOptions(parent=self.eks, import_=node_import_id),
            )

            # Check for and import any existing policy associations for the node entry.
            # Note: EC2_LINUX type entries receive implicit permissions through AWS-managed
            # policies (like AmazonEKSNodePolicy) that are NOT returned by list_associated_access_policies.
            # This means the set below will typically be empty, but we check anyway to import
            # any explicitly-associated policies that may exist.
            node_existing_policies = self._get_associated_policies(existing_node_entry_arn)
            for policy_idx, policy_arn in enumerate(node_existing_policies):
                pulumi.log.debug(f"Setting import flag for Node Policy Association: {policy_arn}")
                node_policy_import = f"{self.name}#{existing_node_entry_arn}#{policy_arn}"
                aws.eks.AccessPolicyAssociation(
                    f"{self.name}-node-policy-association-{policy_idx}",
                    cluster_name=self.eks.name,
                    principal_arn=existing_node_entry_arn,
                    policy_arn=policy_arn,
                    access_scope=aws.eks.AccessPolicyAssociationAccessScopeArgs(
                        type="cluster",
                    ),
                    opts=pulumi.ResourceOptions(parent=self.eks, import_=node_policy_import),
                )
        else:
            # No existing node entry - create a new one
            pulumi.log.info("Creating new Node Access Entry")
            aws.eks.AccessEntry(
                f"{self.name}-node-access-entry",
                cluster_name=self.eks.name,
                principal_arn=node_role.arn,
                type="EC2_LINUX",
                opts=pulumi.ResourceOptions(parent=self.eks),
            )

        # --- Additional Access Entries ---
        if additional_access_entries is None:
            additional_access_entries = []

        for idx, entry in enumerate(additional_access_entries):
            principal_arn = entry.get("principalArn")
            entry_type = entry.get("type", "STANDARD")
            access_policies = entry.get("accessPolicies", [])

            if not principal_arn:
                continue

            # Check for import
            entry_existing_policies = (
                self._get_associated_policies(principal_arn) if principal_arn in existing_entries else set()
            )
            entry_import = access_entry_import_id(principal_arn)

            if entry_import:
                pulumi.log.debug(f"Setting import flag for Additional Access Entry: {principal_arn}")

            access_entry = aws.eks.AccessEntry(
                f"{self.name}-additional-access-entry-{idx}",
                cluster_name=self.eks.name,
                principal_arn=principal_arn,
                type=entry_type,
                opts=pulumi.ResourceOptions(parent=self.eks, import_=entry_import),
            )

            # Create Policy Associations for each access policy
            for policy_idx, policy in enumerate(access_policies):
                policy_arn = policy.get("policyArn")
                access_scope_type = policy.get("accessScope", {}).get("type", "cluster")
                access_scope_namespaces = policy.get("accessScope", {}).get("namespaces", [])

                if not policy_arn:
                    continue

                access_scope_args = aws.eks.AccessPolicyAssociationAccessScopeArgs(
                    type=access_scope_type,
                    namespaces=access_scope_namespaces if access_scope_namespaces else None,
                )

                assoc_import = policy_association_import_id(principal_arn, policy_arn, entry_existing_policies)
                if assoc_import:
                    pulumi.log.debug(f"Setting import flag for Policy Association: {principal_arn} -> {policy_arn}")

                aws.eks.AccessPolicyAssociation(
                    f"{self.name}-additional-policy-association-{idx}-{policy_idx}",
                    cluster_name=self.eks.name,
                    principal_arn=principal_arn,
                    policy_arn=policy_arn,
                    access_scope=access_scope_args,
                    opts=pulumi.ResourceOptions(parent=access_entry, import_=assoc_import),
                )

        return self

    def with_encrypted_ebs_storage_class(self):
        """
        Create an encrypted EBS storage class and set it as the default.

        The AWS EBS CSI Driver creates an unencrypted storage class by default.
        This method creates a new encrypted storage class and marks it as default,
        while marking the original unencrypted class as non-default since we can't
        update encryption on an existing class.
        """
        storage_class_name = "ebs-csi-default-sc-encrypted"
        storage_class = k8s.storage.v1.StorageClass(
            f"{self.name}-{storage_class_name}",
            api_version="storage.k8s.io/v1",
            provisioner="ebs.csi.aws.com",
            allow_volume_expansion=True,
            parameters={"encrypted": "true"},
            metadata=k8s.meta.v1.ObjectMetaArgs(
                annotations={"storageclass.kubernetes.io/is-default-class": "true"},
                name=storage_class_name,
            ),
            reclaim_policy="Delete",
            volume_binding_mode="WaitForFirstConsumer",
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self.eks),
        )

        # change the original ebs class to be non-default
        # The ebs-csi-default-sc StorageClass is created by the EBS CSI addon, so we need to
        # depend on the addon being ready before we can patch it
        patch_depends_on = [storage_class]
        if self.ebs_csi_addon is not None:
            patch_depends_on.append(self.ebs_csi_addon)

        k8s.storage.v1.StorageClassPatch(
            f"{self.name}-ebs-csi-default-sc-patch",
            metadata=k8s.meta.v1.ObjectMetaPatchArgs(
                annotations={"storageclass.kubernetes.io/is-default-class": "false"},
                name="ebs-csi-default-sc",
            ),
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self.eks, depends_on=patch_depends_on),
        )

        return self

    # This will be deprecated once we migrate from old default to new default created via eks add-on
    def with_gp3(self):
        """
        Legacy creation of gp3 storage class as non-default. EBS CSI driver will now manage storage classes by default.

        :param opts: Optional. Resource options
        :return: self
        """

        # Add gp3 storage class
        k8s.storage.v1.StorageClass(
            f"{self.name}-gp3",
            api_version="storage.k8s.io/v1",
            provisioner="kubernetes.io/aws-ebs",
            allow_volume_expansion=True,
            parameters={"type": "gp3", "encrypted": "true"},
            metadata=k8s.meta.v1.ObjectMetaArgs(
                annotations={"storageclass.kubernetes.io/is-default-class": "false"},
                name="gp3",
            ),
            volume_binding_mode="WaitForFirstConsumer",
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self.eks),
        )

        return self

    def with_oidc_provider(self):
        """
        Create an oidc provider

        :param opts: Optional. Resource options
        :return: self
        """

        # Get the issuer url from the cluster
        # Stolen from Cole:
        # https://github.com/rstudio/aws-sol-eng/blob/6a37193de096fd9f86fa5392999d77bc6a3fcc38/colorado.py#L192-L209
        oidc_issuer_url = self.eks.identities[0]["oidcs"][0]["issuer"]
        thumbprint = oidc_issuer_url.apply(
            lambda url: ptd.oidc.get_thumbprint(ptd.oidc.get_network_location_for_oidc_endpoint(url))
        )

        # Create the oidc provider
        self.oidc_provider = aws.iam.OpenIdConnectProvider(
            self.name,
            url=oidc_issuer_url,
            client_id_lists=["sts.amazonaws.com"],
            thumbprint_lists=[thumbprint],
            opts=pulumi.ResourceOptions(
                protect=self.protect_persistent_resources,
                parent=self.eks,
            ),
        )

        return self

    def with_ebs_csi_driver(
        self,
        version: str | None = None,
        resolve_conflicts: str | None = None,
        resolve_conflicts_on_create: str | None = None,
        resolve_conflicts_on_update: str | None = None,
        role_name: str | None = None,
        depends_on: list[pulumi.Resource] | None = None,
    ) -> typing.Self:
        """
        Add the aws-ebs-csi-driver eks addon

        :param version: Optional, String, version of the aws-ebs-csi-driver addon to install, default: None
            By setting this to None, the latest version will be installed on first run, to upgrade versions later, you
            will need to specify a newer version.
        :param resolve_conflicts: Optional, String, PRESERVE, or OVERWRITE, or NONE see
            https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateAddon.html
            Deprecated in pulumi-aws 6.x:
            The "resolve_conflicts" attribute can't be set to "PRESERVE" on initial resource creation.
            Use "resolve_conflicts_on_create" and/or "resolve_conflicts_on_update" instead
        :param resolve_conflicts_on_update: Optional, String, PRESERVE, or OVERWRITE, or NONE see
            https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateAddon.html
            Only use with pulumi-aws 6.x and higher.
        :param resolve_conflicts_on_create: Optional, String, PRESERVE, or OVERWRITE, or NONE see
            https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateAddon.html
            Only use with pulumi-aws 6.x and higher.
        :param depends_on: Optional. Resources that must be created before the addon (e.g., node groups)
        :param opts: Optional. Resource options
        :return: self
        """

        if role_name is None:
            role_name = f"{self.name}-ebs-csi-driver"
        sa_role = self.create_service_account_role(
            role_name=role_name,
            service_accounts=[ServiceAccount(name="ebs-csi-controller-sa", namespace="kube-system")],
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        # Need to attach AmazonEBSCSIDriverPolicy
        aws.iam.RolePolicyAttachment(
            f"{self.name}-ebs-csi-driver",
            policy_arn="arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy",
            role=sa_role.name,
            opts=pulumi.ResourceOptions(parent=sa_role),
        )

        # Install the addon to the cluster
        self.ebs_csi_addon = aws.eks.Addon(
            f"{self.name}-ebs-csi",
            args=aws.eks.AddonArgs(
                addon_name="aws-ebs-csi-driver",
                addon_version=version,
                cluster_name=self.name,
                resolve_conflicts=resolve_conflicts,
                resolve_conflicts_on_create=resolve_conflicts_on_create,
                resolve_conflicts_on_update=resolve_conflicts_on_update,
                service_account_role_arn=sa_role.arn,
                tags=self.eks.tags,
                configuration_values=json.dumps(
                    {
                        "defaultStorageClass": {
                            "enabled": True,
                        },
                    }
                ),
            ),
            opts=pulumi.ResourceOptions(parent=self.eks, depends_on=depends_on),
        )

        return self

    def with_efs_csi_driver(
        self,
        role_name: str | None = None,
    ) -> typing.Self:
        """
        Add the aws-efs-csi-driver eks addon

        :param role_name: Optional, String, name of the IAM role to create for the EFS CSI driver service account
        :return: self
        """

        if role_name is None:
            role_name = f"{self.name}-efs-csi-driver"
        sa_role = self.create_service_account_role(
            role_name=role_name,
            service_accounts=[ServiceAccount(name="efs-csi-controller-sa", namespace="kube-system")],
            opts=pulumi.ResourceOptions(
                parent=self,
            ),
        )

        # Need to attach AmazonEFSCSIDriverPolicy
        aws.iam.RolePolicyAttachment(
            f"{self.name}-efs-csi-driver",
            policy_arn="arn:aws:iam::aws:policy/service-role/AmazonEFSCSIDriverPolicy",
            role=sa_role.name,
            opts=pulumi.ResourceOptions(parent=sa_role),
        )

        # Install the addon to the cluster
        aws.eks.Addon(
            f"{self.name}-efs-csi",
            args=aws.eks.AddonArgs(
                addon_name="aws-efs-csi-driver",
                cluster_name=self.name,
                service_account_role_arn=sa_role.arn,
                tags=self.eks.tags,
            ),
            opts=pulumi.ResourceOptions(parent=self.eks),
        )

        return self

    def attach_efs_security_group(
        self,
        efs_file_system_id: str,
        security_group_id: str,
        *,
        mount_targets_managed: bool = True,
        region: str = "us-east-2",
    ) -> typing.Self:
        """
        Attach a security group to EFS mount targets.

        This method queries all mount targets for the specified EFS file system and attaches
        the provided security group to each one. This is necessary to allow EKS nodes to
        communicate with EFS over NFS (port 2049).

        :param efs_file_system_id: The EFS file system ID (e.g., 'fs-xxxxx')
        :param security_group_id: The security group ID to attach (e.g., 'sg-xxxxx')
        :param mount_targets_managed: If True, attaches the security group to mount targets.
                                      Set to False for BYO-EFS scenarios where mount targets
                                      are in a different VPC. Default: True
        :param region: AWS region where the EFS file system exists. Default: 'us-east-2'
        :return: Self for method chaining

        :raises RuntimeError: If mount target queries or security group modifications fail

        Note:
            - This is a Pulumi dynamic operation that executes during the apply phase
            - If mount_targets_managed=False, this method returns immediately without errors
            - Existing security groups on mount targets are preserved
        """

        if not mount_targets_managed:
            # Don't attach security group if mount targets are not managed
            return self

        # Query EFS mount targets
        def attach_sg_to_mount_targets(args):
            fs_id, sg_id = args
            efs_client = boto3.client("efs", region_name=region)

            try:
                response = efs_client.describe_mount_targets(FileSystemId=fs_id)
            except Exception as e:
                msg = f"Failed to describe mount targets for EFS {fs_id}: {e}"
                raise RuntimeError(msg) from e

            mount_targets = response.get("MountTargets", [])
            if not mount_targets:
                warnings.warn(f"No mount targets found for EFS file system {fs_id}", stacklevel=2)
                return

            for mt in mount_targets:
                mt_id = mt["MountTargetId"]
                existing_sgs = mt.get("SecurityGroups", [])

                # Add our security group if not already present
                if sg_id not in existing_sgs:
                    try:
                        updated_sgs = [*existing_sgs, sg_id]
                        efs_client.modify_mount_target_security_groups(MountTargetId=mt_id, SecurityGroups=updated_sgs)
                        print(f"Added security group {sg_id} to mount target {mt_id}")
                    except Exception as e:
                        msg = f"Failed to attach security group {sg_id} to mount target {mt_id}: {e}"
                        raise RuntimeError(msg) from e
                else:
                    print(f"Security group {sg_id} already attached to mount target {mt_id}")

        # Use pulumi.Output.all to wait for the security group ID
        pulumi.Output.all(efs_file_system_id, security_group_id).apply(attach_sg_to_mount_targets)

        return self

    def with_aws_lbc(
        self,
        version: str | None = None,
        chart_version: str | None = None,
    ) -> typing.Self:
        """
        Add the aws-lbc eks helm chart

        :param version: Optional, String, aws-load-balancer-controller image version, default: None
            By setting this to None, the latest version will be installed on first run, to upgrade versions later, you
            will need to specify a newer version.
        :param chart_version: Optional, String, the version of the aws-load-balancer-controller helm chart to use.
        :param opts: Optional. Resource options
        :return: self
        """

        sa_role = self.create_service_account_role(
            role_name=f"{self.name}-aws-eks-lbc",
            service_accounts=[ServiceAccount(name="aws-load-balancer-controller", namespace="kube-system")],
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Copied from
        # https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/v2.5.3/docs/install/iam_policy.json
        policy_document = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    resources=["*"],
                    actions=["iam:CreateServiceLinkedRole"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            values=["elasticloadbalancing.amazonaws.com"],
                            variable="iam:AWSServiceName",
                        )
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "ec2:DescribeAccountAttributes",
                        "ec2:DescribeAddresses",
                        "ec2:DescribeAvailabilityZones",
                        "ec2:DescribeInternetGateways",
                        "ec2:DescribeVpcs",
                        "ec2:DescribeVpcPeeringConnections",
                        "ec2:DescribeSubnets",
                        "ec2:DescribeSecurityGroups",
                        "ec2:DescribeInstances",
                        "ec2:DescribeNetworkInterfaces",
                        "ec2:DescribeTags",
                        "ec2:GetCoipPoolUsage",
                        "ec2:DescribeCoipPools",
                        "elasticloadbalancing:DescribeLoadBalancers",
                        "elasticloadbalancing:DescribeLoadBalancerAttributes",
                        "elasticloadbalancing:DescribeListeners",
                        "elasticloadbalancing:DescribeListenerAttributes",
                        "elasticloadbalancing:DescribeListenerCertificates",
                        "elasticloadbalancing:DescribeSSLPolicies",
                        "elasticloadbalancing:DescribeRules",
                        "elasticloadbalancing:DescribeTargetGroups",
                        "elasticloadbalancing:DescribeTargetGroupAttributes",
                        "elasticloadbalancing:DescribeTargetHealth",
                        "elasticloadbalancing:DescribeTags",
                    ],
                    resources=["*"],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "cognito-idp:DescribeUserPoolClient",
                        "acm:ListCertificates",
                        "acm:DescribeCertificate",
                        "iam:ListServerCertificates",
                        "iam:GetServerCertificate",
                        "waf-regional:GetWebACL",
                        "waf-regional:GetWebACLForResource",
                        "waf-regional:AssociateWebACL",
                        "waf-regional:DisassociateWebACL",
                        "wafv2:GetWebACL",
                        "wafv2:GetWebACLForResource",
                        "wafv2:AssociateWebACL",
                        "wafv2:DisassociateWebACL",
                        "shield:GetSubscriptionState",
                        "shield:DescribeProtection",
                        "shield:CreateProtection",
                        "shield:DeleteProtection",
                    ],
                    resources=["*"],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "ec2:AuthorizeSecurityGroupIngress",
                        "ec2:RevokeSecurityGroupIngress",
                    ],
                    resources=["*"],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow", actions=["ec2:CreateSecurityGroup"], resources=["*"]
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=["ec2:CreateTags"],
                    resources=["arn:aws:ec2:*:*:security-group/*"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            values=["CreateSecurityGroup"],
                            variable="ec2:CreateAction",
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["false"],
                            variable="aws:RequestTag/elbv2.k8s.aws/cluster",
                        ),
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=["ec2:CreateTags", "ec2:DeleteTags"],
                    resources=["arn:aws:ec2:*:*:security-group/*"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["true"],
                            variable="aws:RequestTag/elbv2.k8s.aws/cluster",
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["false"],
                            variable="aws:ResourceTag/elbv2.k8s.aws/cluster",
                        ),
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "ec2:AuthorizeSecurityGroupIngress",
                        "ec2:RevokeSecurityGroupIngress",
                        "ec2:DeleteSecurityGroup",
                    ],
                    resources=["*"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["false"],
                            variable="aws:ResourceTag/elbv2.k8s.aws/cluster",
                        ),
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "elasticloadbalancing:CreateLoadBalancer",
                        "elasticloadbalancing:CreateTargetGroup",
                    ],
                    resources=["*"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["false"],
                            variable="aws:RequestTag/elbv2.k8s.aws/cluster",
                        )
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "elasticloadbalancing:CreateListener",
                        "elasticloadbalancing:DeleteListener",
                        "elasticloadbalancing:CreateRule",
                        "elasticloadbalancing:DeleteRule",
                    ],
                    resources=["*"],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "elasticloadbalancing:AddTags",
                        "elasticloadbalancing:RemoveTags",
                    ],
                    resources=[
                        "arn:aws:elasticloadbalancing:*:*:targetgroup/*/*",
                        "arn:aws:elasticloadbalancing:*:*:loadbalancer/net/*/*",
                        "arn:aws:elasticloadbalancing:*:*:loadbalancer/app/*/*",
                    ],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["true"],
                            variable="aws:RequestTag/elbv2.k8s.aws/cluster",
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["false"],
                            variable="aws:ResourceTag/elbv2.k8s.aws/cluster",
                        ),
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "elasticloadbalancing:AddTags",
                        "elasticloadbalancing:RemoveTags",
                    ],
                    resources=[
                        "arn:aws:elasticloadbalancing:*:*:listener/net/*/*/*",
                        "arn:aws:elasticloadbalancing:*:*:listener/app/*/*/*",
                        "arn:aws:elasticloadbalancing:*:*:listener-rule/net/*/*/*",
                        "arn:aws:elasticloadbalancing:*:*:listener-rule/app/*/*/*",
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "elasticloadbalancing:ModifyLoadBalancerAttributes",
                        "elasticloadbalancing:SetIpAddressType",
                        "elasticloadbalancing:SetSecurityGroups",
                        "elasticloadbalancing:SetSubnets",
                        "elasticloadbalancing:DeleteLoadBalancer",
                        "elasticloadbalancing:ModifyTargetGroup",
                        "elasticloadbalancing:ModifyTargetGroupAttributes",
                        "elasticloadbalancing:DeleteTargetGroup",
                    ],
                    resources=["*"],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["false"],
                            variable="aws:ResourceTag/elbv2.k8s.aws/cluster",
                        ),
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=["elasticloadbalancing:AddTags"],
                    resources=[
                        "arn:aws:elasticloadbalancing:*:*:targetgroup/*/*",
                        "arn:aws:elasticloadbalancing:*:*:loadbalancer/net/*/*",
                        "arn:aws:elasticloadbalancing:*:*:loadbalancer/app/*/*",
                    ],
                    conditions=[
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="StringEquals",
                            values=["CreateTargetGroup", "CreateLoadBalancer"],
                            variable="elasticloadbalancing:CreateAction",
                        ),
                        aws.iam.GetPolicyDocumentStatementConditionArgs(
                            test="Null",
                            values=["false"],
                            variable="aws:ResourceTag/elbv2.k8s.aws/cluster",
                        ),
                    ],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "elasticloadbalancing:RegisterTargets",
                        "elasticloadbalancing:DeregisterTargets",
                    ],
                    resources=["arn:aws:elasticloadbalancing:*:*:targetgroup/*/*"],
                ),
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "elasticloadbalancing:SetWebAcl",
                        "elasticloadbalancing:ModifyListener",
                        "elasticloadbalancing:AddListenerCertificates",
                        "elasticloadbalancing:RemoveListenerCertificates",
                        "elasticloadbalancing:ModifyRule",
                    ],
                    resources=["*"],
                ),
            ]
        )

        aws_lbc_iam_policy = aws.iam.Policy(
            f"{self.name}-aws-lbc",
            name=f"{self.name}-AWSLoadBalancerControllerIAMPolicy",
            policy=policy_document.json,
            opts=pulumi.ResourceOptions(parent=sa_role, aliases=[pulumi.Alias(parent=self)]),
        )

        # Need to attach aws_lbc_iam_policy
        aws.iam.RolePolicyAttachment(
            f"{self.name}-aws-lbc",
            policy_arn=aws_lbc_iam_policy.arn,
            role=sa_role.name,
            opts=pulumi.ResourceOptions(
                parent=aws_lbc_iam_policy,
                aliases=[pulumi.Alias(parent=sa_role)],
            ),
        )

        # Need to create the aws-load-balancer-controller ServiceAccount
        aws_lbc_sa = k8s.core.v1.ServiceAccount(
            f"{self.name}-aws-lbc",
            metadata=k8s.meta.v1.ObjectMetaArgs(
                name="aws-load-balancer-controller",
                namespace="kube-system",
                labels={
                    "app.kubernetes.io/component": "controller",
                    "app.kubernetes.io/name": "aws-load-balancer-controller",
                },
                annotations={"eks.amazonaws.com/role-arn": sa_role.arn},
            ),
            opts=pulumi.ResourceOptions(
                provider=self.provider,
                parent=self,
                aliases=[pulumi.Alias(parent=sa_role)],
            ),
        )

        # Install the aws-lbc helm chart to the cluster
        k8s.helm.v3.Release(
            f"{self.name}-aws-lbc",
            k8s.helm.v3.ReleaseArgs(
                chart="aws-load-balancer-controller",
                namespace="kube-system",
                name="aws-load-balancer-controller",
                timeout=900,
                version=chart_version,
                repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                    repo="https://aws.github.io/eks-charts",
                ),
                values={
                    "clusterName": self.name,
                    "image": {"tag": version},
                    "serviceAccount": {
                        "create": False,
                        "name": "aws-load-balancer-controller",
                    },
                    "hostNetwork": True,
                },
            ),
            opts=pulumi.ResourceOptions(
                provider=self.provider,
                parent=self,
                aliases=[pulumi.Alias(parent=aws_lbc_sa)],
            ),
        )

        return self

    def with_metrics_server(self, version: str) -> typing.Self:
        k8s.helm.v3.Release(
            f"{self.name}-metrics-server",
            k8s.helm.v3.ReleaseArgs(
                name="metrics-server",
                chart="metrics-server",
                version=version,
                namespace="kube-system",
                repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                    repo="https://kubernetes-sigs.github.io/metrics-server/",
                ),
            ),
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self),
        )
        return self

    def with_secret_store_csi(self, version: str) -> typing.Self:
        k8s.helm.v3.Release(
            f"{self.name}-secret-store-csi",
            k8s.helm.v3.ReleaseArgs(
                name="secrets-store-csi-driver",
                chart="secrets-store-csi-driver",
                version=version,
                namespace="kube-system",
                repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                    repo="https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts",
                ),
                values={
                    "syncSecret": {
                        "enabled": True,
                    },
                },
            ),
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self),
        )
        return self

    def with_secret_store_csi_aws_provider(self, version: str) -> typing.Self:
        k8s.helm.v3.Release(
            f"{self.name}-secrets-store-csi-driver-provider-aws",
            k8s.helm.v3.ReleaseArgs(
                name="secrets-store-csi-driver-provider-aws",
                chart="secrets-store-csi-driver-provider-aws",
                version=version,
                namespace="kube-system",
                repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                    repo="https://aws.github.io/secrets-store-csi-driver-provider-aws",
                ),
                values={},
            ),
            opts=pulumi.ResourceOptions(provider=self.provider, parent=self),
        )

        return self

    def with_traefik_forward_auth(
        self, domain: str, version: str, opts: pulumi.ResourceOptions | None = None
    ) -> typing.Self:
        opts = opts or pulumi.ResourceOptions()
        account_id = aws.get_caller_identity().account_id
        oidc_issuer_url = self.eks.identities[0]["oidcs"][0]["issuer"]
        role_name = f"traefik-forward-auth.{self.name}.posit.team"

        assume_role_policy = oidc_issuer_url.apply(
            lambda url: aws.iam.get_policy_document(
                statements=[
                    aws.iam.GetPolicyDocumentStatementArgs(
                        effect="Allow",
                        principals=[
                            aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                                type="Federated",
                                identifiers=[
                                    f"arn:aws:iam::{aws.get_caller_identity().account_id}:oidc-provider/{url.split('//')[1]}"
                                ],
                            )
                        ],
                        actions=["sts:AssumeRoleWithWebIdentity"],
                        conditions=[
                            aws.iam.GetPolicyDocumentStatementConditionArgs(
                                test="StringEquals",
                                variable=f"{url.split('//')[1]}:aud",
                                values=["sts.amazonaws.com"],
                            ),
                            aws.iam.GetPolicyDocumentStatementConditionArgs(
                                test="StringEquals",
                                variable=f"{url.split('//')[1]}:sub",
                                values=[f"system:serviceaccount:kube-system:{ptd.Roles.TRAEFIK_FORWARD_AUTH}"],
                            ),
                        ],
                    )
                ]
            )
        )

        role = aws.iam.Role(
            role_name,
            name=role_name,
            assume_role_policy=assume_role_policy.json,
            permissions_boundary=self.iam_permissions_boundary,
            opts=pulumi.ResourceOptions.merge(
                opts,
                pulumi.ResourceOptions(parent=self),
            ),
        )

        policy_doc = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "secretsmanager:GetSecretValue",
                        "secretsmanager:DescribeSecret",
                    ],
                    resources=[
                        f"arn:aws:secretsmanager:*:{account_id}:secret:okta-oidc-client-creds-*",
                        f"arn:aws:secretsmanager:*:{account_id}:secret:okta-oidc-client-creds.*.posit.team",
                        f"arn:aws:secretsmanager:*:{account_id}:secret:okta-oidc-client-creds.*.posit.team*",
                    ],
                )
            ]
        )

        policy = aws.iam.Policy(
            f"{self.name}-traefik-forward-auth-secrets-policy",
            name=f"{self.name}-traefik-forward-auth-secrets-policy",
            policy=policy_doc.json,
            opts=pulumi.ResourceOptions.merge(
                opts,
                pulumi.ResourceOptions(parent=role),
            ),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.name}-traefik-forward-auth",
            policy_arn=policy.arn,
            role=role.name,
            opts=pulumi.ResourceOptions.merge(
                opts,
                pulumi.ResourceOptions(parent=policy),
            ),
        )

        k8s.helm.v3.Release(
            f"{self.name}-traefik-forward-auth",
            k8s.helm.v3.ReleaseArgs(
                name="traefik-forward-auth",
                chart="traefik-forward-auth",
                version=version,
                namespace="kube-system",
                repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                    repo="https://colearendt.github.io/helm",
                ),
                values={
                    "config": {
                        "auth-host": f"sso.{domain}",
                        "cookie-domain": domain,
                        "cookie-name": "ptd_mgmt_auth",
                        "csrf-cookie-name": "csrf_ptd_mgmt_auth",
                        "default-provider": "oidc",
                        "log-level": "debug",
                        "providers.oidc.issuer-url": "https://posit.okta.com",
                        "url-path": "/__oauth__",
                    },
                    "serviceAccount": {
                        "create": True,
                        "name": str(ptd.Roles.TRAEFIK_FORWARD_AUTH),
                        "annotations": {
                            "eks.amazonaws.com/role-arn": f"arn:aws:iam::{account_id}:role/" + role_name,
                        },
                    },
                    "extraObjects": self.define_traefik_auth_extra_objects(),
                    "pod": {
                        "env": self.define_traefik_auth_pod_env(),
                        "volumes": self.define_traefik_auth_volumes(),
                        "volumeMounts": self.define_traefik_auth_volume_mounts(),
                    },
                    "ingress": {
                        "enabled": True,
                        "className": "traefik",
                        "annotations": {
                            "traefik.ingress.kubernetes.io/router.middlewares": "kube-system-traefik-forward-auth-add-forwarded-headers@kubernetescrd,kube-system-traefik-forward-auth@kubernetescrd",
                        },
                        "hosts": [
                            {
                                "host": f"sso.{domain}",
                                "paths": ["/"],
                            }
                        ],
                    },
                },
            ),
            opts=pulumi.ResourceOptions.merge(
                opts,
                pulumi.ResourceOptions(provider=self.provider, parent=self, delete_before_replace=True),
            ),
        )

        return self

    def with_grafana(
        self,
        domain: str,
        db_connection_output: pulumi.Output,
        opsgenie_key: str,
        wl_account_ids: set[str],
        version: str,
    ) -> typing.Self:
        grafana_ns = k8s.core.v1.Namespace(
            f"{self.name}-grafana-ns",
            metadata={"name": "grafana"},
            opts=pulumi.ResourceOptions(parent=self, provider=self.provider),
        )

        k8s.core.v1.Secret(
            f"{self.name}-opsgenie-secret",
            metadata={
                "name": "opsgenie-api-key",
                "namespace": "grafana",
            },
            data={"POSIT_OPSGENIE_KEY": base64.b64encode(opsgenie_key.encode()).decode()},
            opts=pulumi.ResourceOptions(parent=self, providers=[self.provider], depends_on=grafana_ns),
        )

        self._create_alert_configmap("pods", grafana_ns)
        self._create_alert_configmap("cloudwatch", grafana_ns)
        self._create_alert_configmap("healthchecks", grafana_ns)
        self._create_alert_configmap("nodes", grafana_ns)
        self._create_alert_configmap("applications", grafana_ns)

        # TODO: auth.proxy should be configurable, prod grafana auth will need tighter controls than letting anyone in as an Editor
        k8s.helm.v3.Release(
            f"{self.name}-grafana",
            k8s.helm.v3.ReleaseArgs(
                name="grafana",
                chart="grafana",
                version=version,
                namespace="grafana",
                repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                    repo="https://grafana.github.io/helm-charts",
                ),
                values={
                    "alerting": {
                        "contactpoints.yaml": {
                            "apiVersion": "v1",
                            "contactPoints": [
                                {
                                    "orgId": 1,
                                    "name": "PositOpsGenie",
                                    "receivers": [
                                        {
                                            "uid": "positOpsGenie",
                                            "type": "opsgenie",
                                            "settings": {
                                                "apiKey": '${{ "{" }}POSIT_OPSGENIE_KEY{{ "}" }}',  # ${POSIT_OPSGENIE_KEY} in the resulting configMap,
                                                "apiUrl": "https://api.opsgenie.com/v2/alerts",
                                            },
                                        }
                                    ],
                                }
                            ],
                        }
                    },
                    "datasources": {
                        "datasources.yaml": {
                            "apiVersion": 1,
                            "datasources": [
                                {
                                    "name": "Mimir",
                                    "uid": "mimir",
                                    "type": "prometheus",
                                    "access": "proxy",
                                    "editable": False,
                                    "url": "http://mimir-gateway.mimir.svc.cluster.local/prometheus",
                                    "isDefault": True,
                                    "jsonData": {"httpHeaderName1": "X-Scope-OrgID"},
                                    "secureJsonData": {"httpHeaderValue1": "|".join(wl_account_ids)},
                                },
                            ],
                        },
                    },
                    "envFromSecret": "opsgenie-api-key",
                    "grafana.ini": {
                        "server": {
                            "domain": domain,
                            "root_url": f"https://{domain}/grafana",
                            "serve_from_sub_path": True,
                        },
                        "auth.proxy": {
                            "enabled": True,
                            "header_name": "X-Forwarded-User",
                            "header_property": "username",
                            "auto_sign_up": True,
                        },
                        "auth": {
                            "disable_signout_menu": True,
                        },
                        "database": {
                            "url": db_connection_output.apply(lambda x: x),
                            "ssl_mode": "require",
                        },
                        "users": {
                            "auto_assign_org_role": "Editor",
                        },
                    },
                    "ingress": {
                        "enabled": True,
                        "annotations": {
                            "traefik.ingress.kubernetes.io/router.middlewares": "kube-system-traefik-forward-auth-add-forwarded-headers@kubernetescrd,kube-system-traefik-forward-auth@kubernetescrd",
                        },
                        "hosts": [domain],
                        "path": "/grafana",
                    },
                    "sidecar": {
                        "alerts": {
                            "enabled": True,
                            "searchNamespace": "grafana",
                        }
                    },
                },
            ),
            opts=pulumi.ResourceOptions(
                provider=self.provider,
                parent=self,
                delete_before_replace=True,
                ignore_changes=["checksum"],
                depends_on=grafana_ns,
            ),
        )

        return self

    def with_mimir(
        self,
        bucket_prefix: str,
        domain: str,
        mimir_creds: dict[str, str],
        salt: str,
        tags: dict[str, str],
        version: str,
    ) -> typing.Self:
        block_storage = aws.s3.Bucket(
            f"{self.name}-mimir-storage",
            aws.s3.BucketArgs(
                bucket_prefix=f"{self.name}-mimir-storage-",
                acl="private",
                tags=tags,
            ),
            opts=pulumi.ResourceOptions(
                parent=self,
                protect=self.protect_persistent_resources,
                retain_on_delete=True,
            ),
        )

        ruler_storage = aws.s3.Bucket(
            f"{self.name}-mimir-ruler-storage",
            aws.s3.BucketArgs(
                bucket_prefix=bucket_prefix,
                acl="private",
                tags=tags,
            ),
            opts=pulumi.ResourceOptions(
                parent=self,
                protect=self.protect_persistent_resources,
                retain_on_delete=True,
            ),
        )

        account_id = aws.get_caller_identity().account_id
        oidc_issuer_url = self.eks.identities[0]["oidcs"][0]["issuer"]

        assume_role_policy = oidc_issuer_url.apply(
            lambda url: aws.iam.get_policy_document(
                statements=[
                    aws.iam.GetPolicyDocumentStatementArgs(
                        effect="Allow",
                        principals=[
                            aws.iam.GetPolicyDocumentStatementPrincipalArgs(
                                type="Federated",
                                identifiers=[f"arn:aws:iam::{account_id}:oidc-provider/{url.split('//')[1]}"],
                            )
                        ],
                        actions=["sts:AssumeRoleWithWebIdentity"],
                        conditions=[
                            aws.iam.GetPolicyDocumentStatementConditionArgs(
                                test="StringEquals",
                                variable=f"{url.split('//')[1]}:aud",
                                values=["sts.amazonaws.com"],
                            ),
                            aws.iam.GetPolicyDocumentStatementConditionArgs(
                                test="StringEquals",
                                variable=f"{url.split('//')[1]}:sub",
                                values=["system:serviceaccount:mimir:mimir"],
                            ),
                        ],
                    )
                ]
            )
        )

        storage_role = aws.iam.Role(
            f"{self.name}-mimir",
            name=f"{self.name}-mimir",
            assume_role_policy=assume_role_policy.json,
            permissions_boundary=self.iam_permissions_boundary,
            opts=pulumi.ResourceOptions(parent=self),
        )

        policy_doc = aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[  # TODO: guessing on this list :( https://github.com/grafana/mimir/issues/5523
                        "s3:PutObject",
                        "s3:GetBucketLocation",
                        "s3:GetObject",
                        "s3:HeadObject",
                        "s3:ListBucket",
                        "s3:ListObjects",
                        "s3:DeleteObject",
                        "s3:GetObjectTagging",
                        "s3:PutObjectTagging",
                    ],
                    resources=[
                        f"arn:aws:s3:::{self.name}-mimir-storage-*",
                        f"arn:aws:s3:::{bucket_prefix}*",
                    ],
                )
            ]
        )

        policy = aws.iam.Policy(
            f"{self.name}-mimir-storage-policy",
            name=f"{self.name}-mimir-storage-policy",
            policy=policy_doc.json,
            opts=pulumi.ResourceOptions(parent=storage_role),
        )

        aws.iam.RolePolicyAttachment(
            f"{self.name}-mimir-storage",
            policy_arn=policy.arn,
            role=storage_role.name,
            opts=pulumi.ResourceOptions(parent=policy),
        )

        mimir_ns_name = "mimir"
        mimir_ns = k8s.core.v1.Namespace(
            f"{self.name}-mimir-ns",
            metadata={"name": mimir_ns_name},
            opts=pulumi.ResourceOptions(parent=self, provider=self.provider),
        )

        s3_endpoint = "s3.us-east-2.amazonaws.com"  # TODO: don't hardcode region

        hashed_creds = {}
        for user, pw in mimir_creds.items():
            hashed_pw = bcrypt.hashpw(
                pw.encode(),
                salt.encode(),
            )
            hashed_creds[user] = hashed_pw.decode()

        k8s.helm.v3.Release(
            f"{self.name}-mimir",
            k8s.helm.v3.ReleaseArgs(
                name="mimir",
                chart="mimir-distributed",
                version=version,
                namespace=mimir_ns.metadata.name,
                repository_opts=k8s.helm.v3.RepositoryOptsArgs(
                    repo="https://grafana.github.io/helm-charts",
                ),
                values={
                    "serviceAccount": {
                        "create": True,
                        "name": "mimir",
                        "annotations": {
                            "eks.amazonaws.com/role-arn": f"arn:aws:iam::{account_id}:role/{self.name}-mimir",
                        },
                    },
                    "minio": {
                        "enabled": False,
                    },
                    "mimir": {
                        "structuredConfig": {
                            "blocks_storage": {
                                "backend": "s3",
                                "s3": {
                                    "bucket_name": block_storage.bucket,
                                    "endpoint": s3_endpoint,
                                    "insecure": False,
                                },
                            },
                            "alertmanager_storage": {
                                "backend": "s3",
                                "s3": {
                                    "bucket_name": ruler_storage.bucket,
                                    "endpoint": s3_endpoint,
                                    "insecure": False,
                                },
                            },
                            "ruler_storage": {
                                "backend": "s3",
                                "s3": {
                                    "bucket_name": ruler_storage.bucket,
                                    "endpoint": s3_endpoint,
                                    "insecure": False,
                                },
                            },
                            "tenant_federation": {
                                "enabled": True,
                            },
                            "limits": {
                                "max_global_series_per_user": 800000,
                                "max_label_names_per_series": 45,
                            },
                        }
                    },
                    "alertmanager": {"enabled": False},
                    "ingester": {"persistentVolume": {"size": "20Gi"}},
                    "compactor": {"persistentVolume": {"size": "20Gi"}},
                    "distributor": {"replicas": 3},
                    "store_gateway": {"persistentVolume": {"size": "20Gi"}, "replicas": 3},
                    "nginx": {"enabled": False},
                    "gateway": {
                        "enabledNonEnterprise": True,
                        "ingress": {
                            "enabled": True,
                            "annotations": {
                                "traefik.ingress.kubernetes.io/router.middlewares": f"{mimir_ns_name}-mimir-basic-auth@kubernetescrd"
                            },
                            "hosts": [
                                {
                                    "host": f"mimir.{domain}",
                                    "paths": [
                                        {
                                            "path": "/",
                                            "pathType": "Prefix",
                                        }
                                    ],
                                }
                            ],
                        },
                    },
                    "extraObjects": [
                        {
                            "apiVersion": "v1",
                            "kind": "Secret",
                            "metadata": {
                                "name": "mimir-basic-auth",
                                "namespace": mimir_ns.metadata.name,
                            },
                            "stringData": {
                                "users": "\n".join(f"{user}:{password}" for user, password in hashed_creds.items())
                            },
                        },
                        {
                            "apiVersion": "traefik.io/v1alpha1",
                            "kind": "Middleware",
                            "metadata": {
                                "name": "mimir-basic-auth",
                                "namespace": mimir_ns.metadata.name,
                            },
                            "spec": {"basicAuth": {"secret": "mimir-basic-auth"}},
                        },
                    ],
                },
            ),
            opts=pulumi.ResourceOptions(parent=mimir_ns, provider=self.provider),
        )

        return self

    @staticmethod
    def define_traefik_auth_extra_objects(
        okta_oidc_client_creds_secret: str = "okta-oidc-client-creds",  # noqa: S107
    ):
        return [
            {
                "apiVersion": "traefik.io/v1alpha1",
                "kind": "Middleware",
                "metadata": {
                    "name": "traefik-forward-auth",
                    "namespace": "kube-system",
                },
                "spec": {
                    "forwardAuth": {
                        "address": "http://traefik-forward-auth.kube-system.svc.cluster.local",
                        "trustForwardHeader": True,
                        "authResponseHeaders": ["X-Forwarded-User"],
                    }
                },
            },
            {
                "apiVersion": "traefik.io/v1alpha1",
                "kind": "Middleware",
                "metadata": {
                    "name": "traefik-forward-auth-add-forwarded-headers",
                    "namespace": "kube-system",
                },
                "spec": {
                    "headers": {
                        "customRequestHeaders": {
                            "X-Forwarded-Proto": "https",
                            "X-Forwarded-Port": "443",
                        },
                    }
                },
            },
            {
                "apiVersion": "secrets-store.csi.x-k8s.io/v1",
                "kind": "SecretProviderClass",
                "metadata": {
                    "name": "traefik-forward-auth-oidc-client-creds",
                    "namespace": "kube-system",
                },
                "spec": {
                    "provider": "aws",
                    "parameters": {
                        "objects": json.dumps(
                            [
                                {
                                    "jmesPath": [
                                        {
                                            "objectAlias": "clientId",
                                            "path": "oidcClientId",
                                        },
                                        {
                                            "objectAlias": "clientSecret",
                                            "path": "oidcClientSecret",
                                        },
                                        {
                                            "objectAlias": "signingSecret",
                                            "path": "signingSecret",
                                        },
                                    ],
                                    "objectName": okta_oidc_client_creds_secret,
                                    "objectType": "secretsmanager",
                                }
                            ]
                        ),
                    },
                    f"se{'' if True else 'dear snyk'}cretObjects": [
                        {
                            f"se{'' if True else 'please calm down'}cretName": "traefik-forward-auth-oidc-client-creds",
                            "type": "Opaque",
                            "data": [
                                {
                                    "key": "clientId",
                                    "objectName": "clientId",
                                },
                                {
                                    "key": "clientSecret",
                                    "objectName": "clientSecret",
                                },
                                {
                                    "key": "signingSecret",
                                    "objectName": "signingSecret",
                                },
                            ],
                        }
                    ],
                },
            },
        ]

    @staticmethod
    def define_traefik_auth_pod_env():
        return [
            {
                "name": "PROVIDERS_OIDC_CLIENT_ID",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": "traefik-forward-auth-oidc-client-creds",
                        "key": "clientId",
                    }
                },
            },
            {
                "name": "PROVIDERS_OIDC_CLIENT_SECRET",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": "traefik-forward-auth-oidc-client-creds",
                        "key": "clientSecret",
                    }
                },
            },
            {
                "name": "SECRET",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": "traefik-forward-auth-oidc-client-creds",
                        "key": "signingSecret",
                    }
                },
            },
        ]

    @staticmethod
    def define_traefik_auth_volumes():
        return [
            {
                "name": "oidc-client-creds",
                "csi": {
                    "driver": "secrets-store.csi.k8s.io",
                    "readOnly": True,
                    "volumeAttributes": {
                        "secretProviderClass": "traefik-forward-auth-oidc-client-creds",
                    },
                },
            },
        ]

    @staticmethod
    def define_traefik_auth_volume_mounts():
        return [
            {
                "name": "oidc-client-creds",
                "mountPath": "/mnt/secrets/oidc-client-creds",
                "readOnly": True,
            }
        ]

    def _get_provider_args(self) -> k8s.ProviderArgs:
        # Generate the kubeconfig
        self.kube_config = self._generate_kube_config()

        return k8s.ProviderArgs(enable_server_side_apply=True, kubeconfig=self.kube_config)

    # Copied from https://github.com/pulumi/examples/blob/master/aws-py-eks/utils.py
    def _generate_kube_config(self):
        """
        Generate the kube config necessary to connect to the cluster as PowerUser or User role
        :return: json kubernetes config string
        """
        return pulumi.Output.all(self.eks.endpoint, self.eks.certificate_authority.apply(lambda v: v.data)).apply(
            lambda args: get_kubeconfig_for_cluster(self.name, self.tailscale_enabled, args[0], args[1])
        )

    def _create_alert_configmap(self, name: str, ns: k8s.core.v1.Namespace) -> k8s.core.v1.ConfigMap:
        file_path = ptd.paths.alerts() / f"{name}.yaml"
        with open(file_path) as alert_file:
            alert_yaml = alert_file.read()

        return k8s.core.v1.ConfigMap(
            f"{self.name}-grafana-{name}-alerts",
            metadata={
                "name": f"grafana-{name}-alerts",
                "namespace": "grafana",
                "labels": {"grafana_alert": "1"},
            },
            data={"alerts.yaml": alert_yaml},
            opts=pulumi.ResourceOptions(parent=self, provider=self.provider, depends_on=ns),
        )

    def setup_tailscale_access(self):
        sg_name = f"{self.sg_prefix}-tailscale"
        self.eks.vpc_config.apply(lambda config: self._setup_sg_access(sg_name, config.vpc_id))

    def setup_bastion_access(self):
        sg_name = f"{self.sg_prefix}-bastion"
        self.eks.vpc_config.apply(lambda config: self._setup_sg_access(sg_name, config.vpc_id))

    def _setup_sg_access(self, sg_name: str, vpc_id: str):
        sg = aws.ec2.get_security_group(
            filters=[
                {"name": "vpc-id", "values": [vpc_id]},
            ],
            tags={"Name": sg_name},
        )

        eks_sg = aws.ec2.get_security_group(
            filters=[
                {"name": "group-id", "values": [self.eks.vpc_config.cluster_security_group_id]},
            ]
        )

        aws.ec2.SecurityGroupRule(
            f"{sg_name}-internal-vpc-allow-inbound",
            type="ingress",
            from_port=0,
            to_port=0,
            protocol="-1",
            security_group_id=eks_sg.id,
            source_security_group_id=sg.id,
        )


def get_provider_for_cluster(name, tailscale_enabled):
    cluster = aws.eks.get_cluster(name=name)
    return k8s.Provider(
        f"{name}-k8s",
        args=k8s.ProviderArgs(
            enable_server_side_apply=True,
            kubeconfig=get_kubeconfig_for_cluster(
                name, tailscale_enabled, cluster.endpoint, cluster.certificate_authorities[0].data
            ),
        ),
    )


def get_kubeconfig_for_cluster(name, tailscale_enabled, endpoint=None, ca_data=None):
    if endpoint is None or ca_data is None:
        cluster = aws.eks.get_cluster(name=name)
        endpoint = cluster.endpoint
        ca_data = cluster.certificate_authorities[0].data

    k = {
        "apiVersion": "v1",
        "clusters": [
            {
                "cluster": {
                    "server": endpoint,
                    "certificate-authority-data": ca_data,
                },
                "name": "kubernetes",
            }
        ],
        "contexts": [
            {
                "context": {
                    "cluster": "kubernetes",
                    "user": "aws",
                },
                "name": "aws",
            }
        ],
        "current-context": "aws",
        "kind": "Config",
        "users": [
            {
                "name": "aws",
                "user": {
                    "exec": {
                        "apiVersion": "client.authentication.k8s.io/v1",
                        "command": "aws",
                        "args": [
                            "eks",
                            "get-token",
                            "--cluster-name",
                            name,
                        ],
                        "interactiveMode": "IfAvailable",
                        "provideClusterInfo": False,
                    },
                },
            }
        ],
    }

    if not tailscale_enabled:
        k["clusters"][0]["cluster"]["proxy-url"] = "socks5://localhost:1080"

    return json.dumps(k)
