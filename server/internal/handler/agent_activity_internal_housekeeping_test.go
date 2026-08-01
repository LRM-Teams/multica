package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// Task #48: agent_reassigned_elsewhere (#1628) is a stale daemon correctly
// recognizing it lost ownership of an agent and stopping its retry loop —
// working-as-designed internal bookkeeping, not something the user needs to
// react to. Before this fix it was recorded as an ordinary activityKindError
// event and shown raw in the user's activity feed ("Agent inbox delivery
// failed: agent_reassigned_elsewhere: ..."), which reads exactly like a
// crash. This pins the classification and the two read-path enforcement
// points that must respect it.
func TestActivityVisibilityFor_AgentReassignedElsewhereIsDiagnosticOnly(t *testing.T) {
	if got := activityVisibilityFor(activityKindError, "", "error", "agent_reassigned_elsewhere"); got != "diagnostic_only" {
		t.Fatalf("agent_reassigned_elsewhere visibility = %q, want diagnostic_only", got)
	}
	// A real agent error (e.g. the provider process crashing) must remain
	// user-facing — this fix must not silence genuine failures.
	if got := activityVisibilityFor(activityKindError, "", "error", "process_failure"); got != "user_facing" {
		t.Fatalf("genuine process_failure visibility = %q, want user_facing (this fix must not silence real errors)", got)
	}
}

// Task #62: restarted_by_user is the same shape as agent_reassigned_elsewhere
// one layer down — a plain restart force-kills the resident process, the
// interrupted turn's own goroutine reports that through the normal
// task-failure path (identical to a real crash from its side), and without
// this classification the user would see their own requested restart show
// up in their activity feed as an unexplained crash.
func TestActivityVisibilityFor_RestartedByUserIsDiagnosticOnly(t *testing.T) {
	if got := activityVisibilityFor(activityKindError, "", "error", "restarted_by_user"); got != "diagnostic_only" {
		t.Fatalf("restarted_by_user visibility = %q, want diagnostic_only", got)
	}
}

func TestAgentActivityTimelineRowIsNarrative_RespectsDiagnosticOnlyVisibilityEvenForErrorKind(t *testing.T) {
	diagnosticErrorRow := agentActivityRawRow{
		Kind:       activityKindError,
		ReasonCode: pgtype.Text{String: "agent_reassigned_elsewhere", Valid: true},
		Visibility: pgtype.Text{String: "diagnostic_only", Valid: true},
	}
	if agentActivityTimelineRowIsNarrative(diagnosticErrorRow) {
		t.Fatalf("diagnostic_only error row must not be narrative, regardless of event kind")
	}
	genuineErrorRow := agentActivityRawRow{
		Kind:       activityKindError,
		Visibility: pgtype.Text{String: "user_facing", Valid: true},
	}
	if !agentActivityTimelineRowIsNarrative(genuineErrorRow) {
		t.Fatalf("user_facing error row must remain narrative")
	}
}

// TestAgentInboxFailureActivity_AgentReassignedElsewhereHiddenFromUserFeed
// exercises the real production path end to end: insertAgentActivityEvent
// (what recordAgentInboxFailureActivity calls when a daemon reports
// agent_reassigned_elsewhere) through to ListAgentActivity, the endpoint
// backing the agent's user-facing Activity tab.
func TestAgentInboxFailureActivity_AgentReassignedElsewhereHiddenFromUserFeed(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createWorkspaceVisibleActivityAgent(t, "activity-reassigned-agent")

	eventID, ok := insertAgentActivityEvent(context.Background(), testPool,
		parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindError, "", "error",
		"agent", parseUUID(agentID), "",
		"agent_reassigned_elsewhere", "Agent inbox delivery failed: agent_reassigned_elsewhere: this agent is now running on a different computer; no action needed on this machine",
		map[string]any{"failure_reason": "agent_reassigned_elsewhere"},
	)
	if !ok {
		t.Fatalf("insertAgentActivityEvent failed")
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_activity_event WHERE id = $1`, uuidToString(eventID))
	})

	var visibility string
	if err := testPool.QueryRow(context.Background(), `SELECT visibility FROM agent_activity_event WHERE id = $1`, uuidToString(eventID)).Scan(&visibility); err != nil {
		t.Fatalf("read back visibility: %v", err)
	}
	if visibility != "diagnostic_only" {
		t.Fatalf("stored visibility = %q, want diagnostic_only", visibility)
	}

	list := listAgentActivityForUser(t, testUserID, agentID, "")
	if got := findActivityItem(list, uuidToString(eventID)); got != nil {
		t.Fatalf("agent_reassigned_elsewhere must not appear in the user-facing Activity feed: %+v", *got)
	}

	events := listAgentActivityEventsForUser(t, testUserID, agentID, "")
	for _, e := range events.resp.Events {
		if e.ID == uuidToString(eventID) {
			t.Fatalf("agent_reassigned_elsewhere must not appear in the user-facing Activity events feed: %+v", e)
		}
	}
}
