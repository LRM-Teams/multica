package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type researchProjectionSeed struct {
	userID string
	wsA    string
	wsB    string
}

// seedResearchProjectionWorkspace provisions two workspaces sharing one owner
// user, each with an agent + research fleet so CreateRun can seed full sessions.
func seedResearchProjectionWorkspace(t *testing.T, ctx context.Context) researchProjectionSeed {
	t.Helper()
	suffix := uuid.NewString()
	userID := uuid.NewString()
	wsA := uuid.NewString()
	wsB := uuid.NewString()

	base := [][]any{
		{`INSERT INTO "user" (id, name, email) VALUES ($1::uuid, $2, $3)`, []any{userID, "Research projection test user", suffix + "@proj.test"}},
		{`INSERT INTO workspace (id, name, slug) VALUES ($1::uuid, $2, $3)`, []any{wsA, "Projection WS A", "proj-a-" + suffix}},
		{`INSERT INTO workspace (id, name, slug) VALUES ($1::uuid, $2, $3)`, []any{wsB, "Projection WS B", "proj-b-" + suffix}},
		{`INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`, []any{wsA, userID}},
		{`INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`, []any{wsB, userID}},
	}
	for _, s := range base {
		if _, err := testPool.Exec(ctx, s[0].(string), s[1].([]any)...); err != nil {
			t.Fatalf("seed base: %v", err)
		}
	}

	// Each workspace needs an agent + research fleet so CreateRun can seed a
	// full session in that workspace.
	createAgentAndFleet := func(wsID, agentName string) {
		runtimeID := uuid.NewString()
		agentID := uuid.NewString()
		agentSeed := [][]any{
			{`INSERT INTO agent_runtime (id, workspace_id, name, runtime_mode, provider, status, owner_id) VALUES ($1::uuid, $2::uuid, $3, 'cloud', 'codex', 'online', $4::uuid)`, []any{runtimeID, wsID, agentName + "-runtime", userID}},
			{`INSERT INTO agent (id, workspace_id, name, runtime_mode, runtime_id, status, owner_id, model) VALUES ($1::uuid, $2::uuid, $3, 'cloud', $4::uuid, 'idle', $5::uuid, 'test-model')`, []any{agentID, wsID, agentName, runtimeID, userID}},
		}
		for _, s := range agentSeed {
			if _, err := testPool.Exec(ctx, s[0].(string), s[1].([]any)...); err != nil {
				t.Fatalf("seed agent %s: %v", agentName, err)
			}
		}
		fleet, err := testHandler.Queries.CreateResearchFleet(ctx, db.CreateResearchFleetParams{WorkspaceID: parseUUID(wsID), LeadAgentID: parseUUID(agentID)})
		if err != nil {
			t.Fatalf("create research fleet %s: %v", agentName, err)
		}
		if _, err = testHandler.Queries.CreateResearchFleetMember(ctx, db.CreateResearchFleetMemberParams{
			WorkspaceID: parseUUID(wsID), FleetID: fleet.ID, AgentID: parseUUID(agentID), Role: "lead", Status: "active", IsLead: true,
		}); err != nil {
			t.Fatalf("create research fleet member %s: %v", agentName, err)
		}
	}
	createAgentAndFleet(wsA, "proj-agent-a")
	createAgentAndFleet(wsB, "proj-agent-b")

	return researchProjectionSeed{userID: userID, wsA: wsA, wsB: wsB}
}

// seedResearchProjectionRun seeds a full research session + run + plan task in a
// workspace using the real store's CreateRun (the same path production uses), so
// the projection's Snapshot reads a coherent run ledger. Returns the session id
// and the plan task id.
func seedResearchProjectionRun(t *testing.T, ctx context.Context, s researchProjectionSeed, wsID, goal string) (sessionID, taskID string) {
	t.Helper()
	fleet, err := testHandler.Queries.GetResearchFleetByWorkspace(ctx, parseUUID(wsID))
	if err != nil {
		t.Fatalf("get research fleet: %v", err)
	}
	store := researchrun.NewPostgresStore(testPool)
	run, _, err := store.CreateRun(ctx, researchrun.StartInput{
		WorkspaceID:  wsID,
		FleetID:      uuidToString(fleet.ID),
		CreatedBy:    s.userID,
		LeadAgentID:  "",
		Goal:         goal,
		Title:        goal,
		DepthTier:    "standard",
		Language:     "English",
		ProductRound: 1, ProductRoundBudget: 5,
	}, researchrun.DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("list tasks: %v tasks=%d", err, len(tasks))
	}
	return run.SessionID, tasks[0].ID
}

