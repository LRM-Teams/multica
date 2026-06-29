package service

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSweLegoCacheKey(t *testing.T) {
	got := SweLegoCacheKey("https://github.com/psf/requests.git", "abc123", "2025-03-14T09:30:00Z", "swe-lego/python:3.11")
	h := sha256.Sum256([]byte("https://github.com/psf/requests.git|abc123|2025-03-14T09:30:00Z|swe-lego/python:3.11"))
	want := hex.EncodeToString(h[:])
	if got != want {
		t.Fatalf("cache key = %q, want %q", got, want)
	}
}

func TestSweLegoCacheKey_StableAcrossOrder(t *testing.T) {
	a := SweLegoCacheKey("r1", "c1", "d1", "b1")
	b := SweLegoCacheKey("r1", "c1", "d1", "b1")
	if a != b {
		t.Fatalf("identical inputs produced different keys: %q vs %q", a, b)
	}
}

func TestSweLegoCacheKey_DistinguishesInputs(t *testing.T) {
	a := SweLegoCacheKey("r1", "c1", "d1", "b1")
	b := SweLegoCacheKey("r1", "c2", "d1", "b1")
	if a == b {
		t.Fatalf("different base_commit produced the same key")
	}
}
