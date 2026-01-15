from __future__ import annotations

import os
import pathlib
import subprocess

HERE = pathlib.Path(__file__).absolute().parent


def top() -> pathlib.Path:
    if "PTD_TOP" in os.environ:
        return pathlib.Path(os.environ["PTD_TOP"])

    return pathlib.Path(
        subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],  # noqa: S607
            text=True,
            capture_output=True,
            check=False,
        ).stdout.strip()
    )


def alerts() -> pathlib.Path:
    return top() / "python-pulumi" / "src" / "ptd" / "grafana_alerts"


class Paths:
    @property
    def root(self) -> pathlib.Path:
        """Return the targets configuration directory.

        This should always be set via the PTD_ROOT environment variable
        by the Go CLI when invoking Python Pulumi stacks.

        Raises:
            RuntimeError: If PTD_ROOT is not set in the environment

        """
        if "PTD_ROOT" not in os.environ:
            msg = "PTD_ROOT environment variable not set."
            raise RuntimeError(msg)

        return pathlib.Path(os.environ["PTD_ROOT"])

    @property
    def cache(self) -> pathlib.Path:
        if "PTD_CACHE" in os.environ:
            return pathlib.Path(os.environ["PTD_CACHE"])

        return top() / ".local"

    @property
    def control_rooms(self) -> pathlib.Path:
        return self.root / "__ctrl__"

    @property
    def workloads(self) -> pathlib.Path:
        return self.root / "__work__"
