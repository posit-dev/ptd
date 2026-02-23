import pytest

from ptd.pulumi_resources.lib import format_lb_tags


def test_format_lb_tags_normal() -> None:
    tags = {
        "posit.team/true-name": "myapp",
        "posit.team/environment": "production",
        "Name": "myapp-production",
    }
    result = format_lb_tags(tags)
    assert result == "posit.team/true-name=myapp,posit.team/environment=production,Name=myapp-production"


def test_format_lb_tags_single_entry() -> None:
    assert format_lb_tags({"key": "value"}) == "key=value"


def test_format_lb_tags_comma_in_key() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        format_lb_tags({"bad,key": "value"})


def test_format_lb_tags_equals_in_key() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        format_lb_tags({"bad=key": "value"})


def test_format_lb_tags_comma_in_value() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        format_lb_tags({"key": "bad,value"})


def test_format_lb_tags_equals_in_value() -> None:
    with pytest.raises(ValueError, match="comma or equals"):
        format_lb_tags({"key": "bad=value"})


def test_format_lb_tags_empty_key() -> None:
    with pytest.raises(ValueError, match="must not be empty"):
        format_lb_tags({"": "value"})


def test_format_lb_tags_empty_value() -> None:
    with pytest.raises(ValueError, match="must not be empty"):
        format_lb_tags({"key": ""})
