"""
Shared functionality for applying custom Kubernetes resources across cloud providers.
"""

import typing

import pulumi
import pulumi_kubernetes as kubernetes

if typing.TYPE_CHECKING:
    import ptd.aws_workload
    import ptd.azure_workload


def apply_custom_k8s_resources(
    workload: "ptd.aws_workload.AWSWorkload | ptd.azure_workload.AzureWorkload",
    managed_clusters_by_release: dict[str, typing.Any],
    kube_providers: dict[str, kubernetes.Provider],
    parent: pulumi.ComponentResource,
) -> None:
    """
    Apply custom Kubernetes resources from the custom_k8s_resources/ directory.

    This function checks for the existence of a custom_k8s_resources/ folder at the workload level,
    and for each cluster that specifies custom_k8s_resources in its config, it applies YAML files
    from the specified subfolders in alphabetical order.

    Args:
        workload: The workload object containing configuration and directory path
        managed_clusters_by_release: Dictionary of managed clusters by release name
        kube_providers: Dictionary of Kubernetes providers by release name
        parent: The parent Pulumi resource
    """
    custom_resources_dir = workload.d / "custom_k8s_resources"

    # Check if custom_k8s_resources directory exists
    if not custom_resources_dir.exists():
        return

    for release in managed_clusters_by_release:
        cluster_config = workload.cfg.clusters.get(release)
        if not cluster_config or not cluster_config.custom_k8s_resources:
            continue

        # Process each specified subfolder
        for subfolder_name in cluster_config.custom_k8s_resources:
            subfolder_path = custom_resources_dir / subfolder_name

            # Validate that the subfolder exists
            if not subfolder_path.exists():
                msg = f"Custom K8s resources subfolder not found: {subfolder_path}"
                raise ValueError(msg)

            if not subfolder_path.is_dir():
                msg = f"Custom K8s resources path is not a directory: {subfolder_path}"
                raise ValueError(msg)

            # Collect all .yaml and .yml files from the subfolder
            yaml_files = []
            for ext in ["*.yaml", "*.yml"]:
                yaml_files.extend(subfolder_path.glob(ext))

            # Sort files alphabetically to ensure consistent ordering
            yaml_files.sort()

            if not yaml_files:
                pulumi.log.warn(f"No YAML files found in custom_k8s_resources/{subfolder_name} for cluster {release}")
                continue

            # Apply each YAML file using kubernetes.yaml.ConfigFile
            for yaml_file in yaml_files:
                resource_name = f"{release}-custom-{subfolder_name}-{yaml_file.stem}"

                try:
                    kubernetes.yaml.ConfigFile(
                        resource_name,
                        file=str(yaml_file),
                        transformations=[add_managed_by_label],
                        opts=pulumi.ResourceOptions(
                            parent=parent,
                            provider=kube_providers[release],
                        ),
                    )
                    pulumi.log.info(f"Applied custom K8s resource: {resource_name} from {yaml_file}")
                except Exception as e:
                    pulumi.log.error(f"Failed to apply custom K8s resource from {yaml_file}: {e}")
                    msg = f"Failed to apply custom K8s resource from {yaml_file}: {e}"
                    raise ValueError(msg) from e


def add_managed_by_label(obj: dict[str, typing.Any], _: pulumi.ResourceOptions) -> None:
    """
    Pulumi transformation to inject the posit.team/managed-by label into all resources.

    Args:
        obj: The Kubernetes resource object to transform
        _: Pulumi resource options (unused)
    """
    # Ensure metadata exists
    if "metadata" not in obj:
        obj["metadata"] = {}

    # Ensure labels exists
    if "labels" not in obj["metadata"]:
        obj["metadata"]["labels"] = {}

    # Add the managed-by label
    obj["metadata"]["labels"]["posit.team/managed-by"] = "ptd-clusters"
