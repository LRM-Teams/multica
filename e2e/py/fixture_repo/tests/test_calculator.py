"""Acceptance tests (fail_to_pass): fail until `add` is implemented."""

from __future__ import annotations

import pytest

from fixture_calc.calculator import add


def test_add_integers() -> None:
    assert add(2, 3) == 5


def test_add_negative() -> None:
    assert add(-4, 1) == -3


def test_add_floats() -> None:
    assert add(0.1, 0.2) == pytest.approx(0.3)
