import typing

import pulumi
import pulumi_kubernetes as kubernetes

import ptd
import ptd.workload

# CRDs that need Helm ownership metadata (names are standard, not transformed)
KUSTOMIZE_CRDS = [
    "chronicles.core.posit.team",
    "connects.core.posit.team",
    "flightdecks.core.posit.team",
    "packagemanagers.core.posit.team",
    "postgresdatabases.core.posit.team",
    "sites.core.posit.team",
    "workbenches.core.posit.team",
]

# Label used by the old kustomize deployment to identify resources
# The old code used: str(ptd.TagKeys.POSIT_TEAM_MANAGED_BY): __name__
# Which translates to: posit.team/managed-by: ptd.pulumi_resources.team_operator
KUSTOMIZE_MANAGED_BY_LABEL = "posit.team/managed-by=ptd.pulumi_resources.team_operator"

# Default Helm chart version (OCI charts require explicit version, no "latest")
DEFAULT_CHART_VERSION = "v1.11.0"


class TeamOperator(pulumi.ComponentResource):
    """Deploy Team Operator using Helm chart."""

    workload: ptd.workload.AbstractWorkload
    release: str
    cluster_cfg: ptd.WorkloadClusterConfig
    service_account_annotations: dict[str, typing.Any]

    posit_team_namespace: kubernetes.core.v1.Namespace
    helm_release: kubernetes.helm.v3.Release
    migration_job: kubernetes.batch.v1.Job | None

    def __init__(
        self,
        workload: ptd.workload.AbstractWorkload,
        release: str,
        service_account_annotations: dict[str, typing.Any] | None = None,
        *args,
        **kwargs,
    ):
        super().__init__(
            f"ptd:{self.__class__.__name__}",
            f"{workload.compound_name}-{release}",
            *args,
            **kwargs,
        )

        self.workload = workload
        self.release = release
        self.service_account_annotations = service_account_annotations or {}

        self.cluster_cfg = self.workload.cfg.clusters[release]
        if self.cluster_cfg is None:
            msg = f"missing config for cluster {release!r}"
            raise ValueError(msg)

        # Helm release name must be predictable for the migration job
        self.helm_release_name = "team-operator"

        self._define_image()
        self._define_posit_team_namespace()
        self._define_migration_resources()
        self._define_helm_release()

    def _define_image(self):
        # Use adhoc_team_operator_image if set, otherwise use team_operator_image
        # adhoc images can be tags like "test", "dev", or full image references
        image_config = self.cluster_cfg.adhoc_team_operator_image or self.cluster_cfg.team_operator_image
        self.image = ptd.define_component_image(
            image_config=image_config,
            component_image=ptd.ComponentImages.TEAM_OPERATOR,
            image_registry_hostname=self.workload.image_registry_hostname,
        )

    def _define_posit_team_namespace(self):
        self.posit_team_namespace = kubernetes.core.v1.Namespace(
            f"{self.workload.compound_name}-{self.release}-{ptd.POSIT_TEAM_NAMESPACE}",
            metadata={"name": ptd.POSIT_TEAM_NAMESPACE},
            opts=pulumi.ResourceOptions(
                parent=self,
                retain_on_delete=True,  # Don't delete namespace during migration
            ),
        )

    def _define_migration_resources(self):
        """Create resources to migrate from kustomize to Helm.

        This creates a Job that patches existing kustomize-managed resources
        with Helm ownership labels/annotations so Helm can adopt them.
        """
        namespace = ptd.POSIT_TEAM_SYSTEM_NAMESPACE
        resource_prefix = f"{self.workload.compound_name}-{self.release}"

        # Service account for the migration job
        migration_sa = kubernetes.core.v1.ServiceAccount(
            f"{resource_prefix}-helm-migration-sa",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="helm-migration",
                namespace=namespace,
            ),
            opts=pulumi.ResourceOptions(parent=self),
        )

        # ClusterRole with permissions to patch CRDs and delete old resources
        migration_role = kubernetes.rbac.v1.ClusterRole(
            f"{resource_prefix}-helm-migration-role",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=f"{resource_prefix}-helm-migration",
            ),
            rules=[
                kubernetes.rbac.v1.PolicyRuleArgs(
                    api_groups=[""],
                    resources=["serviceaccounts", "services"],
                    verbs=["get", "list", "delete"],
                ),
                kubernetes.rbac.v1.PolicyRuleArgs(
                    api_groups=["apps"],
                    resources=["deployments"],
                    verbs=["get", "list", "delete"],
                ),
                kubernetes.rbac.v1.PolicyRuleArgs(
                    api_groups=["rbac.authorization.k8s.io"],
                    resources=["roles", "rolebindings", "clusterroles", "clusterrolebindings"],
                    verbs=["get", "list", "delete"],
                ),
                kubernetes.rbac.v1.PolicyRuleArgs(
                    api_groups=["apiextensions.k8s.io"],
                    resources=["customresourcedefinitions"],
                    verbs=["get", "list", "patch"],
                ),
            ],
            opts=pulumi.ResourceOptions(parent=self),
        )

        # ClusterRoleBinding
        migration_binding = kubernetes.rbac.v1.ClusterRoleBinding(
            f"{resource_prefix}-helm-migration-binding",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name=f"{resource_prefix}-helm-migration",
            ),
            role_ref=kubernetes.rbac.v1.RoleRefArgs(
                api_group="rbac.authorization.k8s.io",
                kind="ClusterRole",
                name=f"{resource_prefix}-helm-migration",
            ),
            subjects=[
                kubernetes.rbac.v1.SubjectArgs(
                    kind="ServiceAccount",
                    name="helm-migration",
                    namespace=namespace,
                ),
            ],
            opts=pulumi.ResourceOptions(parent=self, depends_on=[migration_role]),
        )

        # Build the migration script
        script = self._build_migration_script(namespace)

        # Migration Job
        self.migration_job = kubernetes.batch.v1.Job(
            f"{resource_prefix}-helm-migration-job",
            metadata=kubernetes.meta.v1.ObjectMetaArgs(
                name="helm-migration",
                namespace=namespace,
            ),
            spec=kubernetes.batch.v1.JobSpecArgs(
                ttl_seconds_after_finished=300,
                template=kubernetes.core.v1.PodTemplateSpecArgs(
                    spec=kubernetes.core.v1.PodSpecArgs(
                        service_account_name="helm-migration",
                        restart_policy="Never",
                        # Tolerate all taints - migration job is ephemeral and just needs to run
                        tolerations=[
                            kubernetes.core.v1.TolerationArgs(
                                operator="Exists",
                            ),
                        ],
                        containers=[
                            kubernetes.core.v1.ContainerArgs(
                                name="migrate",
                                image="bitnami/kubectl:latest",
                                command=["/bin/sh", "-c", script],
                            ),
                        ],
                    ),
                ),
            ),
            opts=pulumi.ResourceOptions(
                parent=self,
                depends_on=[migration_sa, migration_binding],
                delete_before_replace=True,  # Job may be deleted by TTL controller
            ),
        )

    def _build_migration_script(self, namespace: str) -> str:
        """Build the shell script to migrate from kustomize to Helm.

        The old kustomize deployment used transformations that renamed resources,
        so we can't predict the exact names. Instead:
        1. Patch CRDs with Helm ownership (CRD names are standard)
        2. Delete other resources by label (the old code added posit.team/managed-by label)
        """
        release_name = self.helm_release_name
        release_namespace = namespace

        # Build patch commands for CRDs (names are standard, not transformed)
        crd_commands = [
            f'kubectl patch crd {crd} --type=merge -p \'{{"metadata":{{"labels":{{"app.kubernetes.io/managed-by":"Helm"}},"annotations":{{"meta.helm.sh/release-name":"{release_name}","meta.helm.sh/release-namespace":"{release_namespace}","helm.sh/resource-policy":"keep"}}}}}}\' 2>/dev/null || echo "  {crd} not found or already adopted"'
            for crd in KUSTOMIZE_CRDS
        ]

        return f"""
set -e
echo "Migrating team-operator from kustomize to Helm..."

echo "Step 1: Patching CRDs with Helm ownership..."
{chr(10).join(crd_commands)}

echo "Step 2: Deleting old kustomize-managed resources (by label)..."
echo "  Label selector: {KUSTOMIZE_MANAGED_BY_LABEL}"

# Delete namespace-scoped resources in posit-team-system
kubectl delete deployment,serviceaccount,service,role,rolebinding \\
    -n {release_namespace} \\
    -l {KUSTOMIZE_MANAGED_BY_LABEL} \\
    --ignore-not-found=true || echo "  No namespaced resources found"

# Delete cluster-scoped resources
kubectl delete clusterrole,clusterrolebinding \\
    -l {KUSTOMIZE_MANAGED_BY_LABEL} \\
    --ignore-not-found=true || echo "  No cluster-scoped resources found"

echo "Migration complete - Helm will now create fresh resources"
"""

    def _define_helm_release(self):
        # Parse self.image (from _define_image) into repository and tag
        # Format is either "repo@sha256:digest" or "repo:tag"
        if "@" in self.image:
            # Image with digest: "hostname/repo@sha256:abc123"
            image_repository, image_tag = self.image.rsplit("@", 1)
            image_tag = f"@{image_tag}"  # Helm needs the @ prefix for digests
        elif ":" in self.image.split("/")[-1]:
            # Image with tag: "hostname/repo:tag"
            image_repository, image_tag = self.image.rsplit(":", 1)
        else:
            # No tag specified, use latest
            image_repository = self.image
            image_tag = "latest"

        # Build environment variables
        env_vars = {
            "WATCH_NAMESPACES": ptd.POSIT_TEAM_NAMESPACE,
        }

        # Add AWS_REGION if we have a region configured
        if self.workload.cfg.region:
            env_vars["AWS_REGION"] = self.workload.cfg.region

        # Helm values for the team-operator chart
        helm_values = {
            "controllerManager": {
                "replicas": 1,
                "container": {
                    "image": {
                        "repository": image_repository,
                        "tag": image_tag,
                    },
                    "env": env_vars,
                },
                # Use default serviceAccountName from chart (team-operator-controller-manager)
                # to match existing kustomize resources for seamless migration
                "serviceAccount": {
                    "annotations": self.service_account_annotations,
                },
                "tolerations": [
                    {
                        "key": t.key,
                        "operator": t.operator,
                        "effect": t.effect,
                        **({"value": t.value} if t.value else {}),
                    }
                    for t in self.cluster_cfg.team_operator_tolerations
                ],
            },
            # CRD configuration for safe migration from kustomize to Helm.
            # When skip_crds=True: crd.enable=False prevents Helm from rendering CRD templates,
            # allowing migration job to patch existing CRDs without risk of deletion.
            # When skip_crds=False (default): Helm manages CRDs normally.
            # crd.keep=True adds helm.sh/resource-policy: keep as defense-in-depth.
            "crd": {
                "enable": not self.cluster_cfg.team_operator_skip_crds,
                "keep": True,
            },
        }

        # OCI Helm chart from public repository
        chart = "oci://ghcr.io/posit-dev/charts/team-operator"

        # Chart version (OCI registries require explicit version)
        chart_version = self.cluster_cfg.team_operator_chart_version or DEFAULT_CHART_VERSION

        # Dependencies for the Helm release
        depends = [self.posit_team_namespace]
        if self.migration_job:
            depends.append(self.migration_job)

        release_args = kubernetes.helm.v3.ReleaseArgs(
            name=self.helm_release_name,
            chart=chart,
            version=chart_version,
            namespace=ptd.POSIT_TEAM_SYSTEM_NAMESPACE,
            create_namespace=True,
            values=helm_values,
            # Skip CRDs at Helm level (belt-and-suspenders with crd.enable in values).
            # This tells Helm CLI to skip the crds/ directory if the chart ever moves
            # CRDs there. Combined with crd.enable=False, provides complete CRD skip.
            skip_crds=self.cluster_cfg.team_operator_skip_crds,
        )

        self.helm_release = kubernetes.helm.v3.Release(
            f"{self.workload.compound_name}-{self.release}-team-operator",
            release_args,
            opts=pulumi.ResourceOptions(
                parent=self,
                depends_on=depends,
            ),
        )
