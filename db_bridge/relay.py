"""Shared HTTP relay helpers for the stub server and executor worker.

The bridge relays raw request/response bytes, so it must strip connection- and
encoding-specific headers that only make sense on the original hop. The body is
always stored and replayed *decoded*, so any ``Content-Encoding`` /
``Content-Length`` from the wire must not leak across the relay.
"""

from __future__ import annotations

from collections.abc import Mapping, Sequence

# Hop-by-hop headers (RFC 7230 §6.1) plus length/encoding headers that the
# receiving HTTP client recomputes from the body we hand it.
_DROP_REQUEST_HEADERS = frozenset(
    {
        "host",
        "content-length",
        "connection",
        "keep-alive",
        "proxy-authenticate",
        "proxy-authorization",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
        "accept-encoding",
        "x-bridge-user-id",
    }
)

_DROP_RESPONSE_HEADERS = frozenset(
    {
        "content-length",
        "content-encoding",
        "connection",
        "keep-alive",
        "transfer-encoding",
        "te",
        "trailer",
        "upgrade",
    }
)


def _normalize(headers: Mapping[str, str]) -> list[tuple[str, str]]:
    return [(str(k).lower(), str(v)) for k, v in headers.items()]


def filter_request_headers(headers: Mapping[str, str]) -> dict[str, str]:
    """Headers safe to replay from the executor to the real upstream.

    Preserves ``Authorization`` (pure pass-through auth) and ``Content-Type``
    (needed for JSON and for the multipart boundary).
    """
    return {k: v for k, v in _normalize(headers) if k not in _DROP_REQUEST_HEADERS}


def filter_response_headers(headers: Mapping[str, str]) -> dict[str, str]:
    """Headers safe to store from the upstream and re-serve from the stub."""
    return {k: v for k, v in _normalize(headers) if k not in _DROP_RESPONSE_HEADERS}


def capture_headers(
    headers: Mapping[str, str] | Sequence[tuple[str, str]],
) -> dict[str, str]:
    """Lowercase and flatten incoming headers for storage.

    Multi-valued headers collapse to the last value, which is sufficient for the
    endpoints the bridge relays (none of which rely on repeated headers).
    """
    items = headers.items() if isinstance(headers, Mapping) else headers
    return {str(k).lower(): str(v) for k, v in items}
