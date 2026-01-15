import pathlib
import uuid

import pytest
import yaml

import ptd.aws_workload  # type: ignore
import ptd.shext


def test_managed_cluster_work_dir(tmp_path: pathlib.Path, monkeypatch: pytest.MonkeyPatch):
    test_root = tmp_path / "infra"

    monkeypatch.setenv("PTD_ROOT", str(test_root))
    monkeypatch.setenv("PTD_CACHE", str(tmp_path / ".cache"))

    test_d = test_root / "__work__" / "banana01-staging"
    test_d.mkdir(parents=True, exist_ok=True, mode=0o755)

    with (test_d / "ptd.yaml").open("w") as out:
        yaml.dump(
            {
                "apiVersion": "posit.team/v1",
                "kind": "AWSWorkloadConfig",
                "spec": {
                    "account_id": "123456789012",
                    "fsx_openzfs_storage_capacity": 4,
                    "fsx_openzfs_throughput_capacity": 5,
                    "control_room_account_id": "99999",
                    "control_room_cluster_name": "castletown01",
                    "control_room_domain": "castletown01.control-room.com",
                    "region": "Beleriand",
                    "external_id": "f0c0fbdd-3cbe-4ed2-a67d-95c4b00533b6",
                    "sites": {"main": {"spec": {"domain": "palm-tree-dialectic.real-domain.biz"}}},
                },
            },
            stream=out,
        )

    workload = ptd.aws_workload.AWSWorkload("banana01-staging")

    assert workload is not None
    assert workload.cfg is not None
    assert workload.cfg.true_name == "banana01"
    assert workload.cfg.environment == "staging"
    assert workload.cfg.account_id == "123456789012"
    assert workload.cfg.region == "Beleriand"
    assert workload.cfg.external_id == uuid.UUID("f0c0fbdd-3cbe-4ed2-a67d-95c4b00533b6")
    assert workload.role_arn == "arn:aws:iam::123456789012:role/admin.posit.team"
    assert workload.cfg.domain == "palm-tree-dialectic.real-domain.biz"
    assert workload.cfg.control_room_cluster_name == "castletown01"
    assert workload.cfg.control_room_domain == "castletown01.control-room.com"
    assert workload.cfg.db_multi_az is False, "db_multi_az should be False in staging environment"
    assert workload.vpc_cidr("0").with_prefixlen == "10.225.0.0/16"
    assert workload.labels("0") == {
        "environment": "staging",
        "release": "0",
        "awsAccountID": "123456789012",
        "trueName": "banana01",
        "domain": "palm-tree-dialectic.real-domain.biz",
        "clusterName": "default_banana01-staging-control-plane",
        "region": "Beleriand",
    }
    assert workload.compound_name == "banana01-staging"
    assert workload.fully_qualified_name("0") == "banana01-staging"
    assert workload.fully_qualified_name("20230814") == "banana01-staging-20230814"
