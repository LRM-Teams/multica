package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestClassifyResearchDispatchErrorOnlyStopsDeterministicSQLFailures(t *testing.T) {
	contractErr := classifyResearchDispatchError(&pgconn.PgError{Code: "42P18", Message: "ambiguous parameter type"})
	var classified interface{ Retryable() bool }
	if !errors.As(contractErr, &classified) || classified.Retryable() {
		t.Fatalf("SQL contract error was not classified non-retryable: %v", contractErr)
	}

	transientErr := classifyResearchDispatchError(&pgconn.PgError{Code: "40001", Message: "serialization failure"})
	classified = nil
	if errors.As(transientErr, &classified) {
		t.Fatalf("transient transaction error was classified as permanent: %v", transientErr)
	}
}

func TestResearchRunStartWillRetryMatchesDispatchClassification(t *testing.T) {
	if researchRunStartWillRetry(nil) {
		t.Fatal("nil start error was classified for retry")
	}
	if researchRunStartWillRetry(researchrun.NonRetryableDispatchError(errors.New("invalid dispatch contract"))) {
		t.Fatal("non-retryable dispatch error was classified for retry")
	}
	if researchRunStartWillRetry(researchrun.ErrCapabilityUnavailable) {
		t.Fatal("missing capability was classified for retry")
	}
	if !researchRunStartWillRetry(errors.New("temporary dispatcher outage")) {
		t.Fatal("unclassified transient error was not classified for retry")
	}
}

