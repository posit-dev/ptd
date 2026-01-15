import dataclasses

import pytest

import ptd


def test_workload_cluster_config_default_initialization():
    """Test that WorkloadClusterConfig initializes with correct default values."""
    config = ptd.WorkloadClusterConfig()

    # Test default values
    assert config.team_operator_image == "latest"
    assert config.ptd_controller_image == "latest"
    assert config.eks_access_entries.enabled is False
    assert config.eks_access_entries.additional_entries == []
    assert config.eks_access_entries.include_same_account_poweruser is False


def test_workload_cluster_config_custom_initialization():
    """Test that WorkloadClusterConfig can be initialized with custom values."""
    additional_entries = [
        {
            "principalArn": "arn:aws:iam::123456789012:role/custom-role",
            "type": "STANDARD",
            "accessPolicies": [
                {
                    "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSEditPolicy",
                    "accessScope": {"type": "cluster"},
                }
            ],
        }
    ]

    eks_config = ptd.EKSAccessEntriesConfig(
        enabled=True,
        additional_entries=additional_entries,
        include_same_account_poweruser=True,
    )

    config = ptd.WorkloadClusterConfig(
        team_operator_image="v1.2.3",
        ptd_controller_image="v2.3.4",
        eks_access_entries=eks_config,
    )

    # Test custom values
    assert config.team_operator_image == "v1.2.3"
    assert config.ptd_controller_image == "v2.3.4"
    assert config.eks_access_entries.enabled is True
    assert config.eks_access_entries.additional_entries == additional_entries
    assert config.eks_access_entries.include_same_account_poweruser is True


def test_workload_cluster_config_is_frozen():
    """Test that WorkloadClusterConfig is frozen (immutable)."""
    config = ptd.WorkloadClusterConfig()

    # Verify that the class is frozen
    assert dataclasses.is_dataclass(config)
    assert config.__dataclass_params__.frozen is True

    # Test that we cannot modify attributes after creation
    try:
        config.eks_access_entries.enabled = True
        pytest.fail("Should not be able to modify frozen dataclass")
    except dataclasses.FrozenInstanceError:
        pass  # Expected behavior


def test_workload_cluster_config_eks_access_entries_default_factory():
    """Test that eks_access_entries uses default_factory to create separate instances."""
    config1 = ptd.WorkloadClusterConfig()
    config2 = ptd.WorkloadClusterConfig()

    # Verify that both configs have empty lists
    assert config1.eks_access_entries.additional_entries == []
    assert config2.eks_access_entries.additional_entries == []

    # Verify that they are separate instances
    assert config1.eks_access_entries is not config2.eks_access_entries
    assert config1.eks_access_entries.additional_entries is not config2.eks_access_entries.additional_entries


def test_workload_cluster_config_eks_access_entries_structure():
    """Test that eks_access_entries can hold properly structured access entry data."""
    access_entries = [
        {
            "principalArn": "arn:aws:iam::123456789012:role/role1",
            "type": "STANDARD",
            "accessPolicies": [
                {
                    "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                    "accessScope": {"type": "cluster"},
                },
                {
                    "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSEditPolicy",
                    "accessScope": {
                        "type": "namespace",
                        "namespaces": ["default", "kube-system"],
                    },
                },
            ],
        },
        {
            "principalArn": "arn:aws:iam::123456789012:role/role2",
            "type": "STANDARD",
            "accessPolicies": [
                {
                    "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy",
                    "accessScope": {"type": "cluster"},
                }
            ],
        },
    ]

    eks_config = ptd.EKSAccessEntriesConfig(
        enabled=True,
        additional_entries=access_entries,
    )
    config = ptd.WorkloadClusterConfig(eks_access_entries=eks_config)

    # Verify that the structure is preserved
    assert len(config.eks_access_entries.additional_entries) == 2

    # Test first entry
    entry1 = config.eks_access_entries.additional_entries[0]
    assert entry1["principalArn"] == "arn:aws:iam::123456789012:role/role1"
    assert entry1["type"] == "STANDARD"
    assert len(entry1["accessPolicies"]) == 2

    # Test first policy of first entry
    policy1 = entry1["accessPolicies"][0]
    assert policy1["policyArn"] == "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
    assert policy1["accessScope"]["type"] == "cluster"

    # Test second policy of first entry (with namespaces)
    policy2 = entry1["accessPolicies"][1]
    assert policy2["policyArn"] == "arn:aws:eks::aws:cluster-access-policy/AmazonEKSEditPolicy"
    assert policy2["accessScope"]["type"] == "namespace"
    assert policy2["accessScope"]["namespaces"] == ["default", "kube-system"]

    # Test second entry
    entry2 = config.eks_access_entries.additional_entries[1]
    assert entry2["principalArn"] == "arn:aws:iam::123456789012:role/role2"
    assert entry2["type"] == "STANDARD"
    assert len(entry2["accessPolicies"]) == 1


def test_workload_cluster_config_empty_eks_access_entries():
    """Test that WorkloadClusterConfig handles empty eks_access_entries properly."""
    config = ptd.WorkloadClusterConfig()

    assert config.eks_access_entries.additional_entries == []
    assert isinstance(config.eks_access_entries.additional_entries, list)


