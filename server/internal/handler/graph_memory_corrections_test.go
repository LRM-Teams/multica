package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// cleanupGraphNodeCorrections removes the correction ledger rows a test
// writes. The Task 8A tables carry no workspace FK, so the workspace-delete
// cleanup in createGraphMemoryTestWorkspace cannot cascade them.
func cleanupGraphNodeCorrections(t *testing.T, workspaceID pgtype.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM memory_deletion_audit WHERE workspace_id = $1`,
			`DELETE FROM quarantined_pending_recompute WHERE workspace_id = $1`,
			`DELETE FROM retraction_registry WHERE workspace_id = $1`,
		} {
			if _, err := testPool.Exec(ctx, stmt, workspaceID); err != nil {
				t.Fatalf("cleanup %s: %v", stmt, err)
			}
		}
	})
}

// seedWorkspaceOwner grants a fresh workspace its mandatory single owner —
// a user other than the shared test user, who joins as a plain member.
func seedWorkspaceOwner(t *testing.T, workspaceID pgtype.UUID) {
	t.Helper()
	var ownerID string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Correction Owner "+suffix, "corrections-owner-"+suffix+"@multica.ai",
	).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
}

func correctionCount(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func postGraphMemoryCorrection(t *testing.T, workspaceID pgtype.UUID, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPost,
		"/api/workspaces/"+workspaceID.String()+"/memory/graph/corrections", body), "id", workspaceID.String())
	rec := httptest.NewRecorder()
	testHandler.GraphMemoryCorrection(rec, req)
	return rec
}

// TestGraphMemoryCorrection_OwnerRetractIsImmediateAndAudited: an owner
// retract commits the quarantine row (consumer_kind graph_node), the
// attributable registry event, and the deletion-audit row together.
func TestGraphMemoryCorrection_OwnerRetractIsImmediateAndAudited(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	cleanupGraphNodeCorrections(t, workspaceID)

	rec := postGraphMemoryCorrection(t, workspaceID, map[string]any{
		"action": "retract", "node_id": "node-stale-fact", "reason": "wrong deadline",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "retracted") {
		t.Fatalf("owner retract: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM retraction_registry WHERE workspace_id=$1 AND actor=$2`,
		workspaceID, "user:"+testUserID); n != 1 {
		t.Fatalf("registry rows=%d, want 1 attributable owner event", n)
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM quarantined_pending_recompute
		 WHERE workspace_id=$1 AND consumer_kind='graph_node' AND consumer_id='node-stale-fact'`,
		workspaceID); n != 1 {
		t.Fatalf("graph_node quarantine rows=%d, want 1", n)
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM memory_deletion_audit
		 WHERE workspace_id=$1 AND source_kind='graph_node' AND source_id='node-stale-fact'`,
		workspaceID); n != 1 {
		t.Fatalf("deletion audit rows=%d, want 1", n)
	}

	// The explicit "correct" action stays a review candidate even for an
	// owner: nothing hides content outside the retract path.
	rec = postGraphMemoryCorrection(t, workspaceID, map[string]any{
		"action": "correct", "node_id": "node-stale-fact", "reason": "typo in date",
	})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "candidate") {
		t.Fatalf("owner correct: status=%d body=%s, want 202 candidate", rec.Code, rec.Body.String())
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM quarantined_pending_recompute
		 WHERE workspace_id=$1 AND consumer_kind='graph_node_correction'`,
		workspaceID); n != 1 {
		t.Fatalf("correction candidate rows=%d, want 1", n)
	}
}

// TestGraphMemoryCorrection_MemberRetractBecomesCandidate: an ordinary
// member never hides content — the same request lands as an inert
// graph_node_correction candidate for owner review.
func TestGraphMemoryCorrection_MemberRetractBecomesCandidate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := createGraphMemoryTestWorkspace(t)
	seedWorkspaceOwner(t, workspaceID)
	mustGraphMemoryMember(t, workspaceID, "member")
	cleanupGraphNodeCorrections(t, workspaceID)

	rec := postGraphMemoryCorrection(t, workspaceID, map[string]any{
		"action": "retract", "node_id": "node-dubious", "reason": "contradicts the launch doc",
	})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "candidate") {
		t.Fatalf("member retract: status=%d body=%s, want 202 candidate", rec.Code, rec.Body.String())
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM quarantined_pending_recompute
		 WHERE workspace_id=$1 AND consumer_kind='graph_node'`,
		workspaceID); n != 0 {
		t.Fatalf("member quarantined graph_node rows=%d, want 0 (node stays visible)", n)
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM quarantined_pending_recompute
		 WHERE workspace_id=$1 AND consumer_kind='graph_node_correction' AND consumer_id='node-dubious'`,
		workspaceID); n != 1 {
		t.Fatalf("member candidate rows=%d, want 1", n)
	}
}

// TestGraphMemoryCorrection_SupersedeRefusedLeavesNodeUntouched: a
// supersede whose replacement evidence fails the durable-evidence policy
// returns 422 promotion_refused and writes no retraction rows — the old
// node survives a refused replacement.
func TestGraphMemoryCorrection_SupersedeRefusedLeavesNodeUntouched(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	cleanupGraphNodeCorrections(t, workspaceID)

	rec := postGraphMemoryCorrection(t, workspaceID, map[string]any{
		"action": "supersede", "node_id": "node-stale-fact", "reason": "replaced by v2 decision",
		"project_id": "11111111-2222-3333-4444-555555555555",
		"replacement": map[string]any{
			"body": "The project decided to ship NIMBUS v2 in October.",
			"evidence": []map[string]any{
				{"kind": "formal_decision", "ref_id": "99999999-8888-7777-6666-555555555555"},
			},
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("refused supersede: status=%d body=%s, want 422", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "promotion_refused") ||
		!strings.Contains(rec.Body.String(), "issue_not_found") {
		t.Fatalf("refused supersede body=%s, want promotion_refused/issue_not_found", rec.Body.String())
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM retraction_registry WHERE workspace_id=$1`, workspaceID); n != 0 {
		t.Fatalf("registry rows after refused supersede=%d, want 0", n)
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM quarantined_pending_recompute WHERE workspace_id=$1`, workspaceID); n != 0 {
		t.Fatalf("quarantine rows after refused supersede=%d, want 0", n)
	}
}

// TestGraphMemoryCorrection_RequestShapeValidation: missing node_id and an
// unknown action are 400 before any ledger write.
func TestGraphMemoryCorrection_RequestShapeValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	cleanupGraphNodeCorrections(t, workspaceID)

	rec := postGraphMemoryCorrection(t, workspaceID, map[string]any{
		"action": "retract", "reason": "no node",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing node_id: status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	rec = postGraphMemoryCorrection(t, workspaceID, map[string]any{
		"action": "purge", "node_id": "node-stale-fact",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown action: status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if n := correctionCount(t,
		`SELECT count(*) FROM retraction_registry WHERE workspace_id=$1`, workspaceID); n != 0 {
		t.Fatalf("registry rows after 400s=%d, want 0", n)
	}
}
