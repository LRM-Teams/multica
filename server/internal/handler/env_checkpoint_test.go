package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- fake ---

type fakeEnvCheckpointService struct {
	createCP     service.EnvCheckpoint
	createErr    error
	getCP        service.EnvCheckpoint
	getErr       error
	listCPs      []service.EnvCheckpoint
	listErr      error
	resumeResult service.ResumeFromCheckpointResult
	resumeErr    error
	createCalls  []service.EnvCheckpointCreateInput
	getCalls     []envCheckpointGetCall
	listCalls    []envCheckpointListCall
	resumeCalls  []envCheckpointResumeCall
}

type envCheckpointGetCall struct {
	checkpointID string
	workspaceID  string
}

type envCheckpointListCall struct {
	workspaceID string
	projectID   string
}

type envCheckpointResumeCall struct {
	workspaceID  string
	checkpointID string
	actorUserID  string
}

func (f *fakeEnvCheckpointService) Create(_ context.Context, in service.EnvCheckpointCreateInput) (service.EnvCheckpoint, error) {
	f.createCalls = append(f.createCalls, in)
	return f.createCP, f.createErr
}

func (f *fakeEnvCheckpointService) Get(_ context.Context, checkpointID, workspaceID string) (service.EnvCheckpoint, error) {
	f.getCalls = append(f.getCalls, envCheckpointGetCall{checkpointID, workspaceID})
	return f.getCP, f.getErr
}

func (f *fakeEnvCheckpointService) List(_ context.Context, workspaceID, projectID string) ([]service.EnvCheckpoint, error) {
	f.listCalls = append(f.listCalls, envCheckpointListCall{workspaceID, projectID})
	return f.listCPs, f.listErr
}

func (f *fakeEnvCheckpointService) ResumeFromCheckpoint(_ context.Context, workspaceID, checkpointID, actorUserID string) (service.ResumeFromCheckpointResult, error) {
	f.resumeCalls = append(f.resumeCalls, envCheckpointResumeCall{workspaceID, checkpointID, actorUserID})
	return f.resumeResult, f.resumeErr
}

// --- helpers ---

func newCheckpointHandler(fake *fakeEnvCheckpointService) *Handler {
	h := newTestHandler(Config{})
	h.EnvCheckpointService = fake
	return h
}

func authedCheckpointRequest(method, path, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("X-User-ID", "u1")
	r = r.WithContext(middleware.SetMemberContext(r.Context(), "ws1", db.Member{}))
	return r
}

// --- tests ---

func TestCreateEnvCheckpointDisabledReturns404(t *testing.T) {
	// ENV_CHECKPOINTS_ENABLED defaults to false → 404.
	h := newCheckpointHandler(&fakeEnvCheckpointService{})
	body := `{"project_id":"` + validUUID + `","event_ref":"evt","kind":"structural"}`
	w := httptest.NewRecorder()
	h.CreateEnvCheckpoint(w, authedCheckpointRequest("POST", "/api/v1/env-checkpoints", body))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (disabled); body=%s", w.Code, w.Body.String())
	}
}

func TestCreateEnvCheckpointReturnsCreatedWhenSaveCompletes(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		createCP: service.EnvCheckpoint{
			ID:          "cp-1",
			WorkspaceID: "ws1",
			ProjectID:   validUUID,
			SaveStatus:  service.EnvCheckpointSaveComplete,
		},
	}
	h := newCheckpointHandler(fake)
	body := `{"project_id":"` + validUUID + `","event_ref":"evt","kind":"structural","sandbox_refs":[{"instance_id":"inst-1","workspace_id":"ws1"}]}`
	w := httptest.NewRecorder()
	h.CreateEnvCheckpoint(w, authedCheckpointRequest("POST", "/api/v1/env-checkpoints", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if len(fake.createCalls) != 1 {
		t.Fatalf("want 1 create call, got %d", len(fake.createCalls))
	}
	if fake.createCalls[0].WorkspaceID != "ws1" {
		t.Fatalf("create call workspace = %q, want ws1", fake.createCalls[0].WorkspaceID)
	}
	if len(fake.createCalls[0].SandboxRefs) != 1 {
		t.Fatalf("want 1 sandbox ref in create call, got %d", len(fake.createCalls[0].SandboxRefs))
	}
}

func TestCreateEnvCheckpointTimeoutMapsToConflict(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		createErr: fmt.Errorf("save timed_out: context deadline exceeded"),
	}
	h := newCheckpointHandler(fake)
	body := `{"project_id":"` + validUUID + `","event_ref":"evt","kind":"structural"}`
	w := httptest.NewRecorder()
	h.CreateEnvCheckpoint(w, authedCheckpointRequest("POST", "/api/v1/env-checkpoints", body))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (timeout); body=%s", w.Code, w.Body.String())
	}
}

