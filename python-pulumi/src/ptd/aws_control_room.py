from __future__ import annotations

import dataclasses
import os
import typing

import yaml

import ptd
import ptd.paths
import ptd.secrecy
import ptd.shext

if typing.TYPE_CHECKING:
    import pathlib


@dataclasses.dataclass(frozen=True)
class TrustedUserIpAddress:
    """Represents an IP address for a trusted user."""

    ip: str
    comment: str = ""


@dataclasses.dataclass(frozen=True)
class TrustedUser:
    """Represents a trusted user with contact info and IP addresses."""

    email: str
    given_name: str
    family_name: str
    ip_addresses: list[TrustedUserIpAddress] = dataclasses.field(default_factory=list)


@dataclasses.dataclass(frozen=True)
class AWSControlRoomConfig:
    account_id: str
    domain: str
    environment: str
    true_name: str

    power_user_arn: str | None = None
    db_allocated_storage: int = 100
    db_engine_version: str = "16.4"
    db_instance_class: str = "db.t3.small"
    eks_k8s_version: str | None = "1.30"
    eks_node_group_max: int = 3
    eks_node_group_min: int = 3
    eks_node_instance_type: str = "m6a.xlarge"
    hosted_zone_id: str | None = None
    manage_ecr_repositories: bool = True
    protect_persistent_resources: bool = True
    region: str = "us-east-2"
    resource_tags: dict[str, str] = dataclasses.field(default_factory=dict)
    traefik_deployment_replicas: int = 3
    trusted_users: list[TrustedUser] = dataclasses.field(default_factory=list)

    # static domain configured with an SSO app, e.g. "cr.ptd.posit.it"
    front_door: str | None = None

    aws_fsx_openzfs_csi_version: str = "1.1.0"
    aws_lbc_version: str = "1.6.0"
    external_dns_version: str = "1.14.4"
    grafana_version: str = "7.0.14"
    kube_state_metrics_version: str = "5.16.0"
    metrics_server_version: str = "3.11.0"
    mimir_version: str = "5.1.3"
    secret_store_csi_aws_provider_version: str = "0.3.5"  # noqa: S105
    secret_store_csi_version: str = "1.3.4"  # noqa: S105
    tailscale_enabled: bool = True
    tigera_operator_version: str = "3.27.2"
    traefik_forward_auth_version: str = "0.0.14"
    traefik_version: str = "24.0.0"
    ebs_csi_addon_version: str = "v1.41.0-eksbuild.1"


class AWSControlRoom:
    d: pathlib.Path
    cfg: AWSControlRoomConfig

    def __init__(self, name: str, paths: ptd.paths.Paths | None = None):
        self.d = (paths or ptd.paths.Paths()).control_rooms / name

        if not self.ptd_yaml.exists():
            return

        self.load()

    @property
    def has_config(self) -> bool:
        return hasattr(self, "cfg") and self.cfg.account_id is not None and self.cfg.account_id != ""

    @property
    def ptd_yaml(self) -> pathlib.Path:
        return self.d / "ptd.yaml"

    @property
    def kubeconfig_path(self) -> pathlib.Path:
        return self.d / "cluster" / "kubeconfig"

    @property
    def required_tags(self) -> dict[str, str]:
        return (self.cfg.resource_tags or {}) | {
            str(ptd.TagKeys.POSIT_TEAM_TRUE_NAME): self.cfg.true_name,
            str(ptd.TagKeys.POSIT_TEAM_ENVIRONMENT): self.cfg.environment,
        }

    def workloads_index(self) -> dict[str, dict[str, str]]:
        all_dirs = [d for d in ptd.paths.Paths().workloads.iterdir() if d.is_dir()]
        workloads = {
            d.name: yaml.safe_load((d / "ptd.yaml").read_text()) for d in all_dirs if (d / "ptd.yaml").exists()
        }

        return {
            name: cfg_dict
            for name, cfg_dict in workloads.items()
            if cfg_dict.get("spec", {}).get("control_room_account_id", "") == self.cfg.account_id
        }

    def aws_assume_role(self, aws_region: str | None = None) -> ptd.AWSSession:
        return ptd.aws_assume_control_room_role(
            account_id=self.cfg.account_id,
            region=(aws_region or self.cfg.region),
        )

    @property
    def state_bucket(self) -> str:
        return f"ptd-{self.compound_name}"

    @property
    def state_backend_url(self) -> str:
        return f"s3://{self.state_bucket}?region={self.cfg.region}"

    @property
    def secrets_provider_url(self) -> str:
        return f"awskms://{ptd.MGMT_KMS_KEY_ALIAS}?region={self.cfg.region}"

    @property
    def compound_name(self) -> str:
        return f"{self.cfg.true_name}-{self.cfg.environment}"

    @property
    def vault_name(self) -> str:
        return f"{self.compound_name}.ctrl.posit.team"

    @property
    def mimir_auth_secret(self) -> str:
        return f"{self.compound_name}.mimir-auth.posit.team"

    @property
    def mimir_ruler_storage_bucket_prefix(self) -> str:
        return f"{self.compound_name}-mrs-"

    def load(self) -> None:
        true_name, environment = self.d.name.split("-", maxsplit=1)

        spec: dict[str, typing.Any] = {
            "true_name": true_name,
            "environment": environment,
        }

        cfg_dict = yaml.safe_load(self.ptd_yaml.read_text())

        if cfg_dict["kind"] != "AWSControlRoomConfig" or cfg_dict["apiVersion"] != "posit.team/v1":
            msg = (
                f"mismatched control room config kind={cfg_dict['kind']!r} "
                f"apiVersion={cfg_dict['apiVersion']!r} in {str(self.ptd_yaml)!r}"
            )
            raise ValueError(msg)

        spec = cfg_dict["spec"] | spec

        # Parse trusted_users field
        trusted_users_raw = spec.get("trusted_users", [])
        spec["trusted_users"] = [
            TrustedUser(
                email=h["email"],
                given_name=h["given_name"],
                family_name=h["family_name"],
                ip_addresses=[
                    TrustedUserIpAddress(ip=ip["ip"], comment=ip.get("comment", "")) for ip in h.get("ip_addresses", [])
                ],
            )
            for h in trusted_users_raw
        ]

        self.cfg = AWSControlRoomConfig(**spec)

    @property
    def pulumi_config(self) -> dict[str, str | list[str]]:
        return {
            "aws:allowedAccountIds": [str(self.cfg.account_id)],
            "aws:region": self.cfg.region,
        }

    @property
    def exe_env(self) -> dict[str, str]:
        env = os.environ.copy()

        env.setdefault("AWS_REGION", self.cfg.region)
        env.setdefault("KUBECONFIG", str(self.kubeconfig_path))

        return env
