import pathlib
import typing
import warnings
from abc import ABC, abstractmethod

import deepmerge  # type: ignore
import yaml

import ptd
import ptd.paths


class AbstractWorkload(ABC):
    d: pathlib.Path
    cfg: ptd.WorkloadConfig
    spec: dict[str, typing.Any]

    def __init__(self, name: str, paths: ptd.paths.Paths | None = None, *, load_yaml=True):
        self.d = (paths or ptd.paths.Paths()).workloads / name

        if not load_yaml:
            return

        if not self.ptd_yaml.exists():
            return

        self._load_common_config()
        self.load_unique_config()

    @abstractmethod
    def load_unique_config(self) -> None:
        pass

    @property
    @abstractmethod
    def cloud_provider(self) -> ptd.CloudProvider:
        pass

    @property
    @abstractmethod
    def image_registry_hostname(self) -> str:
        pass

    @property
    @abstractmethod
    def required_tags(self) -> dict[str, str]:
        pass

    @property
    @abstractmethod
    def secret_name(self) -> str:
        pass

    @abstractmethod
    def resolve_image_digest(self, repository: ptd.ComponentImages, tag: str = ptd.LATEST) -> tuple[str, bool]:
        pass

    @abstractmethod
    def site_secret_name(self, site_name: str) -> str:
        pass

    @property
    def ptd_yaml(self) -> pathlib.Path:
        return self.d / "ptd.yaml"

    @property
    def compound_name(self) -> str:
        return f"{self.cfg.true_name}-{self.cfg.environment}"

    @property
    def prefix(self) -> str:
        return "ptd"

    def fully_qualified_name(self, release: str = ptd.ZERO) -> str:
        if release == ptd.ZERO:
            return self.compound_name

        return f"{self.compound_name}-{release}"

    def _load_common_config(self) -> dict[str, typing.Any]:
        true_name, environment = self.d.name.rsplit("-", maxsplit=1)

        if environment not in ptd.Environments:
            msg = f"Environment {environment!r} is not supported"
            raise ValueError(msg)

        spec: dict[str, typing.Any] = {
            "control_room_account_id": "",
            "control_room_cluster_name": "",
            "control_room_domain": "",
            "control_room_region": "",
            "control_room_role_name": None,
            "control_room_state_bucket": None,
            "environment": environment,
            "network_trust": ptd.NetworkTrust.FULL,
            "true_name": true_name,
        }

        cfg_dict = yaml.safe_load(self.ptd_yaml.read_text())

        if "domain" in cfg_dict["spec"]:
            warnings.warn(
                "'spec.domain' found in workload config; this should be at 'spec.sites.main.domain'",
                stacklevel=2,
            )
            cfg_dict["spec"].pop("domain")

        if "hosted_zone_id" in cfg_dict["spec"]:
            warnings.warn(
                "'spec.hosted_zone_id' found in workload config; this should be at 'spec.sites.main.zone_id'",
                stacklevel=2,
            )
            cfg_dict["spec"].pop("hosted_zone_id")

        deepmerge.always_merger.merge(
            spec,
            cfg_dict["spec"],
        )

        if not hasattr(spec["network_trust"], "name"):
            spec["network_trust"] = ptd.NetworkTrust.__members__[str(spec["network_trust"]).upper()]

        self.spec = spec

    def site_yaml(self, site_name: str, filename: str = "site") -> pathlib.Path:
        return self.d / f"site_{site_name}" / f"{filename.rstrip('.yaml')}.yaml"
