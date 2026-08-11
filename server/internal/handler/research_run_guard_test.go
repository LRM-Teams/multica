package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProjectResearchEventIncludesBudgetExhaustion(t *testing.T) {
	nodeType, title, summary, status := projectResearchEvent(
		researchrun.RunEvent{Type: "budget_exhausted"},
		db.ResearchSession{},
		map[string]any{"budget_kind": "wall_time"},
	)
	if nodeType != "dead_end" || title != "调研预算已耗尽" || summary != "wall_time" || status != "done" {
		t.Fatalf("projected budget event = (%q, %q, %q, %q)", nodeType, title, summary, status)
	}
}

func TestLegacyResearchMutationGuardRejectsInitializedRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var fleetID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO research_fleet (workspace_id)
		VALUES ($1::uuid)
		ON CONFLICT (workspace_id) DO UPDATE SET updated_at = now()
		RETURNING id::text
	`, testWorkspaceID).Scan(&fleetID); err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_session (
			id, workspace_id, fleet_id, created_by, title, goal, status, run_initialized_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'guard', 'guard', 'running', now())
	`, sessionID, testWorkspaceID, fleetID, testUserID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1::uuid, $2::uuid)`, testWorkspaceID, sessionID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_session WHERE id = $1::uuid`, sessionID)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/legacy", nil)
	if rejected := testHandler.rejectLegacyResearchMutation(recorder, request, parseUUID(testWorkspaceID), parseUUID(sessionID)); !rejected {
		t.Fatal("initialized run was not rejected")
	}
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
