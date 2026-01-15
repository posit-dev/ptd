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
import ptd.oidc
import ptd.paths
import ptd.pulumi_resources
import ptd.pulumi_resources.aws_bastion
import ptd.pulumi_resources.aws_control_room_cluster
import ptd.pulumi_resources.aws_control_room_persistent
import ptd.pulumi_resources.aws_control_room_postgres_config
import ptd.pulumi_resources.aws_eks_cluster
import ptd.pulumi_resources.aws_fsx_openzfs_multi
import ptd.pulumi_resources.aws_vpc
import ptd.pulumi_resources.aws_workload_clusters
import ptd.pulumi_resources.aws_workload_persistent
import ptd.pulumi_resources.aws_workload_postgres_config
import ptd.pulumi_resources.external_dns
import ptd.pulumi_resources.grafana_alloy
import ptd.pulumi_resources.keycloak_operator
import ptd.pulumi_resources.kubernetes_role
import ptd.pulumi_resources.network_policies
import ptd.pulumi_resources.team_operator
import ptd.pulumi_resources.team_site
import ptd.pulumi_resources.traefik
import ptd.pulumi_resources.traefik_forward_auth
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
    assert ptd.oidc is not None
    assert ptd.paths is not None
    assert ptd.pulumi_resources is not None
    assert ptd.pulumi_resources.aws_bastion is not None
    assert ptd.pulumi_resources.aws_control_room_cluster is not None
    assert ptd.pulumi_resources.aws_control_room_persistent is not None
    assert ptd.pulumi_resources.aws_control_room_postgres_config is not None
    assert ptd.pulumi_resources.aws_eks_cluster is not None
    assert ptd.pulumi_resources.aws_fsx_openzfs_multi is not None
    assert ptd.pulumi_resources.aws_vpc is not None
    assert ptd.pulumi_resources.aws_workload_clusters is not None
    assert ptd.pulumi_resources.aws_workload_persistent is not None
    assert ptd.pulumi_resources.aws_workload_postgres_config is not None
    assert ptd.pulumi_resources.external_dns is not None
    assert ptd.pulumi_resources.grafana_alloy is not None
    assert ptd.pulumi_resources.keycloak_operator is not None
    assert ptd.pulumi_resources.kubernetes_role is not None
    assert ptd.pulumi_resources.network_policies is not None
    assert ptd.pulumi_resources.team_operator is not None
    assert ptd.pulumi_resources.team_site is not None
    assert ptd.pulumi_resources.traefik is not None
    assert ptd.pulumi_resources.traefik_forward_auth is not None
    assert ptd.secrecy is not None
    assert ptd.shext is not None
