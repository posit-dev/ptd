import typing

import pulumi

import ptd.aws_workload
import ptd.pulumi_resources.aws_vpc


class AWSVpcMocks(pulumi.runtime.Mocks):
    def new_resource(self, args: pulumi.runtime.MockResourceArgs) -> tuple[str | None, dict[typing.Any, typing.Any]]:
        _ = args
        return None, {}

    def call(  # type: ignore
        self, args: pulumi.runtime.MockCallArgs
    ) -> dict[typing.Any, typing.Any] | tuple[dict[typing.Any, typing.Any], list[tuple[str, str]] | None]:
        _ = args
        return {}


pulumi.runtime.set_mocks(AWSVpcMocks(), preview=False)


@pulumi.runtime.test
def test_define_aws_vpc() -> None:
    vpc = ptd.pulumi_resources.aws_vpc.AWSVpc(
        "bologna01", "10.10.0.0/16", ["mpn2-az4", "mpn2-az1"], {"posit.team/purpose": "testing"}
    )

    assert vpc is not None
    assert vpc.name == "bologna01"
    assert vpc.tags is not None
    assert vpc.azs is not None
    assert vpc.subnet_cidr_blocks is not None
    assert vpc.vpc is not None
