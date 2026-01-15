import pulumi_aws as aws


def build_secret_arn(account_id: str, name: str, region: str = "us-east-2") -> str:
    # Example: arn:aws:secretsmanager:us-east-2:123456789012:secret:workload-site.sessions.example.com
    return f"arn:aws:secretsmanager:{region}:{account_id}:secret:{name}*"


def define_read_secret_inline(account_id: str, secret_name: str, region: str) -> str:
    return aws.iam.get_policy_document(
        statements=[
            aws.iam.GetPolicyDocumentStatementArgs(
                effect="Allow",
                actions=[
                    "secretsmanager:Get*",
                    "secretsmanager:Describe*",
                    "secretsmanager:ListSecrets",
                ],
                resources=[build_secret_arn(account_id, secret_name, region)],
            )
        ]
    ).json
