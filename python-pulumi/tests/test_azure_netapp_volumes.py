"""Tests for Azure NetApp Files static volume provisioning."""

import typing

import pulumi

import ptd.azure_workload


class AzureNetappMocks(pulumi.runtime.Mocks):
    def new_resource(self, args: pulumi.runtime.MockResourceArgs) -> tuple[str | None, dict[typing.Any, typing.Any]]:
        return args.name, dict(args.inputs)

    def call(
        self, _args: pulumi.runtime.MockCallArgs
    ) -> dict[typing.Any, typing.Any] | tuple[dict[typing.Any, typing.Any], list[tuple[str, str]] | None]:
        return {}


pulumi.runtime.set_mocks(AzureNetappMocks(), preview=False)


def _make_config(**overrides) -> ptd.azure_workload.AzureWorkloadConfig:
    defaults = {
        "clusters": {},
        "subscription_id": "test-sub-id",
        "tenant_id": "test-tenant-id",
        "network": ptd.azure_workload.NetworkConfig(
            private_subnet_cidr="10.0.1.0/24",
            db_subnet_cidr="10.0.2.0/24",
            netapp_subnet_cidr="10.0.3.0/24",
            app_gateway_subnet_cidr="10.0.4.0/24",
            bastion_subnet_cidr="10.0.5.0/24",
        ),
        "region": "eastus",
        "control_room_account_id": "test-account",
        "control_room_cluster_name": "test-cluster",
        "control_room_domain": "test.example.com",
        "control_room_region": "eastus",
        "control_room_role_name": "test-role",
        "control_room_state_bucket": "test-bucket",
        "environment": "test",
        "network_trust": ptd.NetworkTrust.FULL,
        "sites": {},
        "true_name": "test-workload",
    }
    defaults.update(overrides)
    return ptd.azure_workload.AzureWorkloadConfig(**defaults)


def test_automated_volume_provisioning_defaults_false():
    config = _make_config()
    assert config.automated_volume_provisioning is False


def test_automated_volume_provisioning_can_be_enabled():
    config = _make_config(automated_volume_provisioning=True)
    assert config.automated_volume_provisioning is True


def test_netapp_volume_capacity_defaults():
    config = _make_config()
    assert config.netapp_volume_connect_capacity == 200
    assert config.netapp_volume_workbench_capacity == 200
    assert config.netapp_volume_workbench_shared_capacity == 200


def test_netapp_volume_capacity_custom_values():
    config = _make_config(
        netapp_volume_connect_capacity=100,
        netapp_volume_workbench_capacity=200,
        netapp_volume_workbench_shared_capacity=300,
    )
    assert config.netapp_volume_connect_capacity == 100
    assert config.netapp_volume_workbench_capacity == 200
    assert config.netapp_volume_workbench_shared_capacity == 300


def test_netapp_volume_name():
    """Test the volume naming convention: nav-ptd-{site}-{product}."""
    config = _make_config()
    workload = ptd.azure_workload.AzureWorkload.__new__(ptd.azure_workload.AzureWorkload)
    workload.cfg = config

    assert workload.netapp_volume_name("main", "connect") == "nav-ptd-main-connect"
    assert workload.netapp_volume_name("dev", "workbench") == "nav-ptd-dev-workbench"
    assert workload.netapp_volume_name("main", "workbench-shared") == "nav-ptd-main-workbench-shared"
    assert workload.netapp_volume_name("dev", "connect") == "nav-ptd-dev-connect"
