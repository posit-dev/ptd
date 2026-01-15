import dataclasses

import pytest

import ptd.aws_workload


def test_vpc_endpoints_config_default_initialization():
    """Test that VPCEndpointsConfig initializes with correct default values."""
    config = ptd.aws_workload.VPCEndpointsConfig()

    # Test default values
    assert config.enabled is True
    assert config.excluded_services == []


def test_vpc_endpoints_config_custom_initialization():
    """Test that VPCEndpointsConfig can be initialized with custom values."""
    config = ptd.aws_workload.VPCEndpointsConfig(
        enabled=False,
        excluded_services=["fsx", "kms"],
    )

    # Test custom values
    assert config.enabled is False
    assert config.excluded_services == ["fsx", "kms"]


def test_vpc_endpoints_config_is_frozen():
    """Test that VPCEndpointsConfig is frozen (immutable)."""
    config = ptd.aws_workload.VPCEndpointsConfig()

    # Verify that the class is frozen
    assert dataclasses.is_dataclass(config)
    assert config.__dataclass_params__.frozen is True

    # Test that we cannot modify attributes after creation
    with pytest.raises(dataclasses.FrozenInstanceError):
        config.enabled = False


def test_vpc_endpoints_config_valid_services():
    """Test that VPCEndpointsConfig accepts all valid service names."""
    valid_services = ["ec2", "ec2messages", "ecr.api", "ecr.dkr", "kms", "s3", "ssm", "ssmmessages", "fsx"]

    for service in valid_services:
        config = ptd.aws_workload.VPCEndpointsConfig(excluded_services=[service])
        assert config.excluded_services == [service]


def test_vpc_endpoints_config_invalid_service_raises_error():
    """Test that VPCEndpointsConfig raises error for invalid service names."""
    with pytest.raises(ValueError, match="Invalid service names in excluded_services"):
        ptd.aws_workload.VPCEndpointsConfig(excluded_services=["invalid_service"])


def test_vpc_endpoints_config_mixed_valid_invalid_services():
    """Test that VPCEndpointsConfig raises error when both valid and invalid services are provided."""
    with pytest.raises(ValueError, match="Invalid service names in excluded_services: \\['bad_service', 'invalid'\\]"):
        ptd.aws_workload.VPCEndpointsConfig(excluded_services=["s3", "invalid", "kms", "bad_service"])


def test_vpc_endpoints_config_multiple_valid_services():
    """Test that VPCEndpointsConfig accepts multiple valid service names."""
    config = ptd.aws_workload.VPCEndpointsConfig(
        excluded_services=["fsx", "kms", "ecr.api"],
    )

    assert config.excluded_services == ["fsx", "kms", "ecr.api"]
    assert len(config.excluded_services) == 3


def test_vpc_endpoints_config_disable_all_endpoints():
    """Test that VPCEndpointsConfig can disable all endpoints."""
    config = ptd.aws_workload.VPCEndpointsConfig(enabled=False)

    assert config.enabled is False
    assert config.excluded_services == []


def test_vpc_endpoints_config_exclude_all_services():
    """Test that VPCEndpointsConfig can exclude all services."""
    all_services = ["ec2", "ec2messages", "ecr.api", "ecr.dkr", "kms", "s3", "ssm", "ssmmessages", "fsx"]
    config = ptd.aws_workload.VPCEndpointsConfig(
        enabled=True,
        excluded_services=all_services,
    )

    assert config.enabled is True
    assert len(config.excluded_services) == 9
    assert set(config.excluded_services) == set(all_services)


def test_vpc_endpoints_config_default_factory():
    """Test that excluded_services uses default_factory to create separate instances."""
    config1 = ptd.aws_workload.VPCEndpointsConfig()
    config2 = ptd.aws_workload.VPCEndpointsConfig()

    # Verify that both configs have empty lists
    assert config1.excluded_services == []
    assert config2.excluded_services == []

    # Verify that they are separate instances
    assert config1 is not config2
    assert config1.excluded_services is not config2.excluded_services


def test_vpc_endpoints_config_backwards_compatibility():
    """Test that existing code patterns continue to work without vpc_endpoints config."""
    # Create an AWSWorkloadConfig without vpc_endpoints specified
    config = ptd.aws_workload.AWSWorkloadConfig(
        account_id="123456789012",
        clusters={},
        sites={},
        region="us-east-1",
        control_room_account_id="123456789012",
        control_room_cluster_name="ctrl-cluster",
        control_room_domain="ctrl.example.com",
        control_room_region="us-east-1",
        control_room_role_name="ctrl-role",
        control_room_state_bucket="ctrl-state-bucket",
        environment="test",
        network_trust=ptd.NetworkTrust.FULL,
        true_name="test-workload",
    )

    # Verify vpc_endpoints defaults to None (backward compatible)
    assert config.vpc_endpoints is None


