package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRuntimeLookup struct {
	runtime RuntimeRef
	err     error
	calls   int
}

func (f *fakeRuntimeLookup) FindOnlineSandboxRuntime(_ context.Context, _, _, _ string) (RuntimeRef, error) {
	f.calls++
	return f.runtime, f.err
}

func matchingOnlineRuntime() RuntimeRef {
	return RuntimeRef{
		ID: "rt-1", WorkspaceID: "ws-1", Provider: "pi",
		DaemonID: "daemon-x", SandboxInstanceID: "sbx-1", Status: "online",
	}
}

func TestWaitForOnlineSandboxRuntimeReturnsMatchingOnlineRuntime(t *testing.T) {
	q := &fakeRuntimeLookup{runtime: matchingOnlineRuntime()}
	rt, err := WaitForOnlineSandboxRuntime(context.Background(), q, "ws-1", "daemon-x", "sbx-1", time.Second)
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if rt.ID != "rt-1" {
		t.Fatalf("runtime ID = %q, want rt-1", rt.ID)
	}
	// A first-try match must not poll.
	if q.calls != 1 {
		t.Fatalf("want 1 lookup call on immediate match, got %d", q.calls)
	}
}

func TestWaitForOnlineSandboxRuntimeRejectsIdentityMismatch(t *testing.T) {
	mismatched := matchingOnlineRuntime()
	mismatched.SandboxInstanceID = "sbx-other"
	q := &fakeRuntimeLookup{runtime: mismatched}
	_, err := WaitForOnlineSandboxRuntime(context.Background(), q, "ws-1", "daemon-x", "sbx-expected", 20*time.Millisecond)
	if err == nil {
		t.Fatal("want mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "runtime identity mismatch") {
		t.Fatalf("want mismatch error, got %v", err)
	}
	// No secret crosses this path; the error must never carry a sentinel key.
	if strings.Contains(err.Error(), "sentinel-static-key") {
		t.Fatalf("secret leaked into error: %v", err)
	}
}

func TestWaitForOnlineSandboxRuntimeTimesOutWhenNeverOnline(t *testing.T) {
	q := &fakeRuntimeLookup{err: ErrSandboxRuntimeNotOnline}
	_, err := WaitForOnlineSandboxRuntime(context.Background(), q, "ws-1", "daemon-x", "sbx-1", 250*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "runtime readiness timeout") {
		t.Fatalf("want timeout error, got %v", err)
	}
	// Must poll at least twice before giving up (confirms polling, not an
	// immediate bail). 250ms / 100ms interval allows >= 2 polls even with
	// scheduling jitter.
	if q.calls < 2 {
		t.Fatalf("want >= 2 lookup calls before timeout, got %d", q.calls)
	}
}

func TestWaitForOnlineSandboxRuntimeSurfacesQueryError(t *testing.T) {
	q := &fakeRuntimeLookup{err: errors.New("db connection lost")}
	_, err := WaitForOnlineSandboxRuntime(context.Background(), q, "ws-1", "daemon-x", "sbx-1", time.Second)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "resolve sandbox runtime") {
		t.Fatalf("want wrapped query error, got %v", err)
	}
	if !strings.Contains(err.Error(), "db connection lost") {
		t.Fatalf("want underlying error preserved, got %v", err)
	}
}
