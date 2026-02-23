import dataclasses
from pathlib import Path
from unittest.mock import Mock

import pytest
import yaml

from ptd.pulumi_resources.grafana_alloy import AlloyConfig, _validate_alloy_true_name


class TestValidateAlloyTrueName:
    def test_valid_names(self) -> None:
        _validate_alloy_true_name("myapp")
        _validate_alloy_true_name("my-app")
        _validate_alloy_true_name("my.app.v2")
        _validate_alloy_true_name("app_name")
        _validate_alloy_true_name("myapp-production")
        _validate_alloy_true_name("a1b2c3")

    def test_double_quote_rejected(self) -> None:
        with pytest.raises(ValueError, match="unsafe for Alloy River config"):
            _validate_alloy_true_name('bad"name')

    def test_open_brace_rejected(self) -> None:
        with pytest.raises(ValueError, match="unsafe for Alloy River config"):
            _validate_alloy_true_name("bad{name}")

    def test_close_brace_rejected(self) -> None:
        with pytest.raises(ValueError, match="unsafe for Alloy River config"):
            _validate_alloy_true_name("bad}name")

    def test_space_rejected(self) -> None:
        with pytest.raises(ValueError, match="unsafe for Alloy River config"):
            _validate_alloy_true_name("bad name")

    def test_empty_string_rejected(self) -> None:
        with pytest.raises(ValueError, match="unsafe for Alloy River config"):
            _validate_alloy_true_name("")


@dataclasses.dataclass
class MockSiteConfig:
    """Mock SiteConfig for testing."""

    domain: str
    domain_type: str = ""
    use_traefik_forward_auth: bool = False


def create_mock_workload(sites: dict[str, MockSiteConfig], site_yaml_content: dict[str, dict | None] | None):
    """Helper function to create a mock workload with specified sites.

    Args:
        sites: Dictionary mapping site names to MockSiteConfig objects
        site_yaml_content: Dictionary mapping site names to their YAML content (or None if file doesn't exist)
    """
    mock_workload = Mock()
    mock_cfg = Mock()
    mock_cfg.sites = sites
    mock_workload.cfg = mock_cfg

    # Mock cloud_provider
    mock_cloud_provider = Mock()
    mock_cloud_provider.name = "AWS"
    mock_workload.cloud_provider = mock_cloud_provider

    # Mock site_yaml method to return paths that might or might not exist
    def mock_site_yaml(site_name: str):
        mock_path = Mock(spec=Path)

        if site_yaml_content and site_name in site_yaml_content:
            content = site_yaml_content[site_name]
            if content is None:
                # File doesn't exist
                mock_path.exists.return_value = False
            else:
                # File exists and has content
                mock_path.exists.return_value = True
                # Convert dict to YAML string

                yaml_str = yaml.safe_dump(content)
                mock_path.read_text.return_value = yaml_str
        else:
            # Default: file doesn't exist
            mock_path.exists.return_value = False

        return mock_path

    mock_workload.site_yaml = mock_site_yaml

    return mock_workload


