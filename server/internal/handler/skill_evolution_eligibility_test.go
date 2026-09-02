package handler

// Trajectory eligibility admin API: owner/admin reads, the mandatory
// audit reason, revoke-only idempotency, and role isolation.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func eligibilityRequest(t *testing.T, method, workspaceID, runID, userID string, body any) *http.Request {
	t.Helper()
	path := "/api/workspaces/" + workspaceID + "/skill-evolution/eligibility/" + runID
	if method == http.MethodPost {
		path += "/revoke"
	}
	req := newRequestAs(userID, method, path, body)
	return withRouteParams(req, "id", workspaceID, "runId", runID)
}

// seedEligibility pins one eligible run directly (the projection path
// has its own tests; the handler only needs the row).
func seedEligibility(t *testing.T, workspaceID, runID string) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO skill_trajectory_eligibility (
			workspace_id, run_id, run_kind, evolution_eligible, allowed_purposes, task_type, fixed_at, fixed_by_actor
		) VALUES ($1::uuid, $2::uuid, 'agent_task_run', true, '{skill_evolution}', 'spreadsheet', now(), 'projector:test')`,
		workspaceID, runID)
	require.NoError(t, err)
}

func eligibilityWorkspace(t *testing.T) string {
	t.Helper()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryWorkspaceOwner(t, workspaceID)
	mustGraphMemoryMember(t, workspaceID, "member")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1::uuid`, workspaceID)
	})
	return workspaceID.String()
}

func promoteEligibilityAdmin(t *testing.T, workspaceID string) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`UPDATE member SET role='admin' WHERE workspace_id=$1 AND user_id=$2`,
		workspaceID, parseUUID(testUserID))
	require.NoError(t, err)
}

// GET returns the pinned eligibility; revoke records the audit trail and
// replays idempotently; the reason is mandatory; plain members never get
// through.
func TestSkillEvolutionEligibilityRevokeHandler(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceID := eligibilityWorkspace(t)
	runID := uuid.NewString()
	seedEligibility(t, workspaceID, runID)

	// A plain member is forbidden on both routes.
	rec := httptest.NewRecorder()
	testHandler.GetSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodGet, workspaceID, runID, testUserID, nil))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = httptest.NewRecorder()
	testHandler.RevokeSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodPost, workspaceID, runID, testUserID,
		map[string]any{"reason": "trajectory tainted"}))
	assert.Equal(t, http.StatusForbidden, rec.Code)

	promoteEligibilityAdmin(t, workspaceID)

	// Admin GET sees the pin.
	rec = httptest.NewRecorder()
	testHandler.GetSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodGet, workspaceID, runID, testUserID, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var got skillEligibilityResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.True(t, got.EvolutionEligible)
	assert.Empty(t, got.RevokedByActor)

	// The reason is an audit floor: missing or blank is 422, and a
	// request-supplied actor (unknown field) never even decodes (400).
	for _, tc := range []struct {
		body any
		code int
	}{
		{map[string]any{}, http.StatusUnprocessableEntity},
		{map[string]any{"reason": "   "}, http.StatusUnprocessableEntity},
		{map[string]any{"reason": "x", "actor": "forged"}, http.StatusBadRequest},
	} {
		rec = httptest.NewRecorder()
		testHandler.RevokeSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodPost, workspaceID, runID, testUserID, tc.body))
		assert.Equal(t, tc.code, rec.Code, "body %#v must be refused", tc.body)
	}

	// A valid revoke records the authenticated principal, never a body actor.
	rec = httptest.NewRecorder()
	testHandler.RevokeSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodPost, workspaceID, runID, testUserID,
		map[string]any{"reason": "trajectory output retracted by DPO request"}))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.False(t, got.EvolutionEligible)
	assert.Equal(t, "user:"+testUserID, got.RevokedByActor)
	assert.Equal(t, "trajectory output retracted by DPO request", got.RevokedReason)

	// The pin itself is untouched: only the revocation columns moved.
	var stillPinned bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT fixed_by_actor = 'projector:test' FROM skill_trajectory_eligibility
		 WHERE workspace_id=$1::uuid AND run_id=$2::uuid`, workspaceID, runID).Scan(&stillPinned))
	assert.True(t, stillPinned)

	// Replaying the revoke is idempotent: the recorded revocation returns.
	firstRevokedAt := got.RevokedAt
	rec = httptest.NewRecorder()
	testHandler.RevokeSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodPost, workspaceID, runID, testUserID,
		map[string]any{"reason": "trajectory output retracted by DPO request"}))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, firstRevokedAt, got.RevokedAt, "the first revocation is the only one")

	// Unknown runs are 404.
	rec = httptest.NewRecorder()
	testHandler.GetSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodGet, workspaceID, uuid.NewString(), testUserID, nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = httptest.NewRecorder()
	testHandler.RevokeSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodPost, workspaceID, uuid.NewString(), testUserID,
		map[string]any{"reason": "nothing to revoke"}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Without the ledger configured, the routes answer 503 rather than
// pretending success.
func TestSkillEvolutionEligibilityHandlerRequiresLedger(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := eligibilityWorkspace(t)
	promoteEligibilityAdmin(t, workspaceID)
	ledger := testHandler.SkillEvolutionLedger
	testHandler.SkillEvolutionLedger = nil
	t.Cleanup(func() { testHandler.SkillEvolutionLedger = ledger })

	rec := httptest.NewRecorder()
	testHandler.GetSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodGet, workspaceID, uuid.NewString(), testUserID, nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = httptest.NewRecorder()
	testHandler.RevokeSkillTrajectoryEligibility(rec, eligibilityRequest(t, http.MethodPost, workspaceID, uuid.NewString(), testUserID,
		map[string]any{"reason": "ledger down"}))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
