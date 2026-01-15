import pytest

import ptd.junkdrawer


def test_filter_steps_after_start() -> None:
    steps = [("one", 1), ("two", 2), ("three", 3)]

    assert len(ptd.junkdrawer.filter_steps_after_start("one", steps)) == 3
    assert len(ptd.junkdrawer.filter_steps_after_start("two", steps)) == 2
    assert len(ptd.junkdrawer.filter_steps_after_start("three", steps)) == 1

    # errors for missing step
    with pytest.raises(ValueError, match=".*not in list"):
        ptd.junkdrawer.filter_steps_after_start("something", steps)

    # idempotent when no steps passed
    assert len(ptd.junkdrawer.filter_steps_after_start("something", [])) == 0
