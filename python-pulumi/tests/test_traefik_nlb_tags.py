import pytest

from ptd.pulumi_resources.traefik import _build_nlb_tag_string


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
