package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/memorysignal"
)

func TestFrictionTrackerForTaskLifecycle(t *testing.T) {
	d := &Daemon{}

	// Same task returns the same tracker instance.
	first := d.frictionTrackerForTask("task-1")
	if first == nil {
		t.Fatal("expected tracker")
	}
	if second := d.frictionTrackerForTask("task-1"); second != first {
		t.Fatal("same task should return the same tracker")
	}

	// Reaching the retry threshold produces one episode.
	hash := frictionToolInputHash(map[string]any{"command": "make check"})
	for range 8 {
		first.ObserveToolUse("bash", hash)
	}
	vector := d.takeTaskFrictionVector("task-1")
	if vector.RetryLoop != 1 {
		t.Fatalf("expected one retry episode, got %+v", vector)
	}

	// Take drains the entry: a second take is zero.
	if v := d.takeTaskFrictionVector("task-1"); !v.IsZero() {
		t.Fatalf("expected drained vector, got %+v", v)
	}

	// Empty task IDs never track.
	if tr := d.frictionTrackerForTask("  "); tr != nil {
		t.Fatal("blank task id must not create a tracker")
	}
	if v := d.takeTaskFrictionVector(""); !v.IsZero() {
		t.Fatal("blank task id must return zero vector")
	}
}

func TestFrictionToolInputHashStability(t *testing.T) {
	a := frictionToolInputHash(map[string]any{"path": "a.go", "line": 1})
	b := frictionToolInputHash(map[string]any{"line": 1, "path": "a.go"})
	if a == "" || a != b {
		t.Fatalf("identical inputs must hash identically: %q vs %q", a, b)
	}
	c := frictionToolInputHash(map[string]any{"path": "b.go", "line": 1})
	if c == a {
		t.Fatal("different inputs must hash differently")
	}
	if frictionToolInputHash(nil) != "" {
		t.Fatal("empty input hashes to empty string")
	}
}

func TestNilTrackerObservationsAreSafe(t *testing.T) {
	var tr *memorysignal.FrictionTracker
	tr.ObserveToolUse("bash", "h")
	tr.ObserveError()
	tr.ObserveProgress()
	if !tr.Vector().IsZero() {
		t.Fatal("nil tracker must report zero vector")
	}
}