func TestListEnvCheckpointsRequiresWorkspaceAndNewestFirst(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	base := time.Now()
	fake := &fakeEnvCheckpointService{
		listCPs: []service.EnvCheckpoint{
			{ID: "cp-2", WorkspaceID: "ws1", ProjectID: validUUID, CreatedAt: base},
			{ID: "cp-1", WorkspaceID: "ws1", ProjectID: validUUID, CreatedAt: base.Add(-time.Minute)},
		},
	}
	h := newCheckpointHandler(fake)
	r := authedCheckpointRequest("GET", "/api/v1/projects/"+validUUID+"/env-checkpoints", "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", validUUID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.ListEnvCheckpoints(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(fake.listCalls) != 1 || fake.listCalls[0].workspaceID != "ws1" {
		t.Fatalf("expected 1 list call scoped to ws1, got %+v", fake.listCalls)
	}
}

func TestGetEnvCheckpointCrossWorkspaceDoesNotLeak(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		getErr: fmt.Errorf("not found"),
	}
	h := newCheckpointHandler(fake)
	r := authedCheckpointRequest("GET", "/api/v1/env-checkpoints/"+validUUID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("checkpointID", validUUID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.GetEnvCheckpoint(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-workspace); body=%s", w.Code, w.Body.String())
	}
	if len(fake.getCalls) != 1 || fake.getCalls[0].workspaceID != "ws1" {
		t.Fatalf("expected 1 get call scoped to ws1, got %+v", fake.getCalls)
	}
}

// --- resume handler tests ---

func TestResumeFromCheckpointRouteUsesResumeNaming(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		resumeResult: service.ResumeFromCheckpointResult{
			CheckpointID:  validUUID,
			ProjectID:     validUUID,
			RolloutHandle: "resume:" + validUUID,
		},
	}
	h := newCheckpointHandler(fake)
	r := authedCheckpointRequest("POST", "/api/v1/env-checkpoints/"+validUUID+"/resume", "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("checkpointID", validUUID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.ResumeEnvCheckpoint(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "rollout_handle") {
		t.Fatalf("response should include rollout_handle; body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "resume:"+validUUID) {
		t.Fatalf("response should use resume terminology in handle; body=%s", w.Body.String())
	}
}

func TestResumeFromCheckpointMapsIncompleteToConflict(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		resumeErr: fmt.Errorf("validation_failed: checkpoint save_status is timed_out, must be complete to resume"),
	}
	h := newCheckpointHandler(fake)
	r := authedCheckpointRequest("POST", "/api/v1/env-checkpoints/"+validUUID+"/resume", "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("checkpointID", validUUID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.ResumeEnvCheckpoint(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (incomplete); body=%s", w.Code, w.Body.String())
	}
}

func TestResumeFromCheckpointCrossWorkspaceRejected(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		resumeErr: fmt.Errorf("not found"),
	}
	h := newCheckpointHandler(fake)
	r := authedCheckpointRequest("POST", "/api/v1/env-checkpoints/"+validUUID+"/resume", "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("checkpointID", validUUID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.ResumeEnvCheckpoint(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-workspace); body=%s", w.Code, w.Body.String())
	}
	if len(fake.resumeCalls) != 1 || fake.resumeCalls[0].workspaceID != "ws1" {
		t.Fatalf("expected 1 resume call scoped to ws1, got %+v", fake.resumeCalls)
	}
}
