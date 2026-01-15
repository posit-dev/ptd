from __future__ import annotations

import base64
import hashlib
import json
import os
import secrets
import sys
import traceback
import typing

import click

MAX_IPS = 40


def print_steps(steps: list[tuple[str, typing.Any]]):
    click.secho(
        "∙ " + ("\n∙ ".join([name for name, _ in steps])) + "\n\n",
        fg="white",
        bold=True,
    )


def filter_steps_after_start(start_at_step: str, steps: list[tuple[str, typing.Any]]) -> list[tuple[str, typing.Any]]:
    if len(steps) == 0:
        return steps

    return steps[[name for (name, step) in steps].index(start_at_step) :]


def generate_random_string(length: int) -> str:
    return base64.b64encode(secrets.token_bytes(length), b"-_").decode()


def json_signature(obj: typing.Any) -> str:
    return hashlib.sha256(
        json.dumps(obj, sort_keys=True).encode(),
        usedforsecurity=False,
    ).hexdigest()


def octet_signature(s: str) -> int:
    return sum([ord(c) for c in list(s)]) % 255


def ecr_repository_url(aws_account_id: str, repo_name: str, region: str = "us-east-2") -> str:
    return f"{aws_account_id}.dkr.ecr.{region}.amazonaws.com/{repo_name}"


def ecr_repository_arn(aws_account_id: str, repo_name: str, region: str = "us-east-2") -> str:
    return f"arn:aws:ecr:{region}:{aws_account_id}:repository/{repo_name}"


def import_string(import_name: str) -> typing.Any:
    """This function in borrowed and modified from werkzeug.utils.import_string"""
    try:
        try:
            __import__(import_name)
        except ImportError:
            if "." not in import_name:
                raise
        else:
            return sys.modules[import_name]

        module_name, obj_name = import_name.rsplit(":", 1)
        module = __import__(module_name, globals(), locals(), [obj_name])

        return getattr(module, obj_name, None)

    except ImportError:
        if os.environ.get("PTD_IMPORT_STRING_DEBUG") == "1":
            traceback.print_exc(file=sys.stdout)

    return None
