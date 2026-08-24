package researchrun

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestV6WorkActivityIsDurableIdempotentAndTaskScoped(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Persist task-scoped V6 work activity")
	membershipID, firstWorkID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	firstAttemptID := seedV6RecoveryAttempt(t, run, membershipID, firstWorkID)

	secondWorkID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `
		INSERT INTO research_work_item (
			id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,
			idempotency_key,lease_token,lease_expires_at,payload_schema_id,state_version
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'research','running',$4::uuid,1,$5,$6::uuid,$7,'schema',1)`,
		secondWorkID, run.fixture.workspaceID, run.fixture.sessionID, run.fixture.agentID,
		"test:"+secondWorkID, uuid.NewString(), time.Now().Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	secondAttemptID := seedV6RecoveryAttempt(t, run, membershipID, secondWorkID)

	firstInboxID := attachV6WorkActivityInbox(t, run, firstAttemptID)
	secondInboxID := attachV6WorkActivityInbox(t, run, secondAttemptID)
	firstMessage := protocol.TaskMessagePayload{
		TaskID: firstInboxID, Seq: 1, Type: "tool_use", Tool: "bash",
		Visibility: "user_facing", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Input: map[string]any{"command": "pnpm test", "secret": "do not persist"},
	}
	if err := run.store.RecordV6WorkActivity(run.ctx, run.fixture.workspaceID, firstInboxID, []protocol.TaskMessagePayload{firstMessage}); err != nil {
		t.Fatal(err)
	}
	if err := run.store.RecordV6WorkActivity(run.ctx, run.fixture.workspaceID, firstInboxID, []protocol.TaskMessagePayload{firstMessage}); err != nil {
		t.Fatal(err)
	}
	if err := run.store.RecordV6WorkActivity(run.ctx, run.fixture.workspaceID, secondInboxID, []protocol.TaskMessagePayload{{
		TaskID: secondInboxID, Seq: 1, Type: "tool_use", Tool: "read_file",
		Visibility: "user_facing", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Input: map[string]any{"path": "/other-task/private.txt"},
	}}); err != nil {
		t.Fatal(err)
	}

	activity, err := run.store.ProjectionV6WorkActivity(run.ctx, run.fixture.workspaceID, run.fixture.sessionID, firstWorkID)
	if err != nil {
		t.Fatal(err)
	}
	if activity.AttemptID != firstAttemptID || activity.InboxTaskID != firstInboxID {
		t.Fatalf("activity identity = attempt %q inbox %q", activity.AttemptID, activity.InboxTaskID)
	}
	if len(activity.Timeline) != 1 {
		t.Fatalf("timeline = %+v, want one idempotently persisted row", activity.Timeline)
	}
	row := activity.Timeline[0]
	if row.Title != "Running command" || row.Subtext != "pnpm test" || row.BodyKind != "command" {
		t.Fatalf("timeline row = %+v", row)
	}
	if strings.Contains(row.Subtext, "do not persist") || strings.Contains(row.Subtext, "/other-task/") {
		t.Fatalf("timeline leaked raw or another task's activity: %+v", row)
	}
}

func TestRecordV6WorkActivityTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6WorkActivityRecord, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
		attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
		inboxID := attachV6WorkActivityInbox(t, run, attemptID)
		message := protocol.TaskMessagePayload{
			TaskID: inboxID, Seq: 1, Type: "tool_use", Tool: "bash",
			Visibility: "user_facing", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Input: map[string]any{"command": "pnpm test"},
		}
		invoke := func() error {
			return run.store.RecordV6WorkActivity(run.ctx, run.fixture.workspaceID, inboxID, []protocol.TaskMessagePayload{message})
		}
		count := func() int {
			var value int
			if err := run.pool.QueryRow(run.ctx, `
				SELECT count(*)::int FROM research_work_item_activity_entry
				WHERE workspace_id=$1::uuid AND work_item_attempt_id=$2::uuid
			`, run.fixture.workspaceID, attemptID).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				if got := count(); got != 0 {
					t.Fatalf("work activity rows=%d after rollback", got)
				}
			},
			assertCommitted: func() {
				if got := count(); got != 1 {
					t.Fatalf("work activity rows=%d, want 1", got)
				}
			},
			recover: invoke,
		}
	})
}

func attachV6WorkActivityInbox(t *testing.T, run *transactionRecoveryRun, attemptID string) string {
	t.Helper()
	inboxID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `
		INSERT INTO agent_inbox_event (
			id,workspace_id,agent_id,reason,status,context,started_at
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'research_dispatch','running',
		          '{"type":"research_run_work_item"}'::jsonb,now())`,
		inboxID, run.fixture.workspaceID, run.fixture.agentID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `
		UPDATE research_work_item_attempt
		SET inbox_task_id=$2::uuid,started_at=now(),updated_at=now()
		WHERE id=$1::uuid`, attemptID, inboxID,
	); err != nil {
		t.Fatal(err)
	}
	return inboxID
}
