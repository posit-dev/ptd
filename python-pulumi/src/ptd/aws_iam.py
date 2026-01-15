from __future__ import annotations

import json
import typing

import pulumi
import pulumi_aws as aws

import ptd.shext
from ptd import EKS_URL_REGEX, OIDC_ARN_URL_REGEX

POSIT_TEAM_IAM_PERMISSIONS_BOUNDARY = "PositTeamDedicatedAdmin"


def get_role_name_for_permission_set(permission_set: str):
    """
    :param permission_set: The name of the permission set, eg: PowerUser or User
    :return: the arn of the role requested as a string
    """
    names = aws.iam.get_roles(
        name_regex=f".*_{permission_set}_.*",
        path_prefix="/aws-reserved/sso.amazonaws.com/",
    ).names
    return names[0] if names else None


def get_role_arn_for_permission_set(permission_set: str):
    """
    Get the full ARN for an AWS SSO permission set role, including the path.

    :param permission_set: The name of the permission set, eg: PowerUser or User
    :return: the full ARN of the role including path, or None if not found
    """
    role_name = get_role_name_for_permission_set(permission_set)
    if not role_name:
        return None

    # Get the full role details to retrieve the ARN with path
    role = aws.iam.get_role(name=role_name)
    return role.arn


def get_power_user_enhancements_statement() -> aws.iam.GetPolicyDocumentStatementArgs:
    # TODO: Investigate a statement that allows iam:CreateUser for user names of the form "ses-smtp-user-{region}"

    return aws.iam.GetPolicyDocumentStatementArgs(
        sid="IAMAdditions",
        actions=[
            "iam:AddClientIDToOpenIDConnectProvider",
            "iam:AddRoleToInstanceProfile",
            "iam:AttachRolePolicy",
            "iam:CreateInstanceProfile",
            "iam:CreateOpenIDConnectProvider",
            "iam:CreatePolicy",
            "iam:CreatePolicyVersion",
            "iam:CreateRole",
            "iam:CreateSAMLProvider",
            "iam:DeleteInstanceProfile",
            "iam:DeleteOpenIDConnectProvider",
            "iam:DeletePolicy",
            "iam:DeletePolicyVersion",
            "iam:DeleteRole",
            "iam:DeleteRolePolicy",
            "iam:DetachRolePolicy",
            "iam:DeleteSAMLProvider",
            "iam:GetInstanceProfile",
            "iam:GetOpenIDConnectProvider",
            "iam:GetPolicy",
            "iam:GetPolicyVersion",
            "iam:GetRole",
            "iam:GetRolePolicy",
            "iam:ListAttachedRolePolicies",
            "iam:ListEntitiesForPolicy",
            "iam:ListInstanceProfiles",
            "iam:ListInstanceProfilesForRole",
            "iam:ListPolicies",
            "iam:ListPoliciesGrantingServiceAccess",
            "iam:ListPolicyVersions",
            "iam:ListRolePolicies",
            "iam:ListRoles",
            "iam:ListServerCertificates",
            "iam:PassRole",
            "iam:PutRolePolicy",
            "iam:RemoveClientIDFromOpenIDConnectProvider",
            "iam:RemoveRoleFromInstanceProfile",
            "iam:SimulateCustomPolicy",
            "iam:SimulatePrincipalPolicy",
            "iam:Tag*",
            "iam:Untag*",
            "iam:UpdateAssumeRolePolicy",
            "iam:UpdateOpenIDConnectProviderThumbprint",
            "iam:UpdateRole",
            "iam:UpdateRoleDescription",
            "iam:UpdateSAMLProvider",
            "organizations:DescribeEffectivePolicy",
            "tag:GetResources",
            "tag:TagResources",
            "tag:UnTagResources",
        ],
        resources=["*"],
    )


def build_irsa_role_assume_role_policy(
    namespace: str,
    managed_account_id: str,
    oidc_url_tails: list[str],
    service_accounts: list[str],
) -> dict[str, typing.Any]:
    return {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Action": "sts:AssumeRoleWithWebIdentity",
                "Effect": "Allow",
                "Principal": {
                    "Federated": f"arn:aws:iam::{managed_account_id}:oidc-provider/{oidc_url_tail}",
                },
                "Condition": {
                    "StringEquals": {
                        f"{oidc_url_tail}:aud": "sts.amazonaws.com",
                    }
                    | {
                        f"{oidc_url_tail}:sub": [
                            f"system:serviceaccount:{namespace}:{account}" for account in service_accounts
                        ],
                    }
                },
            }
            for oidc_url_tail in oidc_url_tails
        ],
    }


class AuthIssuer(typing.TypedDict):
    issuer: str
    client_id: str | list[str]
    emails: typing.NotRequired[list[str]]
    subs: typing.NotRequired[list[str]]


