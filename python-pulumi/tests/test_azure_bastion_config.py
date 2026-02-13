"""Tests for Azure Bastion instance type configuration."""

import typing

import pulumi

import ptd.azure_workload


class AzureBastionMocks(pulumi.runtime.Mocks):
    """Mock Pulumi resource calls for testing."""

    def new_resource(self, args: pulumi.runtime.MockResourceArgs) -> tuple[str | None, dict[typing.Any, typing.Any]]:
        """Mock resource creation - return resource ID and properties."""
        # Return the inputs as outputs for testing
        return args.name, dict(args.inputs)

    def call(
        self, _args: pulumi.runtime.MockCallArgs
    ) -> dict[typing.Any, typing.Any] | tuple[dict[typing.Any, typing.Any], list[tuple[str, str]] | None]:
        """Mock function calls."""
        return {}


pulumi.runtime.set_mocks(AzureBastionMocks(), preview=False)


def test_azure_workload_config_default_bastion_instance_type():
    """Test that AzureWorkloadConfig has correct default bastion_instance_type."""
    config = ptd.azure_workload.AzureWorkloadConfig(
        clusters={},
        subscription_id="test-sub-id",
        tenant_id="test-tenant-id",
        client_id="test-client-id",
        secrets_provider_client_id="test-sp-client-id",
        network=ptd.azure_workload.NetworkConfig(
            private_subnet_cidr="10.0.1.0/24",
            db_subnet_cidr="10.0.2.0/24",
            netapp_subnet_cidr="10.0.3.0/24",
            app_gateway_subnet_cidr="10.0.4.0/24",
            bastion_subnet_cidr="10.0.5.0/24",
        ),
        region="eastus",
        control_room_account_id="test-account",
        control_room_cluster_name="test-cluster",
        control_room_domain="test.example.com",
        control_room_region="eastus",
        control_room_role_name="test-role",
        control_room_state_bucket="test-bucket",
        environment="test",
        network_trust=ptd.NetworkTrust.FULL,
        sites={},
        true_name="test-workload",
    )

    # Verify default value
    assert config.bastion_instance_type == "Standard_B1s"


def test_azure_workload_config_custom_bastion_instance_type():
    """Test that AzureWorkloadConfig accepts custom bastion_instance_type."""
    config = ptd.azure_workload.AzureWorkloadConfig(
        clusters={},
        subscription_id="test-sub-id",
        tenant_id="test-tenant-id",
        client_id="test-client-id",
        secrets_provider_client_id="test-sp-client-id",
        network=ptd.azure_workload.NetworkConfig(
            private_subnet_cidr="10.0.1.0/24",
            db_subnet_cidr="10.0.2.0/24",
            netapp_subnet_cidr="10.0.3.0/24",
            app_gateway_subnet_cidr="10.0.4.0/24",
            bastion_subnet_cidr="10.0.5.0/24",
        ),
        region="eastus",
        bastion_instance_type="Standard_B2s",
        control_room_account_id="test-account",
        control_room_cluster_name="test-cluster",
        control_room_domain="test.example.com",
        control_room_region="eastus",
        control_room_role_name="test-role",
        control_room_state_bucket="test-bucket",
        environment="test",
        network_trust=ptd.NetworkTrust.FULL,
        sites={},
        true_name="test-workload",
    )

    # Verify custom value
    assert config.bastion_instance_type == "Standard_B2s"


def test_azure_workload_config_bastion_instance_type_matches_aws_pattern():
    """Test that Azure uses same field name as AWS for consistency."""
    aws_config = ptd.aws_workload.AWSWorkloadConfig(
        clusters={},
        account_id="123456789012",
        region="us-east-1",
        control_room_account_id="123456789012",
        control_room_cluster_name="test-cluster",
        control_room_domain="test.example.com",
        control_room_region="us-east-1",
        control_room_role_name="test-role",
        control_room_state_bucket="test-bucket",
        environment="test",
        network_trust=ptd.NetworkTrust.FULL,
        sites={},
        true_name="test-workload",
    )

    azure_config = ptd.azure_workload.AzureWorkloadConfig(
        clusters={},
        subscription_id="test-sub-id",
        tenant_id="test-tenant-id",
        client_id="test-client-id",
        secrets_provider_client_id="test-sp-client-id",
        network=ptd.azure_workload.NetworkConfig(
            private_subnet_cidr="10.0.1.0/24",
            db_subnet_cidr="10.0.2.0/24",
            netapp_subnet_cidr="10.0.3.0/24",
            app_gateway_subnet_cidr="10.0.4.0/24",
            bastion_subnet_cidr="10.0.5.0/24",
        ),
        region="eastus",
        control_room_account_id="test-account",
        control_room_cluster_name="test-cluster",
        control_room_domain="test.example.com",
        control_room_region="eastus",
        control_room_role_name="test-role",
        control_room_state_bucket="test-bucket",
        environment="test",
        network_trust=ptd.NetworkTrust.FULL,
        sites={},
        true_name="test-workload",
    )

    # Both configs should have the same field name for bastion instance type
    assert hasattr(aws_config, "bastion_instance_type")
    assert hasattr(azure_config, "bastion_instance_type")

    # Verify they have appropriate defaults for their cloud provider
    assert aws_config.bastion_instance_type == "t4g.nano"  # AWS default
    assert azure_config.bastion_instance_type == "Standard_B1s"  # Azure default
