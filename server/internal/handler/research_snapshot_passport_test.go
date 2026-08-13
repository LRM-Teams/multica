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
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type recordingResearchRunEngine struct {
	snapshotCalled           bool
	snapshotForAttemptCalled bool
	snapshotSessionID        string
	snapshotWorkspaceID      string
	snapshotAttemptID        string
}

func (f *recordingResearchRunEngine) Create(context.Context, researchrun.StartInput) (researchrun.Run, error) {
	panic("unexpected Create call")
}

func (f *recordingResearchRunEngine) Snapshot(_ context.Context, sessionID, workspaceID string) (researchrun.RunSnapshot, error) {
	f.snapshotCalled = true
	f.snapshotSessionID = sessionID
	f.snapshotWorkspaceID = workspaceID
	return researchrun.RunSnapshot{}, nil
}

func (f *recordingResearchRunEngine) SnapshotForAttempt(_ context.Context, sessionID, workspaceID, attemptID string) (researchrun.RunSnapshot, error) {
	f.snapshotForAttemptCalled = true
	f.snapshotSessionID = sessionID
	f.snapshotWorkspaceID = workspaceID
	f.snapshotAttemptID = attemptID
	return researchrun.RunSnapshot{}, nil
}

func (f *recordingResearchRunEngine) ListFleetMembers(context.Context, string, string) ([]researchrun.FleetMember, error) {
	return nil, nil
}

func (f *recordingResearchRunEngine) Pause(context.Context, string, string, string) (researchrun.Run, error) {
	panic("unexpected Pause call")
}

func (f *recordingResearchRunEngine) Resume(context.Context, string, string, string) (researchrun.Run, error) {
	panic("unexpected Resume call")
}

func (f *recordingResearchRunEngine) Cancel(context.Context, string, string, string, string) (researchrun.Run, error) {
	panic("unexpected Cancel call")
}

func (f *recordingResearchRunEngine) Archive(context.Context, string, string, string, string) (researchrun.Run, error) {
	panic("unexpected Archive call")
}

func (f *recordingResearchRunEngine) Confirm(context.Context, string, string, string) (researchrun.Run, error) {
	panic("unexpected Confirm call")
}

func (f *recordingResearchRunEngine) Steer(context.Context, researchrun.SteerInput) (researchrun.Run, error) {
	panic("unexpected Steer call")
}

func (f *recordingResearchRunEngine) NodeCommand(context.Context, researchrun.NodeCommandInput) (researchrun.NodeCommandOutcome, error) {
	panic("unexpected NodeCommand call")
}

func (f *recordingResearchRunEngine) SubmitResult(context.Context, string, string, string, string, string, string, json.RawMessage) (researchrun.AcceptResultOutcome, error) {
	panic("unexpected SubmitResult call")
}

func (f *recordingResearchRunEngine) ReconcileDue(context.Context, int) (int, error) {
	panic("unexpected ReconcileDue call")
}

var _ researchrun.ResearchRun = (*recordingResearchRunEngine)(nil)

func useResearchRunEngine(t *testing.T, engine researchrun.ResearchRun) {
	t.Helper()

	prev := testHandler.ResearchRun
	testHandler.ResearchRun = engine
	t.Cleanup(func() { testHandler.ResearchRun = prev })
}

