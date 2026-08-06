package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAttachCanonicalRuntimeStateFillsGenerationOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB unavailable")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Runtime State Attach Agent", nil)
	runtimeID := handlerTestRuntimeID(t)

	// First attach ensures the row (new agents get generation without cutover).
	resp := AgentTaskResponse{RuntimeID: runtimeID}
	testHandler.attachCanonicalRuntimeState(ctx, parseUUID(agentID), runtimeID, &resp)
	if resp.RuntimeStateGeneration <= 0 {
		t.Fatalf("RuntimeStateGeneration = %d, want > 0 after ensure", resp.RuntimeStateGeneration)
	}
	if resp.FreshSessionNoticeReason != "" {
		t.Fatalf("new agent notice = %q, want empty", resp.FreshSessionNoticeReason)
	}

	// Production migration A left many agents with an explicit cutover notice.
	// D6-1a must NOT surface it while legacy PriorSession resume still runs —
	// current daemons inject a false "brand new / history archived" brief.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime_state
		SET fresh_session_notice_reason = 'cutover'
		WHERE agent_id = $1 AND runtime_id = $2
	`, parseUUID(agentID), parseUUID(runtimeID)); err != nil {
		t.Fatalf("seed cutover notice: %v", err)
	}
	resp2 := AgentTaskResponse{
		RuntimeID:      runtimeID,
		PriorSessionID: "legacy-provider-session",
		PriorWorkDir:   "/tmp/legacy-workdir",
	}
	testHandler.attachCanonicalRuntimeState(ctx, parseUUID(agentID), runtimeID, &resp2)
	if resp2.RuntimeStateGeneration != resp.RuntimeStateGeneration {
		t.Fatalf("generation churned on re-ensure: %d -> %d", resp.RuntimeStateGeneration, resp2.RuntimeStateGeneration)
	}
	if resp2.FreshSessionNoticeReason != "" {
		t.Fatalf("FreshSessionNoticeReason = %q, want empty until D6-2 (got cutover leak)", resp2.FreshSessionNoticeReason)
	}
	// D6-1a must not invent or clear resume identity.
	if resp2.PriorSessionID != "legacy-provider-session" || resp2.PriorWorkDir != "/tmp/legacy-workdir" {
		t.Fatalf("attach must not mutate legacy resume fields: session=%q workdir=%q",
			resp2.PriorSessionID, resp2.PriorWorkDir)
	}
}

// TestClaimPathSurfacesGenerationWithoutCutoverNotice locks the real claim
// JSON path (not only attachCanonicalRuntimeState): with migration-218 cutover
// still on the row and legacy PriorSession filled from chat_session, claim
// must return generation>0 and empty fresh_session_notice_reason.
func TestClaimPathSurfacesGenerationWithoutCutoverNotice(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB unavailable")
	}
	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (
			workspace_id, agent_id, creator_id, title,
			session_id, work_dir, runtime_id
		)
		VALUES ($1, $2, $3, 'd6-1a claim wire', 'legacy-claim-session', '/tmp/legacy-claim-wd', $4)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, runtimeID).Scan(&chatSessionID); err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, chatSessionID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content)
		VALUES ($1, 'user', 'd6-1a claim wire message')
	`, chatSessionID); err != nil {
		t.Fatalf("insert chat message: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, chat_session_id,
			status, priority
		)
		VALUES ($1, $2, $3, 'pending', 0)
	`, agentID, runtimeID, chatSessionID); err != nil {
		t.Fatalf("create pending inbox event: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_runtime_state (agent_id, runtime_id, fresh_session_notice_reason)
		VALUES ($1, $2, 'cutover')
		ON CONFLICT (agent_id, runtime_id) DO UPDATE
		SET fresh_session_notice_reason = EXCLUDED.fresh_session_notice_reason
	`, agentID, runtimeID); err != nil {
		t.Fatalf("seed cutover notice: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil,
		testWorkspaceID, daemonID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("runtimeId", runtimeID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	claimTaskThroughInboxForTest(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			ID                       string `json:"id"`
			PriorSessionID           string `json:"prior_session_id"`
			PriorWorkDir             string `json:"prior_work_dir"`
			RuntimeStateGeneration   int64  `json:"runtime_state_generation"`
			FreshSessionNoticeReason string `json:"fresh_session_notice_reason"`
		} `json:"task"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil {
		t.Fatal("expected task in claim response")
	}
	if resp.Task.RuntimeStateGeneration <= 0 {
		t.Fatalf("runtime_state_generation = %d, want > 0", resp.Task.RuntimeStateGeneration)
	}
	if resp.Task.FreshSessionNoticeReason != "" {
		t.Fatalf("fresh_session_notice_reason = %q, want empty under legacy resume", resp.Task.FreshSessionNoticeReason)
	}
	if resp.Task.PriorSessionID != "legacy-claim-session" {
		t.Fatalf("prior_session_id = %q, want legacy-claim-session (resume still live)", resp.Task.PriorSessionID)
	}
	if resp.Task.PriorWorkDir != "/tmp/legacy-claim-wd" {
		t.Fatalf("prior_work_dir = %q, want /tmp/legacy-claim-wd", resp.Task.PriorWorkDir)
	}
	if resp.Task.ID != "" {
		settleClaimedInboxEventForTest(t, resp.Task.ID)
	}
}

func TestAgentRuntimeStateCASPreservesCanonicalSessionBoundary(t *testing.T) {
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Runtime State CAS Agent", nil)
	runtimeID := handlerTestRuntimeID(t)
	queries := db.New(testPool)

	state, err := queries.EnsureAgentRuntimeState(ctx, db.EnsureAgentRuntimeStateParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(runtimeID),
	})
	if err != nil {
		t.Fatalf("ensure runtime state: %v", err)
	}
	if state.Generation != 1 {
		t.Fatalf("initial generation = %d, want 1", state.Generation)
	}
	if state.ProviderSessionID.Valid || state.WorkDir.Valid || state.ProviderConfigFingerprint.Valid || state.LastTurnID.Valid {
		t.Fatalf("new state unexpectedly inherited a legacy pointer: %#v", state)
	}
	if state.FreshSessionNoticeReason.Valid || state.LegacyResumeArchivedAt.Valid {
		t.Fatalf("new agent must not receive a cutover notice: %#v", state)
	}

	// Simulate Migration A for an existing pair. Pending cutover is an honest
	// pre-activation state: the canonical session and archive timestamp are
	// both empty while the legacy source remains live.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime_state
		SET fresh_session_notice_reason = 'cutover',
		    legacy_resume_archived_at = NULL
		WHERE agent_id = $1 AND runtime_id = $2
	`, parseUUID(agentID), parseUUID(runtimeID)); err != nil {
		t.Fatalf("seed pending Migration A state: %v", err)
	}

	pending, err := queries.GetAgentRuntimeState(ctx, db.GetAgentRuntimeStateParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(runtimeID),
	})
	if err != nil {
		t.Fatalf("get pending cutover state: %v", err)
	}
	if !pending.FreshSessionNoticeReason.Valid || pending.FreshSessionNoticeReason.String != "cutover" ||
		pending.ProviderSessionID.Valid || pending.LegacyResumeArchivedAt.Valid {
		t.Fatalf("pending cutover state is not explicit and inert: %#v", pending)
	}

	// D6 will drain first, then record the true legacy archive boundary and
	// establish the canonical session in the same transaction. The archive
	// timestamp remains immutable provenance after the first-wake notice is
	// consumed and through later resets.
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin activation transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	var archivedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `
		UPDATE agent_runtime_state
		SET legacy_resume_archived_at = now()
		WHERE agent_id = $1 AND runtime_id = $2
		RETURNING legacy_resume_archived_at
	`, parseUUID(agentID), parseUUID(runtimeID)).Scan(&archivedAt); err != nil {
		t.Fatalf("record true archive boundary: %v", err)
	}

	turnOne := parseUUID("11111111-1111-4111-8111-111111111111")
	advanced, err := db.New(tx).AdvanceAgentRuntimeStateCAS(ctx, db.AdvanceAgentRuntimeStateCASParams{
		AgentID:                   parseUUID(agentID),
		RuntimeID:                 parseUUID(runtimeID),
		ExpectedGeneration:        1,
		TurnID:                    turnOne,
		ProviderSessionID:         pgtype.Text{String: "provider-session-1", Valid: true},
		WorkDir:                   pgtype.Text{String: "/runtime/agent-1", Valid: true},
		ProviderConfigFingerprint: pgtype.Text{String: "sha256:fingerprint-1", Valid: true},
	})
	if err != nil {
		t.Fatalf("advance runtime state: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit activation transaction: %v", err)
	}
	if advanced.Generation != 2 || !advanced.LastTurnID.Valid || advanced.LastTurnID != turnOne {
		t.Fatalf("advanced state did not record generation/turn: %#v", advanced)
	}
	if !advanced.ProviderSessionID.Valid || advanced.ProviderSessionID.String != "provider-session-1" {
		t.Fatalf("provider session = %#v, want provider-session-1", advanced.ProviderSessionID)
	}
	if !advanced.WorkDir.Valid || advanced.WorkDir.String != "/runtime/agent-1" {
		t.Fatalf("work dir = %#v, want preserved canonical path", advanced.WorkDir)
	}
	if advanced.FreshSessionNoticeReason.Valid {
		t.Fatalf("persisting a real canonical session must consume the cutover notice: %#v", advanced)
	}
	if !advanced.LegacyResumeArchivedAt.Valid || !advanced.LegacyResumeArchivedAt.Time.Equal(archivedAt.Time) {
		t.Fatalf("canonical advance changed legacy archive provenance: got %#v want %#v", advanced.LegacyResumeArchivedAt, archivedAt)
	}

	_, err = queries.AdvanceAgentRuntimeStateCAS(ctx, db.AdvanceAgentRuntimeStateCASParams{
		AgentID:                   parseUUID(agentID),
		RuntimeID:                 parseUUID(runtimeID),
		ExpectedGeneration:        1,
		TurnID:                    parseUUID("22222222-2222-4222-8222-222222222222"),
		ProviderSessionID:         pgtype.Text{String: "stale-session", Valid: true},
		WorkDir:                   pgtype.Text{String: "/runtime/stale", Valid: true},
		ProviderConfigFingerprint: pgtype.Text{String: "sha256:stale", Valid: true},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale advance error = %v, want pgx.ErrNoRows", err)
	}

	turnTwo := parseUUID("33333333-3333-4333-8333-333333333333")
	cleared, err := queries.ClearAgentRuntimeSessionCAS(ctx, db.ClearAgentRuntimeSessionCASParams{
		AgentID:            parseUUID(agentID),
		RuntimeID:          parseUUID(runtimeID),
		ExpectedGeneration: 2,
		TurnID:             turnTwo,
		NoticeReason:       "reset",
	})
	if err != nil {
		t.Fatalf("clear runtime session: %v", err)
	}
	if cleared.Generation != 3 || cleared.ProviderSessionID.Valid || cleared.ProviderConfigFingerprint.Valid {
		t.Fatalf("clear did not invalidate only the current provider generation: %#v", cleared)
	}
	if !cleared.WorkDir.Valid || cleared.WorkDir.String != "/runtime/agent-1" {
		t.Fatalf("session reset must preserve work dir, got %#v", cleared.WorkDir)
	}
	if !cleared.FreshSessionNoticeReason.Valid || cleared.FreshSessionNoticeReason.String != "reset" {
		t.Fatalf("reset notice = %#v, want reset", cleared.FreshSessionNoticeReason)
	}
	if !cleared.LegacyResumeArchivedAt.Valid || !cleared.LegacyResumeArchivedAt.Time.Equal(archivedAt.Time) {
		t.Fatalf("reset must preserve legacy archive provenance: got %#v want %#v", cleared.LegacyResumeArchivedAt, archivedAt)
	}
	if !cleared.LastTurnID.Valid || cleared.LastTurnID != turnTwo {
		t.Fatalf("clear did not record reset turn: %#v", cleared.LastTurnID)
	}
	_, err = queries.AdvanceAgentRuntimeStateCAS(ctx, db.AdvanceAgentRuntimeStateCASParams{
		AgentID:                   parseUUID(agentID),
		RuntimeID:                 parseUUID(runtimeID),
		ExpectedGeneration:        2,
		TurnID:                    turnTwo,
		ProviderSessionID:         pgtype.Text{String: "pre-clear-late-session", Valid: true},
		ProviderConfigFingerprint: pgtype.Text{String: "sha256:pre-clear-late", Valid: true},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("pre-clear late writer error = %v, want pgx.ErrNoRows", err)
	}
	_, err = queries.AdvanceAgentRuntimeStateCAS(ctx, db.AdvanceAgentRuntimeStateCASParams{
		AgentID:            parseUUID(agentID),
		RuntimeID:          parseUUID(runtimeID),
		ExpectedGeneration: 3,
		TurnID:             turnTwo,
		WorkDir:            pgtype.Text{String: "/runtime/agent-1", Valid: true},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("same-turn reset successor without a real provider session error = %v, want pgx.ErrNoRows", err)
	}

	// The daemon's poisoned-resume fallback clears and then starts fresh in the
	// same wake. The clear's successor generation is allowed to establish that
	// fresh provider session and consume the reset notice without weakening the
	// generation fence for old or concurrent writers.
	freshAfterReset, err := queries.AdvanceAgentRuntimeStateCAS(ctx, db.AdvanceAgentRuntimeStateCASParams{
		AgentID:                   parseUUID(agentID),
		RuntimeID:                 parseUUID(runtimeID),
		ExpectedGeneration:        3,
		TurnID:                    turnTwo,
		ProviderSessionID:         pgtype.Text{String: "provider-session-2", Valid: true},
		WorkDir:                   pgtype.Text{String: "/runtime/agent-1", Valid: true},
		ProviderConfigFingerprint: pgtype.Text{String: "sha256:fingerprint-2", Valid: true},
	})
	if err != nil {
		t.Fatalf("same-turn advance after reset: %v", err)
	}
	if freshAfterReset.Generation != 4 ||
		!freshAfterReset.LastTurnID.Valid || freshAfterReset.LastTurnID != turnTwo ||
		!freshAfterReset.ProviderSessionID.Valid || freshAfterReset.ProviderSessionID.String != "provider-session-2" ||
		!freshAfterReset.ProviderConfigFingerprint.Valid || freshAfterReset.ProviderConfigFingerprint.String != "sha256:fingerprint-2" {
		t.Fatalf("same-turn fresh session did not cross the reset boundary: %#v", freshAfterReset)
	}
	if freshAfterReset.FreshSessionNoticeReason.Valid {
		t.Fatalf("same-turn fresh session must consume reset notice: %#v", freshAfterReset)
	}
	if !freshAfterReset.WorkDir.Valid || freshAfterReset.WorkDir.String != "/runtime/agent-1" {
		t.Fatalf("same-turn fresh session changed canonical work dir: %#v", freshAfterReset.WorkDir)
	}
	if !freshAfterReset.LegacyResumeArchivedAt.Valid || !freshAfterReset.LegacyResumeArchivedAt.Time.Equal(archivedAt.Time) {
		t.Fatalf("same-turn fresh session changed archive provenance: got %#v want %#v", freshAfterReset.LegacyResumeArchivedAt, archivedAt)
	}

	for name, params := range map[string]db.AdvanceAgentRuntimeStateCASParams{
		"concurrent writer on consumed generation": {
			AgentID:                   parseUUID(agentID),
			RuntimeID:                 parseUUID(runtimeID),
			ExpectedGeneration:        3,
			TurnID:                    parseUUID("44444444-4444-4444-8444-444444444444"),
			ProviderSessionID:         pgtype.Text{String: "concurrent-late-session", Valid: true},
			ProviderConfigFingerprint: pgtype.Text{String: "sha256:concurrent-late", Valid: true},
		},
		"duplicate same turn after notice consumed": {
			AgentID:                   parseUUID(agentID),
			RuntimeID:                 parseUUID(runtimeID),
			ExpectedGeneration:        4,
			TurnID:                    turnTwo,
			ProviderSessionID:         pgtype.Text{String: "duplicate-same-turn-session", Valid: true},
			ProviderConfigFingerprint: pgtype.Text{String: "sha256:duplicate", Valid: true},
		},
	} {
		if _, err := queries.AdvanceAgentRuntimeStateCAS(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("%s error = %v, want pgx.ErrNoRows", name, err)
		}
	}

	_, err = queries.ClearAgentRuntimeSessionCAS(ctx, db.ClearAgentRuntimeSessionCASParams{
		AgentID:            parseUUID(agentID),
		RuntimeID:          parseUUID(runtimeID),
		ExpectedGeneration: 4,
		TurnID:             parseUUID("44444444-4444-4444-8444-444444444444"),
		NoticeReason:       "cutover",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("runtime reset accepted migration-only cutover reason: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime_state
		SET provider_session_id = NULL,
		    fresh_session_notice_reason = NULL
		WHERE agent_id = $1 AND runtime_id = $2
	`, parseUUID(agentID), parseUUID(runtimeID)); err == nil {
		t.Fatal("archived empty canonical session without notice unexpectedly succeeded")
	}
}

func TestEnsureAgentRuntimeStateRejectsNonCurrentRuntime(t *testing.T) {
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Runtime State Binding Agent", nil)

	var otherRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata
		)
		VALUES ($1, NULL, $2, 'cloud', $3, 'offline', '', '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, "Non-current Runtime", "runtime-state-test").Scan(&otherRuntimeID); err != nil {
		t.Fatalf("create non-current runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, otherRuntimeID)
	})

	_, err := db.New(testPool).EnsureAgentRuntimeState(ctx, db.EnsureAgentRuntimeStateParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(otherRuntimeID),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ensure against non-current runtime error = %v, want pgx.ErrNoRows", err)
	}
}
