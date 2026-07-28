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

// #777 follow-up (product kill after #1257 merged pre-kill head):
// migration 234 strips wendy_ambient authorization; lease hard gate + durable
// poison terminalize remain. Real-PG cases prove ambient stays unauthorized
// even for channel members, never inserts a delivery, and unblocks later wakes.
func TestRadarAmbientEventAuthorizationAndLeaseGate(t *testing.T) {
	if testHandler == nil || testPool == nil || testHandler.TaskService == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	t.Run("channel-member ambient stays unauthorized and terminalizes under kill path", func(t *testing.T) {
		agentID, runtimeID, _ := createRuntimeGuardAgent(t, ctx)
		channelID := seedChannelForTest(t, "radar-ambient-kill-"+uuid.NewString()[:8], testUserID)
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)

ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add agent channel member: %v", err)
		}

		run, task, err := testHandler.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
			WorkspaceID:    parseUUID(testWorkspaceID),
			AgentID:        parseUUID(agentID),
			ChannelID:      parseUUID(channelID),
			ContextMode:    "coordination",
			TriggerKind:    "event",
			TriggerRef:     "task-777-kill-ambient",
			CooldownKey:    "wendy_ambient:" + channelID,
			ContextSummary: "kill-path ambient radar",
			ScheduledFor:   time.Now(),
			Prompt:         "ambient review",
		})
		if err != nil {
			t.Fatalf("enqueue ambient radar: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_radar_run WHERE id = $1`, run.ID)
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, task.ID)
		})

		var authorized bool
		if err := testPool.QueryRow(ctx, `SELECT workspace_radar_task_is_authorized($1)`, task.ID).Scan(&authorized); err != nil {
			t.Fatalf("authorization probe: %v", err)
		}
		if authorized {
			t.Fatal("kill path: event ambient must stay unauthorized after migration 234")
		}

		runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
		if err != nil {
			t.Fatalf("load runtime: %v", err)
		}
		if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("member ambient lease err=%v, want no rows (terminalize, no delivery)", err)
		}

		var status, reason string
		var deliveries int
		if err := testPool.QueryRow(ctx, `
			SELECT status, COALESCE(failure_reason, '')
			FROM agent_inbox_event WHERE id = $1
		`, task.ID).Scan(&status, &reason); err != nil {
			t.Fatalf("read ambient status: %v", err)
		}
		if status != "acked" || reason != "radar_unauthorized" {
			t.Fatalf("ambient status/reason=%s/%s, want acked/radar_unauthorized", status, reason)
		}
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM agent_event_delivery WHERE inbox_event_id = $1
		`, task.ID).Scan(&deliveries); err != nil {
			t.Fatalf("count deliveries: %v", err)
		}
		if deliveries != 0 {
			t.Fatalf("delivery count=%d, want 0", deliveries)
		}
	})

	t.Run("high-priority wake bypasses unauthorized ambient and poison terminalizes after active delivery", func(t *testing.T) {
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

		// Younger, higher-priority directed wake must not stay stuck behind the
		// low-priority poison.
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

		// Priority admission leases the directed wake first. The poison remains
		// pending until the active delivery settles; it must not be delivered.
		delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
		if err != nil {
			t.Fatalf("lease after poison head: %v", err)
		}
		if uuidToString(delivery.InboxEventID) != directedID {
			t.Fatalf("leased event=%s, want directed wake %s", uuidToString(delivery.InboxEventID), directedID)
		}

		var poisonStatus, poisonReason string
		if err := testPool.QueryRow(ctx, `
			SELECT status, COALESCE(failure_reason, '')
			FROM agent_inbox_event WHERE id = $1
		`, poison.ID).Scan(&poisonStatus, &poisonReason); err != nil {
			t.Fatalf("read queued poison status: %v", err)
		}
		if poisonStatus != "pending" || poisonReason != "" {
			t.Fatalf("queued poison status/reason=%s/%s, want pending/empty", poisonStatus, poisonReason)
		}

		// A live delivery still excludes all other work for the Agent.
		if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("second lease err=%v, want no rows while directed delivery active", err)
		}
		settleClaimedInboxEventForTest(t, directedID)

		// The next poll terminalizes the remaining unauthorized poison and
		// commits that cleanup even though it returns no delivery.
		if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("poison cleanup lease err=%v, want no rows", err)
		}

		var runStatus string
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
	})

	// Barry #778 CODE BLOCK: poison-only must commit terminalize, not defer-rollback.
	t.Run("poison-only terminalize is durable without a following wake", func(t *testing.T) {
		agentID, runtimeID, _ := createRuntimeGuardAgent(t, ctx)
		channelID := seedChannelForTest(t, "radar-ambient-poison-only-"+uuid.NewString()[:8], testUserID)

		run, poison, err := testHandler.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
			WorkspaceID:    parseUUID(testWorkspaceID),
			AgentID:        parseUUID(agentID),
			ChannelID:      parseUUID(channelID),
			ContextMode:    "coordination",
			TriggerKind:    "event",
			TriggerRef:     "task-777-poison-only",
			CooldownKey:    "wendy_ambient:" + channelID,
			ContextSummary: "poison only",
			ScheduledFor:   time.Now().Add(-time.Minute),
			Prompt:         "poison only",
		})
		if err != nil {
			t.Fatalf("enqueue poison-only ambient: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_radar_run WHERE id = $1`, run.ID)
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, poison.ID)
		})

		runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
		if err != nil {
			t.Fatalf("load runtime: %v", err)
		}
		if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("poison-only lease err=%v, want no rows", err)
		}

		var status, reason, runStatus string
		var deliveries int
		if err := testPool.QueryRow(ctx, `
			SELECT status, COALESCE(failure_reason, '')
			FROM agent_inbox_event WHERE id = $1
		`, poison.ID).Scan(&status, &reason); err != nil {
			t.Fatalf("read poison-only status: %v", err)
		}
		if status != "acked" || reason != "radar_unauthorized" {
			t.Fatalf("poison-only status/reason=%s/%s, want acked/radar_unauthorized (must not rollback)", status, reason)
		}
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM agent_event_delivery WHERE inbox_event_id = $1
		`, poison.ID).Scan(&deliveries); err != nil {
			t.Fatalf("count poison-only deliveries: %v", err)
		}
		if deliveries != 0 {
			t.Fatalf("poison-only deliveries=%d, want 0", deliveries)
		}
		if err := testPool.QueryRow(ctx, `SELECT status FROM agent_radar_run WHERE id = $1`, run.ID).Scan(&runStatus); err != nil {
			t.Fatalf("read poison-only run: %v", err)
		}
		if runStatus != "failed" {
			t.Fatalf("poison-only run status=%q, want failed", runStatus)
		}
	})

	// Cap is 8 loop steps (attempts 0..8 ⇒ up to 9 durable cleanups per drain).
	// Seed more than one drain can clear; first poll must leave a durable partial
	// batch, second poll finishes the rest — never whole-batch rollback.
	t.Run("poison batch over cap is durable across polls", func(t *testing.T) {
		const poisonCount = 12 // > maxPoisonTerminalizations+1 (9)
		// One shared runtime, many agents (one active radar run per agent).
		runtimeID := createClaimReclaimRuntime(t, ctx, "poison-cap-runtime-"+uuid.NewString()[:8])
		type poisonRow struct {
			taskID string
			runID  string
		}
		poisons := make([]poisonRow, 0, poisonCount)
		for i := 0; i < poisonCount; i++ {
			var agentID string
			if err := testPool.QueryRow(ctx, `
				INSERT INTO agent (
				  workspace_id, name, runtime_mode, runtime_config,
				  runtime_id, visibility, max_concurrent_tasks, owner_id
				)
				VALUES ($1, $2, 'local', '{}'::jsonb, $3, 'workspace', 1, $4)
				RETURNING id
			`, testWorkspaceID, "Poison Cap Agent "+uuid.NewString()[:8], runtimeID, testUserID).Scan(&agentID); err != nil {
				t.Fatalf("create poison agent %d: %v", i, err)
			}
			t.Cleanup(func() {
				_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
			})
			channelID := seedChannelForTest(t, "radar-poison-cap-"+uuid.NewString()[:8], testUserID)
			run, task, err := testHandler.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
				WorkspaceID:    parseUUID(testWorkspaceID),
				AgentID:        parseUUID(agentID),
				ChannelID:      parseUUID(channelID),
				ContextMode:    "coordination",
				TriggerKind:    "event",
				TriggerRef:     "task-777-poison-cap",
				CooldownKey:    "wendy_ambient:" + channelID,
				ContextSummary: "poison cap",
				ScheduledFor:   time.Now().Add(-time.Duration(poisonCount-i) * time.Second),
				Prompt:         "poison cap",
			})
			if err != nil {
				t.Fatalf("enqueue poison cap %d: %v", i, err)
			}
			// Force older created_at so FIFO ordering is deterministic across agents.
			if _, err := testPool.Exec(ctx, `
				UPDATE agent_inbox_event
				SET created_at = now() - make_interval(secs => $2)
				WHERE id = $1
			`, task.ID, poisonCount-i); err != nil {
				t.Fatalf("backdate poison %d: %v", i, err)
			}
			t.Cleanup(func() {
				_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_radar_run WHERE id = $1`, run.ID)
				_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, task.ID)
			})
			poisons = append(poisons, poisonRow{taskID: uuidToString(task.ID), runID: uuidToString(run.ID)})
		}

		runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
		if err != nil {
			t.Fatalf("load runtime: %v", err)
		}

		countByStatus := func() (acked, pending int) {
			t.Helper()
			ids := make([]string, len(poisons))
			for i, p := range poisons {
				ids[i] = p.taskID
			}
			if err := testPool.QueryRow(ctx, `
				SELECT
				  count(*) FILTER (WHERE status = 'acked' AND failure_reason = 'radar_unauthorized'),
				  count(*) FILTER (WHERE status IN ('pending', 'failed'))
				FROM agent_inbox_event
				WHERE id = ANY($1::uuid[])
			`, ids).Scan(&acked, &pending); err != nil {
				t.Fatalf("count poison statuses: %v", err)
			}
			return acked, pending
		}

		if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("first over-cap lease err=%v, want no rows", err)
		}
		acked1, pending1 := countByStatus()
		if acked1 == 0 || pending1 == 0 {
			t.Fatalf("after first poll acked=%d pending=%d, want partial durable cleanup (acked>0 and pending>0)", acked1, pending1)
		}
		if acked1+pending1 != poisonCount {
			t.Fatalf("after first poll acked+pending=%d, want %d", acked1+pending1, poisonCount)
		}

		// Subsequent polls must drain the remainder without re-pending first batch.
		for poll := 0; poll < 4; poll++ {
			if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("follow-up lease poll %d err=%v, want no rows", poll, err)
			}
			acked, pending := countByStatus()
			if acked < acked1 {
				t.Fatalf("poll %d acked=%d regressed below first-batch %d (rollback bug)", poll, acked, acked1)
			}
			if pending == 0 {
				break
			}
		}
		ackedFinal, pendingFinal := countByStatus()
		if pendingFinal != 0 || ackedFinal != poisonCount {
			t.Fatalf("final acked=%d pending=%d, want acked=%d pending=0", ackedFinal, pendingFinal, poisonCount)
		}
		var failedRuns int
		runIDs := make([]string, len(poisons))
		for i, p := range poisons {
			runIDs[i] = p.runID
		}
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM agent_radar_run
			WHERE id = ANY($1::uuid[]) AND status = 'failed'
		`, runIDs).Scan(&failedRuns); err != nil {
			t.Fatalf("count failed runs: %v", err)
		}
		if failedRuns != poisonCount {
			t.Fatalf("failed runs=%d, want %d", failedRuns, poisonCount)
		}
	})
}

// Compile-time guard that db.AgentEventDelivery stays the lease return type.
var _ = db.AgentEventDelivery{}
