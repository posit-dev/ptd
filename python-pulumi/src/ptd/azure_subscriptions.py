from __future__ import annotations

import ptd.shext


def azure_current_subscription_id(exe_env: dict[str, str] | None = None) -> str:
    return ptd.shext.sh(
        ["az", "account", "show", "--query", "id", "--output", "tsv"],
        env=exe_env,
    ).stdout.strip()