class TestDefineBlackboxTargets:
    """Tests for the _define_blackbox_targets method of AlloyConfig."""

    def test_fqdn_enabled_default(self):
        """Test that FQDN health checks are included by default."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload with FQDN health checks enabled (default behavior)
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                        "workbench": {"domainPrefix": "workbench"},
                        "connect": {"domainPrefix": "connect"},
                        "packageManager": {"domainPrefix": "packagemanager"},
                    }
                }
            },
        )

        # Create AlloyConfig instance (we'll access the private method directly for testing)
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: Should contain both internal and FQDN checks
        # Internal checks
        assert 'name = "test-site-workbench"' in result
        assert 'address = "http://test-site-workbench.posit-team.svc.cluster.local/health-check"' in result
        assert '"check_type" = "internal"' in result

        assert 'name = "test-site-connect"' in result
        assert 'address = "http://test-site-connect.posit-team.svc.cluster.local/__ping__"' in result

        assert 'name = "test-site-packagemanager"' in result
        assert 'address = "http://test-site-packagemanager.posit-team.svc.cluster.local/__ping__"' in result

        # FQDN checks
        assert 'name = "test-site-workbench-fqdn"' in result
        assert 'address = "https://workbench.example.com/health-check"' in result
        assert '"check_type" = "fqdn"' in result

        assert 'name = "test-site-connect-fqdn"' in result
        assert 'address = "https://connect.example.com/__ping__"' in result

        assert 'name = "test-site-packagemanager-fqdn"' in result
        assert 'address = "https://packagemanager.example.com/__ping__"' in result

    def test_fqdn_disabled(self):
        """Test that FQDN health checks are excluded when disabled."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload with FQDN health checks disabled
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": False,
                    }
                }
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: Should contain only internal checks, no FQDN checks
        # Internal checks should be present
        assert 'name = "test-site-workbench"' in result
        assert 'address = "http://test-site-workbench.posit-team.svc.cluster.local/health-check"' in result
        assert '"check_type" = "internal"' in result

        assert 'name = "test-site-connect"' in result
        assert 'address = "http://test-site-connect.posit-team.svc.cluster.local/__ping__"' in result

        assert 'name = "test-site-packagemanager"' in result
        assert 'address = "http://test-site-packagemanager.posit-team.svc.cluster.local/__ping__"' in result

        # FQDN checks should NOT be present
        assert 'name = "test-site-workbench-fqdn"' not in result
        assert 'name = "test-site-connect-fqdn"' not in result
        assert 'name = "test-site-packagemanager-fqdn"' not in result
        assert 'address = "https://workbench.example.com' not in result
        assert 'address = "https://connect.example.com' not in result
        assert 'address = "https://packagemanager.example.com' not in result

    def test_fqdn_enabled_no_yaml_file(self):
        """Test that FQDN health checks are included when YAML file doesn't exist (default behavior)."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload where site YAML doesn't exist (will default to enabled)
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": None  # Simulate file doesn't exist
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: Should include FQDN checks with default domain prefixes
        assert 'name = "test-site-workbench-fqdn"' in result
        assert 'address = "https://workbench.example.com/health-check"' in result  # default prefix

        assert 'name = "test-site-connect-fqdn"' in result
        assert 'address = "https://connect.example.com/__ping__"' in result  # default prefix

        assert 'name = "test-site-packagemanager-fqdn"' in result
        assert 'address = "https://packagemanager.example.com/__ping__"' in result  # default prefix

    def test_custom_domain_prefix(self):
        """Test that custom domain prefixes from YAML are used correctly."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload with custom domain prefixes
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                        "workbench": {"domainPrefix": "custom-wb"},
                        "connect": {"domainPrefix": "custom-rsc"},
                        "packageManager": {"domainPrefix": "custom-pm"},
                    }
                }
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: Should use custom prefixes
        assert 'address = "https://custom-wb.example.com/health-check"' in result
        assert 'address = "https://custom-rsc.example.com/__ping__"' in result
        assert 'address = "https://custom-pm.example.com/__ping__"' in result

    def test_multiple_sites(self):
        """Test that targets are generated correctly for multiple sites."""
        # Setup
        sites = {
            "site-one": MockSiteConfig(domain="one.example.com"),
            "site-two": MockSiteConfig(domain="two.example.com"),
        }

        # Create a mock workload with different FQDN settings per site
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "site-one": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                    }
                },
                "site-two": {
                    "spec": {
                        "enableFqdnHealthChecks": False,
                    }
                },
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify site-one has both internal and FQDN checks
        assert 'name = "site-one-workbench"' in result
        assert 'name = "site-one-workbench-fqdn"' in result
        assert 'address = "https://workbench.one.example.com/health-check"' in result

        # Verify site-two has only internal checks
        assert 'name = "site-two-workbench"' in result
        assert 'name = "site-two-workbench-fqdn"' not in result
        assert 'address = "https://workbench.two.example.com' not in result

    def test_output_format(self):
        """Test that the output format is valid HCL/Alloy configuration."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                    }
                }
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify basic HCL structure
        assert result.startswith("target {")
        assert result.endswith("}")

        # Count target blocks (should be 6: 3 internal + 3 fqdn)
        target_count = result.count("target {")
        assert target_count == 6

        # Verify all targets have required fields
        assert result.count("name =") == 6
        assert result.count("address =") == 6
        assert result.count("module =") == 6
        assert result.count("labels =") == 6


class TestReplicasHandling:
    """Tests for handling components with 0 replicas."""

    def test_component_with_zero_replicas_no_health_checks(self):
        """Test that a component with 0 replicas generates no health checks."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload with workbench set to 0 replicas
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                        "workbench": {"replicas": 0},
                    }
                }
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: workbench health checks should NOT be present (neither internal nor FQDN)
        assert 'name = "test-site-workbench"' not in result
        assert 'name = "test-site-workbench-fqdn"' not in result
        assert "test-site-workbench.posit-team.svc.cluster.local" not in result
        assert "workbench.example.com" not in result

        # Verify: connect and packagemanager should still have health checks (default 1 replica)
        assert 'name = "test-site-connect"' in result
        assert 'name = "test-site-connect-fqdn"' in result
        assert 'name = "test-site-packagemanager"' in result
        assert 'name = "test-site-packagemanager-fqdn"' in result

    def test_all_components_with_zero_replicas(self):
        """Test that no health checks are generated when all components have 0 replicas."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload with all components set to 0 replicas
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                        "workbench": {"replicas": 0},
                        "connect": {"replicas": 0},
                        "packageManager": {"replicas": 0},
                    }
                }
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: result should be empty (no health checks generated)
        assert result == ""

    def test_mixed_replica_counts(self):
        """Test that health checks are only generated for components with non-zero replicas."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload with mixed replica counts
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                        "workbench": {"replicas": 1},
                        "connect": {"replicas": 0},
                        "packageManager": {"replicas": 2},
                    }
                }
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: workbench should have health checks (1 replica)
        assert 'name = "test-site-workbench"' in result
        assert 'name = "test-site-workbench-fqdn"' in result

        # Verify: connect should NOT have health checks (0 replicas)
        assert 'name = "test-site-connect"' not in result
        assert 'name = "test-site-connect-fqdn"' not in result
        assert "test-site-connect.posit-team.svc.cluster.local" not in result
        assert "connect.example.com" not in result

        # Verify: packagemanager should have health checks (2 replicas)
        assert 'name = "test-site-packagemanager"' in result
        assert 'name = "test-site-packagemanager-fqdn"' in result

    def test_default_replica_count_when_not_specified(self):
        """Test that components default to 1 replica when not specified in YAML."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload with no replicas specified (should default to 1)
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                        # No replicas specified for any component
                    }
                }
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: all components should have health checks (default 1 replica)
        assert 'name = "test-site-workbench"' in result
        assert 'name = "test-site-workbench-fqdn"' in result
        assert 'name = "test-site-connect"' in result
        assert 'name = "test-site-connect-fqdn"' in result
        assert 'name = "test-site-packagemanager"' in result
        assert 'name = "test-site-packagemanager-fqdn"' in result

    def test_zero_replicas_with_fqdn_disabled(self):
        """Test that components with 0 replicas generate no checks even with FQDN disabled."""
        # Setup
        sites = {
            "test-site": MockSiteConfig(domain="example.com"),
        }

        # Create a mock workload with 0 replicas and FQDN disabled
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "test-site": {
                    "spec": {
                        "enableFqdnHealthChecks": False,
                        "workbench": {"replicas": 0},
                        "connect": {"replicas": 1},
                    }
                }
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify: workbench should have NO health checks (0 replicas)
        assert 'name = "test-site-workbench"' not in result
        assert 'name = "test-site-workbench-fqdn"' not in result

        # Verify: connect should have internal check only (1 replica, FQDN disabled)
        assert 'name = "test-site-connect"' in result
        assert 'name = "test-site-connect-fqdn"' not in result

    def test_multiple_sites_with_different_replica_counts(self):
        """Test that replica counts are handled correctly per site."""
        # Setup
        sites = {
            "site-one": MockSiteConfig(domain="one.example.com"),
            "site-two": MockSiteConfig(domain="two.example.com"),
        }

        # Create a mock workload with different replica counts per site
        mock_workload = create_mock_workload(
            sites,
            site_yaml_content={
                "site-one": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                        "workbench": {"replicas": 0},
                        "connect": {"replicas": 1},
                    }
                },
                "site-two": {
                    "spec": {
                        "enableFqdnHealthChecks": True,
                        "workbench": {"replicas": 1},
                        "connect": {"replicas": 0},
                    }
                },
            },
        )

        # Create AlloyConfig instance
        alloy = AlloyConfig.__new__(AlloyConfig)
        alloy.workload = mock_workload

        # Execute
        result = alloy._define_blackbox_targets()  # noqa: SLF001

        # Verify site-one: no workbench checks, but has connect checks
        assert 'name = "site-one-workbench"' not in result
        assert 'name = "site-one-workbench-fqdn"' not in result
        assert 'name = "site-one-connect"' in result
        assert 'name = "site-one-connect-fqdn"' in result

        # Verify site-two: has workbench checks, no connect checks
        assert 'name = "site-two-workbench"' in result
        assert 'name = "site-two-workbench-fqdn"' in result
        assert 'name = "site-two-connect"' not in result
        assert 'name = "site-two-connect-fqdn"' not in result
