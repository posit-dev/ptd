"""Shared pytest fixtures for PTD Python Pulumi tests.

This module provides common fixtures used across test files:
- ptd_root: Sets PTD_ROOT environment variable
- pulumi_mocks: Standard Pulumi mock class for resource tests
- aws_workload: Mock AWSWorkload with sensible defaults
- azure_workload: Mock AzureWorkload with sensible defaults
"""

import pathlib
import subprocess
import sys
import typing

import pulumi
import pytest

import ptd
import ptd.aws_workload
import ptd.azure_workload

HERE = (
    pathlib.Path(
        subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],  # noqa: S607
            encoding="utf-8",
            capture_output=True,
            check=True,
        ).stdout.strip()
    )
    / "ptd"
)

sys.path.insert(0, str(HERE / "src"))


# ============================================================================
# Environment Setup Fixtures
# ============================================================================


@pytest.fixture
def ptd_root(monkeypatch: pytest.MonkeyPatch, tmp_path: pathlib.Path) -> pathlib.Path:
    """Set PTD_ROOT environment variable to a temporary directory.

    This fixture is required for any test that loads workload configs or uses
    the Paths class. It sets PTD_ROOT to a temporary directory that is cleaned
    up after the test completes.

    Usage:
        def test_something(ptd_root):
            # PTD_ROOT is automatically set
            paths = Paths()
            assert paths.root == ptd_root
    """
    monkeypatch.setenv("PTD_ROOT", str(tmp_path))
    return tmp_path


# ============================================================================
# Pulumi Mock Fixtures
# ============================================================================


class StandardPulumiMocks(pulumi.runtime.Mocks):
    """Standard Pulumi mocks for testing Pulumi resources.

    This mock class implements the minimal interface required by Pulumi for
    testing. It returns resource names as IDs and echoes back all inputs as
    outputs.

    Use this when testing components that create Pulumi resources but you don't
    need to verify specific resource properties.
    """

    def new_resource(self, args: pulumi.runtime.MockResourceArgs) -> tuple[str | None, dict[typing.Any, typing.Any]]:
        """Mock resource creation - returns resource name as ID and inputs as outputs."""
        return args.name, dict(args.inputs)

    def call(
        self, args: pulumi.runtime.MockCallArgs
    ) -> dict[typing.Any, typing.Any] | tuple[dict[typing.Any, typing.Any], list[tuple[str, str]] | None]:
        """Mock function calls - returns empty dict."""
        return {}


@pytest.fixture
def pulumi_mocks() -> type[pulumi.runtime.Mocks]:
    """Returns the standard Pulumi mocks class.

    This fixture provides a standard Pulumi mock class that can be used with
    pulumi.runtime.set_mocks(). The mocks are not automatically set - you must
    call set_mocks() in your test or at module level.

    Usage:
        @pulumi.runtime.test
        def test_my_resource(pulumi_mocks):
            pulumi.runtime.set_mocks(pulumi_mocks(), preview=False)
            # Now test your Pulumi resources
    """
    return StandardPulumiMocks


# ============================================================================
# Workload Mock Fixtures
# ============================================================================


@pytest.fixture
def aws_workload(monkeypatch: pytest.MonkeyPatch, tmp_path: pathlib.Path) -> ptd.aws_workload.AWSWorkload:
    """Create a mock AWSWorkload with sensible defaults for testing.

    This fixture provides a pre-configured AWSWorkload object with test data.
    PTD_ROOT is automatically set to a temporary directory.

    The fixture creates a workload with directory "testing01-test" with:
    - True name: "testing01"
    - Environment: "test"
    - Region: "useast1"
    - Account: "9001"
    - Single cluster: "19551105"
    - Single site: "main" (AWSSiteConfig) with domain "puppy.party"
    - Test control room configuration
    - FULL network trust

    Usage:
        def test_something(aws_workload):
            # Use aws_workload.cfg to access configuration
            assert aws_workload.cfg.environment == "test"
    """
    # Set PTD_ROOT to a temporary path for testing
    monkeypatch.setenv("PTD_ROOT", str(tmp_path))
    wl = ptd.aws_workload.AWSWorkload(name="testing01-test", paths=None, load_yaml=False)
    wl.cfg = ptd.aws_workload.AWSWorkloadConfig(
        control_room_account_id="242324232423",
        control_room_cluster_name="main01-test",
        control_room_domain="cr.test",
        control_room_region="mp-north-4",
        control_room_role_name="garfield.snooze.zone",
        control_room_state_bucket="cr-test-state-ok-great",
        environment="test",
        network_trust=ptd.NetworkTrust.FULL,
        true_name="testing01",
        account_id="9001",
        region="useast1",
        clusters={
            "19551105": ptd.aws_workload.AWSWorkloadClusterConfig(
                components=ptd.aws_workload.AWSWorkloadClusterComponentConfig(),
            ),
        },
        sites={
            "main": ptd.aws_workload.AWSSiteConfig(domain="puppy.party"),
        },
    )

    return wl


@pytest.fixture
def azure_workload(monkeypatch: pytest.MonkeyPatch, tmp_path: pathlib.Path) -> ptd.azure_workload.AzureWorkload:
    """Create a mock AzureWorkload with sensible defaults for testing.

    This fixture provides a pre-configured AzureWorkload object with test data.
    PTD_ROOT is automatically set to a temporary directory.

    The fixture creates a workload with directory "testing01-test" with:
    - True name: "testing01"
    - Environment: "test"
    - Region: "eastus"
    - Single cluster: "19551105"
    - Single site: "main" (SiteConfig) with domain "puppy.party"
    - Test control room configuration
    - FULL network trust
    - Standard network configuration

    Usage:
        def test_something(azure_workload):
            # Use azure_workload.cfg to access configuration
            assert azure_workload.cfg.environment == "test"
    """
    # Set PTD_ROOT to a temporary path for testing
    monkeypatch.setenv("PTD_ROOT", str(tmp_path))
    wl = ptd.azure_workload.AzureWorkload(name="testing01-test", paths=None, load_yaml=False)
    wl.cfg = ptd.azure_workload.AzureWorkloadConfig(
        control_room_account_id="242324232423",
        control_room_cluster_name="main01-test",
        control_room_domain="cr.test",
        control_room_region="eastus",
        control_room_role_name="garfield.snooze.zone",
        control_room_state_bucket="cr-test-state-ok-great",
        environment="test",
        network_trust=ptd.NetworkTrust.FULL,
        true_name="testing01",
        subscription_id="12345678-1234-1234-1234-123456789012",
        tenant_id="87654321-4321-4321-4321-210987654321",
        client_id="abcdef12-3456-7890-abcd-ef1234567890",
        secrets_provider_client_id="fedcba09-8765-4321-fedc-ba0987654321",
        region="eastus",
        network=ptd.azure_workload.NetworkConfig(
            private_subnet_cidr="10.0.1.0/24",
            db_subnet_cidr="10.0.2.0/24",
            netapp_subnet_cidr="10.0.3.0/24",
            app_gateway_subnet_cidr="10.0.4.0/24",
            bastion_subnet_cidr="10.0.5.0/24",
        ),
        clusters={
            "19551105": ptd.azure_workload.AzureWorkloadClusterConfig(
                components=ptd.azure_workload.AzureWorkloadClusterComponentConfig(),
            ),
        },
        sites={
            "main": ptd.SiteConfig(domain="puppy.party"),
        },
    )

    return wl
