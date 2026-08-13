package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestResearchSessionSnapshotSameWorkspaceReturnsOK(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	engine := &recordingResearchRunEngine{}
	useResearchRunEngine(t, engine)

	path := "/api/research/sessions/" + uuidToString(sessionID)
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", uuidToString(sessionID))

	rec := httptest.NewRecorder()
	testHandler.GetResearchSessionSnapshot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-workspace status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if !engine.snapshotCalled {
		t.Fatal("expected Snapshot for same-workspace read")
	}
}

func TestResearchSessionSnapshotCrossWorkspaceReturnsNotFound(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	sessionID := seedInitializedResearchSessionForSnapshotTest(t)

	var foreignWorkspaceID string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, 'Research passport authorization fixture', 'RPA')
		RETURNING id::text
	`, "research-passport-foreign-"+suffix, "research-passport-foreign-"+suffix).Scan(&foreignWorkspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, foreignWorkspaceID)
	})

	path := "/api/research/sessions/" + uuidToString(sessionID)
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", uuidToString(sessionID))
	req.Header.Set("X-Workspace-ID", foreignWorkspaceID)

	rec := httptest.NewRecorder()
	testHandler.GetResearchSessionSnapshot(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
}

func TestResearchSessionSnapshotCrossWorkspaceAttemptIDReturnsNotFound(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	attemptID := uuid.NewString()

	var foreignWorkspaceID string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, 'Research passport authorization fixture', 'RPA')
		RETURNING id::text
	`, "research-passport-foreign-"+suffix, "research-passport-foreign-"+suffix).Scan(&foreignWorkspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, foreignWorkspaceID)
	})

	path := "/api/research/sessions/" + uuidToString(sessionID) + "?attempt_id=" + attemptID
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", uuidToString(sessionID))
	req.Header.Set("X-Workspace-ID", foreignWorkspaceID)

	rec := httptest.NewRecorder()
	testHandler.GetResearchSessionSnapshot(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace attempt snapshot status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
}

func TestResearchSessionSnapshotCrossWorkspaceDoesNotLeakSessionMetadata(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	secretGoal := "verify attempt-bound snapshot routing"
	sessionID := seedInitializedResearchSessionForSnapshotTest(t)

	var foreignWorkspaceID string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, 'Research passport authorization fixture', 'RPA')
		RETURNING id::text
	`, "research-passport-foreign-"+suffix, "research-passport-foreign-"+suffix).Scan(&foreignWorkspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, foreignWorkspaceID)
	})

	path := "/api/research/sessions/" + uuidToString(sessionID)
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", uuidToString(sessionID))
	req.Header.Set("X-Workspace-ID", foreignWorkspaceID)

	rec := httptest.NewRecorder()
	testHandler.GetResearchSessionSnapshot(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, secretGoal) {
		t.Fatalf("cross-workspace 404 leaked session goal in body=%q", body)
	}
	if strings.Contains(body, uuidToString(sessionID)) {
		t.Fatalf("cross-workspace 404 leaked session id in body=%q", body)
	}
}
