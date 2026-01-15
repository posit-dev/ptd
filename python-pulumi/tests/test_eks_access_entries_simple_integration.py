"""
Simple integration tests for EKS Access Entries implementation.

These tests focus on integration patterns without full resource creation
to avoid complex mocking scenarios.
"""

import dataclasses

import pytest

import ptd


def test_workload_cluster_config_integration():
    """Test that WorkloadClusterConfig integrates properly with feature flags."""
    # Test traditional ConfigMap approach
    configmap_config = ptd.WorkloadClusterConfig(
        team_operator_image="v1.0.0",
        ptd_controller_image="v2.0.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=False,
            additional_entries=[],
        ),
    )

    assert configmap_config.eks_access_entries.enabled is False
    assert configmap_config.eks_access_entries.additional_entries == []
    assert configmap_config.team_operator_image == "v1.0.0"
    assert configmap_config.ptd_controller_image == "v2.0.0"

    # Test modern Access Entries approach
    access_entries_config = ptd.WorkloadClusterConfig(
        team_operator_image="v1.1.0",
        ptd_controller_image="v2.1.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            additional_entries=[
                {
                    "principalArn": "arn:aws:iam::123456789012:role/admin-role",
                    "type": "STANDARD",
                    "accessPolicies": [
                        {
                            "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                            "accessScope": {"type": "cluster"},
                        }
                    ],
                }
            ],
        ),
    )

    assert access_entries_config.eks_access_entries.enabled is True
    assert len(access_entries_config.eks_access_entries.additional_entries) == 1
    assert access_entries_config.team_operator_image == "v1.1.0"
    assert access_entries_config.ptd_controller_image == "v2.1.0"


def test_mixed_cluster_configurations():
    """Test that different clusters can use different access methods."""
    # Staging uses ConfigMap (traditional)
    staging_config = ptd.WorkloadClusterConfig(
        team_operator_image="staging-v1.0.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(enabled=False),
    )

    # Production uses Access Entries (modern)
    production_config = ptd.WorkloadClusterConfig(
        team_operator_image="prod-v1.0.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            additional_entries=[
                {
                    "principalArn": "arn:aws:iam::123456789012:role/production-admin",
                    "type": "STANDARD",
                    "accessPolicies": [
                        {
                            "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                            "accessScope": {"type": "cluster"},
                        }
                    ],
                }
            ],
        ),
    )

    # Verify independent configuration
    assert staging_config.eks_access_entries.enabled is False
    assert production_config.eks_access_entries.enabled is True
    assert len(staging_config.eks_access_entries.additional_entries) == 0
    assert len(production_config.eks_access_entries.additional_entries) == 1


def test_access_entry_structure_validation():
    """Test that access entries follow the expected structure."""
    # Cluster-wide admin access
    cluster_admin_entry = {
        "principalArn": "arn:aws:iam::123456789012:role/cluster-admin",
        "type": "STANDARD",
        "accessPolicies": [
            {
                "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                "accessScope": {"type": "cluster"},
            }
        ],
    }

    # Namespace-scoped edit access
    namespace_editor_entry = {
        "principalArn": "arn:aws:iam::123456789012:role/namespace-editor",
        "type": "STANDARD",
        "accessPolicies": [
            {
                "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSEditPolicy",
                "accessScope": {
                    "type": "namespace",
                    "namespaces": ["app-namespace", "dev-namespace"],
                },
            }
        ],
    }

    # View-only access
    viewer_entry = {
        "principalArn": "arn:aws:iam::123456789012:role/viewer",
        "type": "STANDARD",
        "accessPolicies": [
            {
                "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy",
                "accessScope": {"type": "cluster"},
            }
        ],
    }

    # Create config with all variations
    config = ptd.WorkloadClusterConfig(
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            additional_entries=[
                cluster_admin_entry,
                namespace_editor_entry,
                viewer_entry,
            ],
        )
    )

    # Verify all entries are preserved
    assert len(config.eks_access_entries.additional_entries) == 3

    # Verify cluster admin entry
    admin = config.eks_access_entries.additional_entries[0]
    assert admin["principalArn"] == "arn:aws:iam::123456789012:role/cluster-admin"
    assert admin["accessPolicies"][0]["accessScope"]["type"] == "cluster"

    # Verify namespace editor entry
    editor = config.eks_access_entries.additional_entries[1]
    assert editor["principalArn"] == "arn:aws:iam::123456789012:role/namespace-editor"
    assert editor["accessPolicies"][0]["accessScope"]["type"] == "namespace"
    assert "app-namespace" in editor["accessPolicies"][0]["accessScope"]["namespaces"]
    assert "dev-namespace" in editor["accessPolicies"][0]["accessScope"]["namespaces"]

    # Verify viewer entry
    viewer = config.eks_access_entries.additional_entries[2]
    assert viewer["principalArn"] == "arn:aws:iam::123456789012:role/viewer"
    assert viewer["accessPolicies"][0]["policyArn"] == "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"


def test_backward_compatibility_patterns():
    """Test backward compatibility scenarios."""
    # Old pattern: no Access Entries fields specified
    old_config = ptd.WorkloadClusterConfig(
        team_operator_image="v1.0.0",
        ptd_controller_image="v2.0.0",
    )

    # Should have default values for new fields
    assert old_config.eks_access_entries.enabled is False
    assert old_config.eks_access_entries.additional_entries == []

    # Migration pattern: enable Access Entries but no additional entries
    migration_config = ptd.WorkloadClusterConfig(
        team_operator_image="v1.1.0",
        ptd_controller_image="v2.1.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(enabled=True),
    )

    assert migration_config.eks_access_entries.enabled is True
    assert migration_config.eks_access_entries.additional_entries == []

    # Full migration: Access Entries with additional roles
    full_config = ptd.WorkloadClusterConfig(
        team_operator_image="v1.2.0",
        ptd_controller_image="v2.2.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            additional_entries=[
                {
                    "principalArn": "arn:aws:iam::123456789012:role/admin",
                    "type": "STANDARD",
                    "accessPolicies": [
                        {
                            "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                            "accessScope": {"type": "cluster"},
                        }
                    ],
                }
            ],
        ),
    )

    assert full_config.eks_access_entries.enabled is True
    assert len(full_config.eks_access_entries.additional_entries) == 1


