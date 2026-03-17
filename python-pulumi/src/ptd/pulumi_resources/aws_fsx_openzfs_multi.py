from __future__ import annotations

import dataclasses

import pulumi
import pulumi_aws as aws


@dataclasses.dataclass
class AWSFsxOpenZfsMultiArgs:
    automatic_backup_retention_days: pulumi.Input[int]
    copy_tags_to_backups: pulumi.Input[bool]
    copy_tags_to_volumes: pulumi.Input[bool]
    daily_automatic_backup_start_time: pulumi.Input[str] | None
    deployment_type: pulumi.Input[str]
    root_volume_configuration: AWSFsxOpenZfsMultiRootVolumeConfigurationArgs
    route_table_ids: list[str] | list[pulumi.Output[str]]
    security_group_ids: pulumi.Input[list[pulumi.Input[str]]]
    storage_capacity: pulumi.Input[int]
    subnet_ids: list[str] | list[pulumi.Output[str]]
    tags: dict[str, str]
    throughput_capacity: pulumi.Input[int]


@dataclasses.dataclass
class AWSFsxOpenZfsMultiRootVolumeConfigurationArgs:
    copy_tags_to_snapshots: pulumi.Input[bool]
    data_compression_type: pulumi.Input[str]
    nfs_exports: AWSFsxOpenZfsMultiRootVolumeConfigurationNfsExportsArgs


@dataclasses.dataclass
class AWSFsxOpenZfsMultiRootVolumeConfigurationNfsExportsArgs:
    client_configurations: list[AWSFsxOpenZfsMultiRootVolumeConfigurationNfsExportsClientConfigurationArgs]


@dataclasses.dataclass
class AWSFsxOpenZfsMultiRootVolumeConfigurationNfsExportsClientConfigurationArgs:
    clients: pulumi.Input[str]
    options: pulumi.Input[list[pulumi.Input[str]]]


class AWSFsxOpenZfsMulti(pulumi.ComponentResource):
    """
    A component resource that creates an AWS FSx for OpenZFS file system using the native Pulumi AWS provider.
    This replaces the previous custom dynamic resource implementation.
    """

    # Expose the important outputs
    dns_name: pulumi.Output[str]
    root_volume_id: pulumi.Output[str]
    file_system_id: pulumi.Output[str]

    def __init__(
        self,
        name: str,
        props: AWSFsxOpenZfsMultiArgs,
        opts: pulumi.ResourceOptions | None = None,
    ):
        super().__init__("ptd:aws:AWSFsxOpenZfsMulti", name, None, opts)

        # Convert our custom args to the format expected by aws.fsx.OpenZfsFileSystem
        client_configurations = [
            aws.fsx.OpenZfsFileSystemRootVolumeConfigurationNfsExportsClientConfigurationArgs(
                clients=client_config.clients,
                options=client_config.options,
            )
            for client_config in props.root_volume_configuration.nfs_exports.client_configurations
        ]

        nfs_exports = aws.fsx.OpenZfsFileSystemRootVolumeConfigurationNfsExportsArgs(
            client_configurations=client_configurations,
        )

        root_volume_configuration = aws.fsx.OpenZfsFileSystemRootVolumeConfigurationArgs(
            copy_tags_to_snapshots=props.root_volume_configuration.copy_tags_to_snapshots,
            data_compression_type=props.root_volume_configuration.data_compression_type,
            nfs_exports=nfs_exports,
        )

        # Create the FSx for OpenZFS file system using the native provider
        # Note: For MULTI_AZ deployments, we need to specify preferred_subnet_id
        preferred_subnet_id = None
        if props.deployment_type == "MULTI_AZ_1" and len(props.subnet_ids) > 0:
            # Use the first subnet as preferred for MULTI_AZ
            preferred_subnet_id = props.subnet_ids[0]

        # Only pass daily_automatic_backup_start_time if it has a valid value.
        # AWS doesn't return this field properly on refresh, causing state corruption
        # with empty strings that fail validation. By making it conditional, we avoid
        # the validation error and let AWS use its default if the state is corrupted.
        daily_backup_time = props.daily_automatic_backup_start_time
        if daily_backup_time is not None and len(daily_backup_time) != 5:
            # Invalid value (likely empty string from corrupted state), don't pass it
            daily_backup_time = None

        self.file_system = aws.fsx.OpenZfsFileSystem(
            f"{name}-filesystem",
            automatic_backup_retention_days=props.automatic_backup_retention_days,
            deployment_type=props.deployment_type,
            preferred_subnet_id=preferred_subnet_id,
            subnet_ids=props.subnet_ids,
            security_group_ids=props.security_group_ids,
            storage_capacity=props.storage_capacity,
            storage_type="SSD",  # Fixed to SSD as in the original implementation
            throughput_capacity=props.throughput_capacity,
            copy_tags_to_backups=props.copy_tags_to_backups,
            copy_tags_to_volumes=props.copy_tags_to_volumes,
            daily_automatic_backup_start_time=daily_backup_time,
            route_table_ids=props.route_table_ids,
            root_volume_configuration=root_volume_configuration,
            tags=props.tags,
            # ignore_changes for daily_automatic_backup_start_time: This field is not
            # properly read back from AWS after import, causing perpetual diffs.
            # See: https://github.com/posit-dev/ptd/issues/5
            opts=pulumi.ResourceOptions(
                parent=self,
                ignore_changes=["daily_automatic_backup_start_time"],
            ),
        )

        # Export the outputs
        self.dns_name = self.file_system.dns_name
        self.root_volume_id = self.file_system.root_volume_id
        self.file_system_id = self.file_system.id

        # Register the outputs with the component resource
        self.register_outputs(
            {
                "dns_name": self.dns_name,
                "root_volume_id": self.root_volume_id,
                "file_system_id": self.file_system_id,
            }
        )
