"""Tests for the payload codec."""

from __future__ import annotations

import os

import pytest

from db_bridge import codec


def test_empty_roundtrip():
    enc, text = codec.encode(b"")
    assert enc == codec.RAW
    assert text == ""
    assert codec.decode(enc, text) == b""


def test_small_utf8_stored_raw_for_audit():
    payload = b'{"reward": 1.0, "interaction_id": null}'
    enc, text = codec.encode(payload)
    assert enc == codec.RAW
    assert text == payload.decode("utf-8")
    assert codec.decode(enc, text) == payload


def test_small_binary_uses_base64():
    payload = bytes(range(256))  # not valid utf-8
    enc, text = codec.encode(payload)
    assert enc == codec.BASE64
    assert codec.decode(enc, text) == payload


def test_large_text_is_gzip_base64_and_smaller():
    payload = (b"highly compressible " * 100_000)  # ~2 MB, very repetitive
    enc, text = codec.encode(payload)
    assert enc == codec.GZIP_BASE64
    # gzip should massively shrink repetitive text even after base64 inflation.
    assert len(text) < len(payload)
    assert codec.decode(enc, text) == payload


def test_large_binary_5mb_roundtrip():
    payload = os.urandom(5 * 1024 * 1024)
    enc, text = codec.encode(payload)
    assert enc == codec.GZIP_BASE64
    assert codec.decode(enc, text) == payload


def test_threshold_boundary():
    threshold = 16
    at = b"a" * threshold
    over = b"a" * (threshold + 1)
    assert codec.encode(at, threshold=threshold)[0] == codec.RAW
    assert codec.encode(over, threshold=threshold)[0] == codec.GZIP_BASE64


def test_decode_none_text():
    assert codec.decode(codec.GZIP_BASE64, None) == b""


def test_decode_rejects_unknown_encoding():
    with pytest.raises(ValueError):
        codec.decode("rot13", "abc")


def test_encode_rejects_non_bytes():
    with pytest.raises(TypeError):
        codec.encode("not bytes")  # type: ignore[arg-type]
