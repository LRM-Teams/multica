package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// sweLegoBuilderPollInterval paces DB polls while waiting for builder
	// create/exec/snapshot jobs to finish on the sandbox node.
	sweLegoBuilderPollInterval = 2 * time.Second
	// sweLegoBuilderCreateTimeout bounds waiting for the builder sandbox's
	// create job (Cube boot from the parent template).
	sweLegoBuilderCreateTimeout = 10 * time.Minute
	// sweLegoSnapshotTimeout bounds waiting for the create_template job; Cube
	// snapshots pause the sandbox and can take several minutes.
	sweLegoSnapshotTimeout = 15 * time.Minute
	// sweLegoExecTimeoutSeconds is the exec job budget. sandboxd caps exec jobs
	// at 300s; the in-sandbox subprocess timeout stays below the cap so the
	// script's exit code is reported before the job is killed.
	sweLegoExecTimeoutSeconds           = 300
	sweLegoExecSubprocessTimeoutSeconds = 280
	sweLegoBuilderSandboxName           = "swe-lego-template-builder"
	sweLegoTaskTemplateSnapshotName     = "swe-lego-task-template"
	sweLegoTaskTemplateSnapshotDesc     = "SWE-Lego materialized task template"
)

// newSweLegoTemplatePlacement builds the production placement decorator, or
// nil when the handler has no Queries (test fixtures). The same placement
// backs both injection points: env-dispatch uses its read-only
// SweLegoTemplateResolver face, the warm-up endpoint its full builder face.
func newSweLegoTemplatePlacement(h *Handler, lc *service.EnvSandboxLifecycleService) *sweLegoTemplatePlacement {
	if h == nil || h.Queries == nil || lc == nil {
		return nil
	}
	deps := &sweLegoTemplateDepsAdapter{
		h: h, lifecycle: lc,
		builders: map[string]sweLegoBuilderHandle{},
	}
	return &sweLegoTemplatePlacement{h: h, deps: deps, inner: service.NewSweLegoTemplateMaterializer(deps)}
}

// sweLegoTemplatePlacement resolves node placement and the parent Cube
// template before delegating to the service materializer: the node-local
// cache key and the builder sandbox both need them resolved up front.
type sweLegoTemplatePlacement struct {
	h     *Handler
	deps  *sweLegoTemplateDepsAdapter
	inner service.SweLegoTemplateMaterializer
}

func (m *sweLegoTemplatePlacement) Materialize(ctx context.Context, req service.SweLegoTemplateRequest) (string, error) {
	resolved, err := m.resolve(ctx, req)
	if err != nil {
		return "", err
	}
	return m.inner.Materialize(ctx, resolved)
}

// resolve fills NodeID and ParentTemplateID for a materialize request: pick
// an online node for the workspace, then fall back to the node's configured
// default Cube template when the caller did not pin a parent.
func (m *sweLegoTemplatePlacement) resolve(ctx context.Context, req service.SweLegoTemplateRequest) (service.SweLegoTemplateRequest, error) {
	wsUUID, err := util.ParseUUID(req.WorkspaceID)
	if err != nil {
		return req, fmt.Errorf("parse workspace_id: %w", err)
	}
	node, err := m.h.Queries.PickAvailableSandboxNodeForWorkspace(ctx, wsUUID)
	if err != nil {
		return req, fmt.Errorf("pick available sandbox node: %w", err)
	}
	req.NodeID = util.UUIDToString(node.ID)
	if req.ParentTemplateID == "" || req.ParentTemplateID == "default" {
		// Fall back to the node's configured default Cube template; "default"
		// is a sandboxd alias, not a cacheable parent identity.
		req.ParentTemplateID = nodeCubeTemplateID(node.Metadata)
	}
	if req.ParentTemplateID == "" {
		return req, fmt.Errorf("no parent cube template available on node %s (metadata.cube_template_id is empty)", req.NodeID)
	}
	return req, nil
}

// nodeCubeTemplateID reads the node's registered default Cube template
// (sandboxd publishes it as metadata.cube_template_id on registration).
func nodeCubeTemplateID(metadata []byte) string {
	var meta map[string]any
	if len(metadata) == 0 || json.Unmarshal(metadata, &meta) != nil {
		return ""
	}
	return strings.TrimSpace(stringFromAny(meta["cube_template_id"]))
}