def test_workload_cluster_config_dataclass_fields():
    """Test that WorkloadClusterConfig has the expected dataclass fields."""
    fields = dataclasses.fields(ptd.WorkloadClusterConfig)
    field_names = [field.name for field in fields]

    expected_fields = [
        "team_operator_image",
        "ptd_controller_image",
        "eks_access_entries",
    ]

    # Verify all expected fields are present
    for expected_field in expected_fields:
        assert expected_field in field_names

    # Check field types and defaults
    field_dict = {field.name: field for field in fields}

    # team_operator_image field
    team_op_field = field_dict["team_operator_image"]
    assert team_op_field.default == "latest"

    # ptd_controller_image field
    ptd_ctrl_field = field_dict["ptd_controller_image"]
    assert ptd_ctrl_field.default == "latest"

    # eks_access_entries field
    eks_field = field_dict["eks_access_entries"]
    assert eks_field.default_factory is ptd.EKSAccessEntriesConfig


def test_workload_cluster_config_backwards_compatibility():
    """Test that existing code patterns continue to work with the new fields."""
    # Test that we can create a config with just the original fields
    config = ptd.WorkloadClusterConfig(
        team_operator_image="v1.0.0",
        ptd_controller_image="v2.0.0",
    )

    # Verify new fields have defaults
    assert config.eks_access_entries.enabled is False
    assert config.eks_access_entries.additional_entries == []
    assert config.eks_access_entries.include_same_account_poweruser is False

    # Test that we can access all fields
    assert config.team_operator_image == "v1.0.0"
    assert config.ptd_controller_image == "v2.0.0"


def test_workload_cluster_config_in_workload_config():
    """Test that WorkloadClusterConfig works properly as part of WorkloadConfig."""
    # Create cluster configs
    staging_config = ptd.WorkloadClusterConfig(
        team_operator_image="staging-v1.0.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(enabled=False),
    )

    production_eks = ptd.EKSAccessEntriesConfig(
        enabled=True,
        additional_entries=[
            {
                "principalArn": "arn:aws:iam::123456789012:role/production-role",
                "type": "STANDARD",
                "accessPolicies": [
                    {
                        "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                        "accessScope": {"type": "cluster"},
                    }
                ],
            }
        ],
        include_same_account_poweruser=True,
    )
    production_config = ptd.WorkloadClusterConfig(
        team_operator_image="production-v1.0.0",
        eks_access_entries=production_eks,
    )

    # Create workload config with all required fields
    workload_config = ptd.WorkloadConfig(
        clusters={
            "staging": staging_config,
            "production": production_config,
        },
        region="us-east-1",
        control_room_account_id="123456789012",
        control_room_cluster_name="ctrl-cluster",
        control_room_domain="ctrl.example.com",
        control_room_region="us-east-1",
        control_room_role_name="ctrl-role",
        control_room_state_bucket="ctrl-state-bucket",
        environment="test",
        network_trust=ptd.NetworkTrust.FULL,
        sites={"main": ptd.SiteConfig(domain="example.com")},
        true_name="test-workload",
    )

    # Verify that the cluster configs are accessible and have the right values
    assert workload_config.clusters["staging"].eks_access_entries.enabled is False
    assert workload_config.clusters["staging"].team_operator_image == "staging-v1.0.0"
    assert workload_config.clusters["staging"].eks_access_entries.additional_entries == []

    assert workload_config.clusters["production"].eks_access_entries.enabled is True
    assert workload_config.clusters["production"].team_operator_image == "production-v1.0.0"
    assert len(workload_config.clusters["production"].eks_access_entries.additional_entries) == 1
    assert workload_config.clusters["production"].eks_access_entries.include_same_account_poweruser is True


def test_workload_cluster_config_custom_k8s_resources_default():
    """Test that custom_k8s_resources defaults to None."""
    config = ptd.WorkloadClusterConfig()
    assert config.custom_k8s_resources is None


def test_workload_cluster_config_custom_k8s_resources_with_subfolders():
    """Test that custom_k8s_resources can be set with a list of subfolder names."""
    config = ptd.WorkloadClusterConfig(custom_k8s_resources=["storage", "monitoring"])
    assert config.custom_k8s_resources == ["storage", "monitoring"]
    assert len(config.custom_k8s_resources) == 2


def test_workload_cluster_config_custom_k8s_resources_empty_list():
    """Test that custom_k8s_resources can be an empty list."""
    config = ptd.WorkloadClusterConfig(custom_k8s_resources=[])
    assert config.custom_k8s_resources == []
    assert len(config.custom_k8s_resources) == 0


def test_workload_cluster_config_custom_k8s_resources_in_workload():
    """Test that custom_k8s_resources works in a full workload config."""
    cluster1_config = ptd.WorkloadClusterConfig(custom_k8s_resources=["storage", "common"])
    cluster2_config = ptd.WorkloadClusterConfig(custom_k8s_resources=["monitoring"])

    workload_config = ptd.WorkloadConfig(
        clusters={
            "20250328": cluster1_config,
            "20250415": cluster2_config,
        },
        region="us-east-1",
        control_room_account_id="123456789012",
        control_room_cluster_name="ctrl-cluster",
        control_room_domain="ctrl.example.com",
        control_room_region="us-east-1",
        control_room_role_name="ctrl-role",
        control_room_state_bucket="ctrl-state-bucket",
        environment="production",
        network_trust=ptd.NetworkTrust.FULL,
        sites={"main": ptd.SiteConfig(domain="example.com")},
        true_name="test-workload",
    )

    assert workload_config.clusters["20250328"].custom_k8s_resources == ["storage", "common"]
    assert workload_config.clusters["20250415"].custom_k8s_resources == ["monitoring"]
