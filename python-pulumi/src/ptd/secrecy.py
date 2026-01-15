from __future__ import annotations

import base64
import dataclasses
import json
import sys
import typing

import boto3
from botocore.exceptions import ClientError

import ptd.junkdrawer


@dataclasses.dataclass
class SecretResult:
    secret_id: str
    signature: str
    status: str = "failed"
    added: tuple[str, ...] = ()
    removed: tuple[str, ...] = ()
    changed: tuple[str, ...] = ()
    unchanged: tuple[str, ...] = ()

    @property
    def succeeded(self):
        return self.status != "failed"


def format_secret_result(
    res: SecretResult,
    line_prefix: str = "ptd: ",
) -> str:
    changes = (
        [f"+{n}" for n in sorted(res.added)]
        + [f"~{n}" for n in sorted(res.changed)]
        + [f"-{n}" for n in sorted(res.removed)]
    )

    return line_prefix + (
        f"\n{line_prefix}".join(
            [
                line.strip()
                for line in [
                    f"secret_id={res.secret_id!r} signature={res.signature!r}",
                    " ".join(changes),
                ]
                if line.strip() != ""
            ]
        )
    )


def print_secret_result(
    res: SecretResult,
    out: typing.IO[str] = sys.stdout,
    line_prefix: str = "ptd: ",
) -> None:
    print(
        format_secret_result(res, line_prefix=line_prefix),
        file=out,
    )


class SSHKeyPair(typing.TypedDict):
    privkey: str
    pubkey: str


AWSControlRoomSecret = typing.TypedDict(
    "AWSControlRoomSecret",
    {
        "mimir-password-salt": str,
        "opsgenie-api-key": str,
    },
)


class AWSOidcSecret(typing.TypedDict):
    oidcClientId: str
    oidcClientSecret: str
    signingSecret: str


AWSWorkloadSecret = typing.TypedDict(
    "AWSWorkloadSecret",
    {
        "chronicle-bucket": str,
        "fs-dns-name": str,
        "fs-root-volume-id": str,
        "main-database-id": str,
        "main-database-url": str,
        "packagemanager-bucket": str,
        "mimir-password": str,
    },
)


class AzureWorkloadSecret(typing.TypedDict):
    main_db_fqdn: str


SiteSecret = typing.TypedDict(
    "SiteSecret",
    {
        "dev-admin-token": str,
        "dev-client-secret": str,
        "dev-db-password": str,
        "dev-license": str,
        "dev-user-token": str,
        "home-auth-map": str,
        "pkg-db-password": str,
        "pkg-license": str,
        "pkg-secret-key": str,
        "pub-client-secret": str,
        "pub-db-password": str,
        "pub-license": str,
        "pub-secret-key": str,
    },
)


def aws_ensure_secret(
    secret_id: str,
    cur_secret: dict[str, str] | None = None,
    region: str = "us-east-2",
) -> SecretResult:
    has_update = cur_secret is not None
    cur_secret = cur_secret or {}
    if cur_secret is None:
        msg = "current secret value is None (somehow)"
        raise ValueError(msg)

    cur_sig = ptd.junkdrawer.json_signature(cur_secret)

    res = SecretResult(
        secret_id=secret_id,
        signature=cur_sig,
    )

    client = boto3.client("secretsmanager", region_name=region)

    # Try to create the secret
    try:
        client.create_secret(Name=secret_id, SecretString=json.dumps(cur_secret))
    except ClientError as e:
        error_code = e.response.get("Error", {}).get("Code", "")
        if error_code != "ResourceExistsException":
            # Unexpected error
            return res
    else:
        # Secret was created successfully
        res.status = "created"
        res.unchanged = ()
        res.added = tuple(cur_secret.keys())
        return res

    # Secret already exists, set default status
    res.status = "unchanged"
    res.unchanged = tuple(cur_secret.keys())

    # Get current secret value
    try:
        response = client.get_secret_value(SecretId=secret_id)
        existing_secret = json.loads(response.get("SecretString", "{}"))
    except ClientError:
        # Could not retrieve existing secret
        return res

    if existing_secret is None:
        msg = "current secret value is None (somehow)"
        raise ValueError(msg)

    cur_sig = ptd.junkdrawer.json_signature(existing_secret)

    if has_update:
        prev_secret = cur_secret.copy()

        res.added = tuple([k for k in prev_secret if k not in existing_secret])
        res.removed = tuple([k for k in existing_secret if k not in prev_secret])
        res.unchanged = tuple([k for k in prev_secret if k in existing_secret and prev_secret[k] == existing_secret[k]])
        res.changed = tuple([k for k in prev_secret if k in existing_secret and prev_secret[k] != existing_secret[k]])

        # Merge secrets: start with existing, then apply updates
        cur_secret = existing_secret.copy()
        cur_secret |= prev_secret

        # Remove keys that should be removed
        for key in res.removed:
            cur_secret.pop(key, None)

    new_sig = ptd.junkdrawer.json_signature(cur_secret)
    res.signature = new_sig

    if has_update and cur_sig != new_sig:
        # Update the secret
        try:
            client.put_secret_value(SecretId=secret_id, SecretString=json.dumps(cur_secret))
            res.status = "updated"
        except ClientError:
            # Could not update secret
            pass

    return res


def aws_get_secret_value_json(
    secret_id: str, region: str = "us-east-2"
) -> tuple[AWSWorkloadSecret | AWSControlRoomSecret | SSHKeyPair | dict[str, str], bool]:
    try:
        client = boto3.client("secretsmanager", region_name=region)
        response = client.get_secret_value(SecretId=secret_id)
        secret_string = response.get("SecretString", "{}")
        return json.loads(secret_string), True
    except ClientError:
        return {}, False


def normalize_license(lic: str) -> str:
    lic = lic.strip()

    if not lic.startswith("-----BEGIN RSTUDIO LICENSE-----"):
        lic = base64.b64decode(lic.encode()).decode()

    if lic.startswith("-----BEGIN RSTUDIO LICENSE-----"):
        lic = "".join(lic.splitlines())

    return lic
