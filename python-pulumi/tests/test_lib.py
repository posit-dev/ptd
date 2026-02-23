import pytest

from ptd.pulumi_resources.lib import format_lb_tags


def test_format_lb_tags_normal() -> None:
    tags = {
        "posit.team/true-name": "myapp",
        "posit.team/environment": "production",
        "Name": "myapp-production",
    }
    result = format_lb_tags(tags)
    parsed = dict(pair.split("=", 1) for pair in result.split(","))
    assert parsed == {
        "posit.team/true-name": "myapp",
        "posit.team/environment": "production",
        "Name": "myapp-production",
    }


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
    with pytest.raises(ValueError, match="must not be None or empty"):
        format_lb_tags({"key": ""})


def test_format_lb_tags_empty_dict() -> None:
    with pytest.raises(ValueError, match="must not be empty"):
        format_lb_tags({})


def test_format_lb_tags_space_in_key() -> None:
    with pytest.raises(ValueError, match="whitespace"):
        format_lb_tags({"bad key": "value"})


def test_format_lb_tags_space_in_value() -> None:
    with pytest.raises(ValueError, match="whitespace"):
        format_lb_tags({"key": "bad value"})


def test_format_lb_tags_tab_in_key() -> None:
    with pytest.raises(ValueError, match="whitespace"):
        format_lb_tags({"bad\tkey": "value"})


def test_format_lb_tags_newline_in_key() -> None:
    with pytest.raises(ValueError, match="whitespace"):
        format_lb_tags({"bad\nkey": "value"})


def test_format_lb_tags_tab_in_value() -> None:
    with pytest.raises(ValueError, match="whitespace"):
        format_lb_tags({"key": "bad\tvalue"})


def test_format_lb_tags_newline_in_value() -> None:
    with pytest.raises(ValueError, match="whitespace"):
        format_lb_tags({"key": "bad\nvalue"})


def test_format_lb_tags_key_too_long() -> None:
    with pytest.raises(ValueError, match="128-character limit"):
        format_lb_tags({"k" * 129: "value"})


def test_format_lb_tags_value_too_long() -> None:
    with pytest.raises(ValueError, match="256-character limit"):
        format_lb_tags({"key": "v" * 257})


def test_format_lb_tags_aws_reserved_prefix() -> None:
    with pytest.raises(ValueError, match="reserved 'aws:' prefix"):
        format_lb_tags({"aws:foo": "bar"})


def test_format_lb_tags_carriage_return_in_key() -> None:
    with pytest.raises(ValueError, match="whitespace"):
        format_lb_tags({"bad\rkey": "value"})


def test_format_lb_tags_carriage_return_in_value() -> None:
    with pytest.raises(ValueError, match="whitespace"):
        format_lb_tags({"key": "bad\rvalue"})
