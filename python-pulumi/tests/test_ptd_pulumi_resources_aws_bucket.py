import typing

import pulumi
import pulumi_aws as aws

import ptd.aws_workload
import ptd.pulumi_resources.aws_bucket


class AWSBucketMocks(pulumi.runtime.Mocks):
    def new_resource(self, args: pulumi.runtime.MockResourceArgs) -> tuple[str | None, dict[typing.Any, typing.Any]]:
        _ = args
        return None, {}

    def call(  # type: ignore
        self, args: pulumi.runtime.MockCallArgs
    ) -> dict[typing.Any, typing.Any] | tuple[dict[typing.Any, typing.Any], list[tuple[str, str]] | None]:
        _ = args
        return {}


pulumi.runtime.set_mocks(AWSBucketMocks(), preview=False)


@pulumi.runtime.test
def test_define_bucket_policy(aws_workload: ptd.aws_workload.AWSWorkload) -> None:
    bucket = aws.s3.Bucket("testymctestface-bucket")
    policy = ptd.pulumi_resources.aws_bucket.define_bucket_policy(
        name="testymctestface-policy",
        compound_name=aws_workload.compound_name,
        bucket=bucket,
        policy_name="testymctestface-policy",
        policy_description="For when you need to dip",
        policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ,
    )

    def check(args):
        policy: aws.iam.Policy = args[0]
        assert policy is not None

    pulumi.Output.all(policy).apply(check)
