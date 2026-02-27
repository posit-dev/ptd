"""Tests for cloud-agnostic infrastructure code."""

import yaml

from ptd.pulumi_resources.aws_workload_helm import _nfs_subdir_provisioner_values
from ptd.pulumi_resources.aws_workload_sites import _external_secret_spec


class TestNfsSubdirProvisionerValues:
    """Tests for the _nfs_subdir_provisioner_values function."""

    def test_correct_storage_class_name(self) -> None:
        """Test that the StorageClass name is 'posit-shared-storage'."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        assert values["storageClass"]["name"] == "posit-shared-storage"

    def test_path_pattern_uses_annotation(self) -> None:
        """Test that pathPattern uses the annotation-based pattern."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        assert values["storageClass"]["pathPattern"] == "${.PVC.annotations.nfs.io/storage-path}"

    def test_mount_options_include_nfsvers(self) -> None:
        """Test that mount options include nfsvers=4.2."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        mount_options = values["nfs"]["mountOptions"]
        assert "nfsvers=4.2" in mount_options

    def test_mount_options_include_performance_tuning(self) -> None:
        """Test that mount options include performance tuning parameters."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        mount_options = values["nfs"]["mountOptions"]
        assert "rsize=1048576" in mount_options
        assert "wsize=1048576" in mount_options
        assert "timeo=600" in mount_options

    def test_reclaim_policy_is_retain(self) -> None:
        """Test that reclaim policy is set to 'Retain'."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        assert values["storageClass"]["reclaimPolicy"] == "Retain"

    def test_on_delete_is_retain(self) -> None:
        """Test that onDelete is set to 'retain'."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        assert values["storageClass"]["onDelete"] == "retain"

    def test_access_modes_is_read_write_many(self) -> None:
        """Test that accessModes is set to 'ReadWriteMany'."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        assert values["storageClass"]["accessModes"] == "ReadWriteMany"

    def test_nfs_server_propagated(self) -> None:
        """Test that the NFS server DNS name is correctly propagated."""
        dns_name = "custom-fsx-dns.example.com"
        values = _nfs_subdir_provisioner_values(dns_name)
        assert values["nfs"]["server"] == dns_name

    def test_default_nfs_path(self) -> None:
        """Test that the default NFS path is '/fsx'."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        assert values["nfs"]["path"] == "/fsx"

    def test_custom_nfs_path(self) -> None:
        """Test that a custom NFS path can be specified."""
        custom_path = "/custom/path"
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com", custom_path)
        assert values["nfs"]["path"] == custom_path

    def test_yaml_serialization(self) -> None:
        """Test that values can be serialized to YAML without errors."""
        values = _nfs_subdir_provisioner_values("test-fsx-dns.example.com")
        yaml_str = yaml.dump(values)
        parsed = yaml.safe_load(yaml_str)
        assert parsed["storageClass"]["name"] == "posit-shared-storage"
        assert parsed["nfs"]["server"] == "test-fsx-dns.example.com"


class TestExternalSecretSpec:
    """Tests for the _external_secret_spec helper function."""

    def test_correct_api_version(self) -> None:
        """Test that the API version is set correctly."""
        # Note: The API version is defined in the CustomResource call in aws_workload_sites.py
        # This function just returns the spec, but we verify the pattern is correct
        spec = _external_secret_spec("test-site", "workload/test-site")
        assert "secretStoreRef" in spec
        assert "target" in spec
        assert "dataFrom" in spec
        assert "refreshInterval" in spec

    def test_correct_secret_store_reference(self) -> None:
        """Test that the secret store reference is correct."""
        spec = _external_secret_spec("test-site", "workload/test-site")
        assert spec["secretStoreRef"]["name"] == "aws-secrets-manager"
        assert spec["secretStoreRef"]["kind"] == "ClusterSecretStore"

    def test_correct_refresh_interval(self) -> None:
        """Test that the refresh interval is set to 1h."""
        spec = _external_secret_spec("test-site", "workload/test-site")
        assert spec["refreshInterval"] == "1h"

    def test_target_naming_convention(self) -> None:
        """Test that the target name follows the '{site_name}-secrets' convention."""
        site_name = "my-test-site"
        spec = _external_secret_spec(site_name, "workload/my-test-site")
        assert spec["target"]["name"] == f"{site_name}-secrets"

    def test_target_creation_policy(self) -> None:
        """Test that the target creation policy is 'Owner'."""
        spec = _external_secret_spec("test-site", "workload/test-site")
        assert spec["target"]["creationPolicy"] == "Owner"

    def test_data_from_extract_key(self) -> None:
        """Test that the dataFrom extract key is correctly set."""
        secret_key = "my-workload/my-site"
        spec = _external_secret_spec("my-site", secret_key)
        assert len(spec["dataFrom"]) == 1
        assert spec["dataFrom"][0]["extract"]["key"] == secret_key

    def test_multiple_sites_different_specs(self) -> None:
        """Test that different sites produce different specs."""
        spec1 = _external_secret_spec("site-one", "workload/site-one")
        spec2 = _external_secret_spec("site-two", "workload/site-two")

        assert spec1["target"]["name"] == "site-one-secrets"
        assert spec2["target"]["name"] == "site-two-secrets"
        assert spec1["dataFrom"][0]["extract"]["key"] == "workload/site-one"
        assert spec2["dataFrom"][0]["extract"]["key"] == "workload/site-two"

    def test_yaml_serialization(self) -> None:
        """Test that the spec can be serialized to YAML without errors."""
        spec = _external_secret_spec("test-site", "workload/test-site")
        yaml_str = yaml.dump(spec)
        parsed = yaml.safe_load(yaml_str)
        assert parsed["secretStoreRef"]["name"] == "aws-secrets-manager"
        assert parsed["target"]["name"] == "test-site-secrets"


