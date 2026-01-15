from __future__ import annotations

import os


def aws_current_account_id(exe_env: dict[str, str] | None = None) -> str:
    """Get AWS account ID based on AWS_PROFILE environment variable.

    Matches profile names ending in standard environment suffixes and looks up
    the corresponding PTD_AWS_ACCOUNT_* environment variable.

    Supported profile suffixes (case-insensitive):
        - "-team" → PTD_AWS_ACCOUNT_TEAM
        - "-staging" → PTD_AWS_ACCOUNT_STAGING
        - "-production" → PTD_AWS_ACCOUNT_PRODUCTION
        - "-lab-staging" → PTD_AWS_ACCOUNT_LAB_STAGING
        - "-lab-production" → PTD_AWS_ACCOUNT_LAB_PRODUCTION

    Examples:
        - "mycompany-staging" → PTD_AWS_ACCOUNT_STAGING
        - "acme-corp-production" → PTD_AWS_ACCOUNT_PRODUCTION
        - "ptd-lab-staging" → PTD_AWS_ACCOUNT_LAB_STAGING

    If no match is found or the env var is not set, returns empty string.
    The caller (ptd.aws_current_account_id) falls back to STS GetCallerIdentity.

    Args:
        exe_env: Environment dict to use. Defaults to os.environ.

    Returns:
        AWS account ID string, or empty string if not found.
    """
    exe_env = exe_env or os.environ.copy()

    aws_profile = exe_env.get("AWS_PROFILE", "").lower().strip()

    # Order matters: check longer suffixes first to avoid false matches
    # (e.g., "-lab-staging" before "-staging")
    suffix_to_env_var = (
        ("-lab-production", "PTD_AWS_ACCOUNT_LAB_PRODUCTION"),
        ("-lab-staging", "PTD_AWS_ACCOUNT_LAB_STAGING"),
        ("-production", "PTD_AWS_ACCOUNT_PRODUCTION"),
        ("-staging", "PTD_AWS_ACCOUNT_STAGING"),
        ("-team", "PTD_AWS_ACCOUNT_TEAM"),
    )

    for suffix, env_var in suffix_to_env_var:
        if aws_profile.endswith(suffix):
            return exe_env.get(env_var, "")

    return ""
