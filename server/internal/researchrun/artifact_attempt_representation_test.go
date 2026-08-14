package researchrun

import (
	"errors"
	"testing"
)

func TestApplyFrozenAttemptsReplacesRuntimeStateAndPreservesOrder(t *testing.T) {
	live := []Attempt{
		{ID: "attempt-2", Status: AttemptStatusRunning, InboxTaskID: "live-inbox-2"},
		{ID: "attempt-1", Status: AttemptStatusSucceeded, InboxTaskID: "live-inbox-1"},
		{ID: "post-dispatch", Status: AttemptStatusRunning},
	}
	frozen, err := applyFrozenAttempts(live, map[string]Attempt{
		"attempt-1": {ID: "attempt-1", Status: AttemptStatusFailed, InboxTaskID: "frozen-inbox-1"},
		"attempt-2": {ID: "attempt-2", Status: AttemptStatusDispatching},
	}, "attempt-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen) != 2 || frozen[0].ID != "attempt-2" || frozen[0].Status != AttemptStatusDispatching || frozen[0].InboxTaskID != "" ||
		frozen[1].ID != "attempt-1" || frozen[1].Status != AttemptStatusFailed || frozen[1].InboxTaskID != "frozen-inbox-1" {
		t.Fatalf("frozen=%+v", frozen)
	}
}

func TestApplyFrozenAttemptsRequiresCurrentAttempt(t *testing.T) {
	_, err := applyFrozenAttempts([]Attempt{{ID: "attempt-1"}}, map[string]Attempt{"attempt-1": {ID: "attempt-1"}}, "current")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err=%v", err)
	}
}

func TestApplyFrozenAttemptsRejectsMissingOrderingSource(t *testing.T) {
	_, err := applyFrozenAttempts([]Attempt{{ID: "current"}}, map[string]Attempt{
		"current": {ID: "current"}, "missing": {ID: "missing"},
	}, "current")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err=%v", err)
	}
}
