package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTeardownRuntimeWithoutActiveAgents_ProductionScaleSelfFKLookup(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer setupCancel()

	tx, err := testPool.Begin(setupCtx)
	if err != nil {
		t.Fatalf("begin production-scale teardown fixture: %v", err)
	}
	defer tx.Rollback(context.Background())

	var victimRuntimeID, decoyRuntimeID string
	for provider, target := range map[string]*string{
		"kiro":  &victimRuntimeID,
		"codex": &decoyRuntimeID,
	} {
		if err := tx.QueryRow(setupCtx, `
			INSERT INTO agent_runtime (
				workspace_id, daemon_id, name, runtime_mode, provider, status,
				device_info, metadata, owner_id, last_seen_at
			)
			VALUES ($1, $2, $3, 'local', $4, 'offline', $3, '{}'::jsonb, $5, now())
			RETURNING id
		`, testWorkspaceID, "scale-"+uuid.NewString(), "Scale "+provider+" "+uuid.NewString()[:8], provider, testUserID).Scan(target); err != nil {
			t.Fatalf("insert %s scale runtime: %v", provider, err)
		}
	}

	var victimAgentID, decoyAgentID string
	if err := tx.QueryRow(setupCtx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, archived_at
		, model) VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, now(), 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, "scale-victim-"+uuid.NewString()[:8], victimRuntimeID, testUserID).Scan(&victimAgentID); err != nil {
		t.Fatalf("insert scale victim agent: %v", err)
	}
	if err := tx.QueryRow(setupCtx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id
		, model) VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, "scale-decoy-"+uuid.NewString()[:8], decoyRuntimeID, testUserID).Scan(&decoyAgentID); err != nil {
		t.Fatalf("insert scale decoy agent: %v", err)
	}

	// Production had 307,842 inbox rows and 1,568 rows owned by the archived
	// victim. Without the parent_task_id supporting index, PostgreSQL's
	// self-FK ON DELETE SET NULL trigger scans the whole table once per
	// deleted parent and exceeds the 30-second request budget.
	//
	// The handler test harness installs a per-row fixture-normalization
	// trigger. It is unrelated to production and makes a 300k INSERT take
	// minutes, so bypass triggers while loading known-valid rows, then restore
	// normal trigger/FK enforcement before exercising the real teardown.
	if _, err := tx.Exec(setupCtx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("bypass test-only fixture trigger: %v", err)
	}
	if _, err := tx.Exec(setupCtx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, reason, status, priority
		)
		SELECT $1, $2, $3, 'ambient', 'pending', 0
		FROM generate_series(1, 300000)
	`, testWorkspaceID, decoyAgentID, decoyRuntimeID); err != nil {
		t.Fatalf("insert 300k inbox decoys: %v", err)
	}
	if _, err := tx.Exec(setupCtx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, reason, status, priority
		)
		SELECT $1, $2, $3, 'ambient', 'pending', 0
		FROM generate_series(1, 1568)
	`, testWorkspaceID, victimAgentID, victimRuntimeID); err != nil {
		t.Fatalf("insert victim inbox history: %v", err)
	}
	if _, err := tx.Exec(setupCtx, `SET LOCAL session_replication_role = origin`); err != nil {
		t.Fatalf("restore teardown trigger enforcement: %v", err)
	}

	// Keep the test deterministic even before the transaction's uncommitted
	// bulk rows can affect planner statistics. If the supporting index is
	// removed, PostgreSQL still has no alternative to the repeated seq scan
	// and this bounded teardown fails.
	if _, err := tx.Exec(setupCtx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("disable seq scan for scale teardown: %v", err)
	}

	teardownCtx, teardownCancel := context.WithTimeout(setupCtx, 20*time.Second)
	defer teardownCancel()
	startedAt := time.Now()
	if err := teardownRuntimeWithoutActiveAgents(
		teardownCtx,
		testHandler.Queries.WithTx(tx),
		tx,
		parseUUID(victimRuntimeID),
	); err != nil {
		t.Fatalf("production-scale teardown after %s: %v", time.Since(startedAt), err)
	}

	var victimRuntimeCount, victimAgentCount, victimEventCount, decoyEventCount int
	if err := tx.QueryRow(setupCtx, `
		SELECT
			(SELECT count(*) FROM agent_runtime WHERE id = $1),
			(SELECT count(*) FROM agent WHERE id = $2),
			(SELECT count(*) FROM agent_inbox_event WHERE agent_id = $2),
			(SELECT count(*) FROM agent_inbox_event WHERE agent_id = $3)
	`, victimRuntimeID, victimAgentID, decoyAgentID).Scan(
		&victimRuntimeCount,
		&victimAgentCount,
		&victimEventCount,
		&decoyEventCount,
	); err != nil {
		t.Fatalf("inspect production-scale teardown: %v", err)
	}
	if victimRuntimeCount != 0 || victimAgentCount != 0 || victimEventCount != 0 {
		t.Fatalf(
			"victim teardown incomplete: runtime=%d agent=%d events=%d",
			victimRuntimeCount,
			victimAgentCount,
			victimEventCount,
		)
	}
	if decoyEventCount != 300000 {
		t.Fatalf("unrelated inbox history changed: got %d decoys", decoyEventCount)
	}
}

