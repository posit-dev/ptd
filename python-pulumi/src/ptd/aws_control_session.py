from __future__ import annotations

import dataclasses
import functools
import json
import os
import typing

import yaml

import ptd
import ptd.aws_accounts
import ptd.aws_control_room
import ptd.aws_workload
import ptd.junkdrawer
import ptd.paths
import ptd.secrecy
import ptd.shext

if typing.TYPE_CHECKING:
    import ipaddress
    import pathlib


@dataclasses.dataclass(frozen=True)
class AWSControlSessionConfig:
    cluster_name: str
    state_bucket: str
    kubeconfig: pathlib.Path
    region: str
    role_name: str
    workload: ptd.aws_workload.AWSWorkload


class AWSControlSession:
    cfg: AWSControlSessionConfig
    control_room: ptd.aws_control_room.AWSControlRoom

    def __init__(self, cfg: AWSControlSessionConfig):
        self.cfg = cfg
        self.control_room = ptd.aws_control_room.AWSControlRoom(self.wlcfg.control_room_cluster_name)

    @property
    def wlcfg(self) -> ptd.aws_workload.AWSWorkloadConfig:
        return self.cfg.workload.cfg

    @property
    def crcfg(self) -> ptd.aws_control_room.AWSControlRoomConfig:
        return self.control_room.cfg

    @property
    def state_backend_url(self) -> str:
        return f"s3://{self.state_bucket}?region={self.cfg.region}"

    @property
    def state_bucket(self) -> str:
        return self.cfg.state_bucket if self.cfg.state_bucket != "" else f"ptd-{self.cfg.cluster_name}"

    @property
    def secrets_provider_url(self) -> str:
        return f"awskms://{ptd.MGMT_KMS_KEY_ALIAS}?region={self.cfg.region}"

    @property
    def mimir_auth_secret(self) -> str:
        return f"{self.wlcfg.control_room_cluster_name}.mimir-auth.posit.team"

    @functools.cached_property
    def ecr_registry_hostname(self) -> str:
        current_account_id = ptd.aws_current_account_id(exe_env=self.exe_env)

        return f"{current_account_id}.dkr.ecr.{self.cfg.region}.amazonaws.com"

    @property
    def exe_env(self) -> dict[str, str]:
        env = os.environ.copy()

        env.setdefault("KUBECONFIG", str(self.cfg.kubeconfig))
        env.setdefault("AWS_REGION", self.cfg.region)

        env.setdefault(
            "PTD_CONTROL_ROOM_CFG",
            json.dumps(
                {
                    "cluster_name": self.cfg.cluster_name,
                    "kubeconfig": str(self.cfg.kubeconfig.absolute()),
                    "region": self.cfg.region,
                    "role_name": self.cfg.role_name,
                    "workload": {"compound_name": self.cfg.workload.compound_name},
                },
                sort_keys=True,
            ),
        )

        return env

    def assume_workload_role(self) -> ptd.AWSSession:
        return self.cfg.workload.aws_assume_role(self.wlcfg.region)

    def assume_control_room_role(self) -> ptd.AWSSession:
        current_account_id = ptd.aws_current_account_id(exe_env=self.exe_env)

        return ptd.aws_assume_control_room_role(
            account_id=current_account_id,
            region=self.cfg.region,
            role_name=self.cfg.role_name,
            exe_env=self.exe_env,
        )

    def generate_kubeconfig(self, *, assume_role: bool = False) -> str:
        exe_env = self.exe_env
        if assume_role:
            exe_env = self.control_room_role_env()

        kubeconfig = yaml.safe_load(
            ptd.aws_eks_kubeconfig(
                self.cfg.cluster_name,
                exe_env,
                region=self.cfg.region,
            )
        )
        for user in kubeconfig.get("users", []):
            if "exec" not in user["user"]:
                continue

            # NOTE: The env vars are intentionally omitted so that they do not interfere
            # with the `aws eks get-token` command.
            user["user"]["exec"]["env"] = None

        return json.dumps(kubeconfig)

    def pulumi_env(self) -> dict[str, str]:
        env = self.exe_env
        env.setdefault(
            "PULUMI_BACKEND_URL",
            self.state_backend_url,
        )

        return env

    def control_room_role_env(self) -> dict[str, str]:
        env = self.exe_env
        env.setdefault(
            "PULUMI_BACKEND_URL",
            self.state_backend_url,
        )

        return (
            env
            | ptd.aws_env_from_session_credentials(self.assume_control_room_role()["Credentials"])
            | {"AWS_REGION": self.cfg.region}
        )

    def workload_role_env(self) -> dict[str, str]:
        return self.cfg.workload.role_env()

    def persistent_vpc_cidr(self) -> ipaddress.IPv4Network:
        return list(self.cfg.workload.vpc_cidr("0").supernet().subnets(1))[-1]
