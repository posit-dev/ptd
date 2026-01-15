import azure.core.exceptions

from ptd import azure_sdk, junkdrawer, secrecy


def ensure_secret(
    secret_name: str,
    vault_name: str,
    secret: dict[str, str] | None = None,
) -> secrecy.SecretResult:
    cur_sig = junkdrawer.json_signature(secret)

    res = secrecy.SecretResult(
        secret_id=secret_name,
        signature=cur_sig,
    )

    existing_secret = None
    try:
        existing_secret = azure_sdk.get_secret(secret_name, vault_name)

        if existing_secret is None:
            msg = "current secret value is None (somehow)"
            raise ValueError(msg)

        if junkdrawer.json_signature(existing_secret) == cur_sig:
            res.status = "unchanged"
            res.unchanged = tuple(secret.keys())
            return res
    except azure.core.exceptions.ResourceNotFoundError:
        pass

    azure_sdk.set_secret(secret_name, vault_name, secret)

    if existing_secret is None:
        res.status = "created"
        res.added = tuple(secret.keys())
    else:
        res.status = "updated"
        res.added = tuple([k for k in secret if k not in existing_secret])
        res.removed = tuple([k for k in existing_secret if k not in secret])
        res.unchanged = tuple([k for k in secret if k in existing_secret and secret[k] == existing_secret[k]])
        res.changed = tuple([k for k in secret if k in existing_secret and secret[k] != existing_secret[k]])

    return res
