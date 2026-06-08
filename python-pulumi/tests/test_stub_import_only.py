import ptd
import ptd.aws_accounts
import ptd.aws_auth_user
import ptd.aws_control_room
import ptd.aws_control_session
import ptd.aws_iam
import ptd.aws_workload
import ptd.azure_control_session
import ptd.azure_subscriptions
import ptd.azure_workload
import ptd.paths
import ptd.pulumi_resources
import ptd.pulumi_resources.aws_bastion
import ptd.pulumi_resources.aws_fsx_openzfs_multi
import ptd.secrecy
import ptd.shext


def test_import_only() -> None:
    assert ptd is not None
    assert ptd.aws_accounts is not None
    assert ptd.aws_auth_user is not None
    assert ptd.aws_control_room is not None
    assert ptd.aws_control_session is not None
    assert ptd.aws_iam is not None
    assert ptd.aws_workload is not None
    assert ptd.azure_control_session is not None
    assert ptd.azure_subscriptions is not None
    assert ptd.azure_workload is not None
    assert ptd.paths is not None
    assert ptd.pulumi_resources is not None
    assert ptd.pulumi_resources.aws_bastion is not None
    assert ptd.pulumi_resources.aws_fsx_openzfs_multi is not None
    assert ptd.secrecy is not None
    assert ptd.shext is not None
