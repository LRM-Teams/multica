package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type researchSnapshotPrincipalFixture struct {
	sessionID         string
	attemptID         string
	assignedAgentID   string
	unassignedAgentID string
	unboundAgentID    string
	passportID        string
}

func seedResearchSnapshotPrincipalFixture(t *testing.T) researchSnapshotPrincipalFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	assignedAgentID := ensureMergableFleetMember(t)
	unassignedAgentID := createHandlerTestAgent(t, "snapshot-unassigned-"+uuid.NewString()[:8], nil)
	unboundAgentID := createHandlerTestAgent(t, "snapshot-unbound-"+uuid.NewString()[:8], nil)
	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	ctx := context.Background()

	var fleetID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT fleet_id FROM research_session
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, testWorkspaceID, sessionID).Scan(&fleetID); err != nil {
		t.Fatalf("load fixture fleet: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status, is_lead)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'researcher', 'active', false)
	`, testWorkspaceID, fleetID, unassignedAgentID); err != nil {
		t.Fatalf("create unassigned Fleet member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			DELETE FROM research_fleet_member
			WHERE workspace_id = $1::uuid AND fleet_id = $2::uuid AND agent_id = $3::uuid
		`, testWorkspaceID, fleetID, unassignedAgentID)
	})

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin principal matrix fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var taskID pgtype.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_task (
			workspace_id, session_id, client_key, kind, objective,
			required_capability, expected_result, acceptance_criteria,
			priority, status, assigned_agent_id, goal_version, plan_version,
			max_attempts, timeout_seconds, ready_at
		) VALUES (
			$1::uuid, $2::uuid, $3, 'discover', 'authorize an attempt-bound snapshot',
			'research', 'authorized snapshot', '{}'::jsonb,
			0.5, 'running', $4::uuid, 1, 1, 3, 1800, now()
		) RETURNING id
	`, testWorkspaceID, sessionID, "principal-matrix-"+uuid.NewString(), assignedAgentID).Scan(&taskID); err != nil {
		t.Fatalf("create principal matrix task: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_passport (
			id, workspace_id, session_id, entity_kind, provenance_completeness
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'task', 'complete')
	`, taskID, testWorkspaceID, sessionID); err != nil {
		t.Fatalf("create task passport: %v", err)
	}

	var attemptID pgtype.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_task_attempt (
			workspace_id, session_id, task_id, attempt_number,
			assigned_agent_id, dispatch_key, status
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, $4::uuid, $5, 'running')
		RETURNING id
	`, testWorkspaceID, sessionID, taskID, assignedAgentID, "principal-matrix-"+uuid.NewString()).Scan(&attemptID); err != nil {
		t.Fatalf("create principal matrix attempt: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_passport (
			id, workspace_id, session_id, entity_kind, provenance_completeness
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'attempt', 'complete')
	`, attemptID, testWorkspaceID, sessionID); err != nil {
		t.Fatalf("create attempt passport: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit principal matrix fixture: %v", err)
	}

	return researchSnapshotPrincipalFixture{
		sessionID:         uuidToString(sessionID),
		attemptID:         uuidToString(attemptID),
		assignedAgentID:   assignedAgentID,
		unassignedAgentID: unassignedAgentID,
		unboundAgentID:    unboundAgentID,
		passportID:        uuid.NewString(),
	}
}

func principalMatrixSnapshot(f researchSnapshotPrincipalFixture) researchrun.RunSnapshot {
	return researchrun.RunSnapshot{ArtifactProjection: &researchrun.ArtifactProjection{
		ProjectionHash: "sha256:" + strings.Repeat("a", 64),
		Items: []researchrun.ArtifactProjectionItem{{
			ID:                     f.passportID,
			RunID:                  f.sessionID,
			EntityKind:             "attempt",
			EntityID:               f.attemptID,
			EligibilityRevision:    1,
			LifecycleStatus:        "registered",
			ProvenanceCompleteness: "complete",
			SchemaName:             "research.attempt",
			SchemaVersion:          "1",
			AccessLevel:            "verified_only",
			VersionCount:           1,
		}},
	}}
}