func updateResearchProjectionTask(t *testing.T, ctx context.Context, taskID, status string, startedAt, completedAt *time.Time, maxAttempts int) {
	t.Helper()
	var startArg any
	if startedAt != nil {
		startArg = *startedAt
	}
	var doneArg any
	if completedAt != nil {
		doneArg = *completedAt
	}
	_, err := testPool.Exec(ctx, `
		UPDATE research_task
		SET status = $2, started_at = $3, completed_at = $4, max_attempts = $5, updated_at = now()
		WHERE id = $1::uuid
	`, taskID, status, startArg, doneArg, maxAttempts)
	if err != nil {
		t.Fatalf("update research_task: %v", err)
	}
}

// insertResearchProjectionAttempt adds a research_task_attempt row so the
// projection's attempt-count-derived progress (steps_done) reflects real
// attempt activity in the ledger.
func insertResearchProjectionAttempt(t *testing.T, ctx context.Context, taskID, status string, attemptNumber int) string {
	t.Helper()
	var wsID, sessionID string
	if err := testPool.QueryRow(ctx, `SELECT workspace_id::text, session_id::text FROM research_task WHERE id = $1::uuid`, taskID).Scan(&wsID, &sessionID); err != nil {
		t.Fatalf("load task for attempt: %v", err)
	}
	attemptID := uuid.NewString()
	dispatchKey := "proj-attempt-" + taskID + "-" + status
	_, err := testPool.Exec(ctx, `
		INSERT INTO research_task_attempt (
			id, workspace_id, session_id, task_id, attempt_number, assigned_agent_id,
			dispatch_key, status, dispatched_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid,
			$7, $8, now()
		)
	`, attemptID, wsID, sessionID, taskID, attemptNumber, uuid.NewString(), dispatchKey, status)
	if err != nil {
		t.Fatalf("insert research_task_attempt: %v", err)
	}
	return attemptID
}