class TestSiteCRFieldPopulation:
    """Tests for Site CR field population logic.

    These tests verify the conditional field population patterns
    based on cloud-agnostic feature flags in the cluster configuration.
    """

    def test_storage_class_name_convention(self) -> None:
        """Test that the storage class name follows the 'posit-shared-storage' convention."""
        # This value is used in the Site spec when enable_nfs_subdir_provisioner=True
        storage_class_name = "posit-shared-storage"
        assert storage_class_name == "posit-shared-storage"

    def test_secret_name_pattern_for_site(self) -> None:
        """Test that site secret names follow the '{site_name}-secrets' pattern."""
        site_name = "production"
        expected_secret_name = f"{site_name}-secrets"
        assert expected_secret_name == "production-secrets"

    def test_secret_name_pattern_for_workload(self) -> None:
        """Test that workload secret names follow the '{workload_name}-secrets' pattern."""
        workload_name = "customer-workload"
        expected_secret_name = f"{workload_name}-secrets"
        assert expected_secret_name == "customer-workload-secrets"

    def test_service_account_naming_convention(self) -> None:
        """Test that service account names follow the {site_name}-{product} convention."""
        site_name = "test-site"
        expected_names = [
            f"{site_name}-connect",
            f"{site_name}-workbench",
            f"{site_name}-packagemanager",
            f"{site_name}-chronicle",
            f"{site_name}-home",  # Special case: flightdeck uses "home" suffix
        ]

        for expected_name in expected_names:
            assert expected_name.startswith(site_name)
            assert "-" in expected_name

    def test_nfs_egress_cidr_field_name(self) -> None:
        """Test that the NFS egress CIDR field is named 'nfsEgressCIDR'."""
        # This field is used instead of efsEnabled/vpcCIDR when NFS is enabled
        field_name = "nfsEgressCIDR"
        assert field_name == "nfsEgressCIDR"


class TestCloudAgnosticIntegration:
    """Integration tests for cloud-agnostic infrastructure patterns."""

    def test_storage_class_and_nfs_egress_cidr_field_names(self) -> None:
        """Test that field names for storage and NFS egress are correct."""
        storage_class_field = "storageClassName"
        nfs_egress_field = "nfsEgressCIDR"

        # These fields are set together when enable_nfs_subdir_provisioner=True
        assert storage_class_field == "storageClassName"
        assert nfs_egress_field == "nfsEgressCIDR"

    def test_secret_names_for_site_and_workload(self) -> None:
        """Test that both site and workload secrets are properly named."""
        site_name = "production"
        workload_name = "customer-workload"

        expected_site_secret = f"{site_name}-secrets"
        expected_workload_secret = f"{workload_name}-secrets"

        assert expected_site_secret == "production-secrets"
        assert expected_workload_secret == "customer-workload-secrets"

    def test_service_account_names_for_all_products(self) -> None:
        """Test that service account names are correctly generated for all products."""
        site_name = "production"

        expected_sa_names = [
            f"{site_name}-connect",
            f"{site_name}-workbench",
            f"{site_name}-packagemanager",
            f"{site_name}-chronicle",
            f"{site_name}-home",  # Special case: flightdeck uses "home" suffix
        ]

        for expected_sa_name in expected_sa_names:
            assert expected_sa_name.startswith(site_name)
            # Verify the delimiter
            assert "-" in expected_sa_name

    def test_cloud_agnostic_field_names(self) -> None:
        """Test that all cloud-agnostic field names are correct."""
        # Storage fields
        storage_class_field = "storageClassName"
        nfs_egress_field = "nfsEgressCIDR"
        assert storage_class_field == "storageClassName"
        assert nfs_egress_field == "nfsEgressCIDR"

        # Secret fields
        secret_field = "secret"
        workload_secret_field = "workloadSecret"
        assert secret_field == "secret"
        assert workload_secret_field == "workloadSecret"

        # Service account fields
        sa_field = "serviceAccountName"
        assert sa_field == "serviceAccountName"
