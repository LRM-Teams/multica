package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeSweLegoWarmupBackend records endpoint interactions so tests can assert
// cache-hit / trigger / in-progress behavior without a DB or sandbox node.
type fakeSweLegoWarmupBackend struct {
	source    service.SourceTask
	sourceErr error
	status    sweLegoMaterializeStatus
	statusErr error

	mu          sync.Mutex
	checkCalls  []service.SweLegoTemplateRequest
	buildCalled chan service.SweLegoTemplateRequest
}

func (f *fakeSweLegoWarmupBackend) LoadSourceTask(context.Context, string, string) (service.SourceTask, error) {
	return f.source, f.sourceErr
}

func (f *fakeSweLegoWarmupBackend) CheckCache(_ context.Context, req service.SweLegoTemplateRequest) (sweLegoMaterializeStatus, error) {
	f.mu.Lock()
	f.checkCalls = append(f.checkCalls, req)
	f.mu.Unlock()
	return f.status, f.statusErr
}

func (f *fakeSweLegoWarmupBackend) BuildResolved(_ context.Context, resolved service.SweLegoTemplateRequest) (string, error) {
	f.buildCalled <- resolved
	return "tpl-task", nil
}

func newFakeSweLegoWarmupBackend() *fakeSweLegoWarmupBackend {
	return &fakeSweLegoWarmupBackend{buildCalled: make(chan service.SweLegoTemplateRequest, 1)}
}

const (
	warmupWorkspaceID  = "11111111-1111-1111-1111-111111111111"
	warmupUserID       = "22222222-2222-2222-2222-222222222222"
	warmupSourceTaskID = "33333333-3333-3333-3333-333333333333"
)

func issueWarmupSource(payload string) service.SourceTask {
	return service.SourceTask{
		ID: warmupSourceTaskID, WorkspaceID: warmupWorkspaceID,
		Type: service.SourceTaskIssue, Payload: json.RawMessage(payload),
	}
}

