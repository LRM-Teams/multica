package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

func retentionRequest(t *testing.T, method string, workspaceID string, userID string, body any) *http.Request {
	t.Helper()
	// newRequest JSON-encodes non-nil bodies itself.
	req := newRequestAs(userID, method, "/api/workspaces/"+workspaceID+"/memory/retention", body)
	return withRouteParams(req, "id", workspaceID)
}

func retentionWorkspace(t *testing.T) pgtype.UUID {
	t.Helper()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryWorkspaceOwner(t, workspaceID)
	// The requesting test user starts as a plain member (migration 301
	// needs the seeded owner above first).
	mustGraphMemoryMember(t, workspaceID, "member")
	return workspaceID
}

// GET binds the explicit bootstrap policy and exposes the platform caps.
func TestMemoryRetention_HandlerBindsBootstrapPolicy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	workspaceID := retentionWorkspace(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE member SET role='admin' WHERE workspace_id=$1 AND user_id=$2`,
		workspaceID, parseUUID(testUserID)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	testHandler.GetMemoryRetention(rec, retentionRequest(t, http.MethodGet, workspaceID.String(), testUserID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Policy service.MemoryRetentionPolicy `json:"policy"`
		Caps   struct {
			TrajectoryHotDays      int `json:"trajectory_hot_days"`
			ArchiveDays            int `json:"archive_days"`
			TraceHotDays           int `json:"trace_hot_days"`
			DiagnosticThinkingDays int `json:"diagnostic_thinking_days"`
		} `json:"caps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Policy.Version != 1 || resp.Policy.TrajectoryHotDays != 90 ||
		resp.Policy.ArchiveDays != 365 || resp.Policy.TraceHotDays != 30 ||
		resp.Policy.DiagnosticThinkingDays != 30 {
		t.Fatalf("policy = %#v", resp.Policy)
	}
	if resp.Caps.ArchiveDays != 365 || resp.Caps.TraceHotDays != 30 || resp.Caps.DiagnosticThinkingDays != 30 {
		t.Fatalf("caps = %#v", resp.Caps)
	}
}

// PUT shortens with CAS; cap violations are 422, stale versions 409, and
// plain members never reach the policy.
func TestMemoryRetention_HandlerCASAndCaps(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceID := retentionWorkspace(t)

	// A plain member is forbidden.
	rec := httptest.NewRecorder()
	testHandler.UpdateMemoryRetention(rec, retentionRequest(t, http.MethodPut, workspaceID.String(), testUserID,
		map[string]any{"trajectory_hot_days": 30, "archive_days": 180, "trace_hot_days": 14, "diagnostic_thinking_days": 30, "expected_version": 1}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member PUT = %d: %s", rec.Code, rec.Body.String())
	}

	// Promote to admin (the workspace already has its single owner) and
	// shorten.
	if _, err := testPool.Exec(ctx, `
		UPDATE member SET role='admin' WHERE workspace_id=$1 AND user_id=$2`,
		workspaceID, parseUUID(testUserID)); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	testHandler.UpdateMemoryRetention(rec, retentionRequest(t, http.MethodPut, workspaceID.String(), testUserID,
		map[string]any{"trajectory_hot_days": 30, "archive_days": 180, "trace_hot_days": 14, "diagnostic_thinking_days": 30, "expected_version": 1}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":2`) {
		t.Fatalf("owner PUT = %d: %s", rec.Code, rec.Body.String())
	}

	// Stale version conflicts.
	rec = httptest.NewRecorder()
	testHandler.UpdateMemoryRetention(rec, retentionRequest(t, http.MethodPut, workspaceID.String(), testUserID,
		map[string]any{"trajectory_hot_days": 20, "archive_days": 100, "trace_hot_days": 7, "diagnostic_thinking_days": 14, "expected_version": 1}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale PUT = %d: %s", rec.Code, rec.Body.String())
	}

	// Cap violations are unprocessable.
	rec = httptest.NewRecorder()
	testHandler.UpdateMemoryRetention(rec, retentionRequest(t, http.MethodPut, workspaceID.String(), testUserID,
		map[string]any{"trajectory_hot_days": 91, "archive_days": 180, "trace_hot_days": 14, "diagnostic_thinking_days": 30, "expected_version": 2}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cap PUT = %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown fields are rejected.
	rec = httptest.NewRecorder()
	testHandler.UpdateMemoryRetention(rec, retentionRequest(t, http.MethodPut, workspaceID.String(), testUserID,
		map[string]any{"trajectory_hot_days": 30, "archive_days": 180, "trace_hot_days": 14, "diagnostic_thinking_days": 30,
			"expected_version": 2, "extra": 1}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field PUT = %d: %s", rec.Code, rec.Body.String())
	}
	_ = uuid.New()
}