func TestResearchRunDispatcherBindsTypedInboxContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler integration database is unavailable")
	}
	ctx := context.Background()
	suffix := uuid.NewString()
	creatorIDText := uuid.NewString()
	workspaceIDText := uuid.NewString()
	runtimeIDText := uuid.NewString()
	agentIDText := uuid.NewString()
	setup := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO "user" (id, name, email) VALUES ($1::uuid, $2, $3)`, []any{creatorIDText, "Research dispatch test user", suffix + "@dispatch.test"}},
		{`INSERT INTO workspace (id, name, slug) VALUES ($1::uuid, $2, $3)`, []any{workspaceIDText, "Research dispatch test", "research-dispatch-" + suffix}},
		{`INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`, []any{workspaceIDText, creatorIDText}},
		{`INSERT INTO agent_runtime (id, workspace_id, name, runtime_mode, provider, status) VALUES ($1::uuid, $2::uuid, $3, 'cloud', 'codex', 'online')`, []any{runtimeIDText, workspaceIDText, "research-dispatch-runtime-" + suffix}},
		{`INSERT INTO agent (id, workspace_id, name, runtime_mode, runtime_id, status, owner_id, model) VALUES ($1::uuid, $2::uuid, $3, 'cloud', $4::uuid, 'idle', $5::uuid, 'test-model')`, []any{agentIDText, workspaceIDText, "research-dispatch-agent-" + suffix, runtimeIDText, creatorIDText}},
	}
	for _, statement := range setup {
		if _, err := testPool.Exec(ctx, statement.query, statement.args...); err != nil {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, workspaceIDText)
			_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, creatorIDText)
			t.Fatalf("seed dispatch fixture: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, workspaceIDText)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, creatorIDText)
	})
	workspaceID := parseUUID(workspaceIDText)
	creatorID := parseUUID(creatorIDText)
	agentID := parseUUID(agentIDText)

	fleet, err := testHandler.Queries.CreateResearchFleet(ctx, db.CreateResearchFleetParams{
		WorkspaceID: workspaceID,
		LeadAgentID: agentID,
	})
	if err != nil {
		t.Fatalf("create research fleet: %v", err)
	}
	if _, err = testHandler.Queries.CreateResearchFleetMember(ctx, db.CreateResearchFleetMemberParams{
		WorkspaceID: workspaceID,
		FleetID:     fleet.ID,
		AgentID:     agentID,
		Role:        "lead",
		Status:      "active",
		IsLead:      true,
	}); err != nil {
		t.Fatalf("create research fleet member: %v", err)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session tx: %v", err)
	}
	var sessionID pgtype.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, fleet_id, created_by, title, goal, status, current_stage,
			depth_tier, product_round, product_round_budget
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, workspaceID, fleet.ID, creatorID, "Typed dispatch context",
		"Verify the canonical dispatch metadata", "running", "s1_plan", "standard", int32(1), int32(5)).Scan(&sessionID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("create research session: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1, $2)`, workspaceID, sessionID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("ensure run session passport: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit research session: %v", err)
	}

	dispatchKey := "research-dispatch-test:" + uuid.NewString()
	researchTaskID := uuid.NewString()
	attemptID := uuid.NewString()
	criteria := json.RawMessage(`[{"criterion":"return a structured plan"}]`)
	dispatcher := &researchRunDispatcher{handler: testHandler}
	members, err := researchrun.NewPostgresStore(testPool).ListFleetMembers(ctx, uuidToString(sessionID), workspaceIDText)
	if err != nil || len(members) != 1 {
		t.Fatalf("resolve dispatch target members=%+v err=%v", members, err)
	}
	target := members[0].ExecutionTarget
	request := researchrun.DispatchRequest{
		Run: researchrun.Run{
			SessionID:   uuidToString(sessionID),
			WorkspaceID: workspaceIDText,
		},
		Task: researchrun.Task{
			ID:                 researchTaskID,
			TimeoutSeconds:     1800,
			AcceptanceCriteria: criteria,
		},
		AttemptID:    attemptID,
		AgentID:      uuidToString(agentID),
		Target:       target,
		Prompt:       "Return the research plan through the task-result command.",
		Key:          dispatchKey,
		ManifestID:   "40000000-0000-4000-8000-000000000004",
		ManifestHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	result, err := dispatcher.Dispatch(ctx, request)
	if err != nil {
		t.Fatalf("dispatch research task: %v", err)
	}
	if result.InboxTaskID == "" {
		t.Fatal("dispatch returned an empty inbox task ID")
	}

	var gotKey, gotRequestHash, gotSessionID, gotTaskID, gotAttemptID, gotManifestID, gotManifestHash, timeoutJSONType, criteriaJSONType string
	var gotTimeout int
	if err = testPool.QueryRow(ctx, `
		SELECT context->>'research_dispatch_key',
		       context->>'research_dispatch_request_hash',
		       context->>'research_session_id',
		       context->>'research_task_id',
		       context->>'research_attempt_id',
		       context->>'research_manifest_id',
		       context->>'research_manifest_hash',
		       (context->>'research_task_timeout_seconds')::int,
		       jsonb_typeof(context->'research_task_timeout_seconds'),
		       jsonb_typeof(context->'research_task_acceptance_criteria')
		FROM agent_inbox_event
		WHERE id = $1::uuid
	`, result.InboxTaskID).Scan(
		&gotKey, &gotRequestHash, &gotSessionID, &gotTaskID, &gotAttemptID, &gotManifestID, &gotManifestHash,
		&gotTimeout, &timeoutJSONType, &criteriaJSONType,
	); err != nil {
		t.Fatalf("load dispatched inbox context: %v", err)
	}
	if gotKey != dispatchKey || gotSessionID != uuidToString(sessionID) || gotTaskID != researchTaskID || gotAttemptID != attemptID {
		t.Fatalf("dispatch context IDs = %q %q %q %q", gotKey, gotSessionID, gotTaskID, gotAttemptID)
	}
	if gotManifestID != request.ManifestID || gotManifestHash != request.ManifestHash {
		t.Fatalf("dispatch context manifest=%q/%q want=%q/%q", gotManifestID, gotManifestHash, request.ManifestID, request.ManifestHash)
	}
	wantRequestHash, hashErr := researchrun.HashDispatchRequest(request)
	if hashErr != nil || gotRequestHash != wantRequestHash {
		t.Fatalf("dispatch request hash=%q want=%q err=%v", gotRequestHash, wantRequestHash, hashErr)
	}
	if gotTimeout != 1800 || timeoutJSONType != "number" || criteriaJSONType != "array" {
		t.Fatalf("dispatch context types: timeout=%d timeout_type=%q criteria_type=%q", gotTimeout, timeoutJSONType, criteriaJSONType)
	}
	replayed, err := dispatcher.Dispatch(ctx, request)
	if err != nil || replayed.InboxTaskID != result.InboxTaskID {
		t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
	}
	conflicting := request
	conflicting.Prompt = "different payload under the same dispatch key"
	if _, err = dispatcher.Dispatch(ctx, conflicting); err == nil || !strings.Contains(err.Error(), "reused for a different request") {
		t.Fatalf("conflicting dispatch error=%v", err)
	}
	if _, err = testPool.Exec(ctx, `UPDATE agent SET model = 'changed-model' WHERE id = $1::uuid`, agentIDText); err != nil {
		t.Fatal(err)
	}
	staleTarget := request
	staleTarget.Key = "research-dispatch-stale-target:" + uuid.NewString()
	staleTarget.AttemptID = uuid.NewString()
	staleTarget.RequestHash, err = researchrun.HashDispatchRequest(staleTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dispatcher.Dispatch(ctx, staleTarget); err == nil {
		t.Fatal("dispatch accepted a target whose model changed after attempt creation")
	}
	policy := researchrun.DispatchFailurePolicy(err)
	if policy.Class != researchrun.FailureTargetChanged || !policy.Retryable {
		t.Fatalf("stale target policy=%+v error=%v", policy, err)
	}
}
