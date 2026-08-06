package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestNormalizeResearchInboxTaskStateTreatsReplyWithoutResultAsCompletedExecution(t *testing.T) {
	status, retryable, failure := normalizeResearchInboxTaskState("acked", "replied", false, true, "")
	if status != "completed" || retryable || failure != "" {
		t.Fatalf("state=%q retryable=%v failure=%q, want completed execution", status, retryable, failure)
	}
}

func TestProjectedResearchActorUsesAssignedAgentFromSystemTaskEvent(t *testing.T) {
	agentID := uuid.NewString()
	actor := projectedResearchActorAgentID(researchrun.RunEvent{ActorType: "system"}, map[string]any{
		"agent_id": agentID,
	})
	if got := uuidToString(actor); got != agentID {
		t.Fatalf("actor=%q, want %q", got, agentID)
	}
}

func TestNormalizeResearchInboxTaskStatePreservesActualFailures(t *testing.T) {
	status, retryable, failure := normalizeResearchInboxTaskState("acked", "failed", true, true, "provider unavailable")
	if status != "failed" || !retryable || failure != "provider unavailable" {
		t.Fatalf("state=%q retryable=%v failure=%q", status, retryable, failure)
	}
}

func TestResearchRunDispatcherInspectTreatsRepliedTaskAsCompletedExecution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedAgentCredentialTransportFixture(t)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'replied', terminal_at = now(),
		    completed_at = now(), acked_at = now(), retryable = false
		WHERE id = $1::uuid
	`, fixture.event.ID); err != nil {
		t.Fatalf("complete inbox event: %v", err)
	}
	states, err := (&researchRunDispatcher{handler: testHandler}).Inspect(context.Background(), []string{fixture.event.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := states[fixture.event.ID]; got.Status != "completed" {
		t.Fatalf("state=%+v, want completed execution", got)
	}
}

func TestResearchRunDispatcherInspectExposesRuntimeStartLeaseAndCancellationAcknowledgement(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedAgentCredentialTransportFixture(t)
	dispatcher := &researchRunDispatcher{handler: testHandler}
	ctx := context.Background()

	states, err := dispatcher.Inspect(ctx, []string{fixture.event.ID})
	if err != nil {
		t.Fatal(err)
	}
	queued := states[fixture.event.ID]
	if queued.Status != "running" || queued.StartedAt != nil || !queued.HasActiveLease || queued.LeaseExpiresAt == nil || queued.ObservedAt.IsZero() {
		t.Fatalf("claimed runtime state=%+v", queued)
	}

	startedAt := time.Now().UTC().Add(-time.Minute)
	if _, err = testPool.Exec(ctx, `UPDATE agent_inbox_event SET started_at = $2 WHERE id = $1::uuid`, fixture.event.ID, startedAt); err != nil {
		t.Fatal(err)
	}
	states, err = dispatcher.Inspect(ctx, []string{fixture.event.ID})
	if err != nil {
		t.Fatal(err)
	}
	running := states[fixture.event.ID]
	if running.StartedAt == nil || running.StartedAt.Sub(startedAt).Abs() > time.Millisecond || !running.HasActiveLease {
		t.Fatalf("running state=%+v", running)
	}

	if _, err = testHandler.TaskService.CancelTask(ctx, parseUUID(fixture.event.ID)); err != nil {
		t.Fatal(err)
	}
	states, err = dispatcher.Inspect(ctx, []string{fixture.event.ID})
	if err != nil {
		t.Fatal(err)
	}
	cancelRequested := states[fixture.event.ID]
	if cancelRequested.Status != "cancelled" || !cancelRequested.HasActiveLease {
		t.Fatalf("cancel request was mistaken for runtime acknowledgement: %+v", cancelRequested)
	}

	if _, err = testPool.Exec(ctx, `
		UPDATE agent_event_delivery
		SET lease_expires_at = now() - interval '1 second'
		WHERE inbox_event_id = $1::uuid AND status IN ('leased', 'processing')
	`, fixture.event.ID); err != nil {
		t.Fatal(err)
	}
	states, err = dispatcher.Inspect(ctx, []string{fixture.event.ID})
	if err != nil {
		t.Fatal(err)
	}
	acknowledged := states[fixture.event.ID]
	if acknowledged.Status != "cancelled" || acknowledged.HasActiveLease {
		t.Fatalf("expired runtime lease state=%+v", acknowledged)
	}
}

func TestProjectResearchAttemptFailureUsesDiagnostics(t *testing.T) {
	event := researchrun.RunEvent{Type: "task_attempt_failed"}
	_, _, summary, _ := projectResearchEvent(event, db.ResearchSession{}, map[string]any{
		"failure_class": "result_not_submitted",
		"diagnostics":   "Agent completed the turn without submitting a structured research result.",
	})
	if summary != "Agent completed the turn without submitting a structured research result." {
		t.Fatalf("summary=%q", summary)
	}
}