func TestResearchSnapshotPrincipalSurfaceMatrix(t *testing.T) {
	fixture := seedResearchSnapshotPrincipalFixture(t)

	t.Run("same-scope human sees live projection", func(t *testing.T) {
		engine := &recordingResearchRunEngine{snapshot: principalMatrixSnapshot(fixture)}
		useResearchRunEngine(t, engine)
		path := "/api/research/sessions/" + fixture.sessionID
		req := withURLParam(newRequest(http.MethodGet, path, nil), "id", fixture.sessionID)
		recorder := httptest.NewRecorder()

		testHandler.GetResearchSessionSnapshot(recorder, req)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if !engine.snapshotCalled || engine.snapshotForAttemptCalled {
			t.Fatalf("snapshot calls: live=%v attempt=%v", engine.snapshotCalled, engine.snapshotForAttemptCalled)
		}
		if !strings.Contains(recorder.Body.String(), fixture.passportID) {
			t.Fatalf("same-scope human response omitted allowed passport %q", fixture.passportID)
		}
	})

	t.Run("assigned active Agent sees attempt projection", func(t *testing.T) {
		engine := &recordingResearchRunEngine{snapshot: principalMatrixSnapshot(fixture)}
		useResearchRunEngine(t, engine)
		path := fmt.Sprintf("/api/agent/research/sessions/%s?snapshot=1&attempt_id=%s", fixture.sessionID, fixture.attemptID)
		req := withURLParam(newRequest(http.MethodGet, path, nil), "id", fixture.sessionID)
		req = withAgentPrincipal(req, fixture.assignedAgentID, testWorkspaceID, testUserID)
		recorder := httptest.NewRecorder()

		testHandler.GetAgentResearchSessionSnapshot(recorder, req)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if engine.snapshotCalled || !engine.snapshotForAttemptCalled {
			t.Fatalf("snapshot calls: live=%v attempt=%v", engine.snapshotCalled, engine.snapshotForAttemptCalled)
		}
		if engine.snapshotAttemptID != fixture.attemptID {
			t.Fatalf("attempt=%q want=%q", engine.snapshotAttemptID, fixture.attemptID)
		}
		if !strings.Contains(recorder.Body.String(), fixture.passportID) {
			t.Fatalf("assigned Agent response omitted allowed passport %q", fixture.passportID)
		}
	})

	t.Run("revoked manifest grant denies Agent surface without leaking projection", func(t *testing.T) {
		engine := &recordingResearchRunEngine{
			snapshot:              principalMatrixSnapshot(fixture),
			snapshotForAttemptErr: researchrun.ErrArtifactAccessDenied,
		}
		useResearchRunEngine(t, engine)
		path := fmt.Sprintf("/api/agent/research/sessions/%s?attempt_id=%s", fixture.sessionID, fixture.attemptID)
		req := withURLParam(newRequest(http.MethodGet, path, nil), "id", fixture.sessionID)
		req = withAgentPrincipal(req, fixture.assignedAgentID, testWorkspaceID, testUserID)
		recorder := httptest.NewRecorder()

		testHandler.GetAgentResearchSessionSnapshot(recorder, req)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if !engine.snapshotForAttemptCalled || strings.Contains(recorder.Body.String(), fixture.passportID) {
			t.Fatalf("revoked surface call=%v body=%q", engine.snapshotForAttemptCalled, recorder.Body.String())
		}
	})

	denied := []struct {
		name        string
		agentID     string
		workspaceID string
		spoofedID   string
		wantStatus  int
	}{
		{name: "unbound Fleet Agent", agentID: fixture.unboundAgentID, workspaceID: testWorkspaceID, wantStatus: http.StatusForbidden},
		{name: "active but unassigned Fleet Agent", agentID: fixture.unassignedAgentID, workspaceID: testWorkspaceID, wantStatus: http.StatusNotFound},
		{name: "cross-workspace principal", agentID: fixture.assignedAgentID, workspaceID: uuid.NewString(), wantStatus: http.StatusForbidden},
		{name: "spoofed active Agent header", agentID: fixture.unboundAgentID, workspaceID: testWorkspaceID, spoofedID: fixture.assignedAgentID, wantStatus: http.StatusForbidden},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			engine := &recordingResearchRunEngine{snapshot: principalMatrixSnapshot(fixture)}
			useResearchRunEngine(t, engine)
			path := fmt.Sprintf("/api/agent/research/sessions/%s?attempt_id=%s", fixture.sessionID, fixture.attemptID)
			req := withURLParam(newRequest(http.MethodGet, path, nil), "id", fixture.sessionID)
			req = withAgentPrincipal(req, tc.agentID, tc.workspaceID, testUserID)
			if tc.spoofedID != "" {
				req.Header.Set("X-Agent-ID", tc.spoofedID)
			}
			recorder := httptest.NewRecorder()

			testHandler.GetAgentResearchSessionSnapshot(recorder, req)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s want=%d", recorder.Code, recorder.Body.String(), tc.wantStatus)
			}
			if engine.snapshotCalled || engine.snapshotForAttemptCalled {
				t.Fatalf("denied request loaded snapshot: live=%v attempt=%v", engine.snapshotCalled, engine.snapshotForAttemptCalled)
			}
			for _, secret := range []string{
				fixture.sessionID, fixture.attemptID, fixture.assignedAgentID,
				fixture.unassignedAgentID, fixture.unboundAgentID, fixture.passportID,
			} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("denial leaked identifier %q in body=%q", secret, recorder.Body.String())
				}
			}
		})
	}
}