func newMaterializeRequest(sourceTaskID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/source-tasks/"+sourceTaskID+"/materialize", nil)
	req.Header.Set("X-User-ID", warmupUserID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sourceTaskID", sourceTaskID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = middleware.SetMemberContext(ctx, warmupWorkspaceID, db.Member{})
	return req.WithContext(ctx)
}

func serveMaterialize(h *Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.MaterializeSourceTaskTemplate(rec, req)
	return rec
}

func (f *fakeSweLegoWarmupBackend) buildTriggered(t *testing.T) service.SweLegoTemplateRequest {
	t.Helper()
	select {
	case req := <-f.buildCalled:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("expected an async build to be triggered")
		return service.SweLegoTemplateRequest{}
	}
}

func (f *fakeSweLegoWarmupBackend) assertNoBuild(t *testing.T) {
	t.Helper()
	select {
	case <-f.buildCalled:
		t.Fatal("no build should have been triggered")
	case <-time.After(50 * time.Millisecond):
	}
}

// Cache already materialized: 200 ready, no build.
func TestMaterializeSourceTaskTemplate_CacheReady(t *testing.T) {
	backend := newFakeSweLegoWarmupBackend()
	backend.source = issueWarmupSource(`{"repo_url":"https://example.test/r.git","base_commit":"abc","issue_date":"2025-01-02T03:04:05Z"}`)
	backend.status = sweLegoMaterializeStatus{TaskTemplateID: "tpl-task"}
	h := &Handler{SweLegoWarmup: backend}

	rec := serveMaterialize(h, newMaterializeRequest(warmupSourceTaskID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"ready"`) || !strings.Contains(rec.Body.String(), `"tpl-task"`) {
		t.Fatalf("body = %s", rec.Body)
	}
	backend.assertNoBuild(t)
}

// Never built: claim + async build, immediate 202 building.
func TestMaterializeSourceTaskTemplate_FirstTriggerStartsAsyncBuild(t *testing.T) {
	backend := newFakeSweLegoWarmupBackend()
	backend.source = issueWarmupSource(`{"repo_url":"https://example.test/r.git","base_commit":"abc","issue_date":"2025-01-02T03:04:05Z"}`)
	backend.status = sweLegoMaterializeStatus{Resolved: service.SweLegoTemplateRequest{NodeID: "node-1", ParentTemplateID: "tpl-parent"}}
	h := &Handler{SweLegoWarmup: backend}

	rec := serveMaterialize(h, newMaterializeRequest(warmupSourceTaskID))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"building"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	resolved := backend.buildTriggered(t)
	if resolved.NodeID != "node-1" || resolved.ParentTemplateID != "tpl-parent" {
		t.Fatalf("build ran with %+v", resolved)
	}
	if len(backend.checkCalls) != 1 {
		t.Fatalf("check calls = %d", len(backend.checkCalls))
	}
	req := backend.checkCalls[0]
	if req.RepoURL != "https://example.test/r.git" || req.BaseCommit != "abc" || req.IssueDate != "2025-01-02T03:04:05Z" {
		t.Fatalf("check request = %+v", req)
	}
	if req.WorkspaceID != warmupWorkspaceID || req.UserID != warmupUserID || req.SourceTaskID != warmupSourceTaskID {
		t.Fatalf("check request identity = %+v", req)
	}
}

// A build is already in progress: 202 building, no second build.
func TestMaterializeSourceTaskTemplate_BuildAlreadyInProgress(t *testing.T) {
	backend := newFakeSweLegoWarmupBackend()
	backend.source = issueWarmupSource(`{"repo_url":"https://example.test/r.git","base_commit":"abc","issue_date":"2025-01-02T03:04:05Z"}`)
	backend.status = sweLegoMaterializeStatus{Building: true}
	h := &Handler{SweLegoWarmup: backend}

	rec := serveMaterialize(h, newMaterializeRequest(warmupSourceTaskID))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"building"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	backend.assertNoBuild(t)
}

// A failed row is retried and the previous error is surfaced, never hidden.
func TestMaterializeSourceTaskTemplate_FailedRetrySurfacesLastError(t *testing.T) {
	backend := newFakeSweLegoWarmupBackend()
	backend.source = issueWarmupSource(`{"repo_url":"https://example.test/r.git","base_commit":"abc","issue_date":"2025-01-02T03:04:05Z"}`)
	backend.status = sweLegoMaterializeStatus{
		Resolved:    service.SweLegoTemplateRequest{NodeID: "node-1", ParentTemplateID: "tpl-parent"},
		CacheStatus: "failed",
		LastError:   "execute builder: exit 1: git filter-repo: command not found",
	}
	h := &Handler{SweLegoWarmup: backend}

	rec := serveMaterialize(h, newMaterializeRequest(warmupSourceTaskID))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"building"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"cache_status":"failed"`) || !strings.Contains(rec.Body.String(), "git filter-repo") {
		t.Fatalf("previous failure not surfaced: %s", rec.Body)
	}
	backend.buildTriggered(t)
}

// Message source tasks are rejected.
func TestMaterializeSourceTaskTemplate_RejectsMessageSource(t *testing.T) {
	backend := newFakeSweLegoWarmupBackend()
	backend.source = service.SourceTask{
		ID: warmupSourceTaskID, WorkspaceID: warmupWorkspaceID,
		Type: service.SourceTaskMessage, Payload: json.RawMessage(`{"content":"hi"}`),
	}
	h := &Handler{SweLegoWarmup: backend}

	rec := serveMaterialize(h, newMaterializeRequest(warmupSourceTaskID))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "type must be issue") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

// Payloads missing repo_url/base_commit/issue_date (or carrying a malformed
// issue_date) are rejected with the missing fields named.
func TestMaterializeSourceTaskTemplate_RejectsIncompletePayload(t *testing.T) {
	cases := map[string]struct {
		payload string
		want    string
	}{
		"missing repo fields":    {`{"title":"t","description":"d"}`, "repo_url"},
		"missing base commit":    {`{"repo_url":"https://example.test/r.git","issue_date":"2025-01-02T03:04:05Z"}`, "base_commit"},
		"missing issue date":     {`{"repo_url":"https://example.test/r.git","base_commit":"abc"}`, "issue_date"},
		"issue date not RFC3339": {`{"repo_url":"https://example.test/r.git","base_commit":"abc","issue_date":"yesterday"}`, "issue_date must be RFC3339"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			backend := newFakeSweLegoWarmupBackend()
			backend.source = issueWarmupSource(tc.payload)
			h := &Handler{SweLegoWarmup: backend}

			rec := serveMaterialize(h, newMaterializeRequest(warmupSourceTaskID))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status = %d body = %s, want mention of %q", rec.Code, rec.Body, tc.want)
			}
		})
	}
}

// A cross-workspace (or unknown) source task is a 404 and never reaches the
// materializer.
func TestMaterializeSourceTaskTemplate_CrossWorkspaceNotFound(t *testing.T) {
	backend := newFakeSweLegoWarmupBackend()
	backend.sourceErr = pgx.ErrNoRows
	h := &Handler{SweLegoWarmup: backend}

	rec := serveMaterialize(h, newMaterializeRequest(warmupSourceTaskID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if len(backend.checkCalls) != 0 {
		t.Fatalf("materializer must not run for a missing source task, checks = %d", len(backend.checkCalls))
	}
}