# TODO: if this experiment works out well... perhaps we can consolidate with the above...
def build_hybrid_irsa_role_assume_role_policy(
    namespace: str,
    managed_account_id: str,
    oidc_url_tails: list[str],
    service_accounts: list[str] | None = None,
    auth_issuers: list[AuthIssuer] | None = None,
) -> dict[str, typing.Any]:
    if auth_issuers is None:
        auth_issuers = []
    if service_accounts is None:
        service_accounts = []
    return {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Action": "sts:AssumeRoleWithWebIdentity",
                "Effect": "Allow",
                "Principal": {
                    "Federated": f"arn:aws:iam::{managed_account_id}:oidc-provider/{oidc_url_tail}",
                },
                "Condition": {
                    "StringEquals": {
                        f"{oidc_url_tail}:aud": "sts.amazonaws.com",
                    }
                    | {
                        f"{oidc_url_tail}:sub": [
                            f"system:serviceaccount:{namespace}:{account}" for account in service_accounts
                        ],
                    }
                },
            }
            for oidc_url_tail in oidc_url_tails
        ]
        + [
            {
                "Action": "sts:AssumeRoleWithWebIdentity",
                "Effect": "Allow",
                "Principal": {
                    "Federated": f"arn:aws:iam::{managed_account_id}:oidc-provider/{clean_issuer(auth['issuer'])}"
                },
                "Condition": {
                    "StringEquals": {
                        f"{clean_issuer(auth['issuer'])}:aud": auth["client_id"],
                    }
                    | emails_condition(auth)
                }
                | maybe_stringlike(auth),
            }
            for auth in auth_issuers
        ],
    }


def clean_issuer(url: str | pulumi.Output) -> str | pulumi.Output[str]:
    if isinstance(url, pulumi.Output):
        return url.apply(lambda u: str.replace(u, "https://", ""))
    return str.replace(url, "https://", "")


def emails_condition(auth: AuthIssuer) -> dict[str, str | list[str]]:
    if "emails" in auth and len(auth["emails"]) > 0:
        return {f"{clean_issuer(auth['issuer'])}:email": auth["emails"]}

    return {}


def subs_condition(auth: AuthIssuer) -> dict[str, str | list[str]]:
    if "subs" in auth and len(auth["subs"]) > 0:
        return {f"{clean_issuer(auth['issuer'])}:sub": auth["subs"]}

    return {}


def maybe_stringlike(auth: AuthIssuer) -> dict[str, dict[str, str | list[str]]]:
    if "subs" in auth:
        return {"StringLike": subs_condition(auth)}
    return {}


# TODO: these types are quite tricky to use properly...
#   should we be dealing with this level of specificity?
class AwsPolicyDocumentCondition(typing.TypedDict):
    StringEquals: dict[str, str] | None
    StringLike: dict[str, str] | None


class AwsPolicyDocumentStatementPrincipal(typing.TypedDict):
    Federated: str


class AwsPolicyDocumentStatement(typing.TypedDict):
    Principal: AwsPolicyDocumentStatementPrincipal | None
    Condition: AwsPolicyDocumentCondition | None


class AssumeRolePolicyDocument(typing.TypedDict):
    Statement: AwsPolicyDocumentStatement


class AwsRole(typing.TypedDict):
    AssumeRolePolicyDocument: AssumeRolePolicyDocument


# Parse a set of raw AwsRole structs into a dict of "oidc url": ["list of oidc client ids"]
def aws_iam_parse_client_ids_from_federated_roles(
    input_json: typing.Iterable[ptd.aws_iam.AwsRole],
) -> dict[str, list[str]]:
    # use a dict for each oidc_url to ensure uniqueness (i.e. dict of dicts)
    ret: dict[str, typing.Any | dict[str, str]] = {}
    for role in input_json:
        if "AssumeRolePolicyDocument" not in role or "Statement" not in role["AssumeRolePolicyDocument"]:
            continue
        statement: AwsPolicyDocumentStatement = role["AssumeRolePolicyDocument"]["Statement"]
        if "Principal" in statement and "Federated" in statement["Principal"]:
            federated = statement["Principal"]["Federated"]
            match = OIDC_ARN_URL_REGEX.match(federated)

            if match is None:
                continue

            groups = match.groups()
            if len(groups) == 0:
                continue

            oidc_url = groups[0]

            # skip EKS clusters, because they are owned by each workload...
            if EKS_URL_REGEX.match(oidc_url) is not None:
                continue

            oidc_key = f"{oidc_url}:aud"
            # need to have the oidc:aud claim to get the ClientId
            if (
                "Condition" not in statement
                or "StringEquals" not in statement["Condition"]
                or oidc_key not in statement["Condition"]["StringEquals"]
            ):
                continue

            client_id = statement["Condition"]["StringEquals"][oidc_key]
            if oidc_url not in ret:
                ret[oidc_url] = {}

            ret[oidc_url][client_id] = ""

    # reshape the output to a dict of lists
    output: dict[str, list[str]] = {}
    for k in ret:
        output[k] = list(ret[k].keys())

    return output


def aws_iam_roles_federated(exe_env: dict[str, str] | None = None) -> dict[str, list[str]]:
    return aws_iam_parse_client_ids_from_federated_roles(
        json.loads(
            ptd.shext.sh(
                ["aws", "iam", "list-roles", "--output", "json"],
                env=exe_env,
            ).stdout
        ).get("Roles", [])
    )