func seedInitializedResearchSessionForSnapshotTest(t *testing.T) pgtype.UUID {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	ensureMergableFleetMember(t)

	ctx := context.Background()
	var fleetID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM research_fleet WHERE workspace_id = $1
	`, testWorkspaceID).Scan(&fleetID); err != nil {
		t.Fatalf("load research fleet: %v", err)
	}

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sessionID pgtype.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, fleet_id, created_by, title, goal, status, run_initialized_at
		) VALUES ($1, $2, $3, $4, $5, $6, now())
		RETURNING id
	`, parseUUID(testWorkspaceID), fleetID, parseUUID(testUserID),
		"snapshot-passport-"+uuid.NewString()[:8],
		"verify attempt-bound snapshot routing", "running").Scan(&sessionID); err != nil {
		t.Fatalf("create initialized research session: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1, $2)`, parseUUID(testWorkspaceID), sessionID); err != nil {
		t.Fatalf("ensure run session passport: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit initialized research session: %v", err)
	}

	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_session WHERE id = $1`, sessionID)
	})
	return sessionID
}

func TestSnapshotPassportGetResearchSessionSnapshotUsesSnapshotWithoutAttemptID(t *testing.T) {
	sessionID := seedInitializedResearchSessionForSnapshotTest(t)

	engine := &recordingResearchRunEngine{}
	useResearchRunEngine(t, engine)

	path := "/api/research/sessions/" + uuidToString(sessionID)
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", uuidToString(sessionID))

	recorder := httptest.NewRecorder()
	testHandler.GetResearchSessionSnapshot(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !engine.snapshotCalled {
		t.Fatal("expected Snapshot to be called when attempt_id is absent")
	}
	if engine.snapshotForAttemptCalled {
		t.Fatal("SnapshotForAttempt must not be called without attempt_id")
	}
}

func TestResearchFleetMembershipUsesAgentPrincipalInsteadOfSpoofedHeader(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	activeAgentID := ensureMergableFleetMember(t)
	unboundAgentID := createHandlerTestAgent(t, "unbound-research-"+uuid.NewString()[:8], nil)

	request := newRequest(http.MethodGet, "/api/agent/research/session", nil)
	request = withAgentPrincipal(request, unboundAgentID, testWorkspaceID, testUserID)
	request.Header.Set("X-Agent-ID", activeAgentID)
	recorder := httptest.NewRecorder()
	if _, ok := testHandler.requireActiveFleetMember(recorder, request, parseUUID(testWorkspaceID)); ok {
		t.Fatal("unbound AgentPrincipal borrowed active Fleet membership from X-Agent-ID")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", recorder.Code, recorder.Body.String())
	}
	for _, deniedID := range []string{unboundAgentID, activeAgentID} {
		if strings.Contains(recorder.Body.String(), deniedID) {
			t.Fatalf("Fleet denial leaked Agent ID %q in body=%q", deniedID, recorder.Body.String())
		}
	}
	leadRecorder := httptest.NewRecorder()
	if _, ok := testHandler.requireResearchLeadActor(leadRecorder, request, parseUUID(testWorkspaceID)); ok {
		t.Fatal("unbound AgentPrincipal borrowed research Lead identity from X-Agent-ID")
	}
	if leadRecorder.Code != http.StatusForbidden {
		t.Fatalf("lead status=%d body=%s want 403", leadRecorder.Code, leadRecorder.Body.String())
	}

	positive := newRequest(http.MethodGet, "/api/agent/research/session", nil)
	positive = withAgentPrincipal(positive, activeAgentID, testWorkspaceID, testUserID)
	positive.Header.Set("X-Agent-ID", unboundAgentID)
	positiveRecorder := httptest.NewRecorder()
	member, ok := testHandler.requireActiveFleetMember(positiveRecorder, positive, parseUUID(testWorkspaceID))
	if !ok {
		t.Fatalf("active AgentPrincipal rejected through spoofed header: status=%d body=%s", positiveRecorder.Code, positiveRecorder.Body.String())
	}
	if uuidToString(member.AgentID) != activeAgentID {
		t.Fatalf("authorized member=%s want principal=%s", uuidToString(member.AgentID), activeAgentID)
	}
	positiveLeadRecorder := httptest.NewRecorder()
	lead, ok := testHandler.requireResearchLeadActor(positiveLeadRecorder, positive, parseUUID(testWorkspaceID))
	if !ok || uuidToString(lead.AgentID) != activeAgentID {
		t.Fatalf("active Lead principal rejected: ok=%v member=%s status=%d body=%s", ok, uuidToString(lead.AgentID), positiveLeadRecorder.Code, positiveLeadRecorder.Body.String())
	}
}

func TestSnapshotPassportGetResearchSessionSnapshotUsesSnapshotForAttemptWhenAttemptIDPresent(t *testing.T) {
	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	attemptID := uuid.NewString()

	engine := &recordingResearchRunEngine{}
	useResearchRunEngine(t, engine)

	path := "/api/research/sessions/" + uuidToString(sessionID) + "?attempt_id=" + attemptID
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", uuidToString(sessionID))

	recorder := httptest.NewRecorder()
	testHandler.GetResearchSessionSnapshot(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !engine.snapshotForAttemptCalled {
		t.Fatal("expected SnapshotForAttempt to be called when attempt_id is present")
	}
	if engine.snapshotCalled {
		t.Fatal("Snapshot must not be called when attempt_id is present")
	}
	if engine.snapshotSessionID != uuidToString(sessionID) {
		t.Fatalf("snapshot session id=%q, want %q", engine.snapshotSessionID, uuidToString(sessionID))
	}
	if engine.snapshotWorkspaceID != testWorkspaceID {
		t.Fatalf("snapshot workspace id=%q, want %q", engine.snapshotWorkspaceID, testWorkspaceID)
	}
	if engine.snapshotAttemptID != attemptID {
		t.Fatalf("snapshot attempt id=%q, want %q", engine.snapshotAttemptID, attemptID)
	}
}
