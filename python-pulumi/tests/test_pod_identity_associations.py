"""Tests for _define_pod_identity_associations and _define_external_secrets_iam in AWSWorkloadClusters."""

import json
from unittest.mock import MagicMock, patch

from ptd.pulumi_resources.aws_workload_clusters import AWSWorkloadClusters


def _make_clusters_mock(
    releases: list[str],
    sites: list[str],
    enable_pod_identity: bool = True,
    enable_eso: bool = False,
    chronicle_keys: list[str] | None = None,
    home_releases: list[str] | None = None,
    packagemanager_keys: list[str] | None = None,
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

    # chronicle_roles, home_roles, and packagemanager_roles use `in` checks so they must be real dicts
    m.chronicle_roles = {k: MagicMock() for k in (chronicle_keys or [])}
    m.home_roles = {r: MagicMock() for r in (home_releases or [])}
    # Default: populate packagemanager for all release/site combos (the common case)
    if packagemanager_keys is None:
        packagemanager_keys = [f"{r}//{s}" for r in releases for s in sites]
    m.packagemanager_roles = {k: MagicMock() for k in packagemanager_keys}

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


def _make_role_mock(oidc_url_tails: list[str]) -> MagicMock:
    """Build a minimal AWSWorkloadClusters mock for testing _define_k8s_iam_role."""
    m = MagicMock()
    m._oidc_url_tails = oidc_url_tails
    m.workload.cfg.account_id = "123456789012"
    m.workload.iam_permissions_boundary = None
    m.required_tags = {}
    return m


def test_define_k8s_iam_role_trust_policy_includes_pod_identity_statement():
    """With pod_identity=True, the assume_role_policy includes pods.eks.amazonaws.com."""
    m = _make_role_mock(oidc_url_tails=["oidc.eks.us-east-1.amazonaws.com/id/ABCD1234"])
    with (
        patch("ptd.pulumi_resources.aws_workload_clusters.aws.iam.Role"),
        patch("ptd.pulumi_resources.aws_workload_clusters.aws.iam.RoleArgs") as mock_role_args,
    ):
        AWSWorkloadClusters._define_k8s_iam_role(m, name="test-role", pod_identity=True)
        policy = json.loads(mock_role_args.call_args.kwargs["assume_role_policy"])

    services = [
        s.get("Principal", {}).get("Service")
        for s in policy["Statement"]
        if isinstance(s.get("Principal"), dict)
    ]
    assert "pods.eks.amazonaws.com" in services

    pod_stmt = next(s for s in policy["Statement"] if s.get("Principal", {}).get("Service") == "pods.eks.amazonaws.com")
    assert "sts:AssumeRole" in pod_stmt["Action"]
    assert "sts:TagSession" in pod_stmt["Action"]


def test_define_k8s_iam_role_trust_policy_excludes_pod_identity_statement_when_disabled():
    """With pod_identity=False, the assume_role_policy does not include pods.eks.amazonaws.com."""
    m = _make_role_mock(oidc_url_tails=["oidc.eks.us-east-1.amazonaws.com/id/ABCD1234"])
    with (
        patch("ptd.pulumi_resources.aws_workload_clusters.aws.iam.Role"),
        patch("ptd.pulumi_resources.aws_workload_clusters.aws.iam.RoleArgs") as mock_role_args,
    ):
        AWSWorkloadClusters._define_k8s_iam_role(m, name="test-role", pod_identity=False)
        policy = json.loads(mock_role_args.call_args.kwargs["assume_role_policy"])

    services = [
        s.get("Principal", {}).get("Service")
        for s in policy["Statement"]
        if isinstance(s.get("Principal"), dict)
    ]
    assert "pods.eks.amazonaws.com" not in services


def test_define_external_secrets_iam_skipped_when_disabled():
    """When enable_external_secrets_operator=False, no IAM roles are created and external_secrets_roles is empty."""
    m = MagicMock()
    m.managed_clusters_by_release = ["20250328"]
    m.external_secrets_roles = {}
    cluster_cfg = MagicMock()
    cluster_cfg.enable_external_secrets_operator = False
    m.workload.cfg.clusters.__getitem__ = lambda _self, k: cluster_cfg

    AWSWorkloadClusters._define_external_secrets_iam(m)
    # _define_k8s_iam_role is resolved on the mock instance; call_count==0 means it was never called.
    assert m._define_k8s_iam_role.call_count == 0
    assert m.external_secrets_roles == {}


def test_define_external_secrets_iam_creates_role_per_release_when_enabled():
    """When enable_external_secrets_operator=True, one IAM role is created per release."""
    m = MagicMock()
    m.managed_clusters_by_release = ["20250328", "20250415"]
    m.external_secrets_roles = {}
    cluster_cfg = MagicMock()
    cluster_cfg.enable_external_secrets_operator = True
    m.workload.cfg.clusters.__getitem__ = lambda _self, k: cluster_cfg

    AWSWorkloadClusters._define_external_secrets_iam(m)
    assert m._define_k8s_iam_role.call_count == 2
    assert set(m.external_secrets_roles.keys()) == {"20250328", "20250415"}
    for call in m._define_k8s_iam_role.call_args_list:
        assert call.kwargs.get("pod_identity") is True