def test_workload_config_integration():
    """Test WorkloadClusterConfig as part of full WorkloadConfig."""
    # Create cluster configs with different access methods
    staging_config = ptd.WorkloadClusterConfig(
        team_operator_image="staging-v1.0.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(enabled=False),
    )

    production_config = ptd.WorkloadClusterConfig(
        team_operator_image="prod-v1.0.0",
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            additional_entries=[
                {
                    "principalArn": "arn:aws:iam::123456789012:role/production-admin",
                    "type": "STANDARD",
                    "accessPolicies": [
                        {
                            "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                            "accessScope": {"type": "cluster"},
                        }
                    ],
                }
            ],
        ),
    )

    # Create workload config
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

    # Verify cluster configs are properly embedded
    assert workload_config.clusters["staging"].eks_access_entries.enabled is False
    assert workload_config.clusters["staging"].team_operator_image == "staging-v1.0.0"
    assert len(workload_config.clusters["staging"].eks_access_entries.additional_entries) == 0

    assert workload_config.clusters["production"].eks_access_entries.enabled is True
    assert workload_config.clusters["production"].team_operator_image == "prod-v1.0.0"
    assert len(workload_config.clusters["production"].eks_access_entries.additional_entries) == 1

    # Verify access to nested properties
    prod_principal = workload_config.clusters["production"].eks_access_entries.additional_entries[0]["principalArn"]
    assert prod_principal == "arn:aws:iam::123456789012:role/production-admin"


def test_configuration_immutability():
    """Test that configurations are properly immutable."""
    config = ptd.WorkloadClusterConfig(
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            additional_entries=[
                {
                    "principalArn": "arn:aws:iam::123456789012:role/admin",
                    "type": "STANDARD",
                    "accessPolicies": [
                        {
                            "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                            "accessScope": {"type": "cluster"},
                        }
                    ],
                }
            ],
        )
    )

    # Verify dataclass is frozen
    assert dataclasses.is_dataclass(config)
    assert config.__dataclass_params__.frozen

    # Attempting to modify should raise FrozenInstanceError
    try:
        config.eks_access_entries.enabled = False
        pytest.fail("Should not be able to modify frozen dataclass")
    except dataclasses.FrozenInstanceError:
        pass  # Expected behavior

    # Verify separate instances have independent data
    config1 = ptd.WorkloadClusterConfig()
    config2 = ptd.WorkloadClusterConfig()

    assert config1.eks_access_entries is not config2.eks_access_entries


def test_feature_flag_consistency():
    """Test that feature flag behavior is consistent."""
    # Default behavior (ConfigMap)
    default_config = ptd.WorkloadClusterConfig()
    assert default_config.eks_access_entries.enabled is False

    # Explicit ConfigMap mode
    configmap_config = ptd.WorkloadClusterConfig(eks_access_entries=ptd.EKSAccessEntriesConfig(enabled=False))
    assert configmap_config.eks_access_entries.enabled is False

    # Access Entries mode
    access_entries_config = ptd.WorkloadClusterConfig(eks_access_entries=ptd.EKSAccessEntriesConfig(enabled=True))
    assert access_entries_config.eks_access_entries.enabled is True

    # Feature flag should be independent of other settings
    config_with_entries = ptd.WorkloadClusterConfig(
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            additional_entries=[
                {
                    "principalArn": "arn:aws:iam::123456789012:role/admin",
                    "type": "STANDARD",
                    "accessPolicies": [
                        {
                            "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                            "accessScope": {"type": "cluster"},
                        }
                    ],
                }
            ],
        )
    )

    assert config_with_entries.eks_access_entries.enabled is True
    assert len(config_with_entries.eks_access_entries.additional_entries) == 1


def test_poweruser_configuration():
    """Test PowerUser role configuration options."""
    # Default: PowerUser not included
    default_config = ptd.WorkloadClusterConfig()
    assert default_config.eks_access_entries.include_same_account_poweruser is False

    # Explicitly exclude PowerUser
    no_poweruser = ptd.WorkloadClusterConfig(
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            include_same_account_poweruser=False,
        )
    )
    assert no_poweruser.eks_access_entries.include_same_account_poweruser is False

    # Include PowerUser
    with_poweruser = ptd.WorkloadClusterConfig(
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            include_same_account_poweruser=True,
        )
    )
    assert with_poweruser.eks_access_entries.include_same_account_poweruser is True

    # PowerUser with additional entries
    full_config = ptd.WorkloadClusterConfig(
        eks_access_entries=ptd.EKSAccessEntriesConfig(
            enabled=True,
            include_same_account_poweruser=True,
            additional_entries=[
                {
                    "principalArn": "arn:aws:iam::123456789012:role/custom-admin",
                    "type": "STANDARD",
                    "accessPolicies": [
                        {
                            "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
                            "accessScope": {"type": "cluster"},
                        }
                    ],
                }
            ],
        )
    )
    assert full_config.eks_access_entries.include_same_account_poweruser is True
    assert len(full_config.eks_access_entries.additional_entries) == 1
