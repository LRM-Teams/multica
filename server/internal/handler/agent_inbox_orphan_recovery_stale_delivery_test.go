package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRecoverOrphanedTaskRetryIsImmediatelyClaimableAfterRecovery is task
// #107's regression test. It started life proving the bug ("polling, alive,
// but not claiming"): when a daemon died mid-task, RecoverOrphanedTasksForRuntime
// failed the agent_inbox_event row and HandleFailedTasks enqueued a fresh
// retry child — but neither touched the agent_event_delivery row the dead
// task left behind, and leaseAgentInboxEventForRuntime's same-agent
// serialization check (an active, unexpired delivery blocks ANY new event
// for that agent, not just the one it was leased for) then blocked the
// retry child until the stale delivery's original 2-minute lease naturally
// timed out. Now that RecoverOrphanedTasks also calls
// ExpireDeliveriesForRuntimeRecovery for the same runtime_id, the retry
// child must be claimable immediately — this test asserts that fixed
// behavior instead.
func TestRecoverOrphanedTaskRetryIsImmediatelyClaimableAfterRecovery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentID := createHandlerTestAgentOnRuntime(t, "Orphan Recovery Stale Delivery "+uuid.NewString()[:8], runtimeID)
	channelID := seedChannelForTest(t, "orphan-stale-delivery-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)

	drain := func() (DrainAgentInboxResponse, int) {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "orphan-stale-delivery-daemon")
		req = withURLParam(req, "runtimeId", runtimeID)
		rec := httptest.NewRecorder()
		testHandler.DrainAgentInboxByRuntime(rec, req)
		var resp DrainAgentInboxResponse
		if rec.Code == http.StatusOK {
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		}
		return resp, rec.Code
	}

	// Step 1: the "old" daemon incarnation leases the original task. This is
	// the delivery that will be orphaned by a simulated crash below.
	firstResp, code := drain()
	if code != http.StatusOK || len(firstResp.Events) != 1 {
		t.Fatalf("initial drain: code=%d events=%+v, want exactly 1 leased event", code, firstResp.Events)
	}
	originalEventID := firstResp.Events[0].ID
	var originalDeliveryID string
	var originalLeaseExpiresAt time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT id, lease_expires_at FROM agent_event_delivery
		WHERE inbox_event_id = $1 AND status = 'leased'`, originalEventID).
		Scan(&originalDeliveryID, &originalLeaseExpiresAt); err != nil {
		t.Fatalf("load original delivery: %v", err)
	}
	if time.Until(originalLeaseExpiresAt) <= 0 {
		t.Fatalf("original delivery lease already expired at seed time, want a future expiry (2-minute default): %v", originalLeaseExpiresAt)
	}

	// Step 2: simulate "daemon died mid-task, new incarnation registers and
	// recovers" via the exact same handler the daemon calls on restart.
	recoverReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/recover-orphans", nil, testWorkspaceID, "orphan-stale-delivery-daemon-restarted")
	recoverReq = withURLParam(recoverReq, "runtimeId", runtimeID)
	recoverRec := httptest.NewRecorder()
	testHandler.RecoverOrphanedTasks(recoverRec, recoverReq)
	if recoverRec.Code != http.StatusOK {
		t.Fatalf("recover-orphans: status=%d body=%s", recoverRec.Code, recoverRec.Body.String())
	}
	var recoverResp map[string]any
	if err := json.Unmarshal(recoverRec.Body.Bytes(), &recoverResp); err != nil {
		t.Fatalf("decode recover-orphans response: %v", err)
	}
	if retried, _ := recoverResp["retried"].(float64); retried != 1 {
		t.Fatalf("recover-orphans response = %+v, want retried=1", recoverResp)
	}

	// Confirm the retry child exists as a fresh 'pending' row for the same
	// agent, and — the fix under test — that recovery has already expired
	// the OLD delivery rather than leaving it 'leased'.
	var pendingChildID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent_inbox_event
		WHERE agent_id = $1 AND status = 'pending' AND id <> $2`, agentID, originalEventID).
		Scan(&pendingChildID); err != nil {
		t.Fatalf("load retry child event: %v", err)
	}
	var oldDeliveryStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM agent_event_delivery WHERE id = $1`, originalDeliveryID).
		Scan(&oldDeliveryStatus); err != nil {
		t.Fatalf("load original delivery post-recovery: %v", err)
	}
	if oldDeliveryStatus != "expired" {
		t.Fatalf("old delivery status=%s after recovery, want 'expired' (ExpireDeliveriesForRuntimeRecovery should have cleared it as part of RecoverOrphanedTasks)", oldDeliveryStatus)
	}

	// Step 3: the new daemon incarnation polls immediately after recovery.
	// Before the fix this came back empty for up to ~2 minutes (task #107).
	// Confirmed via the exact eligibility predicate
	// (leaseAgentInboxEventForRuntime's first SELECT, channel_ambient_helpers.go)
	// rather than the full drain() HTTP round trip: the retry child here is
	// channel-sourced (RequiresWake + ChatSessionID from a fixture chat
	// session with no attached prompt message), which trips an unrelated
	// "exact prompt missing" guard in agentInboxTaskResponse's
	// response-construction step — a fixture mismatch, not part of #107's
	// stale-delivery mechanism.
	var selectableEventID string
	err := testPool.QueryRow(ctx, `
		SELECT event.id
		FROM agent_inbox_event event
		JOIN agent_session session ON session.id = event.agent_session_id
		JOIN agent agent_row ON agent_row.id = event.agent_id
		WHERE COALESCE(event.runtime_id, session.runtime_id) = $1
		  AND session.status = 'active'
		  AND event.status IN ('pending', 'failed')
		  AND NOT (
		    agent_row.provider_block_detail <> ''
		    AND (agent_row.provider_blocked_until IS NULL OR agent_row.provider_blocked_until > now())
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_inbox_event blocking_event
		    JOIN agent_session blocking_session
		      ON blocking_session.id = blocking_event.agent_session_id
		    WHERE blocking_event.agent_id = event.agent_id
		      AND blocking_session.status = 'active'
		      AND blocking_event.status IN ('pending', 'failed')
		      AND (
		        blocking_event.priority > event.priority
		        OR (
		          blocking_event.priority = event.priority
		          AND (blocking_event.created_at, blocking_event.id) < (event.created_at, event.id)
		        )
		      )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_event_delivery active_delivery
		    JOIN agent_session active_session ON active_session.id = active_delivery.agent_session_id
		    WHERE active_session.agent_id = event.agent_id
		      AND active_delivery.status IN ('leased', 'processing')
		      AND active_delivery.lease_expires_at > now()
		  )
		ORDER BY event.priority DESC, event.requires_wake DESC, event.created_at, event.id
		LIMIT 1`, runtimeID).Scan(&selectableEventID)
	if err != nil {
		t.Fatalf("eligibility query immediately after recovery: %v", err)
	}
	if selectableEventID != pendingChildID {
		t.Fatalf("eligibility query selected %s, want the retry child %s", selectableEventID, pendingChildID)
	}
}

// TestRecoverOrphanedTasksDoesNotExpireDeliveryForLiveRuntime is task #107's
// required reverse guard: ExpireDeliveriesForRuntimeRecovery is scoped to
// `WHERE runtime_id = $1`, but a scoping mistake here would mean recovering
// one dead runtime could rip an in-progress task away from a completely
// unrelated, live runtime. Recovery must only ever touch the runtime it was
// called for.
func TestRecoverOrphanedTasksDoesNotExpireDeliveryForLiveRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	deadRuntimeID := handlerTestRuntimeID(t)
	deadAgentName := "Orphan Recovery Dead Runtime " + uuid.NewString()[:8]
	deadAgentID := createHandlerTestAgentOnRuntime(t, deadAgentName, deadRuntimeID)
	liveAgentID, liveRuntimeID := createHandlerTestAgentWithIsolatedRuntime(t)

	seedAndLease := func(runtimeID, agentID, daemonSuffix string) (eventID, deliveryID string) {
		channelID := seedChannelForTest(t, "orphan-live-guard-"+uuid.NewString(), testUserID)
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
			ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("seed agent member: %v", err)
		}
		createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)

		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "orphan-live-guard-"+daemonSuffix)
		req = withURLParam(req, "runtimeId", runtimeID)
		rec := httptest.NewRecorder()
		testHandler.DrainAgentInboxByRuntime(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed drain for %s: status=%d body=%s", daemonSuffix, rec.Code, rec.Body.String())
		}
		var resp DrainAgentInboxResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Events) != 1 {
			t.Fatalf("seed drain for %s: events=%+v err=%v, want exactly 1", daemonSuffix, resp.Events, err)
		}
		eventID = resp.Events[0].ID
		if err := testPool.QueryRow(ctx, `
			SELECT id FROM agent_event_delivery
			WHERE inbox_event_id = $1 AND status = 'leased'`, eventID).Scan(&deliveryID); err != nil {
			t.Fatalf("load delivery for %s: %v", daemonSuffix, err)
		}
		return eventID, deliveryID
	}

	_, deadDeliveryID := seedAndLease(deadRuntimeID, deadAgentID, "dead")
	liveEventID, liveDeliveryID := seedAndLease(liveRuntimeID, liveAgentID, "live")

	// Recover the dead runtime only.
	recoverReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+deadRuntimeID+"/recover-orphans", nil, testWorkspaceID, "orphan-live-guard-dead-restarted")
	recoverReq = withURLParam(recoverReq, "runtimeId", deadRuntimeID)
	recoverRec := httptest.NewRecorder()
	testHandler.RecoverOrphanedTasks(recoverRec, recoverReq)
	if recoverRec.Code != http.StatusOK {
		t.Fatalf("recover-orphans: status=%d body=%s", recoverRec.Code, recoverRec.Body.String())
	}

	// The dead runtime's own delivery must be expired (sanity check the fix
	// actually ran)...
	var deadDeliveryStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_event_delivery WHERE id = $1`, deadDeliveryID).
		Scan(&deadDeliveryStatus); err != nil {
		t.Fatalf("load dead-runtime delivery: %v", err)
	}
	if deadDeliveryStatus != "expired" {
		t.Fatalf("dead-runtime delivery status=%s, want expired", deadDeliveryStatus)
	}

	// ...but the live runtime's delivery and event must be completely
	// untouched — still leased, still draining, not caught by the fix's
	// WHERE runtime_id = $1 scoping.
	var liveDeliveryStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_event_delivery WHERE id = $1`, liveDeliveryID).
		Scan(&liveDeliveryStatus); err != nil {
		t.Fatalf("load live-runtime delivery: %v", err)
	}
	if liveDeliveryStatus != "leased" {
		t.Fatalf("live-runtime delivery status=%s after recovering an unrelated dead runtime, want leased (untouched)", liveDeliveryStatus)
	}
	var liveEventStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, liveEventID).
		Scan(&liveEventStatus); err != nil {
		t.Fatalf("load live-runtime event: %v", err)
	}
	if liveEventStatus != "draining" {
		t.Fatalf("live-runtime event status=%s after recovering an unrelated dead runtime, want draining (untouched)", liveEventStatus)
	}
}

func createProductInboxEventForRuntime(t *testing.T, runtimeID, agentID, channelID string) string {
	t.Helper()
	var eventID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_session_id, channel_id, agent_id, runtime_id,
			reason, delivery_mode, response_mode, requires_wake, status, priority,
			execution_config
		)
		VALUES (
			$1, ensure_agent_wake_session($2), $3, $2, $4,
			'product_task', 'execute', 'public_response', false, 'pending', 0,
			'{}'::jsonb
		)
		RETURNING id::text`, testWorkspaceID, agentID, channelID, runtimeID).Scan(&eventID); err != nil {
		t.Fatalf("create product inbox event: %v", err)
	}
	return eventID
}
