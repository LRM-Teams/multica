package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResetAgentRuntimeSessionClearsResumePointersAndPreservesWorkDirs(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	create := invokeCreateAgentLifecycle(t, agentID, "71717171-7171-4171-8171-717171717171", agentLifecycleResetSessionRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatalf("decode operation: %v", err)
	}

	var chatSessionID, inboxEventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (
			workspace_id, agent_id, creator_id, title, session_id, work_dir, runtime_id
		) VALUES ($1, $2, $3, 'lifecycle reset', 'chat-provider-session', '/chat/workdir', $4)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, runtimeID).Scan(&chatSessionID); err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, chat_session_id, status, priority, session_id, work_dir
		) VALUES ($1, $2, $3, 'acked', 0, 'event-provider-session', '/event/workdir')
		RETURNING id
	`, agentID, runtimeID, chatSessionID).Scan(&inboxEventID); err != nil {
		t.Fatalf("create inbox event: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_runtime_state (
			agent_id, runtime_id, provider_session_id, work_dir,
			provider_config_fingerprint, generation
		) VALUES ($1, $2, 'canonical-provider-session', '/canonical/workdir', 'sha256:old', 7)
		ON CONFLICT (agent_id, runtime_id) DO UPDATE SET
			provider_session_id = EXCLUDED.provider_session_id,
			work_dir = EXCLUDED.work_dir,
			provider_config_fingerprint = EXCLUDED.provider_config_fingerprint,
			generation = EXCLUDED.generation
	`, agentID, runtimeID); err != nil {
		t.Fatalf("seed runtime state: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, inboxEventID)
		_, _ = testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, chatSessionID)
	})

	invoke := func() *httptest.ResponseRecorder {
		req := newDaemonTokenRequest(http.MethodPost,
			"/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/session/reset",
			map[string]any{"operation_id": operation.ID}, testWorkspaceID, "session-reset-daemon")
		req = withURLParams(req, "runtimeId", runtimeID, "agentId", agentID)
		rec := httptest.NewRecorder()
		testHandler.ResetAgentRuntimeSession(rec, req)
		return rec
	}
	if rec := invoke(); rec.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", rec.Code, rec.Body.String())
	}

	var canonicalSession, canonicalWorkDir, fingerprint *string
	var generation int64
	if err := testPool.QueryRow(ctx, `
		SELECT provider_session_id, work_dir, provider_config_fingerprint, generation
		FROM agent_runtime_state WHERE agent_id = $1 AND runtime_id = $2
	`, agentID, runtimeID).Scan(&canonicalSession, &canonicalWorkDir, &fingerprint, &generation); err != nil {
		t.Fatalf("load runtime state: %v", err)
	}
	if canonicalSession != nil || fingerprint != nil || canonicalWorkDir == nil || *canonicalWorkDir != "/canonical/workdir" || generation != 8 {
		t.Fatalf("canonical reset mismatch: session=%v workdir=%v fingerprint=%v generation=%d", canonicalSession, canonicalWorkDir, fingerprint, generation)
	}
	var chatSession, chatWorkDir, eventSession, eventWorkDir *string
	if err := testPool.QueryRow(ctx, `SELECT session_id, work_dir FROM chat_session WHERE id = $1`, chatSessionID).Scan(&chatSession, &chatWorkDir); err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT session_id, work_dir FROM agent_inbox_event WHERE id = $1`, inboxEventID).Scan(&eventSession, &eventWorkDir); err != nil {
		t.Fatalf("load inbox event: %v", err)
	}
	if chatSession != nil || eventSession != nil || chatWorkDir == nil || *chatWorkDir != "/chat/workdir" || eventWorkDir == nil || *eventWorkDir != "/event/workdir" {
		t.Fatalf("legacy reset mismatch: chat=(%v,%v) event=(%v,%v)", chatSession, chatWorkDir, eventSession, eventWorkDir)
	}

	// Retrying the same operation must not advance the canonical generation.
	if rec := invoke(); rec.Code != http.StatusOK {
		t.Fatalf("idempotent reset status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := testPool.QueryRow(ctx, `
		SELECT generation FROM agent_runtime_state WHERE agent_id = $1 AND runtime_id = $2
	`, agentID, runtimeID).Scan(&generation); err != nil || generation != 8 {
		t.Fatalf("idempotent generation=%d err=%v", generation, err)
	}
}
