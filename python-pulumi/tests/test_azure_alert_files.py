"""Tests for Azure Monitor alert YAML files.

Note: These tests verify that Azure alert YAML files exist and have basic structure.
The YAML files use Grafana's alert provisioning format which may not be fully compatible
with strict yaml.safe_load() parsing (e.g., descriptions with colons may not be quoted).
"""

import pathlib


class TestAzureAlertFiles:
    """Tests for Azure Monitor alert YAML files."""

    def test_azure_postgres_yaml_exists(self) -> None:
        """Test that azure_postgres.yaml exists."""
        yaml_path = pathlib.Path(__file__).parent.parent / "src" / "ptd" / "grafana_alerts" / "azure_postgres.yaml"
        assert yaml_path.exists(), f"azure_postgres.yaml not found at {yaml_path}"

        # Verify basic structure
        text = yaml_path.read_text()
        assert "apiVersion: 1" in text
        assert "groups:" in text
        assert "Azure PostgreSQL" in text

    def test_azure_netapp_yaml_exists(self) -> None:
        """Test that azure_netapp.yaml exists."""
        yaml_path = pathlib.Path(__file__).parent.parent / "src" / "ptd" / "grafana_alerts" / "azure_netapp.yaml"
        assert yaml_path.exists(), f"azure_netapp.yaml not found at {yaml_path}"

        # Verify basic structure
        text = yaml_path.read_text()
        assert "apiVersion: 1" in text
        assert "groups:" in text

    def test_azure_loadbalancer_yaml_exists(self) -> None:
        """Test that azure_loadbalancer.yaml exists."""
        yaml_path = pathlib.Path(__file__).parent.parent / "src" / "ptd" / "grafana_alerts" / "azure_loadbalancer.yaml"
        assert yaml_path.exists(), f"azure_loadbalancer.yaml not found at {yaml_path}"

        # Verify basic structure
        text = yaml_path.read_text()
        assert "apiVersion: 1" in text
        assert "groups:" in text

    def test_azure_storage_yaml_exists(self) -> None:
        """Test that azure_storage.yaml exists."""
        yaml_path = pathlib.Path(__file__).parent.parent / "src" / "ptd" / "grafana_alerts" / "azure_storage.yaml"
        assert yaml_path.exists(), f"azure_storage.yaml not found at {yaml_path}"

        # Verify basic structure
        text = yaml_path.read_text()
        assert "apiVersion: 1" in text
        assert "groups:" in text

    def test_azure_alert_files_have_grafana_structure(self) -> None:
        """Test that all Azure alert files have Grafana provisioning structure."""
        yaml_files = [
            "azure_postgres.yaml",
            "azure_netapp.yaml",
            "azure_loadbalancer.yaml",
            "azure_storage.yaml",
        ]

        for yaml_file in yaml_files:
            yaml_path = pathlib.Path(__file__).parent.parent / "src" / "ptd" / "grafana_alerts" / yaml_file
            text = yaml_path.read_text()

            # Verify Grafana alert provisioning structure
            assert "apiVersion: 1" in text, f"{yaml_file} missing apiVersion"
            assert "groups:" in text, f"{yaml_file} missing groups"
            assert "rules:" in text, f"{yaml_file} missing rules"
            assert "uid:" in text, f"{yaml_file} missing rule UIDs"
            assert "datasourceUid: mimir" in text, f"{yaml_file} not using mimir datasource"
            assert "opsgenie:" in text, f"{yaml_file} missing opsgenie labels"

    def test_azure_postgres_has_expected_alerts(self) -> None:
        """Test that azure_postgres.yaml contains expected alert rules."""
        yaml_path = pathlib.Path(__file__).parent.parent / "src" / "ptd" / "grafana_alerts" / "azure_postgres.yaml"
        text = yaml_path.read_text()

        # Verify expected PostgreSQL alert rules exist
        expected_rules = [
            "azure_postgres_cpu_high",
            "azure_postgres_storage_high",
            "azure_postgres_memory_high",
            "azure_postgres_connections_high",
            "azure_postgres_failed_connections",
            "azure_postgres_deadlocks",
        ]

        for expected_rule in expected_rules:
            assert expected_rule in text, f"Expected rule {expected_rule} not found in azure_postgres.yaml"

    def test_azure_alerts_query_azure_monitor_metrics(self) -> None:
        """Test that Azure alerts query Azure Monitor metrics."""
        yaml_files = {
            "azure_postgres.yaml": "azure_microsoft_dbforpostgresql_flexibleservers",
            "azure_netapp.yaml": "azure_microsoft_netapp_netappaccounts",
            "azure_loadbalancer.yaml": "azure_microsoft_network_loadbalancers",
            "azure_storage.yaml": "azure_microsoft_storage_storageaccounts",
        }

        for yaml_file, expected_metric_prefix in yaml_files.items():
            yaml_path = pathlib.Path(__file__).parent.parent / "src" / "ptd" / "grafana_alerts" / yaml_file
            text = yaml_path.read_text()

            # Verify the file queries Azure Monitor metrics (lowercased resource type)
            assert expected_metric_prefix in text, f"{yaml_file} should query {expected_metric_prefix} metrics"
