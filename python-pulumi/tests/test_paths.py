"""Tests for ptd.paths module."""

import pathlib

import pytest

from ptd.paths import Paths


def test_paths_root_with_ptd_root_set(monkeypatch: pytest.MonkeyPatch) -> None:
    """Test that Paths.root returns PTD_ROOT when set."""
    test_path = "/custom/targets/path"
    monkeypatch.setenv("PTD_ROOT", test_path)

    paths = Paths()
    assert paths.root == pathlib.Path(test_path)


def test_paths_root_without_ptd_root_raises_error(monkeypatch: pytest.MonkeyPatch) -> None:
    """Test that Paths.root raises RuntimeError when PTD_ROOT not set."""
    monkeypatch.delenv("PTD_ROOT", raising=False)

    paths = Paths()
    with pytest.raises(RuntimeError, match="PTD_ROOT environment variable not set"):
        _ = paths.root


def test_paths_control_rooms(monkeypatch: pytest.MonkeyPatch) -> None:
    """Test that control_rooms property works with PTD_ROOT."""
    test_path = "/custom/targets"
    monkeypatch.setenv("PTD_ROOT", test_path)

    paths = Paths()
    assert paths.control_rooms == pathlib.Path(test_path) / "__ctrl__"


def test_paths_workloads(monkeypatch: pytest.MonkeyPatch) -> None:
    """Test that workloads property works with PTD_ROOT."""
    test_path = "/custom/targets"
    monkeypatch.setenv("PTD_ROOT", test_path)

    paths = Paths()
    assert paths.workloads == pathlib.Path(test_path) / "__work__"


def test_paths_cache_with_ptd_cache_set(monkeypatch: pytest.MonkeyPatch) -> None:
    """Test that Paths.cache returns PTD_CACHE when set."""
    test_cache_path = "/custom/cache"
    monkeypatch.setenv("PTD_CACHE", test_cache_path)

    paths = Paths()
    assert paths.cache == pathlib.Path(test_cache_path)


def test_paths_cache_default(monkeypatch: pytest.MonkeyPatch, tmp_path: pathlib.Path) -> None:
    """Test that Paths.cache uses default when PTD_CACHE not set."""
    monkeypatch.delenv("PTD_CACHE", raising=False)

    # Mock the top() function by setting PTD_TOP
    test_top = str(tmp_path / "project")
    monkeypatch.setenv("PTD_TOP", test_top)

    paths = Paths()
    assert paths.cache == pathlib.Path(test_top) / ".local"
