"""Tests demonstrating the use of shared fixtures from conftest.py.

This file serves as both a test of the fixtures themselves and documentation
of how to use them in your own tests.
"""

import pathlib
import typing

import pulumi
import pulumi_aws as aws
import pytest

import ptd.aws_workload
import ptd.azure_workload
from ptd.paths import Paths


# ============================================================================
# Tests for ptd_root fixture
# ============================================================================


def test_ptd_root_fixture_sets_environment(ptd_root: pathlib.Path) -> None:
    """Test that ptd_root fixture sets PTD_ROOT environment variable."""
    paths = Paths()
    assert paths.root == ptd_root


def test_ptd_root_is_temporary_directory(ptd_root: pathlib.Path) -> None:
    """Test that ptd_root provides a temporary directory that can be written to."""
    test_file = ptd_root / "test.txt"
    test_file.write_text("test content")

    assert test_file.exists()
    assert test_file.read_text() == "test content"


# ============================================================================
# Tests for pulumi_mocks fixture
# ============================================================================


@pulumi.runtime.test
def test_pulumi_mocks_fixture(pulumi_mocks: type[pulumi.runtime.Mocks]) -> None:
    """Test that pulumi_mocks fixture provides a working mock class."""
    pulumi.runtime.set_mocks(pulumi_mocks(), preview=False)

    # Create a simple resource
    bucket = aws.s3.Bucket("test-bucket")

    def check_bucket(bucket_id: str) -> None:
        assert bucket_id == "test-bucket"

    bucket.id.apply(check_bucket)


# ============================================================================
# Tests for aws_workload fixture
# ============================================================================


def test_aws_workload_fixture_provides_workload(aws_workload: ptd.aws_workload.AWSWorkload) -> None:
    """Test that aws_workload fixture provides a configured AWSWorkload."""
    assert aws_workload.cfg.environment == "test"
    assert aws_workload.cfg.region == "useast1"
    assert aws_workload.cfg.account_id == "9001"
    assert aws_workload.cfg.true_name == "testing01"


def test_aws_workload_has_cluster(aws_workload: ptd.aws_workload.AWSWorkload) -> None:
    """Test that aws_workload includes a default cluster."""
    assert "19551105" in aws_workload.cfg.clusters
    cluster = aws_workload.cfg.clusters["19551105"]
    assert cluster.components is not None


def test_aws_workload_has_site(aws_workload: ptd.aws_workload.AWSWorkload) -> None:
    """Test that aws_workload includes a default site."""
    assert "main" in aws_workload.cfg.sites
    site = aws_workload.cfg.sites["main"]
    assert site.domain == "puppy.party"


def test_aws_workload_sets_ptd_root(aws_workload: ptd.aws_workload.AWSWorkload) -> None:
    """Test that aws_workload fixture also sets PTD_ROOT."""
    paths = Paths()
    # Should not raise RuntimeError
    assert paths.root is not None


# ============================================================================
# Tests for azure_workload fixture
# ============================================================================


def test_azure_workload_fixture_provides_workload(azure_workload: ptd.azure_workload.AzureWorkload) -> None:
    """Test that azure_workload fixture provides a configured AzureWorkload."""
    assert azure_workload.cfg.environment == "test"
    assert azure_workload.cfg.region == "eastus"
    assert azure_workload.cfg.subscription_id == "12345678-1234-1234-1234-123456789012"
    assert azure_workload.cfg.true_name == "testing01"


def test_azure_workload_has_cluster(azure_workload: ptd.azure_workload.AzureWorkload) -> None:
    """Test that azure_workload includes a default cluster."""
    assert "19551105" in azure_workload.cfg.clusters
    cluster = azure_workload.cfg.clusters["19551105"]
    assert cluster.components is not None


def test_azure_workload_has_site(azure_workload: ptd.azure_workload.AzureWorkload) -> None:
    """Test that azure_workload includes a default site."""
    assert "main" in azure_workload.cfg.sites
    site = azure_workload.cfg.sites["main"]
    assert site.domain == "puppy.party"


def test_azure_workload_has_network_config(azure_workload: ptd.azure_workload.AzureWorkload) -> None:
    """Test that azure_workload includes network configuration."""
    network = azure_workload.cfg.network
    assert network.private_subnet_cidr == "10.0.1.0/24"
    assert network.db_subnet_cidr == "10.0.2.0/24"


def test_azure_workload_sets_ptd_root(azure_workload: ptd.azure_workload.AzureWorkload) -> None:
    """Test that azure_workload fixture also sets PTD_ROOT."""
    paths = Paths()
    # Should not raise RuntimeError
    assert paths.root is not None


# ============================================================================
# Demonstration of combining fixtures
# ============================================================================


def test_combining_ptd_root_and_aws_workload(
    ptd_root: pathlib.Path, aws_workload: ptd.aws_workload.AWSWorkload
) -> None:
    """Demonstrate using multiple fixtures together.

    Note: Both fixtures set PTD_ROOT, but that's okay - they use monkeypatch
    which is scoped to the test function.
    """
    # Can use ptd_root directly
    test_file = ptd_root / "config.yaml"
    test_file.write_text("test: value")

    # Can also use aws_workload
    assert aws_workload.cfg.environment == "test"

    # And verify PTD_ROOT is set correctly
    paths = Paths()
    assert paths.root == ptd_root
