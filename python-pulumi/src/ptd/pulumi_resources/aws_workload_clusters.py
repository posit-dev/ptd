import base64
import json
import typing

import pulumi
import pulumi_aws as aws
import pulumi_kubernetes as kubernetes

import ptd
import ptd.aws_iam
import ptd.aws_workload
import ptd.oidc
import ptd.pulumi_resources.aws_bucket
import ptd.pulumi_resources.aws_eks_cluster
import ptd.pulumi_resources.aws_iam
import ptd.pulumi_resources.aws_karpenter
import ptd.pulumi_resources.custom_k8s_resources
import ptd.pulumi_resources.external_dns
import ptd.pulumi_resources.helm_controller
import ptd.pulumi_resources.keycloak_operator
import ptd.pulumi_resources.kubernetes_role
import ptd.pulumi_resources.network_policies
import ptd.pulumi_resources.team_operator
import ptd.pulumi_resources.team_site
import ptd.pulumi_resources.traefik_forward_auth
import ptd.pulumi_resources.traefik_forward_auth_aws
import ptd.secrecy


class AWSWorkloadClusters(pulumi.ComponentResource):
    workload: ptd.aws_workload.AWSWorkload

    required_tags: dict[str, str]
    kube_providers: dict[str, kubernetes.Provider]

    managed_clusters: list[dict[str, typing.Any]]
    managed_clusters_by_release: dict[str, dict[str, typing.Any]]
    oidc_urls: list[str]

    chronicle_bucket: aws.s3.Bucket
    chronicle_bucket_ro_policies: dict[str, aws.iam.Policy]
    chronicle_roles: dict[str, aws.iam.Role]
    connect_roles: dict[str, aws.iam.Role]
    connect_session_roles: dict[str, aws.iam.Role]
    home_roles: dict[str, aws.iam.Role]
    karpenter_node_roles: dict[str, aws.iam.Role]
    karpenter_controller_roles: dict[str, aws.iam.Role]
    autoscaling_queues: dict[str, aws.sqs.Queue]
    packagemanager_roles: dict[str, aws.iam.Role | pulumi.Output[aws.iam.Role]]
    team_operator_roles: dict[str, aws.iam.Role]
    external_secrets_roles: dict[str, aws.iam.Role]
    workbench_roles: dict[str, aws.iam.Role]
    workbench_session_roles: dict[str, aws.iam.Role]

    external_dnss: dict[str, ptd.pulumi_resources.external_dns.ExternalDNS]
    helm_controllers: dict[str, ptd.pulumi_resources.helm_controller.HelmController]
    keycloak_operators: dict[str, ptd.pulumi_resources.keycloak_operator.KeycloakOperator]
    network_policies: dict[str, ptd.pulumi_resources.network_policies.NetworkPolicies]
    team_operators: dict[str, ptd.pulumi_resources.team_operator.TeamOperator]
    team_sites: dict[str, ptd.pulumi_resources.team_site.TeamSite]
    traefik_forward_auths: dict[str, ptd.pulumi_resources.traefik_forward_auth_aws.TraefikForwardAuthAWS]

    @classmethod
    def autoload(cls) -> "AWSWorkloadClusters":
        return cls(workload=ptd.aws_workload.AWSWorkload(pulumi.get_stack()))

    def __init__(self, workload: ptd.aws_workload.AWSWorkload, *args, **kwargs):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            workload.compound_name,
            *args,
            **kwargs,
        )

        self.workload = workload
        self.required_tags = self.workload.required_tags | {
            str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__,
        }

        self.managed_clusters = self.workload.managed_clusters(assume_role=False)
        self.managed_clusters_by_release = self.workload.managed_clusters_by_release(assume_role=False)
        self.kube_providers = {
            release: ptd.pulumi_resources.aws_eks_cluster.get_provider_for_cluster(
                cluster["cluster"]["name"], self.workload.cfg.tailscale_enabled
            )
            for release, cluster in self.managed_clusters_by_release.items()
        }
        self.oidc_urls = [
            u for u in [ptd.get_oidc_url(c) for c in self.managed_clusters] if u is not None
        ] + self.workload.cfg.extra_cluster_oidc_urls

        self.workload_secrets_dict, ok = ptd.secrecy.aws_get_secret_value_json(
            self.workload.secret_name, region=self.workload.cfg.region
        )

        if not ok:
            msg = f"Failed to look up secret {self.workload.secret_name!r}"
            pulumi.error(msg, self)

            raise ValueError(msg)

        persistent_stack = pulumi.StackReference(
            f"organization/ptd-aws-workload-persistent/{self.workload.compound_name}"
        )

        self._define_home_iam()
        self._define_chronicle_iam(persistent_stack)
        self._define_connect_iam()
        self._define_workbench_iam()
        self._define_packagemanager_iam(persistent_stack)
        self._define_team_operator_iam()
        self._define_external_secrets_iam()
        # Create Pod Identity associations for all products (ADDITIVE - keeps IRSA for backward compatibility)
        self._define_pod_identity_associations()
        self._apply_custom_k8s_resources()
        self._define_team_operator()
        # after team operator so we can reuse the namespaces
        if self.workload.cfg.keycloak_enabled:
            self._define_keycloak_iam()
            self._define_keycloak_operator()
        if self.workload.cfg.external_dns_enabled:
            self._define_external_dnss()
        self._define_grafana_prereqs(persistent_stack)
        self._define_helm_controllers()
        self._define_traefik_forward_auths()
        self._define_network_policies()

        # Initialize karpenter-related attributes as empty dicts
        self.karpenter_node_roles = {}
        self.karpenter_controller_roles = {}
        self.autoscaling_queues = {}
        if self.workload.cfg.autoscaling_enabled:
            self._define_karpenter()

    @property
    def _oidc_url_tails(self):
        return [u.split("//")[1] for u in self.oidc_urls]

    @staticmethod
    def _define_read_secrets_inline() -> str:
        return aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "secretsmanager:Get*",
                        "secretsmanager:Describe*",
                        "secretsmanager:ListSecrets",
                    ],
                    resources=["*"],
                )
            ]
        ).json

    @staticmethod
    def _define_streaming_bedrock_access() -> str:
        return aws.iam.get_policy_document(
            statements=[
                aws.iam.GetPolicyDocumentStatementArgs(
                    effect="Allow",
                    actions=[
                        "bedrock:Get*",
                        "bedrock:List*",
                        "bedrock:Retrieve",
                        "bedrock:RetrieveAndGenerate",
                        "bedrock:ApplyGuardrail",
                        "bedrock:Invoke*",
                    ],
                    resources=["*"],
                )
            ]
        ).json

    def _define_efs_access(
        self,
        file_system_id: str,
        access_point_id: str | None = None,
    ) -> str:
        """
        Define IAM policy for EFS access.

        When access_point_id is provided, the policy is scoped to that specific
        access point using a condition. This is required for IAM authentication
        with access points.

        :param file_system_id: EFS file system ID (e.g., fs-xxxxx)
        :param access_point_id: Optional EFS access point ID (e.g., fsap-xxxxx)
        :return: JSON policy document
        """
        account_id = aws.get_caller_identity().account_id
        region = self.workload.cfg.region

        statements = [
            # ClientMount and ClientWrite scoped to the specific file system
            aws.iam.GetPolicyDocumentStatementArgs(
                effect="Allow",
                actions=[
                    "elasticfilesystem:ClientMount",
                    "elasticfilesystem:ClientWrite",
                ],
                resources=[f"arn:aws:elasticfilesystem:{region}:{account_id}:file-system/{file_system_id}"],
                conditions=[
                    aws.iam.GetPolicyDocumentStatementConditionArgs(
                        test="StringEquals",
                        variable="elasticfilesystem:AccessPointArn",
                        values=[f"arn:aws:elasticfilesystem:{region}:{account_id}:access-point/{access_point_id}"],
                    )
                ]
                if access_point_id
                else None,
            ),
            # Describe operations can be on all resources (metadata only)
            aws.iam.GetPolicyDocumentStatementArgs(
                effect="Allow",
                actions=[
                    "elasticfilesystem:DescribeFileSystems",
                    "elasticfilesystem:DescribeMountTargets",
                ],
                resources=["*"],
            ),
        ]

        return aws.iam.get_policy_document(statements=statements).json

    def _define_home_iam(self):
        self.home_roles = {}

        for release in self.managed_clusters_by_release:
            self.home_roles[release] = self._define_k8s_iam_role(
                name=self.workload.cluster_home_role_name(release),
                release=release,
                namespace=ptd.POSIT_TEAM_NAMESPACE,
                service_accounts=[f"{site_name}-home" for site_name in sorted(self.workload.cfg.sites.keys())],
                role_policies=[
                    self._define_read_secrets_inline(),
                ],
            )

    def _define_connect_iam(self):
        self.connect_roles = {}
        self.connect_session_roles = {}

        for release in self.managed_clusters_by_release:
            self.connect_roles[release] = self._define_k8s_iam_role(
                name=self.workload.cluster_connect_role_name(release),
                release=release,
                namespace=ptd.POSIT_TEAM_NAMESPACE,
                service_accounts=[f"{site_name}-connect" for site_name in sorted(self.workload.cfg.sites.keys())],
                role_policies=[self._define_read_secrets_inline()],
            )

            for site_name in sorted(self.workload.cfg.sites.keys()):
                policy = self.chronicle_bucket_ro_policies[f"{release}-{site_name}"]
                role_name = self.workload.cluster_connect_session_role_name(release, site_name)
                self.connect_session_roles[f"{release}-{site_name}"] = self._define_k8s_iam_role(
                    name=role_name,
                    release=release,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_accounts=[f"{site_name}-connect-session"],
                    policy=policy,
                    policy_name=role_name,
                    role_policies=[self._define_streaming_bedrock_access()],
                )

    def _define_workbench_iam(self):
        self.workbench_roles = {}
        self.workbench_session_roles = {}

        for release in self.managed_clusters_by_release:
            # Check if EFS is enabled for this cluster
            cluster_cfg = self.workload.cfg.clusters[release]
            efs_enabled = cluster_cfg.enable_efs_csi_driver or cluster_cfg.efs_config is not None

            # Build role policies list - always include secrets access
            workbench_role_policies = [self._define_read_secrets_inline()]

            # Add EFS permissions if EFS is enabled
            if efs_enabled and cluster_cfg.efs_config is not None:
                # Use scoped policy with access point when efs_config is provided
                workbench_role_policies.append(
                    self._define_efs_access(
                        file_system_id=cluster_cfg.efs_config.file_system_id,
                        access_point_id=cluster_cfg.efs_config.access_point_id,
                    )
                )

            self.workbench_roles[release] = self._define_k8s_iam_role(
                name=self.workload.cluster_workbench_role_name(release),
                release=release,
                namespace=ptd.POSIT_TEAM_NAMESPACE,
                service_accounts=[f"{site_name}-workbench" for site_name in sorted(self.workload.cfg.sites.keys())],
                role_policies=workbench_role_policies,
            )

            for site_name in sorted(self.workload.cfg.sites.keys()):
                # Build role policies for workbench sessions
                workbench_session_role_policies = [self._define_streaming_bedrock_access()]

                # Add EFS permissions if EFS is enabled
                if efs_enabled and cluster_cfg.efs_config is not None:
                    # Use scoped policy with access point when efs_config is provided
                    workbench_session_role_policies.append(
                        self._define_efs_access(
                            file_system_id=cluster_cfg.efs_config.file_system_id,
                            access_point_id=cluster_cfg.efs_config.access_point_id,
                        )
                    )

                role_name = self.workload.cluster_workbench_session_role_name(release, site_name)
                self.workbench_session_roles[f"{release}-{site_name}"] = self._define_k8s_iam_role(
                    name=role_name,
                    release=release,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    policy=self.chronicle_bucket_ro_policies[f"{release}-{site_name}"],
                    policy_name=role_name,
                    service_accounts=[f"{site_name}-workbench-session"],
                    role_policies=workbench_session_role_policies,
                )

    def _define_packagemanager_iam(self, persistent_stack):
        packagemanager_bucket = persistent_stack.require_output("packagemanager_bucket").apply(lambda x: x)
        bucket = aws.s3.Bucket.get(
            f"{self.workload.compound_name}-ppm-bucket",
            packagemanager_bucket,
        )

        self.packagemanager_roles = {}

        for release in self.managed_clusters_by_release:
            for site_name in sorted(self.workload.cfg.sites.keys()):
                policy_name = self.workload.ppm_s3_bucket_policy_name_site(release, site_name)

                policy = ptd.pulumi_resources.aws_bucket.define_bucket_policy(
                    name=f"ppm-{site_name}-{release}",
                    compound_name=self.workload.compound_name,
                    bucket=bucket,
                    policy_name=policy_name,
                    policy_description=f"Posit Team Dedicated policy for {self.workload.compound_name} to read the PPM S3 bucket at the {site_name}/ and below paths",
                    policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ_WRITE,
                    prefix_path=site_name,
                    required_tags=self.required_tags,
                )

                self.packagemanager_roles[release + "//" + site_name] = self._define_k8s_iam_role(
                    name=self.workload.cluster_packagemanager_role_name(release, site_name),
                    release=release,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_accounts=[f"{site_name}-packagemanager"],
                    policy=policy,
                    policy_name=policy_name,
                    role_policies=[self._define_read_secrets_inline()],
                )

    def _define_k8s_iam_role(
        self,
        name: str,
        release: str = "",
        namespace: str = "default",
        service_accounts: list[str] | None = None,
        policy_name: str = "",
        policy: aws.iam.Policy | None = None,
        # this typing was taken directly from the aws.iam.Role class
        role_policies: pulumi.Input[typing.Sequence[pulumi.Input[str]],] | None = None,
        auth_issuers: list[ptd.aws_iam.AuthIssuer] | None = None,
        opts: pulumi.ResourceOptions | None = None,
    ) -> aws.iam.Role:
        """
        Define a Kubernetes IAM role with appropriate trust relationships.

        :param name: The name of the IAM role
        :param release: The release / cluster used in naming the resources
        :param namespace: The Kubernetes namespace that should establish trust for this IAM role
        :param service_accounts: Kubernetes Service Accounts that should be able to assume this IAM role
        :param policy_name: The policy name to attach to the role
        :param role_policies: Role policies to attach to the role (Previously known as inline_policies)
        :param auth_issuers: A list of auth issuers that the role should trust. DO NOT list the same auth issuer more
            than once! Use a list of client_ids instead
        :return: aws.iam.Role
        """
        if auth_issuers is None:
            auth_issuers = []
        if service_accounts is None:
            service_accounts = []
        if opts is None:
            opts = pulumi.ResourceOptions()
        role = aws.iam.Role(
            name,
            aws.iam.RoleArgs(
                name=name,
                assume_role_policy=json.dumps(
                    (
                        ptd.aws_iam.build_hybrid_irsa_role_assume_role_policy(
                            service_accounts=service_accounts,
                            namespace=namespace,
                            managed_account_id=self.workload.cfg.account_id,
                            oidc_url_tails=self._oidc_url_tails,
                            auth_issuers=auth_issuers,
                        )
                        if len(self._oidc_url_tails) > 0 or len(auth_issuers) > 0
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
            ),
            opts=pulumi.ResourceOptions.merge(
                opts,
                pulumi.ResourceOptions(parent=self, delete_before_replace=True),
            ),
        )

        if role_policies:
            for idx, role_policy in enumerate(role_policies):
                aws.iam.RolePolicy(
                    f"{name}-role-policy-{idx}", name=f"{name}-role-policy-{idx}", role=role.id, policy=role_policy
                )

        if policy_name != "" and policy is None:
            policy = aws.iam.Policy.get(
                f"{policy_name}-{release}",
                id=f"arn:aws:iam::{self.workload.cfg.account_id}:policy/{policy_name}",
                opts=pulumi.ResourceOptions(parent=self),
            )

        if policy is not None:
            aws.iam.RolePolicyAttachment(
                f"{policy_name}-{release}-att",
                role=role.name,
                policy_arn=policy.arn,
                opts=pulumi.ResourceOptions.merge(
                    opts,
                    pulumi.ResourceOptions(
                        parent=role,
                        delete_before_replace=True,
                    ),
                ),
            )

        return role

    def _define_chronicle_iam(self, persistent_stack):
        chronicle_bucket = persistent_stack.require_output("chronicle_bucket").apply(lambda x: x)
        self.chronicle_bucket = aws.s3.Bucket.get(
            f"{self.workload.compound_name}-chronicle-bucket",
            chronicle_bucket,
        )

        self.chronicle_roles = {}
        self.chronicle_bucket_ro_policies = {}

        for release in self.managed_clusters_by_release:
            for site_name in sorted(self.workload.cfg.sites.keys()):
                policy_name = self.workload.chronicle_s3_bucket_policy_name(release, site_name)
                policy = ptd.pulumi_resources.aws_bucket.define_bucket_policy(
                    name=policy_name,
                    compound_name=self.workload.compound_name,
                    bucket=self.chronicle_bucket,
                    policy_name=policy_name,
                    policy_description=f"Posit Team Dedicated policy for {self.workload.compound_name} to read/write the Chronicle S3 bucket at the {site_name}/ and below paths",
                    policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ_WRITE,
                    prefix_path=site_name,
                )

                self.chronicle_roles[f"{release}-{site_name}"] = self._define_k8s_iam_role(
                    name=self.workload.cluster_chronicle_role_name(release, site_name),
                    release=release,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_accounts=[f"{site_name}-chronicle"],
                    policy=policy,
                    policy_name=policy_name,
                    role_policies=[self._define_read_secrets_inline()],
                )

                read_only_policy_name = self.workload.chronicle_read_only_s3_bucket_policy_name(release, site_name)
                read_only_policy = ptd.pulumi_resources.aws_bucket.define_bucket_policy(
                    name=read_only_policy_name,
                    compound_name=self.workload.compound_name,
                    bucket=self.chronicle_bucket,
                    policy_name=read_only_policy_name,
                    policy_description=f"Posit Team Dedicated policy for {self.workload.compound_name} to read the Chronicle S3 bucket at the {site_name}/ and below paths",
                    policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ,
                    prefix_path=site_name,
                )
                self.chronicle_bucket_ro_policies[f"{release}-{site_name}"] = read_only_policy

                self.chronicle_roles[f"{release}-{site_name}-read-only"] = self._define_k8s_iam_role(
                    name=self.workload.cluster_chronicle_read_only_role_name(release, site_name),
                    release=release,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_accounts=[f"{site_name}-chronicle"],
                    policy=read_only_policy,
                    policy_name=read_only_policy_name,
                    role_policies=[self._define_read_secrets_inline()],
                )

    def _define_keycloak_iam(self):
        self.keycloak_roles = {}

        for release in self.managed_clusters_by_release:
            self.keycloak_roles[release] = self._define_k8s_iam_role(
                name=self.workload.cluster_keycloak_role_name(release),
                release=release,
                namespace=ptd.POSIT_TEAM_NAMESPACE,
                service_accounts=[f"{site_name}-keycloak" for site_name in sorted(self.workload.cfg.sites.keys())],
                role_policies=[self._define_read_secrets_inline()],
            )

    def _define_keycloak_operator(self):
        self.keycloak_operators = {}

        for release in self.managed_clusters_by_release:

            def generate_set_irsa_annotation(
                role_arn: str,
            ) -> ptd.pulumi_resources.KustomizeTransformationFunc:
                def set_irsa_annotation(
                    obj: dict[str, typing.Any],
                    _: pulumi.ResourceOptions,
                ):
                    if obj["kind"] != "ServiceAccount":
                        return

                    obj.setdefault("metadata", {})
                    obj["metadata"].setdefault("annotations", {})
                    obj["metadata"]["annotations"]["eks.amazonaws.com/role-arn"] = role_arn

                return set_irsa_annotation

            self.keycloak_operators[release] = ptd.pulumi_resources.keycloak_operator.KeycloakOperator(
                workload=self.workload,
                release=release,
                transformations=[generate_set_irsa_annotation(self.keycloak_roles[release].arn.apply(lambda s: s))],
                opts=pulumi.ResourceOptions(
                    parent=self,
                    providers=[self.kube_providers[release]],
                ),
            )

    def _define_team_operator_iam(self):
        self.team_operator_roles = {}

        # Service account name must match the Helm chart default (team-operator-controller-manager)
        # This is set in team-operator/dist/chart/values.yaml under controllerManager.serviceAccountName
        helm_service_account_name = "team-operator-controller-manager"

        for release in self.managed_clusters_by_release:
            self.team_operator_roles[release] = self._define_k8s_iam_role(
                name=self.workload.cluster_team_operator_role_name(release),
                release=release,
                namespace=ptd.POSIT_TEAM_SYSTEM_NAMESPACE,
                service_accounts=[helm_service_account_name],
                policy_name=self.workload.team_operator_policy_name,
            )

    def _define_external_secrets_iam(self):
        """Define IAM roles for external-secrets-operator to access AWS Secrets Manager."""
        self.external_secrets_roles = {}

        for release in self.managed_clusters_by_release:
            cluster_cfg = self.workload.cfg.clusters[release]
            if not cluster_cfg.enable_external_secrets_operator:
                continue
            if not cluster_cfg.enable_pod_identity_agent:
                msg = (
                    f"Release '{release}': enable_external_secrets_operator requires enable_pod_identity_agent=True "
                    "(ClusterSecretStore uses no auth block and relies on Pod Identity for credentials)."
                )
                raise ValueError(msg)
            self.external_secrets_roles[release] = self._define_k8s_iam_role(
                name=self.workload.external_secrets_role_name(release),
                release=release,
                namespace="external-secrets",
                service_accounts=["external-secrets"],
                role_policies=[self._define_read_secrets_inline()],
            )

    def _define_pod_identity_associations(self):
        """
        Create EKS Pod Identity associations for all product service accounts.

        This is ADDITIVE - existing IRSA roles and annotations are kept for backward compatibility.
        Both Pod Identity and IRSA can coexist. The operator will be updated to stop computing
        IRSA annotations in a future phase.

        Pod Identity associations connect service accounts directly to IAM roles without requiring
        annotations on the ServiceAccount resource.

        Note: team_operator_roles is intentionally excluded here. The team-operator's service
        account retains IRSA-based access; Pod Identity will be added in a future phase once
        the operator itself is updated to remove IRSA annotation computation.

        Note: fsx_openzfs_roles is also intentionally excluded. The FSx OpenZFS CSI driver uses
        node-level IAM (instance profile) rather than pod-level credentials, so no Pod Identity
        association is needed for those roles.
        """
        for release in self.managed_clusters_by_release:
            cluster_cfg = self.workload.cfg.clusters[release]
            if not cluster_cfg.enable_pod_identity_agent:
                continue

            cluster_name = f"{self.workload.compound_name}-{release}"

            # External Secrets Operator (per-release, only if ESO is also enabled)
            if cluster_cfg.enable_external_secrets_operator:
                aws.eks.PodIdentityAssociation(
                    f"{cluster_name}-external-secrets-pod-identity",
                    cluster_name=cluster_name,
                    namespace="external-secrets",
                    service_account="external-secrets",
                    role_arn=self.external_secrets_roles[release].arn,
                    opts=pulumi.ResourceOptions(parent=self),
                )

            # Per-site product associations
            for site_name in sorted(self.workload.cfg.sites.keys()):
                # Connect
                aws.eks.PodIdentityAssociation(
                    f"{cluster_name}-{site_name}-connect-pod-identity",
                    cluster_name=cluster_name,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_account=f"{site_name}-connect",
                    role_arn=self.connect_roles[release].arn,
                    opts=pulumi.ResourceOptions(parent=self),
                )

                # Connect Session
                aws.eks.PodIdentityAssociation(
                    f"{cluster_name}-{site_name}-connect-session-pod-identity",
                    cluster_name=cluster_name,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_account=f"{site_name}-connect-session",
                    role_arn=self.connect_session_roles[f"{release}-{site_name}"].arn,
                    opts=pulumi.ResourceOptions(parent=self),
                )

                # Workbench
                aws.eks.PodIdentityAssociation(
                    f"{cluster_name}-{site_name}-workbench-pod-identity",
                    cluster_name=cluster_name,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_account=f"{site_name}-workbench",
                    role_arn=self.workbench_roles[release].arn,
                    opts=pulumi.ResourceOptions(parent=self),
                )

                # Workbench Session
                aws.eks.PodIdentityAssociation(
                    f"{cluster_name}-{site_name}-workbench-session-pod-identity",
                    cluster_name=cluster_name,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_account=f"{site_name}-workbench-session",
                    role_arn=self.workbench_session_roles[f"{release}-{site_name}"].arn,
                    opts=pulumi.ResourceOptions(parent=self),
                )

                # Package Manager
                aws.eks.PodIdentityAssociation(
                    f"{cluster_name}-{site_name}-packagemanager-pod-identity",
                    cluster_name=cluster_name,
                    namespace=ptd.POSIT_TEAM_NAMESPACE,
                    service_account=f"{site_name}-packagemanager",
                    role_arn=self.packagemanager_roles[release + "//" + site_name].arn,
                    opts=pulumi.ResourceOptions(parent=self),
                )

                # Chronicle (optional product — skip if not configured for this release/site)
                if f"{release}-{site_name}" in self.chronicle_roles:
                    aws.eks.PodIdentityAssociation(
                        f"{cluster_name}-{site_name}-chronicle-pod-identity",
                        cluster_name=cluster_name,
                        namespace=ptd.POSIT_TEAM_NAMESPACE,
                        service_account=f"{site_name}-chronicle",
                        role_arn=self.chronicle_roles[f"{release}-{site_name}"].arn,
                        opts=pulumi.ResourceOptions(parent=self),
                    )

                # Home/Flightdeck (optional product — skip if not configured for this release)
                if release in self.home_roles:
                    aws.eks.PodIdentityAssociation(
                        f"{cluster_name}-{site_name}-home-pod-identity",
                        cluster_name=cluster_name,
                        namespace=ptd.POSIT_TEAM_NAMESPACE,
                        service_account=f"{site_name}-home",
                        role_arn=self.home_roles[release].arn,
                        opts=pulumi.ResourceOptions(parent=self),
                    )

    def _apply_custom_k8s_resources(self):
        """Apply custom Kubernetes resources from the custom_k8s_resources/ directory."""
        ptd.pulumi_resources.custom_k8s_resources.apply_custom_k8s_resources(
            workload=self.workload,
            managed_clusters_by_release=self.managed_clusters_by_release,
            kube_providers=self.kube_providers,
            parent=self,
        )

    def _define_team_operator(self):
        self.team_operators = {}

        for release in self.managed_clusters_by_release:
            # Build IRSA annotations for the service account
            service_account_annotations = {
                "eks.amazonaws.com/role-arn": self.team_operator_roles[release].arn,
            }

            self.team_operators[release] = ptd.pulumi_resources.team_operator.TeamOperator(
                workload=self.workload,
                release=release,
                service_account_annotations=service_account_annotations,
                opts=pulumi.ResourceOptions(
                    parent=self,
                    providers=[self.kube_providers[release]],
                ),
            )

    def _define_external_dnss(self):
        self.external_dnss = {}

        for release in self.managed_clusters_by_release:
            self.external_dnss[release] = ptd.pulumi_resources.external_dns.ExternalDNS(
                workload=self.workload,
                release=release,
                opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
            )

    def _define_grafana_prereqs(self, persistent_stack) -> None:
        db_address_output = persistent_stack.require_output("db_address")
        pg_cfg_stack = pulumi.StackReference(
            f"organization/ptd-aws-workload-postgres-config/{self.workload.compound_name}"
        )
        pw_output = pg_cfg_stack.require_output("db_grafana_pw")

        for release in self.managed_clusters_by_release:
            kubernetes.core.v1.Namespace(
                f"{self.workload.compound_name}-{release}-grafana-ns",
                metadata={
                    "name": "grafana",
                },
                opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
            )

            role = database = f"grafana-{self.workload.compound_name}"
            kubernetes.core.v1.Secret(
                f"{self.workload.compound_name}-{release}-grafana-db-url",
                metadata={
                    "name": "grafana-db-url",
                    "namespace": "grafana",
                },
                data={
                    "PTD_DATABASE_URL": pulumi.Output.all(pw_output, db_address_output).apply(
                        lambda args, r=role, d=database: _build_encoded_ptd_database_url(r, args[0], args[1], d)
                    )
                },
                opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
            )

    def _define_traefik_forward_auths(self):
        self.traefik_forward_auths = {}

        for release in self.managed_clusters_by_release:
            comps = self.workload.cfg.clusters[release].components
            if comps is not None and comps.traefik_forward_auth_version is not None:
                self.traefik_forward_auths[release] = (
                    ptd.pulumi_resources.traefik_forward_auth_aws.TraefikForwardAuthAWS(
                        workload=self.workload,
                        release=release,
                        chart_version=comps.traefik_forward_auth_version,
                        opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
                    )
                )

    def _define_network_policies(self) -> None:
        self.network_policies = {}

        for release in self.managed_clusters_by_release:
            self.network_policies[release] = ptd.pulumi_resources.network_policies.NetworkPolicies(
                workload=self.workload,
                release=release,
                network_trust=self.workload.cfg.network_trust,
                opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
            )

    def _define_helm_controllers(self):
        self.helm_controllers = {}
        for release in self.managed_clusters_by_release:
            self.helm_controllers[release] = ptd.pulumi_resources.helm_controller.HelmController(
                workload=self.workload,
                release=release,
                opts=pulumi.ResourceOptions(parent=self, providers=[self.kube_providers[release]]),
            )

    def _define_karpenter(self) -> None:
        """
        Main function to set up Karpenter for all clusters.
        """
        # Collect use_eks_access_entries flag for each cluster
        use_eks_access_entries_by_release = {}
        for release in self.managed_clusters_by_release:
            cluster_cfg = self.workload.cfg.clusters.get(release)
            if cluster_cfg and hasattr(cluster_cfg, "eks_access_entries"):
                # Access as attribute since cluster_cfg is an AWSWorkloadClusterConfig object
                use_eks_access_entries_by_release[release] = cluster_cfg.eks_access_entries.enabled
            else:
                use_eks_access_entries_by_release[release] = False

        karpenter = ptd.pulumi_resources.aws_karpenter.AWSKarpenter(
            workload=self.workload,
            managed_clusters=self.managed_clusters,
            managed_clusters_by_release=self.managed_clusters_by_release,
            kube_providers=self.kube_providers,
            required_tags=self.required_tags,
            define_k8s_iam_role_func=self._define_k8s_iam_role,
            use_eks_access_entries_by_release=use_eks_access_entries_by_release,
            parent=self,
            opts=pulumi.ResourceOptions(parent=self),
        )

        # Store the created roles and queues so they can be accessed by other components
        self.karpenter_node_roles = karpenter.karpenter_node_roles
        self.karpenter_node_instance_profiles = karpenter.karpenter_node_instance_profiles
        self.karpenter_controller_roles = karpenter.karpenter_controller_roles
        self.autoscaling_queues = karpenter.autoscaling_queues

    def _create_oidc_provider(
        self, name: str, issuer_url: str, client_ids: list[str], additional_client_ids: list[str] | None = None
    ) -> aws.iam.OpenIdConnectProvider:
        """Create an OIDC provider with proper thumbprint discovery and configuration."""
        thumbprint = ptd.oidc.get_thumbprint(ptd.oidc.get_network_location_for_oidc_endpoint(issuer_url))

        all_client_ids = client_ids[:]
        if additional_client_ids:
            all_client_ids.extend(additional_client_ids)

        return aws.iam.OpenIdConnectProvider(
            name,
            aws.iam.OpenIdConnectProviderArgs(
                url=issuer_url,
                client_id_lists=["sts.amazonaws.com", *all_client_ids],
                thumbprint_lists=[thumbprint],
            ),
            opts=pulumi.ResourceOptions(
                protect=self.workload.cfg.protect_persistent_resources,
                parent=self,
                delete_before_replace=True,
            ),
        )


def _build_encoded_ptd_database_url(role, pw, db_address, database):
    s = f"postgres://{role}:{pw}@{db_address}/{database}"
    return base64.b64encode(s.encode()).decode()
