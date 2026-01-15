from __future__ import annotations

import functools
import json
import subprocess

sh = functools.partial(
    subprocess.run,
    check=True,
    capture_output=True,
    text=True,
)


def shj(*args, **kwargs):
    return json.loads(sh(*args, **kwargs).stdout)
