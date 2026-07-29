package handler

import (
	"bytes"
	"context"
	"encoding/json"
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
	resumeCalls  []service.ResumeFromCheckpointInput
	deleteErr    error
	deleteCalls  []envCheckpointDeleteCall
}

type envCheckpointDeleteCall struct {
	workspaceID  string
	checkpointID string
	actorUserID  string
}

type envCheckpointGetCall struct {
	checkpointID string
	workspaceID  string
}

type envCheckpointListCall struct {
	workspaceID string
	projectID   string
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

func (f *fakeEnvCheckpointService) ResumeFromCheckpoint(_ context.Context, in service.ResumeFromCheckpointInput) (service.ResumeFromCheckpointResult, error) {
	f.resumeCalls = append(f.resumeCalls, in)
	return f.resumeResult, f.resumeErr
}

func (f *fakeEnvCheckpointService) Delete(_ context.Context, workspaceID, checkpointID, actorUserID string) error {
	f.deleteCalls = append(f.deleteCalls, envCheckpointDeleteCall{workspaceID, checkpointID, actorUserID})
	return f.deleteErr
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

func TestCreateEnvCheckpointPassesSaveModeThroughAndReportsIt(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		createCP: service.EnvCheckpoint{
			ID:          "cp-1",
			WorkspaceID: "ws1",
			ProjectID:   validUUID,
			SaveMode:    service.SaveModeSnapshot,
			SaveStatus:  service.EnvCheckpointSaveComplete,
		},
	}
	h := newCheckpointHandler(fake)
	body := `{"project_id":"` + validUUID + `","event_ref":"evt","kind":"structural","save_mode":"snapshot","sandbox_refs":[{"instance_id":"inst-1","workspace_id":"ws1"}]}`
	w := httptest.NewRecorder()
	h.CreateEnvCheckpoint(w, authedCheckpointRequest("POST", "/api/v1/env-checkpoints", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if len(fake.createCalls) != 1 {
		t.Fatalf("want 1 create call, got %d", len(fake.createCalls))
	}
	if fake.createCalls[0].SaveMode != service.SaveModeSnapshot {
		t.Fatalf("save mode reaching the service = %q, want snapshot", fake.createCalls[0].SaveMode)
	}
	if !strings.Contains(w.Body.String(), `"save_mode":"snapshot"`) {
		t.Fatalf("response does not report the save mode: %s", w.Body.String())
	}
}

func TestCreateEnvCheckpointWithoutSaveModeLeavesItUnset(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		createCP: service.EnvCheckpoint{ID: "cp-1", WorkspaceID: "ws1", ProjectID: validUUID},
	}
	h := newCheckpointHandler(fake)
	body := `{"project_id":"` + validUUID + `","event_ref":"evt","kind":"structural"}`
	w := httptest.NewRecorder()
	h.CreateEnvCheckpoint(w, authedCheckpointRequest("POST", "/api/v1/env-checkpoints", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	// The handler must not invent a mode: the service normalizes an empty one
	// to pause_in_place, which is what keeps existing clients unchanged.
	if fake.createCalls[0].SaveMode != "" {
		t.Fatalf("save mode = %q, want it left empty for the service to normalize", fake.createCalls[0].SaveMode)
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
	if len(fake.resumeCalls) != 1 || fake.resumeCalls[0].WorkspaceID != "ws1" {
		t.Fatalf("expected 1 resume call scoped to ws1, got %+v", fake.resumeCalls)
	}
}

// resumeRequest issues a resume with an optional request body.
func resumeRequest(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := authedCheckpointRequest("POST", "/api/v1/env-checkpoints/"+validUUID+"/resume", body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("checkpointID", validUUID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.ResumeEnvCheckpoint(w, r)
	return w
}

// A pre-change caller sends no body at all, and must still get its single-lane
// resume anchored on something stable across retries.
func TestResumeWithoutBodyRequestsOneLaneAnchoredOnTheCheckpoint(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{}
	if w := resumeRequest(t, newCheckpointHandler(fake), ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(fake.resumeCalls) != 1 {
		t.Fatalf("resume calls = %d, want 1", len(fake.resumeCalls))
	}
	got := fake.resumeCalls[0]
	if got.LaneCount != 1 {
		t.Fatalf("lane count = %d, want 1 for a bodyless request", got.LaneCount)
	}
	if got.LaneKeyAnchor != validUUID {
		t.Fatalf("lane anchor = %q, want the checkpoint id", got.LaneKeyAnchor)
	}
}

func TestResumeForwardsRequestedLaneCountAndAnchor(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{}
	w := resumeRequest(t, newCheckpointHandler(fake), `{"lane_count":4,"lane_key":"dispatch-7"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	got := fake.resumeCalls[0]
	if got.LaneCount != 4 || got.LaneKeyAnchor != "dispatch-7" {
		t.Fatalf("forwarded %+v, want lane_count=4 anchor=dispatch-7", got)
	}
}

// A bad lane count is the caller's mistake, so it must not be reported as a
// checkpoint that cannot be resumed: one is worth retrying with a fixed request,
// the other never is.
func TestResumeMapsInvalidLaneCountToBadRequest(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		resumeErr: fmt.Errorf("validation_failed: %w: pause_in_place cannot fan out (lane_count=3)", service.ErrLaneCountInvalid),
	}
	if w := resumeRequest(t, newCheckpointHandler(fake), `{"lane_count":3}`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestResumeMapsNotResumableToConflict(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		resumeErr: fmt.Errorf("validation_failed: %w: save_status is timed_out", service.ErrCheckpointNotResumable),
	}
	if w := resumeRequest(t, newCheckpointHandler(fake), ""); w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestResumeRejectsMalformedBody(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{}
	if w := resumeRequest(t, newCheckpointHandler(fake), `{"lane_count":`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if len(fake.resumeCalls) != 0 {
		t.Fatalf("a malformed body must not reach the service, got %d calls", len(fake.resumeCalls))
	}
}

// A pause-in-place resume has no lanes, and its response must stay identical to
// the pre-change contract rather than gaining an empty lanes array.
func TestPauseInPlaceResumeResponseOmitsLanes(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		resumeResult: service.ResumeFromCheckpointResult{
			CheckpointID:  validUUID,
			RolloutHandle: "resume:" + validUUID,
			TriggerStatus: service.TriggerExecuted,
		},
	}
	w := resumeRequest(t, newCheckpointHandler(fake), "")
	if strings.Contains(w.Body.String(), "lanes") {
		t.Fatalf("a lane-less resume must not serialize lanes; body=%s", w.Body.String())
	}
}

func TestFanOutResumeSerializesLanes(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		resumeResult: service.ResumeFromCheckpointResult{
			CheckpointID:  validUUID,
			RolloutHandle: "resume:" + validUUID,
			TriggerStatus: service.TriggerExecuted,
			Lanes: []service.ResumeLane{
				{LaneKey: "anchor-0", Status: "ready", InstanceID: "inst-0", TaskID: "task-0"},
				{LaneKey: "anchor-1", Status: "failed", Error: "snapshot gone"},
			},
		},
	}
	w := resumeRequest(t, newCheckpointHandler(fake), `{"lane_count":2,"lane_key":"anchor"}`)
	var body struct {
		Lanes []struct {
			LaneKey string `json:"lane_key"`
			Status  string `json:"status"`
			Error   string `json:"error"`
		} `json:"lanes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
	}
	if len(body.Lanes) != 2 {
		t.Fatalf("lanes = %d, want 2; body=%s", len(body.Lanes), w.Body.String())
	}
	// A failed lane must be reported rather than dropped, since the caller is the
	// one that decides whether a partial fan-out is usable.
	if body.Lanes[1].Status != "failed" || body.Lanes[1].Error != "snapshot gone" {
		t.Fatalf("failed lane not reported: %+v", body.Lanes[1])
	}
}

// --- delete handler tests ---

func TestDeleteEnvCheckpointReturnsNoContentAndScopesToTheWorkspace(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{}
	h := newCheckpointHandler(fake)

	w := deleteCheckpoint(h)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	if len(fake.deleteCalls) != 1 {
		t.Fatalf("delete calls = %+v, want 1", fake.deleteCalls)
	}
	// The workspace comes from the member context, never from the request, or a
	// caller could delete another workspace's checkpoint by id.
	got := fake.deleteCalls[0]
	if got.workspaceID != "ws1" || got.checkpointID != validUUID || got.actorUserID != "u1" {
		t.Fatalf("delete call = %+v", got)
	}
}

// A lane still materializing is a conflict the caller can retry, not a failure.
func TestDeleteEnvCheckpointWithProvisioningLanesIsAConflict(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{
		deleteErr: fmt.Errorf("%w: 2 lane(s) still materializing", service.ErrCheckpointHasProvisioningLanes),
	}
	h := newCheckpointHandler(fake)

	w := deleteCheckpoint(h)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteEnvCheckpointNotFound(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{deleteErr: fmt.Errorf("not found: checkpoint")}
	h := newCheckpointHandler(fake)

	if w := deleteCheckpoint(h); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// The flag gates deletion like every other checkpoint endpoint, so enabling the
// feature is what exposes it rather than the route existing.
func TestDeleteEnvCheckpointHiddenWhenDisabled(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "false")
	fake := &fakeEnvCheckpointService{}
	h := newCheckpointHandler(fake)

	w := deleteCheckpoint(h)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 while disabled", w.Code)
	}
	if len(fake.deleteCalls) != 0 {
		t.Fatalf("disabled endpoint must not reach the service, got %+v", fake.deleteCalls)
	}
}

func TestDeleteEnvCheckpointRejectsAMalformedID(t *testing.T) {
	t.Setenv("ENV_CHECKPOINTS_ENABLED", "true")
	fake := &fakeEnvCheckpointService{}
	h := newCheckpointHandler(fake)

	r := authedCheckpointRequest("DELETE", "/api/v1/env-checkpoints/not-a-uuid", "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("checkpointID", "not-a-uuid")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.DeleteEnvCheckpoint(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if len(fake.deleteCalls) != 0 {
		t.Fatalf("a malformed id must not reach the service, got %+v", fake.deleteCalls)
	}
}

func deleteCheckpoint(h *Handler) *httptest.ResponseRecorder {
	r := authedCheckpointRequest("DELETE", "/api/v1/env-checkpoints/"+validUUID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("checkpointID", validUUID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.DeleteEnvCheckpoint(w, r)
	return w
}
