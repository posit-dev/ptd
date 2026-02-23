import pytest

from ptd.pulumi_resources.traefik import _build_nlb_tag_string


def test_build_nlb_tag_string_happy_path() -> None:
    result = _build_nlb_tag_string(
        tags={"posit.team/true-name": "myapp", "posit.team/environment": "production"},
        cluster_name="myapp-cluster",
    )
    assert result == "posit.team/true-name=myapp,posit.team/environment=production,Name=myapp-cluster"


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
    with pytest.raises(ValueError):
        _build_nlb_tag_string(
            tags={"posit.team/true-name": "myapp", "posit.team/environment": "production"},
            cluster_name="bad,name",
        )