func TestDeleteComputer_SucceedsWithHistoricalInboxRuntimeSnapshot(t *testing.T) {
	// LRM-437/438: migration 183 left agent_inbox_event.runtime_id without
	// ON DELETE, so a historical inbox snapshot blocked Computer bulk delete
	// with generic "failed to delete runtimes" (Frank IMG_3127).
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-inbox-fk-" + uuid.NewString()

	victim := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	// Agent lives on a different machine so the active-agent gate does not fire;
	// only the inbox row's immutable runtime_id snapshot points at victim.
	otherDaemon := "other-inbox-fk-" + uuid.NewString()
	otherRT := createBulkDaemonRuntime(t, ctx, otherDaemon, "claude", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, otherRT, "Inbox Snapshot Agent")

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, reason, requires_wake, status, priority, runtime_id
		)
		VALUES ($1, $2, 'ambient', false, 'acked', 0, $3)
		RETURNING id
	`, testWorkspaceID, agentID, victim).Scan(&eventID); err != nil {
		t.Fatalf("insert inbox event with runtime snapshot: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with inbox runtime snapshot, got %d: %s", w.Code, w.Body.String())
	}
	assertRuntimeGone(t, ctx, victim)
	assertRuntimeExists(t, ctx, otherRT)

	var stillPinned int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE id = $1 AND runtime_id IS NOT NULL
	`, eventID).Scan(&stillPinned); err != nil {
		t.Fatalf("count inbox runtime snapshot: %v", err)
	}
	if stillPinned != 0 {
		t.Fatalf("expected inbox runtime_id nulled on runtime delete, still pinned=%d", stillPinned)
	}
}

func TestDeleteComputer_HappyPathDeletesAllOfflineProviders(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-daemon-" + uuid.NewString()

	rtClaude := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	rtCodex := createBulkDaemonRuntime(t, ctx, daemonID, "codex", "offline")
	// Unrelated machine must survive.
	other := createBulkDaemonRuntime(t, ctx, "other-daemon-"+uuid.NewString(), "claude", "offline")
	if _, _, err := testHandler.issueDaemonRegisterToken(
		ctx,
		parseUUID(testWorkspaceID),
		strings.ToUpper(daemonID),
	); err != nil {
		t.Fatalf("issue daemon token: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Status            string   `json:"status"`
		DaemonID          string   `json:"daemon_id"`
		DeletedCount      int      `json:"deleted_count"`
		DeletedRuntimeIDs []string `json:"deleted_runtime_ids"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" || body.DaemonID != daemonID || body.DeletedCount != 2 {
		t.Fatalf("unexpected body: %+v", body)
	}
	got := map[string]bool{}
	for _, id := range body.DeletedRuntimeIDs {
		got[id] = true
	}
	if !got[rtClaude] || !got[rtCodex] {
		t.Fatalf("deleted ids missing expected runtimes: %+v", body.DeletedRuntimeIDs)
	}

	assertRuntimeGone(t, ctx, rtClaude)
	assertRuntimeGone(t, ctx, rtCodex)
	assertRuntimeExists(t, ctx, other)
	assertDaemonTombstoned(t, ctx, daemonID)

	var tokenCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM daemon_token
		WHERE workspace_id = $1 AND lower(daemon_id) = lower($2)
	`, testWorkspaceID, daemonID).Scan(&tokenCount); err != nil {
		t.Fatalf("count daemon tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Fatalf("expected all daemon tokens revoked, count=%d", tokenCount)
	}
}

func TestDeleteComputer_DeletesOnlineComputer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-online-" + uuid.NewString()
	siblingWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	if w := createComputerWorkspaceBindingForTest(t, testUserID, daemonID, testWorkspaceID); w.Code != http.StatusOK {
		t.Fatalf("establish Computer connection: got %d: %s", w.Code, w.Body.String())
	}
	if w := createComputerWorkspaceBindingForTest(t, testUserID, daemonID, siblingWorkspaceID); w.Code != http.StatusOK {
		t.Fatalf("establish sibling Computer connection: got %d: %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_registration_tombstone WHERE workspace_id=$1 AND daemon_id=lower($2)`, testWorkspaceID, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id=$1`, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, daemonID)
	})

	onlineID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "online")
	offlineID := createBulkDaemonRuntime(t, ctx, daemonID, "codex", "offline")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertRuntimeGone(t, ctx, onlineID)
	assertRuntimeGone(t, ctx, offlineID)
	assertDaemonTombstoned(t, ctx, daemonID)
	assertComputerWorkspaceBindingActive(t, ctx, daemonID, false)
	var siblingActive bool
	var siblingTokens int
	if err := testPool.QueryRow(ctx, `
		SELECT b.active,
		       (SELECT count(*) FROM daemon_token t WHERE t.workspace_id=b.workspace_id AND t.daemon_id=b.daemon_id)
		FROM computer_workspace_bindings b
		WHERE b.daemon_id=$1 AND b.workspace_id=$2
	`, daemonID, siblingWorkspaceID).Scan(&siblingActive, &siblingTokens); err != nil {
		t.Fatalf("read sibling Computer connection: %v", err)
	}
	if !siblingActive || siblingTokens == 0 {
		t.Fatalf("sibling connection active/tokens = %v/%d, want true/non-zero", siblingActive, siblingTokens)
	}
}

