package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// TestFailAgentInboxEvent_StickyQuotaWritesProviderBlock is the #1815 write-path
// acceptance Parker asked for on #77: a real inbox fail with usage-limit 429 must
// persist provider_blocked_* — not just classify/display in pure functions.
// Confirm-broken: skip applyAgentProviderQuotaBlock in recordAgentInboxFailureActivity
// and this fails with empty detail.
func TestFailAgentInboxEvent_StickyQuotaWritesProviderBlock(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID, eventID, deliveryID, leaseToken, daemonID := seedLeasedInboxForProviderBlockTest(t)

	stickyErr := `429: {"code":"1310","message":"已达到 7 天使用上限，2026-08-03 13:52:38 后可继续使用。"}`
	failReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+eventID+"/fail", FailAgentInboxEventRequest{
		DeliveryID:    deliveryID,
		LeaseToken:    leaseToken,
		Error:         stickyErr,
		FailureReason: string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		ReasonCode:    string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
	}, testWorkspaceID, daemonID)
	failReq = withURLParam(failReq, "eventId", eventID)
	failRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failRec, failReq)
	if failRec.Code != http.StatusOK {
		t.Fatalf("fail sticky quota: status=%d body=%s", failRec.Code, failRec.Body.String())
	}

	var detail string
	var until *time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT provider_block_detail, provider_blocked_until
		FROM agent WHERE id = $1`, agentID).Scan(&detail, &until); err != nil {
		t.Fatalf("read provider block columns: %v", err)
	}
	if detail == "" {
		t.Fatal("provider_block_detail empty after sticky quota fail — write path did not run")
	}
	if until == nil {
		t.Fatal("provider_blocked_until NULL — stamp in error text should have been parsed")
	}
	wantUntil, ok := taskfailure.ParseProviderBlockedUntil(stickyErr, time.Now(), time.Local)
	if !ok {
		t.Fatal("test fixture stamp must parse")
	}
	if until.UTC().Truncate(time.Second) != wantUntil.UTC().Truncate(time.Second) {
		t.Fatalf("provider_blocked_until = %v, want %v", until.UTC(), wantUntil.UTC())
	}

	// Display wiring: same agent must surface blocked via attachAgentRuntimeNames.
	resps := []AgentResponse{{ID: agentID, RuntimeID: runtimeID, Status: "idle"}}
	testHandler.attachAgentRuntimeNames(ctx, resps)
	if resps[0].RuntimeDisplayStatus != agentDisplayStatusBlocked {
		t.Fatalf("RuntimeDisplayStatus = %q, want %q after sticky fail wrote the lock",
			resps[0].RuntimeDisplayStatus, agentDisplayStatusBlocked)
	}
	_ = eventID
}

// TestFailAgentInboxEvent_Transient429DoesNotWriteProviderBlock is the reverse:
// bare capacity 429 must not sticky-lock (would strand agents on transient blips).
func TestFailAgentInboxEvent_Transient429DoesNotWriteProviderBlock(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, _, eventID, deliveryID, leaseToken, daemonID := seedLeasedInboxForProviderBlockTest(t)

	failReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+eventID+"/fail", FailAgentInboxEventRequest{
		DeliveryID:    deliveryID,
		LeaseToken:    leaseToken,
		Error:         "API Error: 429 Too Many Requests",
		FailureReason: string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		ReasonCode:    string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
	}, testWorkspaceID, daemonID)
	failReq = withURLParam(failReq, "eventId", eventID)
	failRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failRec, failReq)
	if failRec.Code != http.StatusOK {
		t.Fatalf("fail transient 429: status=%d body=%s", failRec.Code, failRec.Body.String())
	}

	var detail string
	if err := testPool.QueryRow(ctx, `
		SELECT provider_block_detail FROM agent WHERE id = $1`, agentID).Scan(&detail); err != nil {
		t.Fatalf("read provider_block_detail: %v", err)
	}
	if detail != "" {
		t.Fatalf("provider_block_detail = %q, want empty for transient capacity 429", detail)
	}
}

func seedLeasedInboxForProviderBlockTest(t *testing.T) (agentID, runtimeID, eventID, deliveryID, leaseToken, daemonID string) {
	t.Helper()
	ctx := context.Background()
	agentID, runtimeID = createAgentHealthFixture(t, "online", time.Now(), time.Now())
	daemonID = "provider-block-write-" + randomID()
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime SET daemon_id = $1, runtime_mode = 'local' WHERE id = $2`,
		daemonID, runtimeID); err != nil {
		t.Fatalf("attach daemon_id: %v", err)
	}

	var sessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_session (workspace_id, agent_id, runtime_id, scope, status)
		VALUES ($1, $2, $3, 'direct_chat', 'active')
		RETURNING id
	`, testWorkspaceID, agentID, runtimeID).Scan(&sessionID); err != nil {
		t.Fatalf("create agent session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_session WHERE id = $1`, sessionID)
	})

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
		  workspace_id, agent_session_id, runtime_id, agent_id,
		  reason, requires_wake, status, priority, seq_from, seq_to
		)
		VALUES ($1, $2, $3, $4, 'dm', true, 'pending', 10, 1, 1)
		RETURNING id
	`, testWorkspaceID, sessionID, runtimeID, agentID).Scan(&eventID); err != nil {
		t.Fatalf("create inbox wake: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_event_delivery WHERE inbox_event_id = $1`, eventID)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
		_, _ = testPool.Exec(ctx, `UPDATE agent SET provider_blocked_until = NULL, provider_block_detail = '' WHERE id = $1`, agentID)
	})

	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
	if err != nil {
		t.Fatalf("lease inbox: %v", err)
	}
	deliveryID = uuidToString(delivery.ID)
	leaseToken = uuidToString(delivery.LeaseToken)
	return agentID, runtimeID, eventID, deliveryID, leaseToken, daemonID
}
