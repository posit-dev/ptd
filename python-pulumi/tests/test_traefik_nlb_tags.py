import pytest

from ptd.pulumi_resources.traefik import _build_nlb_tag_string, _build_traefik_helm_values


def test_build_nlb_tag_string_happy_path() -> None:
    result = _build_nlb_tag_string(
        tags={"posit.team/true-name": "myapp", "posit.team/environment": "production"},
        cluster_name="myapp-cluster",
    )
    # Parse into key=value pairs to avoid coupling the test to dict insertion order
    parsed = dict(pair.split("=", 1) for pair in result.split(","))
    assert parsed == {
        "posit.team/true-name": "myapp",
        "posit.team/environment": "production",
        "Name": "myapp-cluster",
    }


def test_build_nlb_tag_string_tags_none() -> None:
    with pytest.raises(ValueError, match="must not be None"):
        _build_nlb_tag_string(tags=None, cluster_name="myapp-cluster")


def test_build_nlb_tag_string_missing_true_name() -> None:
    with pytest.raises(ValueError, match="posit.team/true-name"):
        _build_nlb_tag_string(
            tags={"posit.team/environment": "production"},
            cluster_name="myapp-cluster",
        )


def test_build_nlb_tag_string_missing_environment() -> None:
    with pytest.raises(ValueError, match="posit.team/environment"):
        _build_nlb_tag_string(
            tags={"posit.team/true-name": "myapp"},
            cluster_name="myapp-cluster",
        )


def test_build_nlb_tag_string_invalid_cluster_name() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        _build_nlb_tag_string(
            tags={"posit.team/true-name": "myapp", "posit.team/environment": "production"},
            cluster_name="bad,name",
        )


def test_build_nlb_tag_string_empty_cluster_name() -> None:
    with pytest.raises(ValueError, match="must not be None or empty"):
        _build_nlb_tag_string(
            tags={"posit.team/true-name": "myapp", "posit.team/environment": "production"},
            cluster_name="",
        )


def test_build_nlb_tag_string_invalid_true_name_value() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        _build_nlb_tag_string(
            tags={"posit.team/true-name": "bad,name", "posit.team/environment": "prod"},
            cluster_name="cluster",
        )


def test_build_nlb_tag_string_invalid_environment_value() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        _build_nlb_tag_string(
            tags={"posit.team/true-name": "myapp", "posit.team/environment": "bad=env"},
            cluster_name="cluster",
        )


def _make_traefik_values(**overrides):
    """Helper to create traefik helm values with sensible defaults."""
    defaults = dict(
        node_selector="",
        deployment_replicas=3,
        cert_arn=None,
        nlb_tags="posit.team/true-name=myapp,posit.team/environment=prod,Name=cluster",
    )
    defaults.update(overrides)
    return _build_traefik_helm_values(**defaults)


def test_traefik_v3_redirect_uses_redirections_syntax() -> None:
    values = _make_traefik_values()
    web = values["ports"]["web"]
    assert "redirectTo" not in web, "v2 redirect syntax must not be present"
    assert "redirections" in web
    assert web["redirections"]["entryPoint"]["to"] == "websecure"
    assert web["redirections"]["entryPoint"]["scheme"] == "https"
    assert web["redirections"]["entryPoint"]["permanent"] is True


def test_traefik_v3_ingress_class_uses_is_default_class() -> None:
    values = _make_traefik_values()
    ingress_class = values["ingressClass"]
    assert "default" not in ingress_class, "v2 'default' key must not be present"
    assert ingress_class["isDefaultClass"] is True
    assert ingress_class["enabled"] is True


def test_traefik_node_selector_empty_string_yields_none() -> None:
    values = _make_traefik_values(node_selector="")
    assert values["nodeSelector"] is None


def test_traefik_node_selector_set_yields_dict() -> None:
    values = _make_traefik_values(node_selector="m5.xlarge")
    assert values["nodeSelector"] == {"node.kubernetes.io/instance-type": "m5.xlarge"}


def test_traefik_deployment_replicas_propagated() -> None:
    values = _make_traefik_values(deployment_replicas=5)
    assert values["deployment"]["replicas"] == 5


def test_traefik_cert_arn_none_sets_annotation_to_none() -> None:
    values = _make_traefik_values(cert_arn=None)
    assert values["service"]["annotations"]["service.beta.kubernetes.io/aws-load-balancer-ssl-cert"] is None


def test_traefik_cert_arn_set_propagates_to_annotation() -> None:
    values = _make_traefik_values(cert_arn="arn:aws:acm:us-east-1:123:certificate/abc")
    assert (
        values["service"]["annotations"]["service.beta.kubernetes.io/aws-load-balancer-ssl-cert"]
        == "arn:aws:acm:us-east-1:123:certificate/abc"
    )


def test_build_nlb_tag_string_extra_tags_are_dropped() -> None:
    """Extra tags in the input dict (e.g. aws:created-by, Cost-Center) are intentionally
    discarded; only true-name, environment, and Name should appear in the output."""
    result = _build_nlb_tag_string(
        tags={
            "posit.team/true-name": "myapp",
            "posit.team/environment": "production",
            "aws:created-by": "someone",
            "Cost-Center": "123",
        },
        cluster_name="myapp-cluster",
    )
    parsed = dict(pair.split("=", 1) for pair in result.split(","))
    assert parsed == {
        "posit.team/true-name": "myapp",
        "posit.team/environment": "production",
        "Name": "myapp-cluster",
    }
