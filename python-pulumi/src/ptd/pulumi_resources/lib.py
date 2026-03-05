_AWS_TAG_KEY_MAX_LENGTH = 128
_AWS_TAG_VALUE_MAX_LENGTH = 256


def format_lb_tags(tags: dict[str, str]) -> str:
    """Format tags as comma-separated key=value pairs for AWS LB Controller annotations.

    Validates that tag keys and values do not contain commas or equals signs,
    which would break the annotation format. Whitespace (spaces, tabs, newlines)
    is also rejected in both keys and values; while AWS tag values permit spaces,
    this function is used exclusively for LB controller annotation strings where
    whitespace would be ambiguous. This is a deliberate constraint, not an AWS limit.
    """
    if not tags:
        msg = "tags must not be empty"
        raise ValueError(msg)
    for key, value in tags.items():
        if not key:
            msg = "LB tag key must not be empty"
            raise ValueError(msg)
        if key.startswith("aws:"):
            msg = f"LB tag key uses reserved 'aws:' prefix: {key!r}"
            raise ValueError(msg)
        if len(key) > _AWS_TAG_KEY_MAX_LENGTH:
            msg = f"LB tag key exceeds AWS 128-character limit ({len(key)} chars): {key!r}"
            raise ValueError(msg)
        if "," in key or "=" in key:
            msg = f"LB tag key contains invalid characters (comma or equals): {key}"
            raise ValueError(msg)
        if any(c in key for c in (" ", "\t", "\n", "\r")):
            msg = f"LB tag key contains invalid whitespace character: {key!r}"
            raise ValueError(msg)
        if not value:
            msg = f"LB tag value must not be None or empty: key={key}"
            raise ValueError(msg)
        if len(value) > _AWS_TAG_VALUE_MAX_LENGTH:
            msg = f"LB tag value exceeds AWS 256-character limit ({len(value)} chars): key={key}"
            raise ValueError(msg)
        if "," in value or "=" in value:
            msg = f"LB tag value contains invalid characters (comma or equals): {key}={value}"
            raise ValueError(msg)
        if any(c in value for c in (" ", "\t", "\n", "\r")):
            msg = f"LB tag value contains invalid whitespace character: {key}={value!r}"
            raise ValueError(msg)
    return ",".join(f"{k}={v}" for k, v in tags.items())
