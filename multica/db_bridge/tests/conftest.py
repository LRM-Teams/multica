"""Test fixtures for the db_bridge package.

Ensures the repository root (parent of ``db_bridge``) is importable so tests
can ``import db_bridge`` regardless of pytest's invocation directory.
"""

from __future__ import annotations

import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))