func TestDeleteComputer_DeletesBindingOnlyComputer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-binding-only-" + uuid.NewString()
	if w := createComputerWorkspaceBindingForTest(t, testUserID, daemonID, testWorkspaceID); w.Code != http.StatusOK {
		t.Fatalf("establish Computer connection: got %d: %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_registration_tombstone WHERE workspace_id=$1 AND daemon_id=lower($2)`, testWorkspaceID, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id=$1`, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, daemonID)
	})

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertComputerWorkspaceBindingActive(t, ctx, daemonID, false)
	assertDaemonTombstoned(t, ctx, daemonID)
}

func TestDeleteComputer_BlocksWhileActiveAgentsRemain(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-agents-" + uuid.NewString()
	if w := createComputerWorkspaceBindingForTest(t, testUserID, daemonID, testWorkspaceID); w.Code != http.StatusOK {
		t.Fatalf("establish Computer connection: got %d: %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_registration_tombstone WHERE workspace_id=$1 AND daemon_id=lower($2)`, testWorkspaceID, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id=$1`, daemonID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, daemonID)
	})

	rtID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, rtID, "Bulk Block Agent")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Code         string          `json:"code"`
		ActiveAgents []AgentResponse `json:"active_agents"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "computer_has_active_agents" {
		t.Fatalf("expected computer_has_active_agents, got %q", body.Code)
	}
	if len(body.ActiveAgents) != 1 || body.ActiveAgents[0].ID != agentID {
		t.Fatalf("expected active agent %s, got %+v", agentID, body.ActiveAgents)
	}
	assertRuntimeExists(t, ctx, rtID)
	assertComputerWorkspaceBindingActive(t, ctx, daemonID, true)
}

func TestDeleteComputer_DeletesVoiceCallsAndDetachesRestrictDependents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-agent-dependents-" + uuid.NewString()

	targetRuntimeID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	targetAgentID := createCascadeFixtureAgent(t, ctx, targetRuntimeID, "Bulk Dependent Target")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET archived_at = now(), archived_by = $2 WHERE id = $1
	`, targetAgentID, testUserID); err != nil {
		t.Fatalf("archive target agent: %v", err)
	}
	_, autopilotEventID, executionID := createBulkRunningLegacyAutopilotTask(
		t,
		ctx,
		targetAgentID,
		targetRuntimeID,
	)
	var agentSessionID, deliveryID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_session (
			workspace_id, agent_id, runtime_id, scope, status,
			lease_token, lease_expires_at
		)
		VALUES ($1, $2, $3, 'direct_chat', 'active', gen_random_uuid(), now() + interval '2 minutes')
		RETURNING id
	`, testWorkspaceID, targetAgentID, targetRuntimeID).Scan(&agentSessionID); err != nil {
		t.Fatalf("insert active agent session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event SET agent_session_id = $2 WHERE id = $1
	`, autopilotEventID, agentSessionID); err != nil {
		t.Fatalf("attach active agent session: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_event_delivery (
			workspace_id, agent_session_id, inbox_event_id, runtime_id,
			status, lease_expires_at
		)
		VALUES ($1, $2, $3, $4, 'processing', now() + interval '2 minutes')
		RETURNING id
	`, testWorkspaceID, agentSessionID, autopilotEventID, targetRuntimeID).Scan(&deliveryID); err != nil {
		t.Fatalf("insert active agent delivery: %v", err)
	}

	// A surviving derived agent must not be hard-deleted just because its source
	// computer is removed; the source lineage is detached instead.
	otherRuntimeID := createBulkDaemonRuntime(t, ctx, "bulk-dependent-survivor-"+uuid.NewString(), "codex", "offline")
	survivorAgentID := createCascadeFixtureAgent(t, ctx, otherRuntimeID, "Bulk Dependent Survivor")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET source_agent_id = $2 WHERE id = $1
	`, survivorAgentID, targetAgentID); err != nil {
		t.Fatalf("attach derived-agent lineage: %v", err)
	}

	// Migration 222 emptied/retired Squads but intentionally kept the legacy
	// schema. An unexpected legacy row must not retain the archived agent via its
	// RESTRICT leader FK.
	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, leader_id, creator_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, testWorkspaceID, "legacy-delete-"+uuid.NewString()[:8], targetAgentID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("insert legacy squad: %v", err)
	}

	channelID := seedChannelForTest(t, "bulk-voice-"+uuid.NewString(), testUserID)
	var endedCallID, activeCallID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO voice_call_session (
			workspace_id, channel_id, agent_id, user_id, provider, status,
			started_at, connected_at, ended_at, end_reason
		)
		VALUES (
			$1, $2, $3, $4, 'test', 'ended',
			now() - interval '3 minutes', now() - interval '2 minutes',
			now() - interval '1 minute', 'completed'
		)
		RETURNING id
	`, testWorkspaceID, channelID, targetAgentID, testUserID).Scan(&endedCallID); err != nil {
		t.Fatalf("insert ended voice call: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO voice_call_session (
			workspace_id, channel_id, agent_id, user_id, provider, status,
			started_at, connected_at
		)
		VALUES (
			$1, $2, $3, $4, 'test', 'active',
			now() - interval '30 seconds', now() - interval '20 seconds'
		)
		RETURNING id
	`, testWorkspaceID, channelID, targetAgentID, testUserID).Scan(&activeCallID); err != nil {
		t.Fatalf("insert active voice call: %v", err)
	}

	var endedTurnID, activeTurnID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO voice_call_turn (
			call_session_id, sequence, speaker, transcript, started_at, ended_at
		)
		VALUES ($1, 1, 'agent', 'ended call history', now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, endedCallID).Scan(&endedTurnID); err != nil {
		t.Fatalf("insert ended voice turn: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO voice_call_turn (
			call_session_id, sequence, speaker, transcript, started_at, ended_at
		)
		VALUES ($1, 1, 'member', 'active call history', now() - interval '10 seconds', now())
		RETURNING id
	`, activeCallID).Scan(&activeTurnID); err != nil {
		t.Fatalf("insert active voice turn: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM voice_call_session WHERE id IN ($1, $2)`, endedCallID, activeCallID)
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID)
		testPool.Exec(context.Background(), `UPDATE agent SET source_agent_id = NULL WHERE id = $1`, survivorAgentID)
	})

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with agent dependents, got %d: %s", w.Code, w.Body.String())
	}

	assertRuntimeGone(t, ctx, targetRuntimeID)
	assertRuntimeExists(t, ctx, otherRuntimeID)
	assertDaemonTombstoned(t, ctx, daemonID)

	var targetAgents, calls, turns, squads, sessions, deliveries int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent WHERE id = $1`, targetAgentID).Scan(&targetAgents); err != nil {
		t.Fatalf("count target agent: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM voice_call_session WHERE id IN ($1, $2)
	`, endedCallID, activeCallID).Scan(&calls); err != nil {
		t.Fatalf("count voice calls: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM voice_call_turn WHERE id IN ($1, $2)
	`, endedTurnID, activeTurnID).Scan(&turns); err != nil {
		t.Fatalf("count voice turns: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM squad WHERE id = $1`, squadID).Scan(&squads); err != nil {
		t.Fatalf("count legacy squad: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_session WHERE id = $1`, agentSessionID).Scan(&sessions); err != nil {
		t.Fatalf("count agent sessions: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_event_delivery WHERE id = $1`, deliveryID).Scan(&deliveries); err != nil {
		t.Fatalf("count agent deliveries: %v", err)
	}
	if targetAgents != 0 || calls != 0 || turns != 0 || squads != 0 || sessions != 0 || deliveries != 0 {
		t.Fatalf(
			"expected complete dependent teardown, agents=%d calls=%d turns=%d squads=%d sessions=%d deliveries=%d",
			targetAgents, calls, turns, squads, sessions, deliveries,
		)
	}

	var survivorSource *string
	if err := testPool.QueryRow(ctx, `
		SELECT source_agent_id::text FROM agent WHERE id = $1
	`, survivorAgentID).Scan(&survivorSource); err != nil {
		t.Fatalf("read surviving derived agent: %v", err)
	}
	if survivorSource != nil {
		t.Fatalf("expected surviving derived agent lineage detached, got source=%s", *survivorSource)
	}

	var executionStatus, executionReason string
	var executionCompleted bool
	if err := testPool.QueryRow(ctx, `
		SELECT status, completed_at IS NOT NULL, failure_reason
		FROM agent_execution WHERE id = $1
	`, executionID).Scan(&executionStatus, &executionCompleted, &executionReason); err != nil {
		t.Fatalf("read preserved execution ledger: %v", err)
	}
	if executionStatus != "cancelled" ||
		!executionCompleted ||
		executionReason != "agent permanently deleted" {
		t.Fatalf(
			"expected permanent delete to terminalize execution ledger, status=%s completed=%t reason=%q",
			executionStatus, executionCompleted, executionReason,
		)
	}
}

func TestPermanentAgentDeleteNonCascadingFKInventory(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	expected := map[string]bool{
		"agent_source_workspace_fk":                                       true,
		"env_dispatch_delivery_obligation_source_recipient_agent_id_fkey": true,
		"env_dispatch_run_agent_execution_agent_id_fkey":                  true,
		"env_dispatch_run_agent_source_agent_id_fkey":                     true,
		"squad_leader_id_fkey":                                            true,
		"voice_call_session_agent_id_fkey":                                true,
	}
	rows, err := testPool.Query(context.Background(), `
		SELECT conname
		FROM pg_constraint
		WHERE contype = 'f'
		  AND confrelid = 'agent'::regclass
		  AND confdeltype IN ('a', 'r')
		ORDER BY conname
	`)
	if err != nil {
		t.Fatalf("list non-cascading agent FKs: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan non-cascading agent FK: %v", err)
		}
		if !expected[name] {
			t.Fatalf("unexpected non-cascading agent FK %q lacks permanent-delete teardown", name)
		}
		delete(expected, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate non-cascading agent FKs: %v", err)
	}
	if len(expected) != 0 {
		t.Fatalf("expected non-cascading agent FKs missing from schema: %+v", expected)
	}
}

func TestDeleteComputer_CancelsArchivedAgentTaskWithoutRuntimeSnapshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-tasks-" + uuid.NewString()

	rtID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	// Active work on an already-removed agent is cancelled atomically with the
	// empty-computer cleanup.
	agentID := createCascadeFixtureAgent(t, ctx, rtID, "Bulk Task Agent")
	// Archive the agent so the active-agents guard does not fire. The task has
	// no runtime snapshot, so only the archived-agent id can match it.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET archived_at = now(), archived_by = $2 WHERE id = $1
	`, agentID, testUserID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	issueID := createBulkFixtureIssue(t, ctx)
	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, issue_id, reason, status, priority
		)
		VALUES ($1, $2, NULL, $3, 'issue', 'pending', 0)
		RETURNING id
	`, testWorkspaceID, agentID, issueID).Scan(&eventID); err != nil {
		t.Fatalf("insert active task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		TasksCancelled int `json:"tasks_cancelled"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TasksCancelled != 1 {
		t.Fatalf("expected one cancelled task, got %+v", body)
	}
	assertRuntimeGone(t, ctx, rtID)

	var eventCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE id = $1
	`, eventID).Scan(&eventCount); err != nil {
		t.Fatalf("count cancelled task: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("expected removed agent task history deleted, count=%d", eventCount)
	}
}

func TestDeleteComputer_CancelsActiveInboxWork(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-inbox-active-" + uuid.NewString()

	rtID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, rtID, "Bulk Inbox Active Agent")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET archived_at = now(), archived_by = $2 WHERE id = $1
	`, agentID, testUserID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (workspace_id, agent_id, runtime_id, reason, status, priority)
		VALUES ($1, $2, $3, 'mention', 'pending', 0)
		RETURNING id
	`, testWorkspaceID, agentID, rtID).Scan(&eventID); err != nil {
		t.Fatalf("insert active inbox event: %v", err)
	}
	defer testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		TasksCancelled int `json:"tasks_cancelled"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TasksCancelled != 1 {
		t.Fatalf("expected one cancelled inbox event, got %+v", body)
	}
	assertRuntimeGone(t, ctx, rtID)
	var eventCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE id = $1
	`, eventID).Scan(&eventCount); err != nil {
		t.Fatalf("count cancelled inbox event: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("expected removed agent inbox history deleted, count=%d", eventCount)
	}
}

func TestDeleteComputer_RuntimeModeQueryCannotNarrowDeletion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-mode-" + uuid.NewString()

	localID := createBulkDaemonRuntimeWithMode(t, ctx, daemonID, "claude", "offline", "local")
	cloudID := createBulkDaemonRuntimeWithMode(t, ctx, daemonID, "codex", "offline", "cloud")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID+"?runtime_mode=local", nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertRuntimeGone(t, ctx, localID)
	assertRuntimeGone(t, ctx, cloudID)
}

func TestDeleteComputer_DetachesTerminalInboxEvents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-inbox-terminal-" + uuid.NewString()

	targetRuntimeID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	otherRuntimeID := createBulkDaemonRuntime(t, ctx, "other-inbox-"+uuid.NewString(), "codex", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, otherRuntimeID, "Bulk Inbox Agent")
	eventID := createBulkInboxEvent(t, ctx, targetRuntimeID, agentID, "acked")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertRuntimeGone(t, ctx, targetRuntimeID)
	assertInboxEventRuntimeCleared(t, ctx, eventID)
	assertRuntimeExists(t, ctx, otherRuntimeID)
}

func TestDeleteComputer_CancelsActiveInboxEventOnOtherAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-inbox-active-" + uuid.NewString()

	targetRuntimeID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	otherRuntimeID := createBulkDaemonRuntime(t, ctx, "other-inbox-"+uuid.NewString(), "codex", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, otherRuntimeID, "Bulk Active Inbox Agent")
	createBulkInboxEvent(t, ctx, targetRuntimeID, agentID, "pending")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/computers/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteComputer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertRuntimeGone(t, ctx, targetRuntimeID)
	assertRuntimeExists(t, ctx, otherRuntimeID)
}

func TestDaemonRegister_RejectsPermanentlyRemovedComputer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "removed-daemon-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO daemon_registration_tombstone (workspace_id, daemon_id, removed_by)
		VALUES ($1, lower($2), $3)
	`, testWorkspaceID, daemonID, testUserID); err != nil {
		t.Fatalf("insert daemon tombstone: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `
			DELETE FROM daemon_registration_tombstone
			WHERE workspace_id = $1 AND daemon_id = lower($2)
		`, testWorkspaceID, daemonID)
	})

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    strings.ToUpper(daemonID),
		"device_name":  "removed computer",
		"runtimes": []map[string]any{
			{"name": "claude", "type": "claude", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "daemon_removed" {
		t.Fatalf("expected daemon_removed, got %q", body.Code)
	}

	var runtimeCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_runtime
		WHERE workspace_id = $1 AND lower(daemon_id) = lower($2)
	`, testWorkspaceID, daemonID).Scan(&runtimeCount); err != nil {
		t.Fatalf("count recreated runtimes: %v", err)
	}
	if runtimeCount != 0 {
		t.Fatalf("expected no recreated runtime, count=%d", runtimeCount)
	}
}

func createBulkDaemonRuntime(t *testing.T, ctx context.Context, daemonID, provider, status string) string {
	t.Helper()
	return createBulkDaemonRuntimeWithMode(t, ctx, daemonID, provider, status, "local")
}

func createBulkDaemonRuntimeWithMode(t *testing.T, ctx context.Context, daemonID, provider, status, mode string) string {
	t.Helper()
	var runtimeID string
	name := "Bulk " + provider + " " + uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '{}'::jsonb, now() - interval '1 hour')
		RETURNING id
	`, testWorkspaceID, daemonID, name, mode, provider, status, name+" device").Scan(&runtimeID); err != nil {
		t.Fatalf("insert bulk daemon runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE runtime_id = $1`, runtimeID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID
}

