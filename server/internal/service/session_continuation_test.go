// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func runtimeUUID(t *testing.T, b byte) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	u.Valid = true
	u.Bytes[15] = b
	return u
}

func sessText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

// A pause_in_place resume re-activates the SAME task row, and that row still
// carries the session the agent pinned mid-flight. Nothing else can supply it:
// GetLastTaskSession and GetLastChatTaskSession only match terminal tasks, and
// this row is not terminal.
func TestResumedTaskContinuesItsOwnRecordedSession(t *testing.T) {
	rt := runtimeUUID(t, 1)
	event := db.AgentInboxEvent{
		Status:    "pending",
		RuntimeID: rt,
		SessionID: sessText("sess-mid-flight"),
		WorkDir:   sessText("/work/proj"),
	}

	sessionID, workDir := OwnPinnedSession(event, rt)
	if sessionID != "sess-mid-flight" {
		t.Fatalf("session = %q, want the session pinned on the row", sessionID)
	}
	if workDir != "/work/proj" {
		t.Fatalf("work dir = %q, want the row's pinned work dir", workDir)
	}
}

// A forked lane gets a new task row with no pinned session, so it starts cold.
// This is the property that keeps N lanes from resuming one mutable session file
// under N runtimes.
func TestForkedLaneTaskHasNoSessionToContinue(t *testing.T) {
	laneRuntime := runtimeUUID(t, 2)
	lane := db.AgentInboxEvent{Status: "pending", RuntimeID: laneRuntime}

	sessionID, workDir := OwnPinnedSession(lane, laneRuntime)
	if sessionID != "" || workDir != "" {
		t.Fatalf("lane got session %q / work dir %q, want both empty", sessionID, workDir)
	}
}

// The source task's session belongs to the source runtime. A lane claiming with
// its own runtime must not inherit it even if it somehow reads that row, because
// a session file is tied to the disk and process it was written by.
func TestSessionIsNotHandedToADifferentRuntime(t *testing.T) {
	sourceRuntime := runtimeUUID(t, 1)
	laneRuntime := runtimeUUID(t, 2)
	source := db.AgentInboxEvent{
		Status:    "pending",
		RuntimeID: sourceRuntime,
		SessionID: sessText("sess-src"),
		WorkDir:   sessText("/work/src"),
	}

	sessionID, workDir := OwnPinnedSession(source, laneRuntime)
	if sessionID != "" || workDir != "" {
		t.Fatalf("cross-runtime got session %q / work dir %q, want both empty", sessionID, workDir)
	}
}

// force_fresh_session is a deliberate instruction that the prior conversation is
// bad. It has to beat the row's own pinned session, or a manual rerun would
// replay the state the user rejected.
func TestForceFreshSessionBeatsTheRowsOwnSession(t *testing.T) {
	rt := runtimeUUID(t, 1)
	event := db.AgentInboxEvent{
		Status:            "pending",
		RuntimeID:         rt,
		SessionID:         sessText("sess-poisoned"),
		WorkDir:           sessText("/work/proj"),
		ForceFreshSession: true,
	}

	sessionID, workDir := OwnPinnedSession(event, rt)
	if sessionID != "" || workDir != "" {
		t.Fatalf("forced-fresh got session %q / work dir %q, want both empty", sessionID, workDir)
	}
}

// A terminal row's session is the cross-task lookups' business, and they filter
// out poisoned outcomes (iteration_limit, api_invalid_request, ...) before
// resuming. Reading a terminal row's session here would bypass that filter.
func TestTerminalTaskSessionIsLeftToTheFilteredLookups(t *testing.T) {
	rt := runtimeUUID(t, 1)
	for _, status := range []string{"acked", "failed", "suppressed"} {
		t.Run(status, func(t *testing.T) {
			event := db.AgentInboxEvent{
				Status:    status,
				RuntimeID: rt,
				SessionID: sessText("sess-maybe-poisoned"),
			}
			if sessionID, _ := OwnPinnedSession(event, rt); sessionID != "" {
				t.Fatalf("terminal %s row handed back %q", status, sessionID)
			}
		})
	}
}

// A work dir with no session is still worth continuing: the checkout is on the
// preserved disk even when the agent never pinned a session.
func TestWorkDirIsReturnedWithoutASession(t *testing.T) {
	rt := runtimeUUID(t, 1)
	event := db.AgentInboxEvent{Status: "draining", RuntimeID: rt, WorkDir: sessText("/work/proj")}

	sessionID, workDir := OwnPinnedSession(event, rt)
	if sessionID != "" {
		t.Fatalf("session = %q, want empty", sessionID)
	}
	if workDir != "/work/proj" {
		t.Fatalf("work dir = %q, want it returned on its own", workDir)
	}
}

func TestNoSessionWithoutARuntime(t *testing.T) {
	event := db.AgentInboxEvent{Status: "pending", SessionID: sessText("sess-1")}

	if sessionID, _ := OwnPinnedSession(event, pgtype.UUID{}); sessionID != "" {
		t.Fatalf("session = %q, want empty when the runtime is unknown", sessionID)
	}
}
