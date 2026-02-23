import pytest

from ptd.pulumi_resources.aws_workload_helm import _build_alb_tag_string


def test_build_alb_tag_string_happy_path() -> None:
    result = _build_alb_tag_string(
        true_name="myapp",
        environment="production",
        compound_name="myapp-production",
    )
    parsed = dict(pair.split("=", 1) for pair in result.split(","))
    assert parsed == {
        "posit.team/true-name": "myapp",
        "posit.team/environment": "production",
        "Name": "myapp-production",
    }


def test_build_alb_tag_string_tags_key_present() -> None:
    result = _build_alb_tag_string(
        true_name="myapp",
        environment="staging",
        compound_name="myapp-staging",
    )
    assert "posit.team/true-name=myapp" in result
    assert "posit.team/environment=staging" in result
    assert "Name=myapp-staging" in result


def test_build_alb_tag_string_invalid_compound_name() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        _build_alb_tag_string(
            true_name="myapp",
            environment="production",
            compound_name="bad,name",
        )


def test_build_alb_tag_string_empty_compound_name() -> None:
    with pytest.raises(ValueError, match="must not be None or empty"):
        _build_alb_tag_string(
            true_name="myapp",
            environment="production",
            compound_name="",
        )


def test_build_alb_tag_string_invalid_true_name_value() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        _build_alb_tag_string(
            true_name="bad,name",
            environment="production",
            compound_name="myapp-production",
        )


def test_build_alb_tag_string_invalid_environment_value() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        _build_alb_tag_string(
            true_name="myapp",
            environment="bad=env",
            compound_name="myapp-production",
        )
