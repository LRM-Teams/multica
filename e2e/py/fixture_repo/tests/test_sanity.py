"""Baseline tests (pass_to_pass): always green, must stay green."""

from __future__ import annotations

import fixture_calc
from fixture_calc import calculator


def test_package_importable() -> None:
    assert fixture_calc is not None
    assert hasattr(calculator, "add")


def test_add_is_callable() -> None:
    assert callable(calculator.add)
