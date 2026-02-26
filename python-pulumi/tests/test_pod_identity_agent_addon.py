"""Tests for AWSEKSCluster.with_pod_identity_agent."""

from unittest.mock import MagicMock, patch

from ptd.pulumi_resources.aws_eks_cluster import AWSEKSCluster


def _make_cluster_mock(name: str = "my-cluster") -> MagicMock:
    """Build a minimal AWSEKSCluster mock for testing with_pod_identity_agent."""
    m = MagicMock(spec=AWSEKSCluster)
    m.name = name
    m.eks = MagicMock()
    m.eks.tags = {"env": "test"}
    return m


def test_addon_name_is_eks_pod_identity_agent():
    """with_pod_identity_agent creates an addon named 'eks-pod-identity-agent'."""
    mock = _make_cluster_mock()
    with patch("ptd.pulumi_resources.aws_eks_cluster.aws.eks.Addon") as mock_addon:
        AWSEKSCluster.with_pod_identity_agent(mock)
        assert mock_addon.call_count == 1
        _, kwargs = mock_addon.call_args
        assert kwargs["args"].addon_name == "eks-pod-identity-agent"


def test_version_none_passes_addon_version_none():
    """When version=None, addon_version=None is passed (installs latest)."""
    mock = _make_cluster_mock()
    with patch("ptd.pulumi_resources.aws_eks_cluster.aws.eks.Addon") as mock_addon:
        AWSEKSCluster.with_pod_identity_agent(mock, version=None)
        _, kwargs = mock_addon.call_args
        assert kwargs["args"].addon_version is None


def test_explicit_version_is_passed_through():
    """When a version string is provided, it is passed as addon_version."""
    mock = _make_cluster_mock()
    with patch("ptd.pulumi_resources.aws_eks_cluster.aws.eks.Addon") as mock_addon:
        AWSEKSCluster.with_pod_identity_agent(mock, version="v1.3.3-eksbuild.1")
        _, kwargs = mock_addon.call_args
        assert kwargs["args"].addon_version == "v1.3.3-eksbuild.1"


def test_parent_is_set_to_eks():
    """The addon's parent is set to self.eks."""
    mock = _make_cluster_mock()
    with patch("ptd.pulumi_resources.aws_eks_cluster.aws.eks.Addon") as mock_addon:
        with patch("ptd.pulumi_resources.aws_eks_cluster.pulumi.ResourceOptions") as mock_opts:
            AWSEKSCluster.with_pod_identity_agent(mock)
            mock_opts.assert_called_once_with(parent=mock.eks)


def test_cluster_name_matches_self_name():
    """The addon's cluster_name is set to self.name."""
    mock = _make_cluster_mock(name="test-cluster-20250328")
    with patch("ptd.pulumi_resources.aws_eks_cluster.aws.eks.Addon") as mock_addon:
        AWSEKSCluster.with_pod_identity_agent(mock)
        _, kwargs = mock_addon.call_args
        assert kwargs["args"].cluster_name == "test-cluster-20250328"


def test_returns_self():
    """with_pod_identity_agent returns self for chaining."""
    mock = _make_cluster_mock()
    with patch("ptd.pulumi_resources.aws_eks_cluster.aws.eks.Addon"):
        result = AWSEKSCluster.with_pod_identity_agent(mock)
        assert result is mock
