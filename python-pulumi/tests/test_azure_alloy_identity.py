"""Tests for Azure Alloy monitoring identity creation."""

import inspect

import ptd.pulumi_resources.azure_workload_helm


class TestAzureAlloyMonitoringIdentity:
    """Tests for the _define_alloy_monitoring_identity method on AzureWorkloadHelm."""

    def test_define_alloy_monitoring_identity_method_exists(self) -> None:
        """Test that AzureWorkloadHelm has _define_alloy_monitoring_identity method."""
        assert hasattr(
            ptd.pulumi_resources.azure_workload_helm.AzureWorkloadHelm,
            "_define_alloy_monitoring_identity",
        ), "AzureWorkloadHelm should have _define_alloy_monitoring_identity method"

    def test_define_alloy_monitoring_identity_is_callable(self) -> None:
        """Test that _define_alloy_monitoring_identity is callable."""
        method = ptd.pulumi_resources.azure_workload_helm.AzureWorkloadHelm._define_alloy_monitoring_identity  # noqa: SLF001
        assert callable(method), "_define_alloy_monitoring_identity should be callable"

    def test_define_alloy_monitoring_identity_signature(self) -> None:
        """Test that _define_alloy_monitoring_identity has expected signature."""
        method = ptd.pulumi_resources.azure_workload_helm.AzureWorkloadHelm._define_alloy_monitoring_identity  # noqa: SLF001
        sig = inspect.signature(method)

        # Verify it takes self and release parameters
        params = list(sig.parameters.keys())
        assert "self" in params, "_define_alloy_monitoring_identity should have self parameter"
        assert "release" in params, "_define_alloy_monitoring_identity should have release parameter"

        # Verify release parameter is a string
        release_param = sig.parameters["release"]
        assert release_param.annotation in {str, inspect.Parameter.empty}, (
            "_define_alloy_monitoring_identity release parameter should be a string"
        )