func createBulkFixtureIssue(t *testing.T, ctx context.Context) string {
	t.Helper()
	var issueID string
	var number int
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(max(number), 0) + 1 FROM issue WHERE workspace_id = $1`, testWorkspaceID).Scan(&number); err != nil {
		t.Fatalf("next issue number: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, number, creator_id, creator_type)
		VALUES ($1, $2, 'todo', 'none', $3, $4, 'member')
		RETURNING id
	`, testWorkspaceID, "bulk-delete-task-"+uuid.NewString()[:8], number, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("insert bulk fixture issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})
	return issueID
}

func createBulkInboxEvent(t *testing.T, ctx context.Context, runtimeID, agentID, status string) string {
	t.Helper()
	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (workspace_id, agent_id, runtime_id, reason, status, priority)
		VALUES ($1, $2, $3, 'mention', $4, 0)
		RETURNING id
	`, testWorkspaceID, agentID, runtimeID, status).Scan(&eventID); err != nil {
		t.Fatalf("insert bulk inbox event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})
	return eventID
}

// createBulkRunningLegacyAutopilotTask seeds a draining inbox event that still
// carries an orphan autopilot_run_id after LRM-1051 dropped autopilot* tables.
func createBulkRunningLegacyAutopilotTask(
	t *testing.T,
	ctx context.Context,
	agentID string,
	runtimeID string,
) (runID, eventID, executionID string) {
	t.Helper()
	runID = uuid.NewString()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, reason, status, priority,
			autopilot_run_id
		)
		VALUES ($1, $2, $3, 'autopilot', 'draining', 0, $4)
		RETURNING id
	`, testWorkspaceID, agentID, runtimeID, runID).Scan(&eventID); err != nil {
		t.Fatalf("insert bulk legacy autopilot inbox event: %v", err)
	}
	executionID = uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_execution (
			id, source_kind, source_event_id, source, workspace_id, runtime_id,
			agent_id, status
		)
		VALUES ($1, 'inbox', $2, 'chat', $3, $4, $5, 'running')
	`, executionID, eventID, testWorkspaceID, runtimeID, agentID); err != nil {
		t.Fatalf("insert bulk agent execution: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_execution WHERE id = $1`, executionID)
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})
	return runID, eventID, executionID
}

