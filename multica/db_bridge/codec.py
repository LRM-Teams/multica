"""Payload codec for the DB bridge.

Request/response bodies (and inline multipart file payloads) are stored in
``text`` columns rather than ``jsonb`` to avoid the ~256 MB jsonb ceiling and
its parse overhead, and to stay binary-safe. Small UTF-8 payloads are stored
raw for auditability; small binary payloads are base64-encoded; large payloads
are gzip-compressed then base64-encoded to shrink them well under Supabase's
REST body limit and reduce TOAST/I/O cost.

Encodings (stored alongside the text in a sibling ``*_encoding`` column):

* ``raw``         -- the column holds the UTF-8 text verbatim.
* ``base64``      -- the column holds base64(raw_bytes).
* ``gzip+base64`` -- the column holds base64(gzip(raw_bytes)).
"""

from __future__ import annotations

import base64
import gzip
from typing import Final

RAW: Final = "raw"
BASE64: Final = "base64"
GZIP_BASE64: Final = "gzip+base64"

VALID_ENCODINGS: Final = frozenset({RAW, BASE64, GZIP_BASE64})

# Bodies at or below this many bytes are stored without compression. Values
# above Postgres' ~2 KB inline threshold get TOASTed regardless, so compressing
# large payloads is what actually matters for size/throughput.
DEFAULT_THRESHOLD: Final = 2048


def encode(data: bytes, *, threshold: int = DEFAULT_THRESHOLD) -> tuple[str, str]:
    """Encode raw bytes for storage in a text column.

    Returns ``(encoding, text)`` where ``encoding`` is one of the module
    constants. Empty input encodes as ``(RAW, "")``.
    """
    if not isinstance(data, (bytes, bytearray)):
        raise TypeError(f"encode expects bytes, got {type(data).__name__}")
    data = bytes(data)
    if not data:
        return RAW, ""

    if len(data) <= threshold:
        try:
            return RAW, data.decode("utf-8")
        except UnicodeDecodeError:
            return BASE64, base64.b64encode(data).decode("ascii")

    compressed = gzip.compress(data, compresslevel=6)
    return GZIP_BASE64, base64.b64encode(compressed).decode("ascii")


def decode(encoding: str | None, text: str | None) -> bytes:
    """Decode a stored ``(encoding, text)`` pair back into raw bytes."""
    if text is None:
        return b""
    enc = encoding or RAW
    if enc == RAW:
        return text.encode("utf-8")
    if enc == BASE64:
        return base64.b64decode(text)
    if enc == GZIP_BASE64:
        return gzip.decompress(base64.b64decode(text))
    raise ValueError(f"unknown body encoding: {encoding!r}")
