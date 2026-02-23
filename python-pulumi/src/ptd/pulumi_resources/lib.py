def format_lb_tags(tags: dict[str, str]) -> str:
    """Format tags as comma-separated key=value pairs for AWS LB Controller annotations.

    Validates that tag keys and values do not contain commas or equals signs,
    which would break the annotation format.
    """
    for key, value in tags.items():
        if not key:
            msg = "LB tag key must not be empty"
            raise ValueError(msg)
        if "," in key or "=" in key:
            msg = f"LB tag key contains invalid characters (comma or equals): {key}"
            raise ValueError(msg)
        if not value:
            msg = f"LB tag value must not be empty: key={key}"
            raise ValueError(msg)
        if "," in value or "=" in value:
            msg = f"LB tag value contains invalid characters (comma or equals): {key}={value}"
            raise ValueError(msg)
    return ",".join(f"{k}={v}" for k, v in tags.items())
