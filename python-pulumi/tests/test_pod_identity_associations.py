"""Tests for _define_pod_identity_associations in AWSWorkloadClusters."""

from unittest.mock import MagicMock, patch

from ptd.pulumi_resources.aws_workload_clusters import AWSWorkloadClusters


def _make_clusters_mock(
    releases: list[str],
    sites: list[str],
    enable_pod_identity: bool = True,
    enable_eso: bool = False,
    chronicle_keys: list[str] | None = None,
    home_releases: list[str] | None = None,
) -> MagicMock:
    """Build a minimal AWSWorkloadClusters mock for testing _define_pod_identity_associations."""
    m = MagicMock()
    m.managed_clusters_by_release = releases
    m.workload.compound_name = "myworkload"
    m.workload.cfg.sites = {s: MagicMock() for s in sites}

    cluster_cfgs = {}
    for release in releases:
        cfg = MagicMock()
        cfg.enable_pod_identity_agent = enable_pod_identity
        cfg.enable_external_secrets_operator = enable_eso
        cluster_cfgs[release] = cfg
    m.workload.cfg.clusters.__getitem__ = lambda _self, k: cluster_cfgs[k]

    # chronicle_roles and home_roles use `in` checks so they must be real dicts
    m.chronicle_roles = {k: MagicMock() for k in (chronicle_keys or [])}
    m.home_roles = {r: MagicMock() for r in (home_releases or [])}

    return m


def test_no_associations_when_pod_identity_disabled():
    """When enable_pod_identity_agent=False, no PodIdentityAssociation resources are created."""
    mock = _make_clusters_mock(
        releases=["20250328"],
        sites=["siteA", "siteB"],
        enable_pod_identity=False,
    )
    with patch("ptd.pulumi_resources.aws_workload_clusters.aws.eks.PodIdentityAssociation") as mock_pia:
        AWSWorkloadClusters._define_pod_identity_associations(mock)
        assert mock_pia.call_count == 0


def test_associations_count_two_sites_no_optional_products():
    """With 2 sites and no optional products (no ESO, chronicle, home): 2×5 = 10 associations."""
    mock = _make_clusters_mock(
        releases=["20250328"],
        sites=["siteA", "siteB"],
        enable_pod_identity=True,
        enable_eso=False,
    )
    with patch("ptd.pulumi_resources.aws_workload_clusters.aws.eks.PodIdentityAssociation") as mock_pia:
        AWSWorkloadClusters._define_pod_identity_associations(mock)
        # 2 sites × 5 mandatory products (connect, connect-session, workbench, workbench-session, packagemanager)
        assert mock_pia.call_count == 10


def test_associations_count_with_eso():
    """With 2 sites and ESO enabled: 2×5 products + 1 ESO = 11 associations."""
    mock = _make_clusters_mock(
        releases=["20250328"],
        sites=["siteA", "siteB"],
        enable_pod_identity=True,
        enable_eso=True,
    )
    with patch("ptd.pulumi_resources.aws_workload_clusters.aws.eks.PodIdentityAssociation") as mock_pia:
        AWSWorkloadClusters._define_pod_identity_associations(mock)
        assert mock_pia.call_count == 11  # 2×5 + 1 ESO


def test_chronicle_association_created_only_when_role_present():
    """Chronicle PodIdentityAssociation is only created when the role key exists in chronicle_roles."""
    release = "20250328"
    mock_with_chronicle = _make_clusters_mock(
        releases=[release],
        sites=["siteA"],
        enable_pod_identity=True,
        chronicle_keys=[f"{release}-siteA"],
    )
    mock_without_chronicle = _make_clusters_mock(
        releases=[release],
        sites=["siteA"],
        enable_pod_identity=True,
        chronicle_keys=[],
    )
    with patch("ptd.pulumi_resources.aws_workload_clusters.aws.eks.PodIdentityAssociation") as mock_pia:
        AWSWorkloadClusters._define_pod_identity_associations(mock_with_chronicle)
        assert mock_pia.call_count == 6  # 5 mandatory + 1 chronicle
        names_called = [c[0][0] for c in mock_pia.call_args_list]
        assert any("chronicle" in n for n in names_called)

    with patch("ptd.pulumi_resources.aws_workload_clusters.aws.eks.PodIdentityAssociation") as mock_pia:
        AWSWorkloadClusters._define_pod_identity_associations(mock_without_chronicle)
        assert mock_pia.call_count == 5  # 5 mandatory, no chronicle


def test_home_association_created_per_site_when_role_present():
    """Home PodIdentityAssociation is created once per site when release key is in home_roles."""
    release = "20250328"
    mock = _make_clusters_mock(
        releases=[release],
        sites=["siteA", "siteB"],
        enable_pod_identity=True,
        home_releases=[release],
    )
    with patch("ptd.pulumi_resources.aws_workload_clusters.aws.eks.PodIdentityAssociation") as mock_pia:
        AWSWorkloadClusters._define_pod_identity_associations(mock)
        # 2 sites × (5 mandatory + 1 home) = 12
        assert mock_pia.call_count == 12
        names_called = [c[0][0] for c in mock_pia.call_args_list]
        assert sum(1 for n in names_called if "home" in n) == 2  # one per site
