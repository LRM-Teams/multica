package handler

import (
	"testing"
	"time"
)

func TestRuntimeUpdateError_SurfacesFailedAttempt(t *testing.T) {
	t.Parallel()
	cur := "0.3.99"
	upd := &UpdateRequest{
		Status:        UpdateFailed,
		TargetVersion: "v0.4.0",
		Error:         "stage release failed: invalid staged metadata for v0.3.99",
		UpdatedAt:     time.Now(),
	}
	got := runtimeUpdateError(upd, &cur, "failed")
	if got == nil || *got == "" {
		t.Fatal("expected non-empty update_error")
	}
	if *got != upd.Error {
		t.Fatalf("update_error=%q want %q", *got, upd.Error)
	}
}

func TestRuntimeUpdateError_TimeoutDefault(t *testing.T) {
	t.Parallel()
	cur := "0.3.99"
	upd := &UpdateRequest{
		Status:        UpdateTimeout,
		TargetVersion: "v0.4.0",
		Error:         "",
		UpdatedAt:     time.Now(),
	}
	got := runtimeUpdateError(upd, &cur, "timed_out")
	if got == nil || *got != "runtime_update_timed_out" {
		t.Fatalf("got %v", got)
	}
}
