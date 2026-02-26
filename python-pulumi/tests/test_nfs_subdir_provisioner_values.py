"""Tests for NFS subdir provisioner Helm values structure."""

import pytest
import yaml
from unittest.mock import MagicMock, patch

from ptd.pulumi_resources.aws_workload_helm import (
    _nfs_subdir_provisioner_values,
    AWSWorkloadHelm,
)


def test_mount_options_nested_under_nfs():
    """mountOptions must be nested under nfs, not a top-level dot-notation key."""
    values = _nfs_subdir_provisioner_values("fs-12345.fsx.us-east-1.amazonaws.com")
    assert "nfs.mountOptions" not in values, "nfs.mountOptions must not be a top-level key"
    assert "mountOptions" in values["nfs"], "mountOptions must be nested under nfs"
    assert values["nfs"]["mountOptions"] == [
        "nfsvers=4.2",
        "rsize=1048576",
        "wsize=1048576",
        "timeo=600",
    ]


def test_nfs_server_and_path_set():
    dns = "fs-12345.fsx.us-east-1.amazonaws.com"
    path = "/my-fsx"
    values = _nfs_subdir_provisioner_values(dns, path)
    assert values["nfs"]["server"] == dns
    assert values["nfs"]["path"] == path


def test_nfs_default_path():
    values = _nfs_subdir_provisioner_values("fs-123.fsx.us-east-1.amazonaws.com")
    assert values["nfs"]["path"] == "/fsx"


def test_values_yaml_roundtrip():
    """Verify the structure survives a yaml.dump/yaml.safe_load round-trip."""
    values = _nfs_subdir_provisioner_values("fs-abc.fsx.us-east-1.amazonaws.com")
    parsed = yaml.safe_load(yaml.dump(values))
    assert parsed["nfs"]["mountOptions"] == [
        "nfsvers=4.2",
        "rsize=1048576",
        "wsize=1048576",
        "timeo=600",
    ]
    assert "nfs.mountOptions" not in parsed


def _make_helm_mock(secret_name: str = "my-workload-secret") -> MagicMock:
    """Return a minimal mock that satisfies _define_nfs_subdir_provisioner's self usage."""
    helm = MagicMock()
    helm.workload.secret_name = secret_name
    helm.workload.cfg.region = "us-east-1"
    return helm


def test_nfs_provisioner_warns_on_dry_run_when_secret_fetch_fails():
    """When secret fetch fails during a dry run, warn and return without raising."""
    with patch("ptd.secrecy.aws_get_secret_value_json", return_value=({}, False)):
        with patch("pulumi.runtime.is_dry_run", return_value=True):
            with patch("pulumi.warn") as mock_warn:
                AWSWorkloadHelm._define_nfs_subdir_provisioner(_make_helm_mock(), "20250328", "4.0.18")
                assert mock_warn.called
                assert "fs-dns-name" in mock_warn.call_args[0][0]


def test_nfs_provisioner_raises_on_live_run_when_secret_fetch_fails():
    """When secret fetch fails on a live deploy, raise ValueError."""
    with patch("ptd.secrecy.aws_get_secret_value_json", return_value=({}, False)):
        with patch("pulumi.runtime.is_dry_run", return_value=False):
            with pytest.raises(ValueError, match="fs-dns-name"):
                AWSWorkloadHelm._define_nfs_subdir_provisioner(_make_helm_mock(), "20250328", "4.0.18")


def test_nfs_provisioner_raises_on_live_run_when_key_missing():
    """When fs-dns-name key is absent on a live deploy, raise ValueError."""
    with patch("ptd.secrecy.aws_get_secret_value_json", return_value=({"other-key": "value"}, True)):
        with patch("pulumi.runtime.is_dry_run", return_value=False):
            with pytest.raises(ValueError, match="fs-dns-name"):
                AWSWorkloadHelm._define_nfs_subdir_provisioner(_make_helm_mock(), "20250328", "4.0.18")