func assertInboxEventRuntimeCleared(t *testing.T, ctx context.Context, eventID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE id = $1 AND runtime_id IS NULL
	`, eventID).Scan(&n); err != nil {
		t.Fatalf("count inbox event: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected inbox event %s runtime_id cleared, count=%d", eventID, n)
	}
}

func assertRuntimeGone(t *testing.T, ctx context.Context, runtimeID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&n); err != nil {
		t.Fatalf("count runtime: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected runtime %s deleted, count=%d", runtimeID, n)
	}
}

func assertRuntimeExists(t *testing.T, ctx context.Context, runtimeID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&n); err != nil {
		t.Fatalf("count runtime: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected runtime %s to exist, count=%d", runtimeID, n)
	}
}

func assertDaemonTombstoned(t *testing.T, ctx context.Context, daemonID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM daemon_registration_tombstone
		WHERE workspace_id = $1 AND daemon_id = lower($2)
	`, testWorkspaceID, daemonID).Scan(&n); err != nil {
		t.Fatalf("count daemon tombstone: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected daemon %s tombstoned, count=%d", daemonID, n)
	}
}

func assertComputerWorkspaceBindingActive(t *testing.T, ctx context.Context, daemonID string, want bool) {
	t.Helper()
	var active bool
	if err := testPool.QueryRow(ctx, `
		SELECT active FROM computer_workspace_bindings
		WHERE daemon_id = $1 AND workspace_id = $2
	`, daemonID, testWorkspaceID).Scan(&active); err != nil {
		t.Fatalf("read Computer Workspace binding: %v", err)
	}
	if active != want {
		t.Fatalf("Computer Workspace binding active = %v, want %v", active, want)
	}
}
