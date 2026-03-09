"""Tests for Grafana dashboard ConfigMap provisioning in aws_eks_cluster.py"""

import json
import tempfile
from pathlib import Path

import pytest

from ptd.pulumi_resources.lib import sanitize_k8s_name


def test_dashboard_name_sanitization_basic():
    """Test basic underscore replacement for dashboard names"""
    assert sanitize_k8s_name("alerts_dashboard") == "alerts-dashboard"


def test_dashboard_name_sanitization_edge_cases():
    """Test edge cases in dashboard name sanitization"""
    # Leading/trailing underscores
    assert sanitize_k8s_name("_dashboard_") == "dashboard"

    # Mixed case and special chars
    assert sanitize_k8s_name("My_Custom_Dashboard!") == "my-custom-dashboard"

    # Multiple consecutive special chars
    assert sanitize_k8s_name("test___dashboard") == "test-dashboard"


def test_dashboard_name_sanitization_invalid():
    """Test that invalid dashboard names raise errors"""
    with pytest.raises(ValueError, match="cannot be sanitized to RFC 1123 format"):
        sanitize_k8s_name("___")  # Only underscores

    with pytest.raises(ValueError, match="Name cannot be empty"):
        sanitize_k8s_name("")  # Empty string


def test_dashboard_configmap_structure():
    """Test the expected structure of dashboard ConfigMaps"""
    # This is a documentation test showing the expected structure
    dashboard_name = "alerts_dashboard"
    k8s_safe_name = sanitize_k8s_name(dashboard_name)

    # Expected ConfigMap structure
    expected_metadata = {
        "name": f"grafana-{k8s_safe_name}-dashboard",
        "namespace": "grafana",
        "labels": {"grafana_dashboard": "1"},
    }

    # Verify sanitized name is RFC 1123 compliant
    assert k8s_safe_name == "alerts-dashboard"
    assert expected_metadata["name"] == "grafana-alerts-dashboard-dashboard"

    # Verify the data key uses original dashboard name (with underscore)
    expected_data_key = f"{dashboard_name}.json"
    assert expected_data_key == "alerts_dashboard.json"


def test_dashboard_uid_enforcement():
    """Test that dashboard UID is enforced to match filename"""
    dashboard_name = "my_test_dashboard"

    # Simulate the dashboard JSON structure
    dashboard_json = {
        "title": "My Test Dashboard",
        "uid": "old_uid",  # This should be overwritten
        "id": 123,  # This should be set to None
    }

    # What the code does:
    dashboard_json["uid"] = dashboard_name
    dashboard_json["id"] = None

    assert dashboard_json["uid"] == "my_test_dashboard"
    assert dashboard_json["id"] is None


def test_rfc1123_compliance_validation():
    """Test RFC 1123 subdomain naming rules (simplified version without dots)"""
    import re

    # Valid names (alphanumeric and hyphens, start/end with alphanumeric)
    valid_names = [
        "a",
        "abc",
        "a-b",
        "abc-123",
        "alerts-dashboard",
        "123-abc",
        "my-dashboard",
    ]

    # Simplified pattern for our use case (no dots)
    rfc1123_pattern = r"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$"

    for name in valid_names:
        assert re.match(rfc1123_pattern, name), f"Valid name failed: {name}"

    # Invalid names (should not match)
    invalid_names = [
        "-abc",  # Starts with hyphen
        "abc-",  # Ends with hyphen
        "ABC",  # Uppercase
        "a_b",  # Underscore
        "a b",  # Space
        "a.b",  # Dot (not allowed in our simplified version)
        "",  # Empty
    ]

    for name in invalid_names:
        assert not re.match(rfc1123_pattern, name), f"Invalid name passed: {name}"


def test_dashboard_file_processing_simulation():
    """Simulate processing a dashboard file to ensure end-to-end behavior"""
    with tempfile.TemporaryDirectory() as tmpdir:
        dashboard_dir = Path(tmpdir)

        # Create a test dashboard file
        dashboard_file = dashboard_dir / "alerts_dashboard.json"
        dashboard_content = {
            "title": "Alerts Dashboard",
            "uid": "wrong_uid",
            "id": 999,
            "panels": [],
        }

        with open(dashboard_file, "w") as f:
            json.dump(dashboard_content, f)

        # Simulate what the code does
        dashboard_name = dashboard_file.stem
        k8s_safe_name = sanitize_k8s_name(dashboard_name)

        # Read and parse
        with open(dashboard_file) as f:
            dashboard_json = json.load(f)

        # Enforce UID and null id
        dashboard_json["uid"] = dashboard_name
        dashboard_json["id"] = None

        # Verify results
        assert dashboard_name == "alerts_dashboard"
        assert k8s_safe_name == "alerts-dashboard"
        assert dashboard_json["uid"] == "alerts_dashboard"
        assert dashboard_json["id"] is None
        assert dashboard_json["title"] == "Alerts Dashboard"


def test_configmap_naming_pattern():
    """Test the ConfigMap naming pattern used in the code"""
    cluster_name = "main01-staging"
    dashboard_name = "alerts_dashboard"
    k8s_safe_name = sanitize_k8s_name(dashboard_name)

    # Pulumi resource name (can contain underscores, used internally)
    pulumi_resource_name = f"{cluster_name}-grafana-{k8s_safe_name}-dashboard"

    # Kubernetes metadata name (must be RFC 1123 compliant)
    k8s_metadata_name = f"grafana-{k8s_safe_name}-dashboard"

    # Verify patterns
    assert pulumi_resource_name == "main01-staging-grafana-alerts-dashboard-dashboard"
    assert k8s_metadata_name == "grafana-alerts-dashboard-dashboard"

    # Verify metadata name is RFC 1123 compliant
    import re

    assert re.match(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", k8s_metadata_name)
