"""Tests for ESO Helm values and ExternalSecret/ClusterSecretStore CR structure."""

import yaml

from ptd.pulumi_resources.aws_workload_helm import (
    _cluster_secret_store_spec,
    _eso_helm_values,
)
from ptd.pulumi_resources.aws_workload_sites import _external_secret_spec as _build_external_secret_spec


def test_eso_helm_values_install_crds():
    values = _eso_helm_values()
    assert values["installCRDs"] is True


def test_eso_helm_values_service_account():
    values = _eso_helm_values()
    sa = values["serviceAccount"]
    assert sa["create"] is True
    assert sa["name"] == "external-secrets"
    # No IRSA annotations — Pod Identity is used instead
    assert "annotations" not in sa


def test_eso_helm_values_yaml_roundtrip():
    values = _eso_helm_values()
    parsed = yaml.safe_load(yaml.dump(values))
    assert parsed["installCRDs"] is True
    assert parsed["serviceAccount"]["name"] == "external-secrets"
    assert "annotations" not in parsed["serviceAccount"]


def test_cluster_secret_store_no_auth_block():
    """ClusterSecretStore must have no auth block — credentials come from Pod Identity."""
    spec = _cluster_secret_store_spec("us-east-1")
    aws_provider = spec["provider"]["aws"]
    assert aws_provider["service"] == "SecretsManager"
    assert aws_provider["region"] == "us-east-1"
    assert "auth" not in aws_provider, "auth block must be absent; Pod Identity provides ambient credentials"


def test_cluster_secret_store_region_propagated():
    spec = _cluster_secret_store_spec("eu-west-1")
    assert spec["provider"]["aws"]["region"] == "eu-west-1"


def test_external_secret_store_ref():
    spec = _build_external_secret_spec("mysite", "myworkload/mysite")
    assert spec["secretStoreRef"]["name"] == "aws-secrets-manager"
    assert spec["secretStoreRef"]["kind"] == "ClusterSecretStore"


def test_external_secret_refresh_interval():
    spec = _build_external_secret_spec("mysite", "myworkload/mysite")
    assert spec["refreshInterval"] == "1h"


def test_external_secret_target_name():
    spec = _build_external_secret_spec("mysite", "myworkload/mysite")
    assert spec["target"]["name"] == "mysite-secrets"
    assert spec["target"]["creationPolicy"] == "Owner"


def test_external_secret_data_from_extract():
    secret_key = "myworkload/mysite"
    spec = _build_external_secret_spec("mysite", secret_key)
    assert len(spec["dataFrom"]) == 1
    assert spec["dataFrom"][0]["extract"]["key"] == secret_key
