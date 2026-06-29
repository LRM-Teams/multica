package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
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
	issueDate := "2025-03-14T09:30:00Z"
	script, err := SweLegoBuildScript("https://github.com/psf/requests.git", "abc123", issueDate, "swe-lego/python:3.11", "deadbeef")
	if err != nil {
		t.Fatalf("SweLegoBuildScript returned error: %v", err)
	}
	// clone + checkout base_commit
	assertContains(t, script, "git clone --filter=blob:none 'https://github.com/psf/requests.git'")
	assertContains(t, script, "git fetch origin 'abc123'")
	assertContains(t, script, "git checkout 'abc123'")
	// SWE-Lego anti-hacking: filter-repo drops commits after issue_date.
	// The issue_date is parsed to a Unix timestamp and embedded in the callback.
	expectedTs := mustParseRFC3339(t, issueDate).Unix()
	assertContains(t, script, "git filter-repo --force --commit-callback")
	assertContains(t, script, "commit.skip()")
	assertContains(t, script, fmt.Sprintf("int(commit.committer_date.split()[0]) > %d", expectedTs))
	// docker build tagged with the cache key
	assertContains(t, script, "docker build -t 'swe-lego:deadbeef'")
}

func TestSweLegoBuildScript_PipInstallBestEffort(t *testing.T) {
	script, err := SweLegoBuildScript("r", "c", "2025-03-14T09:30:00Z", "swe-lego/python:3.11", "k")
	if err != nil {
		t.Fatalf("SweLegoBuildScript returned error: %v", err)
	}
	// Skywork-SWE pattern: best-effort pip install, never fail the build on it
	assertContains(t, script, "pip install -e . 2>/dev/null || true")
}

func TestSweLegoBuildScript_InvalidIssueDate(t *testing.T) {
	_, err := SweLegoBuildScript("r", "c", "not-a-date", "swe-lego/python:3.11", "k")
	if err == nil {
		t.Fatalf("expected error for invalid issue_date, got nil")
	}
}

func assertContains(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Fatalf("build script missing %q\n--- script ---\n%s", want, s)
	}
}

func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("invalid test date %q: %v", s, err)
	}
	return ts
}
