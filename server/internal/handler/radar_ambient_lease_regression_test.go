package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// #774 / #777: migration 223 dropped the event+wendy_ambient authorization
// branch; lease ignored UPDATE row-count and leaked deliveries that livelocked
// per-agent FIFO. These real-PG cases prove:
//  1) authorized ambient Radar drains to draining + one delivery
//  2) unauthorized ambient Radar never inserts a delivery, is terminalized,
//     and unblocks a later directed wake on the same agent.
func TestRadarAmbientEventAuthorizationAndLeaseGate(t *testing.T) {
	if testHandler == nil || testPool == nil || testHandler.TaskService == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	t.Run("authorized ambient radar leases", func(t *testing.T) {
		agentID, runtimeID, _ := createRuntimeGuardAgent(t, ctx)
		channelID := seedChannelForTest(t, "radar-ambient-auth-"+uuid.NewString()[:8], testUserID)
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
		`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add agent channel member: %v", err)
		}

		run, task, err := testHandler.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
			WorkspaceID:    parseUUID(testWorkspaceID),
			AgentID:        parseUUID(agentID),
			ChannelID:      parseUUID(channelID),
			ContextMode:    "coordination",
			TriggerKind:    "event",
			TriggerRef:     "task-777-authorized-ambient",
			CooldownKey:    "wendy_ambient:" + channelID,
			ContextSummary: "authorized ambient radar",
			ScheduledFor:   time.Now(),
			Prompt:         "ambient review",
		})
		if err != nil {
			t.Fatalf("enqueue authorized ambient radar: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_radar_run WHERE id = $1`, run.ID)
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, task.ID)
		})

		var authorized bool
		if err := testPool.QueryRow(ctx, `SELECT workspace_radar_task_is_authorized($1)`, task.ID).Scan(&authorized); err != nil {
			t.Fatalf("authorization probe: %v", err)
		}
		if !authorized {
			t.Fatal("expected authorized ambient radar after migration 233 restore")
		}

		runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
		if err != nil {
			t.Fatalf("load runtime: %v", err)
		}
		delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
		if err != nil {
			t.Fatalf("lease authorized ambient: %v", err)
		}
		if uuidToString(delivery.InboxEventID) != uuidToString(task.ID) {
			t.Fatalf("leased event=%s, want ambient task %s", uuidToString(delivery.InboxEventID), uuidToString(task.ID))
		}

		var status string
		var deliveryCount int
		if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, task.ID).Scan(&status); err != nil {
			t.Fatalf("read ambient status: %v", err)
		}
		if status != "draining" {
			t.Fatalf("ambient status=%q, want draining", status)
		}
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM agent_event_delivery WHERE inbox_event_id = $1
		`, task.ID).Scan(&deliveryCount); err != nil {
			t.Fatalf("count deliveries: %v", err)
		}
		if deliveryCount != 1 {
			t.Fatalf("delivery count=%d, want 1", deliveryCount)
		}
	})

	t.Run("unauthorized ambient poison head terminalizes without delivery and unblocks later wake", func(t *testing.T) {
		agentID, runtimeID, _ := createRuntimeGuardAgent(t, ctx)
		// Channel exists for cooldown key shape, but agent is NOT a member → unauthorized.
		channelID := seedChannelForTest(t, "radar-ambient-poison-"+uuid.NewString()[:8], testUserID)

		run, poison, err := testHandler.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
			WorkspaceID:    parseUUID(testWorkspaceID),
			AgentID:        parseUUID(agentID),
			ChannelID:      parseUUID(channelID),
			ContextMode:    "coordination",
			TriggerKind:    "event",
			TriggerRef:     "task-777-poison-ambient",
			CooldownKey:    "wendy_ambient:" + channelID,
			ContextSummary: "unauthorized ambient radar poison",
			ScheduledFor:   time.Now().Add(-2 * time.Minute),
			Prompt:         "poison ambient",
		})
		if err != nil {
			t.Fatalf("enqueue unauthorized ambient radar: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_radar_run WHERE id = $1`, run.ID)
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, poison.ID)
		})

		var authorized bool
		if err := testPool.QueryRow(ctx, `SELECT workspace_radar_task_is_authorized($1)`, poison.ID).Scan(&authorized); err != nil {
			t.Fatalf("authorization probe: %v", err)
		}
		if authorized {
			t.Fatal("expected unauthorized ambient when agent is not a channel member")
		}

		// Younger directed wake that must not stay stuck behind the poison head.
		var sessionID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_session (
			  workspace_id, agent_id, runtime_id, scope, status
			)
			VALUES ($1, $2, $3, 'direct_chat', 'active')
			RETURNING id
		`, testWorkspaceID, agentID, runtimeID).Scan(&sessionID); err != nil {
			t.Fatalf("create agent session: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_session WHERE id = $1`, sessionID)
		})
		var directedID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (
			  workspace_id, agent_session_id, runtime_id, agent_id,
			  reason, requires_wake, status, priority, seq_from, seq_to, created_at
			)
			VALUES ($1, $2, $3, $4, 'dm', true, 'pending', 100, 1, 1, now())
			RETURNING id
		`, testWorkspaceID, sessionID, runtimeID, agentID).Scan(&directedID); err != nil {
			t.Fatalf("create directed wake: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_event_delivery WHERE inbox_event_id = $1`, directedID)
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, directedID)
		})

		runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
		if err != nil {
			t.Fatalf("load runtime: %v", err)
		}

		// One lease call must terminalize the poison head and admit the directed wake.
		delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
		if err != nil {
			t.Fatalf("lease after poison head: %v", err)
		}
		if uuidToString(delivery.InboxEventID) != directedID {
			t.Fatalf("leased event=%s, want directed wake %s", uuidToString(delivery.InboxEventID), directedID)
		}

		var poisonStatus, poisonReason, runStatus string
		var poisonDeliveries int
		if err := testPool.QueryRow(ctx, `
			SELECT status, COALESCE(failure_reason, '')
			FROM agent_inbox_event WHERE id = $1
		`, poison.ID).Scan(&poisonStatus, &poisonReason); err != nil {
			t.Fatalf("read poison status: %v", err)
		}
		if poisonStatus != "acked" || poisonReason != "radar_unauthorized" {
			t.Fatalf("poison status/reason=%s/%s, want acked/radar_unauthorized", poisonStatus, poisonReason)
		}
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM agent_event_delivery WHERE inbox_event_id = $1
		`, poison.ID).Scan(&poisonDeliveries); err != nil {
			t.Fatalf("count poison deliveries: %v", err)
		}
		if poisonDeliveries != 0 {
			t.Fatalf("poison delivery count=%d, want 0 (hard gate must not insert)", poisonDeliveries)
		}
		if err := testPool.QueryRow(ctx, `SELECT status FROM agent_radar_run WHERE id = $1`, run.ID).Scan(&runStatus); err != nil {
			t.Fatalf("read poison radar run: %v", err)
		}
		if runStatus != "failed" {
			t.Fatalf("poison radar run status=%q, want failed", runStatus)
		}

		// Second lease must not re-pick anything while directed delivery is active.
		if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("second lease err=%v, want no rows while directed delivery active", err)
		}
	})
}

// Compile-time guard that db.AgentEventDelivery stays the lease return type.
var _ = db.AgentEventDelivery{}
