from __future__ import annotations

import dataclasses
import functools
import json
import os
import typing

import click

import ptd.aws_control_room
import ptd.azure_secrecy
import ptd.azure_workload
import ptd.junkdrawer
import ptd.paths
import ptd.secrecy
import ptd.shext

if typing.TYPE_CHECKING:
    import pathlib


@dataclasses.dataclass(frozen=True)
class AzureControlSessionConfig:
    cluster_name: str
    state_bucket: str
    kubeconfig: pathlib.Path
    region: str
    role_name: str
    workload: ptd.azure_workload.AzureWorkload


class AzureControlSession:
    cfg: AzureControlSessionConfig
    control_room: ptd.aws_control_room.AWSControlRoom

    def __init__(self, cfg: AzureControlSessionConfig):
        self.cfg = cfg
        self.control_room = ptd.aws_control_room.AWSControlRoom(self.wlcfg.control_room_cluster_name)

    @property
    def exe_env(self) -> dict[str, str]:
        env = os.environ.copy()
        env.setdefault("KUBECONFIG", str(self.cfg.kubeconfig))
        env.setdefault("SUBSCRIPTION_ID", self.wlcfg.subscription_id)
        return env

    @property
    def wlcfg(self) -> ptd.azure_workload.AzureWorkloadConfig:
        return self.cfg.workload.cfg

    @functools.cached_property
    def ecr_registry_hostname(self) -> str:
        current_account_id = ptd.aws_current_account_id(exe_env=self.exe_env)

        return f"{current_account_id}.dkr.ecr.{self.cfg.region}.amazonaws.com"

    def workload_role_env(self) -> dict[str, str]:
        return self.cfg.workload.role_env()

    def _ensure_ad_service_principal(self, common_tags: dict[str, str]) -> bool:
        click.secho("Checking current Azure AD Service Principals", bold=True)
        cur_res = ptd.shext.sh(
            [
                "az",
                "ad",
                "sp",
                "list",
                "--display-name",
                str(ptd.Roles.POSIT_TEAM_ADMIN),
                "--output",
                "json",
            ]
        )

        if len(json.loads(cur_res.stdout)) == 1:
            click.secho(
                f"Azure AD Service Principal for {ptd.Roles.POSIT_TEAM_ADMIN} exists",
                fg="green",
                bold=True,
            )

            return True

        # TODO: this command will have to have been run by customers to give us initial access
        res = ptd.shext.sh(
            (
                [
                    "az",
                    "ad",
                    "sp",
                    "create-for-rbac",
                    "--name",
                    ptd.Roles.POSIT_TEAM_ADMIN,
                    "--role",
                    "contributor",
                    "--scopes",
                    f"/subscriptions/{self.wlcfg.subscription_id}",
                    "--output",
                    "json",
                    "--tags",
                ]
                + [f"{k}={v}" for k, v in common_tags.items()]
            ),
            check=False,
        )

        if res.returncode != 0:
            print(res.stderr)
            res.check_returncode()

        ad_sp = json.loads(res.stdout)

        sec_res = ptd.secrecy.aws_ensure_secret(f"{self.cfg.workload.compound_name}.azureadsp.posit.team", ad_sp)

        click.secho(
            f"Azure AD Service Principal for {ptd.Roles.POSIT_TEAM_ADMIN} created",
            fg="green",
            bold=True,
        )
        click.secho(ptd.secrecy.format_secret_result(sec_res), fg="green")

        return True
