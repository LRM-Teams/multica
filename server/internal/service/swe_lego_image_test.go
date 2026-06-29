package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
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

func TestSweLegoBuildScript_ContainsFilterRepoCutoff(t *testing.T) {
	script := SweLegoBuildScript("https://github.com/psf/requests.git", "abc123", "2025-03-14T09:30:00Z", "swe-lego/python:3.11", "deadbeef")
	// clone + checkout base_commit
	assertContains(t, script, "git clone --filter=blob:none 'https://github.com/psf/requests.git'")
	assertContains(t, script, "git fetch origin 'abc123'")
	assertContains(t, script, "git checkout 'abc123'")
	// SWE-Lego anti-hacking: filter-repo with cutoff computed from issue_date
	assertContains(t, script, "git rev-list -1 --before='2025-03-14T09:30:00Z' HEAD")
	assertContains(t, script, "git filter-repo --replace-ref refs/heads/main:")
	assertContains(t, script, "--commit-cutoff")
	// docker build tagged with the cache key
	assertContains(t, script, "docker build -t 'swe-lego:deadbeef'")
}

func TestSweLegoBuildScript_PipInstallBestEffort(t *testing.T) {
	script := SweLegoBuildScript("r", "c", "d", "swe-lego/python:3.11", "k")
	// Skywork-SWE pattern: best-effort pip install, never fail the build on it
	assertContains(t, script, "pip install -e . 2>/dev/null || true")
}

func assertContains(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Fatalf("build script missing %q\n--- script ---\n%s", want, s)
	}
}
