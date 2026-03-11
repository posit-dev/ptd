import pytest

from ptd.pulumi_resources.lib import format_lb_tags, sanitize_k8s_name


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


def test_format_lb_tags_key_at_limit() -> None:
    # 128 chars — should succeed (boundary check for > comparison)
    format_lb_tags({"k" * 128: "value"})


def test_format_lb_tags_key_too_long() -> None:
    with pytest.raises(ValueError, match="128-character limit"):
        format_lb_tags({"k" * 129: "value"})


def test_format_lb_tags_value_at_limit() -> None:
    # 256 chars — should succeed (boundary check for > comparison)
    format_lb_tags({"key": "v" * 256})


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


def test_format_lb_tags_slash_in_key() -> None:
    # Production keys like posit.team/true-name contain /; must be accepted
    result = format_lb_tags({"posit.team/true-name": "myapp", "posit.team/environment": "production"})
    assert "posit.team/true-name=myapp" in result
    assert "posit.team/environment=production" in result


# ===== sanitize_k8s_name tests =====


def test_sanitize_k8s_name_basic_underscore_replacement() -> None:
    """Basic test: underscores replaced with hyphens"""
    assert sanitize_k8s_name("alerts_dashboard") == "alerts-dashboard"


def test_sanitize_k8s_name_multiple_underscores() -> None:
    """Multiple underscores are all replaced"""
    assert sanitize_k8s_name("my_test_dashboard") == "my-test-dashboard"


def test_sanitize_k8s_name_uppercase_lowercased() -> None:
    """Uppercase letters are lowercased"""
    assert sanitize_k8s_name("MyDashboard") == "mydashboard"


def test_sanitize_k8s_name_mixed_case_and_underscores() -> None:
    """Mix of uppercase and underscores"""
    assert sanitize_k8s_name("My_Dashboard") == "my-dashboard"


def test_sanitize_k8s_name_leading_underscore_stripped() -> None:
    """Leading underscores are stripped"""
    assert sanitize_k8s_name("_dashboard") == "dashboard"


def test_sanitize_k8s_name_trailing_underscore_stripped() -> None:
    """Trailing underscores are stripped"""
    assert sanitize_k8s_name("dashboard_") == "dashboard"


def test_sanitize_k8s_name_leading_and_trailing_underscores() -> None:
    """Both leading and trailing underscores are stripped"""
    assert sanitize_k8s_name("_dashboard_") == "dashboard"


def test_sanitize_k8s_name_special_chars_replaced() -> None:
    """Special characters replaced with hyphens"""
    assert sanitize_k8s_name("my@dashboard!") == "my-dashboard"


def test_sanitize_k8s_name_spaces_replaced() -> None:
    """Spaces replaced with hyphens"""
    assert sanitize_k8s_name("my dashboard") == "my-dashboard"


def test_sanitize_k8s_name_dots_replaced() -> None:
    """Dots are replaced with hyphens (simpler for dashboard names)"""
    assert sanitize_k8s_name("my.dashboard") == "my-dashboard"


def test_sanitize_k8s_name_hyphens_preserved() -> None:
    """Hyphens are preserved"""
    assert sanitize_k8s_name("my-dashboard") == "my-dashboard"


def test_sanitize_k8s_name_alphanumeric_preserved() -> None:
    """Alphanumeric characters are preserved (lowercased)"""
    assert sanitize_k8s_name("Dashboard123") == "dashboard123"


def test_sanitize_k8s_name_consecutive_special_chars() -> None:
    """Consecutive special characters are collapsed into a single hyphen"""
    assert sanitize_k8s_name("my___dashboard") == "my-dashboard"


def test_sanitize_k8s_name_only_special_chars_fails() -> None:
    """Name with only special characters cannot be sanitized"""
    with pytest.raises(ValueError, match="cannot be sanitized to RFC 1123 format"):
        sanitize_k8s_name("___")


def test_sanitize_k8s_name_empty_string_fails() -> None:
    """Empty string raises ValueError"""
    with pytest.raises(ValueError, match="Name cannot be empty"):
        sanitize_k8s_name("")


def test_sanitize_k8s_name_only_leading_underscore_fails() -> None:
    """Name that becomes empty after stripping fails"""
    with pytest.raises(ValueError, match="cannot be sanitized to RFC 1123 format"):
        sanitize_k8s_name("_")


def test_sanitize_k8s_name_complex_case() -> None:
    """Complex real-world example"""
    assert sanitize_k8s_name("_My_Dashboard_2024!") == "my-dashboard-2024"


def test_sanitize_k8s_name_already_valid() -> None:
    """Already valid names pass through (lowercased)"""
    assert sanitize_k8s_name("valid-name") == "valid-name"


def test_sanitize_k8s_name_single_char() -> None:
    """Single character names are valid"""
    assert sanitize_k8s_name("a") == "a"
    assert sanitize_k8s_name("1") == "1"


def test_sanitize_k8s_name_two_chars() -> None:
    """Two character names are valid"""
    assert sanitize_k8s_name("ab") == "ab"


def test_sanitize_k8s_name_hyphen_in_middle() -> None:
    """Hyphen in middle is valid (part of RFC 1123 pattern)"""
    assert sanitize_k8s_name("a-b") == "a-b"


def test_sanitize_k8s_name_leading_hyphen_after_special_char() -> None:
    """Special char at start becomes hyphen, then gets stripped"""
    assert sanitize_k8s_name("!dashboard") == "dashboard"


def test_sanitize_k8s_name_trailing_hyphen_after_special_char() -> None:
    """Special char at end becomes hyphen, then gets stripped"""
    assert sanitize_k8s_name("dashboard!") == "dashboard"