// TestResearchWorkProjectionHTTP_LifecycleAndIsolation drives a single task
// through ready -> running -> succeeded and asserts the /work-projection
// endpoint reflects each change while retaining history, plus that it is scoped
// to the caller's workspace (cross-workspace reads are refused).
func TestResearchWorkProjectionHTTP_LifecycleAndIsolation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler integration database is unavailable")
	}
	ctx := context.Background()
	seed := seedResearchProjectionWorkspace(t, ctx)
	t.Cleanup(func() {
		for _, ws := range []string{seed.wsA, seed.wsB} {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, ws)
		}
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, seed.userID)
	})

	sessionA, taskA := seedResearchProjectionRun(t, ctx, seed, seed.wsA, "Projection A goal")
	_, _ = sessionA, taskA
	sessionB, _ := seedResearchProjectionRun(t, ctx, seed, seed.wsB, "Projection B goal")

	router := chi.NewRouter()
	router.Get("/api/research/sessions/{id}/work-projection", testHandler.GetResearchWorkProjection)
	projFor := func(workspaceID, sessionID string) (int, workProjectionResponse) {
		req := httptest.NewRequest(http.MethodGet, "/api/research/sessions/"+sessionID+"/work-projection", nil)
		req.Header.Set("X-Workspace-ID", workspaceID)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var out workProjectionResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// Step 1: freshly seeded plan task (ready) — no fabricated percent.
	code, proj := projFor(seed.wsA, sessionA)
	entry := requireSingleProjectionEntry(t, code, proj, seed.wsA, sessionA)
	if entry.Status != "ready" {
		t.Errorf("step1 status = %q, want ready (fresh plan task)", entry.Status)
	}
	if entry.ProgressPercent != nil {
		t.Errorf("step1 fabricated percent %d", *entry.ProgressPercent)
	}
	if entry.TaskID != taskA {
		t.Errorf("step1 task_id = %q, want %q", entry.TaskID, taskA)
	}

	// Step 2: dispatching (legal transition from ready) reflects in the projection.
	updateResearchProjectionTask(t, ctx, taskA, "dispatching", nil, nil, 3)
	code, proj = projFor(seed.wsA, sessionA)
	entry = requireSingleProjectionEntry(t, code, proj, seed.wsA, sessionA)
	if entry.Status != "dispatching" || entry.Stage != "dispatch" {
		t.Errorf("step2 status/stage = %q/%q, want dispatching/dispatch", entry.Status, entry.Stage)
	}

	// Step 3: running task (legal from dispatching) with a real attempt row — the
	// projection reflects the change and keeps earlier events.
	started := researchProjTime(2026, 8, 11, 9, 0)
	updateResearchProjectionTask(t, ctx, taskA, "running", &started, nil, 3)
	insertResearchProjectionAttempt(t, ctx, taskA, "running", 1)
	code, proj = projFor(seed.wsA, sessionA)
	entry = requireSingleProjectionEntry(t, code, proj, seed.wsA, sessionA)
	if entry.Status != "running" || entry.Stage != "executing" {
		t.Errorf("step3 status/stage = %q/%q, want running/executing", entry.Status, entry.Stage)
	}
	if entry.ProgressPercent == nil || *entry.ProgressPercent != 33 {
		t.Errorf("step3 progress = %v, want 33 (1/3 attempts)", entry.ProgressPercent)
	}
	if !timelineHasKind(entry.Timeline, "dispatch") {
		t.Errorf("step3 timeline missing dispatch: %+v", entry.Timeline)
	}

	// Step 4: succeeded (legal from running) — truthful 100%, history retained
	// through the refresh.
	completed := started.Add(30 * time.Minute)
	updateResearchProjectionTask(t, ctx, taskA, "succeeded", &started, &completed, 3)
	code, proj = projFor(seed.wsA, sessionA)
	entry = requireSingleProjectionEntry(t, code, proj, seed.wsA, sessionA)
	if entry.Status != "succeeded" || entry.Stage != "complete" {
		t.Errorf("step4 status/stage = %q/%q, want succeeded/complete", entry.Status, entry.Stage)
	}
	if entry.ProgressPercent == nil || *entry.ProgressPercent != 100 {
		t.Errorf("step4 progress = %v, want 100", entry.ProgressPercent)
	}
	if !timelineHasKind(entry.Timeline, "dispatch") || !timelineHasKind(entry.Timeline, "complete") {
		t.Errorf("step4 timeline lost history or missing complete: %+v", entry.Timeline)
	}

	// Workspace isolation: session B is not visible from workspace A (and vice
	// versa), even though both sessions exist in the same database.
	if codeBAsA, _ := projFor(seed.wsA, sessionB); codeBAsA != http.StatusNotFound {
		t.Errorf("session B readable from workspace A: status = %d, want 404", codeBAsA)
	}
	if codeAAsB, _ := projFor(seed.wsB, sessionA); codeAAsB != http.StatusNotFound {
		t.Errorf("session A readable from workspace B: status = %d, want 404", codeAAsB)
	}
	// And workspace A's own projection does not leak workspace B's task.
	if _, projA := projFor(seed.wsA, sessionA); len(projA.Entries) != 1 {
		t.Errorf("ws A projection leaked %d entries, want 1", len(projA.Entries))
	}
}

func requireSingleProjectionEntry(t *testing.T, code int, proj workProjectionResponse, wsID, sessionID string) WorkProjectionEntry {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("projection status = %d, want 200 (session %s in ws %s)", code, sessionID, wsID)
	}
	if len(proj.Entries) != 1 {
		t.Fatalf("projection entries = %d, want 1: %+v", len(proj.Entries), proj.Entries)
	}
	return proj.Entries[0]
}

func timelineHasKind(timeline []WorkTimelineEvent, kind string) bool {
	for _, ev := range timeline {
		if ev.Kind == kind {
			return true
		}
	}
	return false
}