// sweLegoBuilderHandle carries the builder sandbox identity the exec/snapshot/
// delete steps need after CreateBuilder returns; the materializer's deps
// interface passes only builderID/localRef downstream.
type sweLegoBuilderHandle struct {
	WorkspaceID string
	UserID      string
	NodeID      string
	LocalRef    string
}

// sweLegoTemplateDepsAdapter bridges service.SweLegoTemplateMaterializerDeps
// to *Handler.Queries (cache rows + job/snapshot lifecycle) and the shared
// env-sandbox lifecycle service (builder create/delete).
type sweLegoTemplateDepsAdapter struct {
	h         *Handler
	lifecycle *service.EnvSandboxLifecycleService

	mu       sync.Mutex
	builders map[string]sweLegoBuilderHandle // builderInstanceID -> handle
}

var _ service.SweLegoTemplateMaterializerDeps = (*sweLegoTemplateDepsAdapter)(nil)

func (a *sweLegoTemplateDepsAdapter) GetCache(ctx context.Context, nodeID, cacheKey string) (service.SweLegoTemplateCacheRecord, bool, error) {
	nodeUUID, err := util.ParseUUID(nodeID)
	if err != nil {
		return service.SweLegoTemplateCacheRecord{}, false, fmt.Errorf("parse node_id: %w", err)
	}
	row, err := a.h.Queries.GetSweLegoTemplateCache(ctx, db.GetSweLegoTemplateCacheParams{NodeID: nodeUUID, CacheKey: cacheKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return service.SweLegoTemplateCacheRecord{}, false, nil
	}
	if err != nil {
		return service.SweLegoTemplateCacheRecord{}, false, err
	}
	return service.SweLegoTemplateCacheRecord{
		Status:           row.Status,
		ParentTemplateID: row.ParentTemplateID,
		TaskTemplateID:   row.TaskTemplateID.String,
		Error:            row.Error.String,
	}, true, nil
}

func (a *sweLegoTemplateDepsAdapter) ClaimBuild(ctx context.Context, nodeID, cacheKey, parentTemplateID string) (bool, error) {
	nodeUUID, err := util.ParseUUID(nodeID)
	if err != nil {
		return false, fmt.Errorf("parse node_id: %w", err)
	}
	_, err = a.h.Queries.ClaimSweLegoTemplateBuild(ctx, db.ClaimSweLegoTemplateBuildParams{
		NodeID: nodeUUID, CacheKey: cacheKey, ParentTemplateID: parentTemplateID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// An existing building/ready row holds the claim.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (a *sweLegoTemplateDepsAdapter) CompleteBuild(ctx context.Context, nodeID, cacheKey, taskTemplateID string) error {
	nodeUUID, err := util.ParseUUID(nodeID)
	if err != nil {
		return fmt.Errorf("parse node_id: %w", err)
	}
	_, err = a.h.Queries.CompleteSweLegoTemplateBuild(ctx, db.CompleteSweLegoTemplateBuildParams{
		NodeID: nodeUUID, CacheKey: cacheKey, TaskTemplateID: pgtype.Text{String: taskTemplateID, Valid: taskTemplateID != ""},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("template build is not in building state")
	}
	return err
}

func (a *sweLegoTemplateDepsAdapter) FailBuild(ctx context.Context, nodeID, cacheKey, buildErr string) error {
	nodeUUID, err := util.ParseUUID(nodeID)
	if err != nil {
		return fmt.Errorf("parse node_id: %w", err)
	}
	_, err = a.h.Queries.FailSweLegoTemplateBuild(ctx, db.FailSweLegoTemplateBuildParams{
		NodeID: nodeUUID, CacheKey: cacheKey,
		Error: pgtype.Text{String: buildErr, Valid: buildErr != ""},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The row already resolved (e.g. completed concurrently); failing it is
		// best-effort cleanup, not a new failure.
		return nil
	}
	return err
}

// CreateBuilder boots a daemon-less builder sandbox from the parent template
// on the resolved node and waits for the create job to populate local_ref.
func (a *sweLegoTemplateDepsAdapter) CreateBuilder(ctx context.Context, req service.SweLegoTemplateRequest) (string, string, error) {
	ref, err := a.lifecycle.CreateSandboxInstance(ctx, service.CreateSandboxInstanceInput{
		WorkspaceID: req.WorkspaceID,
		NodeID:      req.NodeID,
		Template:    req.ParentTemplateID,
		Name:        sweLegoBuilderSandboxName,
		// DaemonEnabled stays false: the builder only holds the derived image;
		// no daemon (or credential mint) ever runs in it.
	}, req.UserID)
	if err != nil {
		return "", "", err
	}
	waitCtx, cancel := context.WithTimeout(ctx, sweLegoBuilderCreateTimeout)
	defer cancel()
	localRef, err := a.waitBuilderReady(waitCtx, req.WorkspaceID, ref.InstanceID)
	if err != nil {
		return "", "", err
	}
	a.mu.Lock()
	a.builders[ref.InstanceID] = sweLegoBuilderHandle{
		WorkspaceID: req.WorkspaceID, UserID: req.UserID, NodeID: ref.NodeID, LocalRef: localRef,
	}
	a.mu.Unlock()
	return ref.InstanceID, localRef, nil
}

// ExecBuilder runs the build script inside the builder via a sandboxd exec
// job and waits for the job to reach a terminal state.
func (a *sweLegoTemplateDepsAdapter) ExecBuilder(ctx context.Context, localRef, script string) error {
	handle, instanceID, err := a.builderByLocalRef(localRef)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"instance_id":     instanceID,
		"local_ref":       localRef,
		"code":            sweLegoBuilderExecCode(script),
		"language":        "python",
		"timeout_seconds": sweLegoExecTimeoutSeconds,
	})
	if err != nil {
		return fmt.Errorf("build exec payload: %w", err)
	}
	wsUUID, err := util.ParseUUID(handle.WorkspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace_id: %w", err)
	}
	userUUID, err := util.ParseUUID(handle.UserID)
	if err != nil {
		return fmt.Errorf("parse user_id: %w", err)
	}
	nodeUUID, err := util.ParseUUID(handle.NodeID)
	if err != nil {
		return fmt.Errorf("parse node_id: %w", err)
	}
	instUUID, err := util.ParseUUID(instanceID)
	if err != nil {
		return fmt.Errorf("parse instance_id: %w", err)
	}
	job, err := a.h.Queries.CreateSandboxJob(ctx, db.CreateSandboxJobParams{
		WorkspaceID: wsUUID, InitiatorUserID: userUUID, NodeID: nodeUUID, InstanceID: instUUID,
		Type: "exec", Payload: payload,
	})
	if err != nil {
		return fmt.Errorf("enqueue exec job: %w", err)
	}
	if a.h.SandboxHub != nil {
		a.h.SandboxHub.NotifyJobAvailable(handle.NodeID, util.UUIDToString(job.ID))
	}
	return a.waitSandboxJob(ctx, job.ID)
}

// SnapshotBuilder freezes the builder into a reusable Cube template via the
// existing sandbox_snapshot + create_template job lifecycle, then waits for
// the snapshot row to turn ready and returns the Cube template id.
func (a *sweLegoTemplateDepsAdapter) SnapshotBuilder(ctx context.Context, builderID string) (string, error) {
	handle, ok := a.builderByID(builderID)
	if !ok {
		return "", fmt.Errorf("unknown builder sandbox %q", builderID)
	}
	wsUUID, err := util.ParseUUID(handle.WorkspaceID)
	if err != nil {
		return "", fmt.Errorf("parse workspace_id: %w", err)
	}
	userUUID, err := util.ParseUUID(handle.UserID)
	if err != nil {
		return "", fmt.Errorf("parse user_id: %w", err)
	}
	instUUID, err := util.ParseUUID(builderID)
	if err != nil {
		return "", fmt.Errorf("parse instance_id: %w", err)
	}
	inst, err := a.h.Queries.GetSandboxInstanceForWorkspace(ctx, db.GetSandboxInstanceForWorkspaceParams{ID: instUUID, WorkspaceID: wsUUID})
	if err != nil {
		return "", fmt.Errorf("get builder sandbox: %w", err)
	}
	localRef := textValue(inst.LocalRef)
	if inst.Status != "running" || localRef == "" {
		return "", fmt.Errorf("builder sandbox is not ready for snapshot (status=%s)", inst.Status)
	}
	snap, err := a.h.Queries.CreateSandboxSnapshot(ctx, db.CreateSandboxSnapshotParams{
		WorkspaceID: wsUUID, NodeID: inst.NodeID, InstanceID: instUUID, CreatorUserID: userUUID,
		CubeSnapshotID: "",
		Name:           sweLegoTaskTemplateSnapshotName,
		Description:    sweLegoTaskTemplateSnapshotDesc,
		Status:         "creating",
		Metadata:       []byte("{}"),
	})
	if err != nil {
		return "", fmt.Errorf("create sandbox snapshot row: %w", err)
	}
	_, _ = a.h.Queries.UpdateSandboxInstanceStatus(ctx, db.UpdateSandboxInstanceStatusParams{
		ID: instUUID, Status: "snapshotting", Error: pgtype.Text{},
	})
	payload, err := json.Marshal(map[string]any{
		"instance_id": builderID,
		"local_ref":   localRef,
		"snapshot_id": util.UUIDToString(snap.ID),
		"name":        snap.Name,
		"description": snap.Description,
	})
	if err != nil {
		return "", fmt.Errorf("build create_template payload: %w", err)
	}
	job, err := a.h.Queries.CreateSandboxJob(ctx, db.CreateSandboxJobParams{
		WorkspaceID: wsUUID, InitiatorUserID: userUUID, NodeID: inst.NodeID, InstanceID: instUUID,
		Type: "create_template", Payload: payload,
	})
	if err != nil {
		_, _ = a.h.Queries.MarkSandboxSnapshotFailed(ctx, db.MarkSandboxSnapshotFailedParams{
			ID: snap.ID, WorkspaceID: wsUUID,
			Error: pgtype.Text{String: "failed to enqueue sandbox job", Valid: true},
		})
		return "", fmt.Errorf("enqueue create_template job: %w", err)
	}
	if a.h.SandboxHub != nil {
		a.h.SandboxHub.NotifyJobAvailable(handle.NodeID, util.UUIDToString(job.ID))
	}
	waitCtx, cancel := context.WithTimeout(ctx, sweLegoSnapshotTimeout)
	defer cancel()
	return a.waitSnapshotReady(waitCtx, wsUUID, snap.ID)
}

// DeleteBuilder reclaims the builder sandbox through the shared lifecycle
// delete path (sandboxd delete job with DB force-delete fallback).
func (a *sweLegoTemplateDepsAdapter) DeleteBuilder(ctx context.Context, builderID string) error {
	handle, ok := a.builderByID(builderID)
	a.forgetBuilder(builderID)
	if !ok {
		return nil
	}
	ref, err := a.lifecycle.GetSandboxInstanceRef(ctx, handle.WorkspaceID, builderID)
	if err != nil {
		return nil // already gone
	}
	return a.lifecycle.DeleteSandboxInstance(ctx, ref, handle.UserID)
}

// waitBuilderReady polls the instance row until the create job has populated
// local_ref (the Cube sandbox id) and the instance is running.
func (a *sweLegoTemplateDepsAdapter) waitBuilderReady(ctx context.Context, workspaceID, instanceID string) (string, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return "", fmt.Errorf("parse workspace_id: %w", err)
	}
	instUUID, err := util.ParseUUID(instanceID)
	if err != nil {
		return "", fmt.Errorf("parse instance_id: %w", err)
	}
	for {
		row, err := a.h.Queries.GetSandboxInstanceForWorkspace(ctx, db.GetSandboxInstanceForWorkspaceParams{ID: instUUID, WorkspaceID: wsUUID})
		if err != nil {
			return "", fmt.Errorf("get builder sandbox: %w", err)
		}
		switch row.Status {
		case "running":
			if ref := textValue(row.LocalRef); ref != "" {
				return ref, nil
			}
		case "failed":
			return "", fmt.Errorf("builder sandbox create failed: %s", textValue(row.Error))
		}
		if err := sweLegoSleep(ctx, sweLegoBuilderPollInterval); err != nil {
			return "", fmt.Errorf("wait for builder sandbox: %w", err)
		}
	}
}

// waitSandboxJob polls the job row until sandboxd reports a terminal state.
func (a *sweLegoTemplateDepsAdapter) waitSandboxJob(ctx context.Context, jobID pgtype.UUID) error {
	for {
		var status string
		var jobErr pgtype.Text
		if err := a.h.DB.QueryRow(ctx, "SELECT status, error FROM sandbox_job WHERE id = $1", jobID).Scan(&status, &jobErr); err != nil {
			return fmt.Errorf("poll sandbox job: %w", err)
		}
		switch status {
		case "completed":
			return nil
		case "failed":
			return fmt.Errorf("sandbox job failed: %s", strings.TrimSpace(jobErr.String))
		}
		if err := sweLegoSleep(ctx, sweLegoBuilderPollInterval); err != nil {
			return fmt.Errorf("wait for sandbox job: %w", err)
		}
	}
}

// waitSnapshotReady polls the snapshot row until the create_template job
// marks it ready (carrying the Cube snapshot id) or failed.
func (a *sweLegoTemplateDepsAdapter) waitSnapshotReady(ctx context.Context, wsUUID, snapID pgtype.UUID) (string, error) {
	for {
		row, err := a.h.Queries.GetSandboxSnapshotForWorkspace(ctx, db.GetSandboxSnapshotForWorkspaceParams{ID: snapID, WorkspaceID: wsUUID})
		if err != nil {
			return "", fmt.Errorf("get sandbox snapshot: %w", err)
		}
		switch row.Status {
		case "ready":
			if row.CubeSnapshotID == "" {
				return "", fmt.Errorf("snapshot ready without a cube snapshot id")
			}
			return row.CubeSnapshotID, nil
		case "failed":
			return "", fmt.Errorf("snapshot failed: %s", strings.TrimSpace(row.Error.String))
		}
		if err := sweLegoSleep(ctx, sweLegoBuilderPollInterval); err != nil {
			return "", fmt.Errorf("wait for sandbox snapshot: %w", err)
		}
	}
}

func (a *sweLegoTemplateDepsAdapter) builderByLocalRef(localRef string) (sweLegoBuilderHandle, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for instanceID, handle := range a.builders {
		if handle.LocalRef == localRef {
			return handle, instanceID, nil
		}
	}
	return sweLegoBuilderHandle{}, "", fmt.Errorf("unknown builder sandbox local_ref %q", localRef)
}

func (a *sweLegoTemplateDepsAdapter) builderByID(builderID string) (sweLegoBuilderHandle, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	handle, ok := a.builders[builderID]
	return handle, ok
}

func (a *sweLegoTemplateDepsAdapter) forgetBuilder(builderID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.builders, builderID)
}

// sweLegoBuilderExecCode wraps the bash build script in the Cube /execute
// python contract (exit-code sentinel + SystemExit on the subprocess exit
// code, mirroring the e2e sandbox-exec contract) so a failed build surfaces
// as an error event and fails the exec job.
func sweLegoBuilderExecCode(script string) string {
	quoted, _ := json.Marshal(script) // a JSON string literal is a valid python string literal here
	return "import subprocess\n" +
		"p = subprocess.run([\"bash\", \"-lc\", " + string(quoted) + "], capture_output=True, text=True, timeout=" +
		fmt.Sprintf("%d", sweLegoExecSubprocessTimeoutSeconds) + ")\n" +
		"print(p.stdout)\n" +
		"print(p.stderr)\n" +
		"print(f\"__EXIT_CODE__={p.returncode}\")\n" +
		"raise SystemExit(p.returncode)\n"
}

func sweLegoSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Manual warm-up endpoint: POST /api/v1/source-tasks/{sourceTaskID}/materialize
// ---------------------------------------------------------------------------

// sweLegoMaterializeStatus is the cache state for one warmup request.
type sweLegoMaterializeStatus struct {
	// Resolved carries the placement-resolved request the async build runs
	// with when the cache is neither ready nor building.
	Resolved service.SweLegoTemplateRequest
	// TaskTemplateID is non-empty iff the cache already holds a ready
	// materialized template (the warm-up is a no-op).
	TaskTemplateID string
	// Building reports a build already in progress for this cache key.
	Building bool
	// CacheStatus is the raw cache row status ("building" / "ready" /
	// "failed"), empty when no cache row exists. Lets read-only callers
	// (the env-dispatch resolver) report the precise state.
	CacheStatus string
	// LastError carries the cache row's recorded build error (failed rows),
	// so a warm-up retry can surface why the previous build failed instead
	// of silently retriggering.
	LastError string
}

// sweLegoWarmupBackend backs the manual source-task materialize endpoint.
// *sweLegoTemplatePlacement implements it in production; tests inject a fake.
type sweLegoWarmupBackend interface {
	LoadSourceTask(ctx context.Context, workspaceID, sourceTaskID string) (service.SourceTask, error)
	CheckCache(ctx context.Context, req service.SweLegoTemplateRequest) (sweLegoMaterializeStatus, error)
	BuildResolved(ctx context.Context, resolved service.SweLegoTemplateRequest) (string, error)
}

var _ sweLegoWarmupBackend = (*sweLegoTemplatePlacement)(nil)

// LoadSourceTask loads the workspace-scoped source task for the warm-up
// endpoint. A cross-workspace id is indistinguishable from a missing id
// (pgx.ErrNoRows) so the payload never leaks across workspaces.
func (m *sweLegoTemplatePlacement) LoadSourceTask(ctx context.Context, workspaceID, sourceTaskID string) (service.SourceTask, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return service.SourceTask{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	taskUUID, err := util.ParseUUID(sourceTaskID)
	if err != nil {
		return service.SourceTask{}, fmt.Errorf("parse source_task_id: %w", err)
	}
	row, err := m.h.Queries.GetSourceTaskForWorkspace(ctx, db.GetSourceTaskForWorkspaceParams{ID: taskUUID, WorkspaceID: wsUUID})
	if err != nil {
		return service.SourceTask{}, err
	}
	return service.SourceTask{
		ID:          util.UUIDToString(row.ID),
		WorkspaceID: util.UUIDToString(row.WorkspaceID),
		Type:        service.SourceTaskType(row.Type),
		Payload:     row.Payload,
		ContentHash: row.ContentHash,
	}, nil
}

// CheckCache resolves placement and reports the cache state for the request.
// Cache lookup failures are wrapped with the "check template cache:" prefix
// so the endpoint can tell them (500) apart from placement failures (503).
func (m *sweLegoTemplatePlacement) CheckCache(ctx context.Context, req service.SweLegoTemplateRequest) (sweLegoMaterializeStatus, error) {
	resolved, err := m.resolve(ctx, req)
	if err != nil {
		return sweLegoMaterializeStatus{}, err
	}
	key := service.SweLegoTemplateCacheKey(resolved.RepoURL, resolved.BaseCommit, resolved.IssueDate, resolved.ParentTemplateID)
	rec, ok, err := m.deps.GetCache(ctx, resolved.NodeID, key)
	if err != nil {
		return sweLegoMaterializeStatus{}, fmt.Errorf("check template cache: %w", err)
	}
	status := sweLegoMaterializeStatus{Resolved: resolved}
	if !ok {
		return status, nil
	}
	status.CacheStatus = rec.Status
	if rec.Status == "ready" && rec.TaskTemplateID != "" {
		status.TaskTemplateID = rec.TaskTemplateID
		return status, nil
	}
	status.Building = rec.Status == "building"
	status.LastError = rec.Error
	return status, nil
}

// LookupReadyTemplate is the read-only resolver face env-dispatch uses: it
// never claims or builds, only reports the cache row (template id on ready,
// raw status otherwise).
func (m *sweLegoTemplatePlacement) LookupReadyTemplate(ctx context.Context, req service.SweLegoTemplateRequest) (string, string, error) {
	status, err := m.CheckCache(ctx, req)
	if err != nil {
		return "", "", err
	}
	return status.TaskTemplateID, status.CacheStatus, nil
}

var _ service.SweLegoTemplateResolver = (*sweLegoTemplatePlacement)(nil)

// BuildResolved runs the materializer on an already placement-resolved
// request. A concurrent claim loss surfaces as an "already in progress"
// error; the endpoint treats it as a logged no-op (the other builder owns
// the row).
func (m *sweLegoTemplatePlacement) BuildResolved(ctx context.Context, resolved service.SweLegoTemplateRequest) (string, error) {
	return m.inner.Materialize(ctx, resolved)
}

// sweLegoWarmup returns the warm-up backend: the injected test double when
// set, otherwise the lazily constructed production placement (nil when the
// handler has no sandbox lifecycle, e.g. test fixtures without Queries).
func (h *Handler) sweLegoWarmup() sweLegoWarmupBackend {
	if h.SweLegoWarmup != nil {
		return h.SweLegoWarmup
	}
	return newSweLegoTemplatePlacement(h, newEnvSandboxLifecycleService(h))
}

type materializeSourceTaskResponse struct {
	Status         string `json:"status"` // "ready" | "building"
	TaskTemplateID string `json:"task_template_id,omitempty"`
	// CacheStatus is the cache row state observed before this response
	// ("building" / "failed"); empty when no row existed (fresh trigger).
	CacheStatus string `json:"cache_status,omitempty"`
	// LastError surfaces the previous build's recorded error when a failed
	// row is being retried, so operators can see why builds keep failing.
	LastError string `json:"last_error,omitempty"`
}

// sweLegoWarmupIssuePayload is the subset of the canonical issue source-task
// payload the warm-up endpoint validates and forwards to the materializer.
type sweLegoWarmupIssuePayload struct {
	RepoURL    string `json:"repo_url"`
	BaseCommit string `json:"base_commit"`
	IssueDate  string `json:"issue_date"`
}

// MaterializeSourceTaskTemplate manually warms the SWE-Lego task-template
// cache for an issue source task. The dispatch path is read-only — it fails
// validation until this endpoint has produced a ready cache row. Responses:
//
//   - 200 {"status":"ready","task_template_id":...}: cache already materialized.
//   - 202 {"status":"building"}: a build is in progress — either just claimed
//     by this request (which now runs it asynchronously) or claimed
//     concurrently. Poll again later.
//   - 400: not an issue source task, or the payload lacks repo_url /
//     base_commit / a valid RFC3339 issue_date.
//   - 404: unknown or cross-workspace source task.
//
// Operational semantics: an asynchronous build failure marks the cache row
// failed (the materializer already does this); retrying POST re-claims from
// the failed state and rebuilds. Dispatch never builds: it only reads the
// cache and fails validation until a ready row exists.
func (h *Handler) MaterializeSourceTaskTemplate(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	sourceTaskID := chi.URLParam(r, "sourceTaskID")
	if _, ok := parseUUIDOrBadRequest(w, sourceTaskID, "source_task_id"); !ok {
		return
	}
	backend := h.sweLegoWarmup()
	if backend == nil {
		writeError(w, http.StatusServiceUnavailable, "template materialization unavailable")
		return
	}
	source, err := backend.LoadSourceTask(r.Context(), workspaceID, sourceTaskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "source task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "get source task")
		return
	}
	if source.Type != service.SourceTaskIssue {
		writeError(w, http.StatusBadRequest, "source task type must be issue")
		return
	}
	var payload sweLegoWarmupIssuePayload
	if err := json.Unmarshal(source.Payload, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "decode issue source task payload")
		return
	}
	var missing []string
	if strings.TrimSpace(payload.RepoURL) == "" {
		missing = append(missing, "repo_url")
	}
	if strings.TrimSpace(payload.BaseCommit) == "" {
		missing = append(missing, "base_commit")
	}
	if strings.TrimSpace(payload.IssueDate) == "" {
		missing = append(missing, "issue_date")
	}
	if len(missing) > 0 {
		writeError(w, http.StatusBadRequest, "source task payload missing "+strings.Join(missing, ", "))
		return
	}
	if _, err := time.Parse(time.RFC3339, payload.IssueDate); err != nil {
		writeError(w, http.StatusBadRequest, "source task payload issue_date must be RFC3339")
		return
	}
	status, err := backend.CheckCache(r.Context(), service.SweLegoTemplateRequest{
		WorkspaceID:  workspaceID,
		UserID:       userID,
		SourceTaskID: sourceTaskID,
		RepoURL:      payload.RepoURL,
		BaseCommit:   payload.BaseCommit,
		IssueDate:    payload.IssueDate,
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "check template cache:") {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if status.TaskTemplateID != "" {
		writeJSON(w, http.StatusOK, materializeSourceTaskResponse{Status: "ready", TaskTemplateID: status.TaskTemplateID})
		return
	}
	if !status.Building {
		// First trigger (or a retry after a failed build): claim + build
		// asynchronously, detached from the request lifecycle. A concurrent
		// claim loss is a logged no-op — the other builder owns the row.
		go func() {
			if _, err := backend.BuildResolved(context.WithoutCancel(r.Context()), status.Resolved); err != nil {
				slog.Warn("swe-lego template warm-up build failed",
					"workspace_id", workspaceID, "source_task_id", sourceTaskID, "error", err)
			}
		}()
	}
	writeJSON(w, http.StatusAccepted, materializeSourceTaskResponse{Status: "building", CacheStatus: status.CacheStatus, LastError: status.LastError})
}
