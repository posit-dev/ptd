from __future__ import annotations

import enum

import pulumi
import pulumi_aws as aws


class PolicyType(enum.StrEnum):
    READ = "read"
    READ_WRITE = "read_write"


def define_bucket_policy(
    name: str,
    compound_name: str,
    bucket: aws.s3.Bucket,
    policy_name: str,
    policy_type: PolicyType,
    policy_description: str = "",
    prefix_path: str = "/",
    required_tags: dict[str, str] | None = None,
    opts: pulumi.ResourceOptions | None = None,
) -> aws.iam.Policy:
    if required_tags is None:
        required_tags = {}

    if opts is None:
        opts = pulumi.ResourceOptions()

    actual_policy_description: str | pulumi.Output[str] = policy_description

    if policy_type == PolicyType.READ:
        actions = [
            "s3:ListBucket",
            "s3:ListObjects",
            "s3:GetObject",
            "s3:GetObjectTagging",
            "s3:HeadObject",
        ]
        policy_tag = f"{compound_name}-{name}-s3-bucket-read-only-policy"
        if policy_description == "":
            actual_policy_description = bucket.bucket.apply(
                lambda x: f"Posit Team Dedicated policy for {compound_name} to read the {x} S3 bucket"
            )

    elif policy_type == PolicyType.READ_WRITE:
        actions = [
            "s3:AbortMultipartUpload",
            "s3:DeleteObject",
            "s3:GetBucketLocation",
            "s3:GetObject",
            "s3:GetObjectTagging",
            "s3:HeadObject",
            "s3:ListBucket",
            "s3:ListObjects",
            "s3:PutObject",
            "s3:PutObjectTagging",
        ]
        policy_tag = f"{compound_name}-{name}-s3-bucket-policy"
        if policy_description == "":
            actual_policy_description = bucket.bucket.apply(
                lambda x: f"Posit Team Dedicated policy for {compound_name} to read/write the {x} S3 bucket"
            )
    else:
        err_msg = f"unknown policy type: {policy_type}"
        raise ValueError(err_msg)

    resources = [bucket.arn.apply(str)]

    if prefix_path == "/":
        resources += [bucket.arn.apply(lambda arn: str(arn) + "/*")]
    else:
        resources += [
            bucket.arn.apply(lambda arn: str(arn) + "/" + prefix_path.removeprefix("/")),
            bucket.arn.apply(lambda arn: str(arn) + "/" + prefix_path.removeprefix("/").removesuffix("/") + "/*"),
        ]

    policy_doc = aws.iam.get_policy_document(
        statements=[
            aws.iam.GetPolicyDocumentStatementArgs(
                actions=actions,
                resources=resources,  # type: ignore
            ),
        ]
    )

    return aws.iam.Policy(
        policy_name,
        aws.iam.PolicyArgs(
            name=policy_name,
            description=actual_policy_description,
            # NOTE: in testing context the policy_doc.json is None, which is super
            # confusing! Tracing through _how_ this becomes possible is a ridiculous task
            # given all of pulumi's layers of promises and dynamic bits. Wheee.
            policy=policy_doc.json or "",
            tags=required_tags | {"Name": policy_tag},
        ),
        opts=pulumi.ResourceOptions.merge(opts, pulumi.ResourceOptions(parent=bucket)),
    )
