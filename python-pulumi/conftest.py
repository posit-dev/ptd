import pathlib
import subprocess
import sys

import pytest

import ptd
import ptd.aws_workload

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


@pytest.fixture
def aws_workload(monkeypatch: pytest.MonkeyPatch, tmp_path: pathlib.Path) -> ptd.aws_workload.AWSWorkload:
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
