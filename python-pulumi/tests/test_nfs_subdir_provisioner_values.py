"""Tests for NFS subdir provisioner Helm values structure."""

import yaml


def _build_nfs_values(fsx_dns_name: str, fsx_nfs_path: str = "/fsx") -> dict:
    """Build the NFS provisioner values dict (mirrors _define_nfs_subdir_provisioner)."""
    return {
        "nfs": {
            "server": fsx_dns_name,
            "path": fsx_nfs_path,
            "mountOptions": [
                "nfsvers=4.2",
                "rsize=1048576",
                "wsize=1048576",
                "timeo=600",
            ],
        },
        "storageClass": {
            "name": "posit-shared-storage",
            "reclaimPolicy": "Retain",
            "accessModes": "ReadWriteMany",
            "onDelete": "retain",
            "pathPattern": "${.PVC.annotations.nfs.io/storage-path}",
        },
    }


def test_mount_options_nested_under_nfs():
    """mountOptions must be nested under nfs, not a top-level dot-notation key."""
    values = _build_nfs_values("fs-12345.fsx.us-east-1.amazonaws.com")
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
    values = _build_nfs_values(dns, path)
    assert values["nfs"]["server"] == dns
    assert values["nfs"]["path"] == path


def test_values_yaml_roundtrip():
    """Verify the structure survives a yaml.dump/yaml.safe_load round-trip."""
    values = _build_nfs_values("fs-abc.fsx.us-east-1.amazonaws.com")
    parsed = yaml.safe_load(yaml.dump(values))
    assert parsed["nfs"]["mountOptions"] == [
        "nfsvers=4.2",
        "rsize=1048576",
        "wsize=1048576",
        "timeo=600",
    ]
    assert "nfs.mountOptions" not in parsed