def test_vpc_endpoints_config_in_aws_workload_config():
    """Test that VPCEndpointsConfig works properly as part of AWSWorkloadConfig."""
    vpc_endpoints = ptd.aws_workload.VPCEndpointsConfig(
        enabled=True,
        excluded_services=["fsx"],
    )

    config = ptd.aws_workload.AWSWorkloadConfig(
        account_id="123456789012",
        clusters={},
        sites={},
        region="us-east-1",
        control_room_account_id="123456789012",
        control_room_cluster_name="ctrl-cluster",
        control_room_domain="ctrl.example.com",
        control_room_region="us-east-1",
        control_room_role_name="ctrl-role",
        control_room_state_bucket="ctrl-state-bucket",
        environment="production",
        network_trust=ptd.NetworkTrust.FULL,
        true_name="test-workload",
        vpc_endpoints=vpc_endpoints,
    )

    # Verify the configuration
    assert config.vpc_endpoints is not None
    assert config.vpc_endpoints.enabled is True
    assert config.vpc_endpoints.excluded_services == ["fsx"]


def test_vpc_endpoints_config_disabled_in_aws_workload_config():
    """Test that VPCEndpointsConfig with enabled=False works in AWSWorkloadConfig."""
    vpc_endpoints = ptd.aws_workload.VPCEndpointsConfig(enabled=False)

    config = ptd.aws_workload.AWSWorkloadConfig(
        account_id="123456789012",
        clusters={},
        sites={},
        region="us-east-1",
        control_room_account_id="123456789012",
        control_room_cluster_name="ctrl-cluster",
        control_room_domain="ctrl.example.com",
        control_room_region="us-east-1",
        control_room_role_name="ctrl-role",
        control_room_state_bucket="ctrl-state-bucket",
        environment="development",
        network_trust=ptd.NetworkTrust.FULL,
        true_name="test-workload",
        vpc_endpoints=vpc_endpoints,
    )

    # Verify the configuration
    assert config.vpc_endpoints is not None
    assert config.vpc_endpoints.enabled is False
    assert config.vpc_endpoints.excluded_services == []


def test_vpc_endpoints_config_dataclass_fields():
    """Test that VPCEndpointsConfig has the expected dataclass fields."""
    fields = dataclasses.fields(ptd.aws_workload.VPCEndpointsConfig)
    field_names = [field.name for field in fields]

    expected_fields = ["enabled", "excluded_services"]

    # Verify all expected fields are present
    for expected_field in expected_fields:
        assert expected_field in field_names

    # Check field types and defaults
    field_dict = {field.name: field for field in fields}

    # enabled field
    enabled_field = field_dict["enabled"]
    assert enabled_field.default is True

    # excluded_services field
    excluded_services_field = field_dict["excluded_services"]
    assert excluded_services_field.default == dataclasses.MISSING
    assert excluded_services_field.default_factory is not dataclasses.MISSING


def test_vpc_endpoints_config_valid_services_constant():
    """Test that VALID_VPC_ENDPOINT_SERVICES constant contains all expected services."""
    expected_services = {"ec2", "ec2messages", "ecr.api", "ecr.dkr", "fsx", "kms", "s3", "ssm", "ssmmessages"}

    assert expected_services == ptd.aws_workload.VALID_VPC_ENDPOINT_SERVICES


def test_vpc_endpoints_config_case_sensitive():
    """Test that service names are case-sensitive."""
    # Uppercase version should fail
    with pytest.raises(ValueError, match="Invalid service names in excluded_services"):
        ptd.aws_workload.VPCEndpointsConfig(excluded_services=["FSX"])

    # Mixed case should fail
    with pytest.raises(ValueError, match="Invalid service names in excluded_services"):
        ptd.aws_workload.VPCEndpointsConfig(excluded_services=["Fsx"])

    # Lowercase should succeed
    config = ptd.aws_workload.VPCEndpointsConfig(excluded_services=["fsx"])
    assert config.excluded_services == ["fsx"]


def test_vpc_endpoints_config_empty_excluded_services():
    """Test that VPCEndpointsConfig handles empty excluded_services properly."""
    config = ptd.aws_workload.VPCEndpointsConfig(enabled=True, excluded_services=[])

    assert config.enabled is True
    assert config.excluded_services == []
    assert isinstance(config.excluded_services, list)
