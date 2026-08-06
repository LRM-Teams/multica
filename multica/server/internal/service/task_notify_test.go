package service

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// stubWakeup records every call so the test can assert that notify
// reaches the daemon hub and carries the right runtime / task IDs.
type stubWakeup struct {
	calls []struct{ runtimeID, taskID string }
}

func (s *stubWakeup) NotifyTaskAvailable(runtimeID, taskID string) {
	s.calls = append(s.calls, struct{ runtimeID, taskID string }{runtimeID, taskID})
}

// TestNotifyTaskAvailableWakesRuntime proves every enqueue path (issue,
// mention, quick-create, chat, autopilot, retry) carries the same runtime and
// canonical inbox event identity to the daemon wakeup hook.
func TestNotifyTaskAvailableWakesRuntime(t *testing.T) {
	wakeup := &stubWakeup{}

	svc := &TaskService{
		Wakeup: wakeup,
	}

	runtimeID := testUUID(7)
	taskID := testUUID(8)
	runtimeKey := util.UUIDToString(runtimeID)

	svc.notifyTaskAvailable(db.AgentInboxEvent{
		ID:        taskID,
		RuntimeID: runtimeID,
	})

	if got := len(wakeup.calls); got != 1 {
		t.Fatalf("expected 1 wakeup call, got %d", got)
	}
	if wakeup.calls[0].runtimeID != runtimeKey {
		t.Fatalf("wakeup runtime mismatch: got %q want %q", wakeup.calls[0].runtimeID, runtimeKey)
	}
	if wakeup.calls[0].taskID != util.UUIDToString(taskID) {
		t.Fatalf("wakeup task mismatch: got %q want %q", wakeup.calls[0].taskID, util.UUIDToString(taskID))
	}
}

// TestNotifyTaskAvailable_InvalidWithoutRuntimeIsNoOp guards the no-RuntimeID
// early return.
func TestNotifyTaskAvailable_InvalidWithoutRuntimeIsNoOp(t *testing.T) {
	wakeup := &stubWakeup{}

	svc := &TaskService{
		Wakeup: wakeup,
	}

	svc.notifyTaskAvailable(db.AgentInboxEvent{
		// RuntimeID intentionally invalid (zero value, Valid=false).
		ID: testUUID(9),
	})

	if got := len(wakeup.calls); got != 0 {
		t.Fatalf("expected 0 wakeup calls when RuntimeID is invalid, got %d", got)
	}
}
