package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

type fakeNodeExec struct {
	calls            []string
	inspectOK        bool // if true, the image is already cached on the node
	inspectErr       error
	buildExitOK      bool // if true, the build script exits 0
	pickBuildNodeErr error
}

func (f *fakeNodeExec) Exec(ctx context.Context, nodeID string, cmd []string) (stdout string, exitCode int, err error) {
	f.calls = append(f.calls, fmt.Sprintf("%s:%s", nodeID, strings.Join(cmd, " ")))
	joined := strings.Join(cmd, " ")
	switch {
	case strings.Contains(joined, "docker image inspect"):
		if f.inspectErr != nil {
			return "", 1, f.inspectErr
		}
		if f.inspectOK {
			return "", 0, nil
		}
		return "no such image", 1, nil
	case strings.Contains(joined, "set -euo pipefail"):
		if f.buildExitOK {
			return "", 0, nil
		}
		return "build failed", 1, nil
	}
	panic(fmt.Sprintf("fakeNodeExec: unrecognized command: %s", joined))
}

func (f *fakeNodeExec) PickBuildNode(ctx context.Context) (string, error) {
	if f.pickBuildNodeErr != nil {
		return "", f.pickBuildNodeErr
	}
	return "node-1", nil
}

func TestBuildOrReuse_CacheHitShortCircuits(t *testing.T) {
	ctx := context.Background()
	fe := &fakeNodeExec{inspectOK: true}
	ref, nodeID, err := BuildOrReuse(ctx, fe, "r", "c", "d", "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nodeID != "node-1" {
		t.Fatalf("nodeID = %q, want node-1", nodeID)
	}
	// Only the inspect call should have fired; no build script.
	if len(fe.calls) != 1 || !strings.Contains(fe.calls[0], "docker image inspect") {
		t.Fatalf("expected exactly one inspect call, got %v", fe.calls)
	}
	if ref == "" {
		t.Fatal("expected non-empty image ref")
	}
}

func TestBuildOrReuse_CacheMissRunsBuild(t *testing.T) {
	ctx := context.Background()
	fe := &fakeNodeExec{inspectOK: false, buildExitOK: true}
	ref, nodeID, err := BuildOrReuse(ctx, fe, "r", "c", "2025-03-14T09:30:00Z", "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nodeID != "node-1" {
		t.Fatalf("nodeID = %q, want node-1", nodeID)
	}
	// Two calls: inspect (cache miss) + build script.
	if len(fe.calls) != 2 {
		t.Fatalf("expected 2 calls, got %v", fe.calls)
	}
	if !strings.Contains(fe.calls[1], "git filter-repo") {
		t.Fatalf("build script not shipped: %v", fe.calls[1])
	}
	if ref == "" {
		t.Fatal("expected non-empty image ref")
	}
}

func TestBuildOrReuse_BuildFailureReturnsError(t *testing.T) {
	ctx := context.Background()
	fe := &fakeNodeExec{inspectOK: false, buildExitOK: false}
	_, _, err := BuildOrReuse(ctx, fe, "r", "c", "2025-03-14T09:30:00Z", "b")
	if err == nil {
		t.Fatal("expected error on build failure")
	}
	if !errors.Is(err, ErrSweLegoBuildFailed) {
		t.Fatalf("expected error to wrap ErrSweLegoBuildFailed, got: %v", err)
	}
}

func TestBuildOrReuse_PickBuildNodeFailure(t *testing.T) {
	ctx := context.Background()
	fe := &fakeNodeExec{pickBuildNodeErr: fmt.Errorf("fleet unavailable")}
	_, _, err := BuildOrReuse(ctx, fe, "r", "c", "2025-03-14T09:30:00Z", "b")
	if err == nil {
		t.Fatal("expected error when PickBuildNode fails")
	}
	if !strings.Contains(err.Error(), "pick build node") {
		t.Fatalf("expected error to mention 'pick build node', got: %v", err)
	}
}

func TestBuildOrReuse_InspectTransportError(t *testing.T) {
	ctx := context.Background()
	fe := &fakeNodeExec{inspectErr: fmt.Errorf("connection refused")}
	_, _, err := BuildOrReuse(ctx, fe, "r", "c", "2025-03-14T09:30:00Z", "b")
	if err == nil {
		t.Fatal("expected error when inspect fails with transport error")
	}
	if !strings.Contains(err.Error(), "cache inspect transport error") {
		t.Fatalf("expected error to mention 'cache inspect transport error', got: %v", err)
	}
}

func TestSweLegoDockerfileTemplate(t *testing.T) {
	tmpl, err := SweLegoDockerfile("swe-lego/python:3.11")
	if err != nil {
		t.Fatalf("render template: %v", err)
	}
	assertContains(t, tmpl, "FROM swe-lego/python:3.11")
	assertContains(t, tmpl, "COPY repo/ /workspace/repo")
	assertContains(t, tmpl, "WORKDIR /workspace/repo")
	assertContains(t, tmpl, "pip install -e . 2>/dev/null || true")
	assertContains(t, tmpl, "COPY multica-daemon /usr/local/bin/multica-daemon")
	assertContains(t, tmpl, "ENV MULTICA_DAEMON_AUTO_REGISTER=1")
	assertContains(t, tmpl, `CMD ["multica-daemon", "run"]`)
}
