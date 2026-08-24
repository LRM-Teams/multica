package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// envSandboxLifecycleDefaultTimeout is the default save/resume wait for the
// env sandbox lifecycle service. Create operations are synchronous on the DB
// and do not wait on this timeout.
const envSandboxLifecycleDefaultTimeout = 30 * time.Second

// newEnvSandboxLifecycleService constructs the production lifecycle service
// for env-dispatch injection, or nil when the handler has no Queries (test
// fixtures). When non-nil, env-dispatch calls WithSandboxLifecycle so trained
// rollouts create sandbox_instances instead of forking Fleet sandboxes.
func newEnvSandboxLifecycleService(h *Handler) *service.EnvSandboxLifecycleService {
	deps := newEnvSandboxLifecycleDepsAdapter(h)
	if deps == nil {
		return nil
	}
	return service.NewEnvSandboxLifecycleService(deps, envSandboxLifecycleDefaultTimeout)
}

// envSandboxLifecycleDepsAdapter bridges service.EnvSandboxLifecycleDeps to
// *Handler.Queries (DB) and *Handler.SandboxHub (websocket). It wraps the
// existing sandbox_instance/sandbox_job queries that the CreateSandboxInstance
// handler uses, so env-dispatch and checkpointing share one lifecycle path.
type envSandboxLifecycleDepsAdapter struct {
	h *Handler
}

// newEnvSandboxLifecycleDepsAdapter returns the production lifecycle deps
// adapter wired to real sqlc queries + the sandbox websocket hub. Returns nil
// when the handler has no Queries (test fixtures) so the lifecycle service is
// not constructed.
func newEnvSandboxLifecycleDepsAdapter(h *Handler) service.EnvSandboxLifecycleDeps {
	if h.Queries == nil {
		return nil
	}
	return &envSandboxLifecycleDepsAdapter{h: h}
}

func (a *envSandboxLifecycleDepsAdapter) GetSandboxInstanceRef(ctx context.Context, workspaceID, instanceID string) (service.SandboxInstanceRef, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return service.SandboxInstanceRef{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	instUUID, err := util.ParseUUID(instanceID)
	if err != nil {
		return service.SandboxInstanceRef{}, fmt.Errorf("parse instance_id: %w", err)
	}
	row, err := a.h.Queries.GetSandboxInstanceForWorkspace(ctx, db.GetSandboxInstanceForWorkspaceParams{ID: instUUID, WorkspaceID: wsUUID})
	if err != nil {
		return service.SandboxInstanceRef{}, fmt.Errorf("get sandbox instance: %w", err)
	}
	return sandboxInstanceRowToRef(sandboxInstanceFromGetRow(row)), nil
}

// sandboxInstanceFromGetRow normalizes the identical GetSandboxInstanceForWorkspace
// row (regenerated sqlc emits a per-query Row) to the ListSandboxInstancesByWorkspace
// row shape the ref mapper consumes.
func sandboxInstanceFromGetRow(r db.GetSandboxInstanceForWorkspaceRow) db.ListSandboxInstancesByWorkspaceRow {
	return db.ListSandboxInstancesByWorkspaceRow{
		ID: r.ID, WorkspaceID: r.WorkspaceID, CreatorUserID: r.CreatorUserID, NodeID: r.NodeID,
		Status: r.Status, Template: r.Template, LocalRef: r.LocalRef, EndpointInfo: r.EndpointInfo,
		Limits: r.Limits, Metadata: r.Metadata, Error: r.Error, CreatedAt: r.CreatedAt,
	}
}

// MintSandboxRuntimeEnv satisfies EnvSandboxLifecycleDeps. It mints the daemon
// bootstrap env (server URL + PAT + workspace + MULTICA_DAEMON_ENABLED=1 +
// profile + optional display name) for an env-dispatch-created sandbox so the
// in-sandbox daemon can reach multica on boot. Unlike the UI create path,
// server-side dispatch has no inbound request, so the server URL must come from
// the configured public URL env vars; an unconfigured URL is a hard error (the
// daemon needs a reachable server to register + claim tasks).
func (a *envSandboxLifecycleDepsAdapter) MintSandboxRuntimeEnv(ctx context.Context, workspaceID, actorUserID, instanceID, displayName string) (map[string]string, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("parse workspace_id: %w", err)
	}
	userUUID, err := util.ParseUUID(actorUserID)
	if err != nil {
		return nil, fmt.Errorf("parse actor_user_id: %w", err)
	}
	serverURL := firstNonEmptyString(os.Getenv("MULTICA_PUBLIC_URL"), os.Getenv("MULTICA_APP_URL"), os.Getenv("MULTICA_SERVER_URL"))
	if serverURL == "" {
		return nil, fmt.Errorf("sandbox runtime env: server URL not configured (set MULTICA_PUBLIC_URL/MULTICA_APP_URL/MULTICA_SERVER_URL)")
	}
	return a.h.mintSandboxRuntimeEnv(ctx, wsUUID, userUUID, instanceID, serverURL, displayName)
}

func (a *envSandboxLifecycleDepsAdapter) InsertSandboxInstance(ctx context.Context, in service.CreateSandboxInstanceInput, actorUserID string) (service.SandboxInstanceRef, error) {
	wsUUID, err := util.ParseUUID(in.WorkspaceID)
	if err != nil {
		return service.SandboxInstanceRef{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	userUUID, err := util.ParseUUID(actorUserID)
	if err != nil {
		return service.SandboxInstanceRef{}, fmt.Errorf("parse actor_user_id: %w", err)
	}
	// Node selection (mirrors CreateSandboxInstance handler): use the provided
	// NodeID when set, else pick an available node for the workspace.
	var nodeID pgtype.UUID
	if in.NodeID != "" {
		nodeUUID, err := util.ParseUUID(in.NodeID)
		if err != nil {
			return service.SandboxInstanceRef{}, fmt.Errorf("parse node_id: %w", err)
		}
		node, err := a.h.Queries.PickSandboxNodeForWorkspace(ctx, db.PickSandboxNodeForWorkspaceParams{WorkspaceID: wsUUID, NodeID: nodeUUID})
		if err != nil {
			return service.SandboxInstanceRef{}, fmt.Errorf("pick sandbox node: %w", err)
		}
		nodeID = node.ID
	} else {
		node, err := a.h.Queries.PickAvailableSandboxNodeForWorkspace(ctx, wsUUID)
		if err != nil {
			return service.SandboxInstanceRef{}, fmt.Errorf("pick available sandbox node: %w", err)
		}
		nodeID = node.ID
	}
	template := in.Template
	if template == "" {
		template = "default"
	}
	metadata := buildSandboxMetadata(nil, in.Name, in.Runtime)
	inst, err := a.h.Queries.CreateSandboxInstance(ctx, db.CreateSandboxInstanceParams{
		WorkspaceID:   wsUUID,
		CreatorUserID: userUUID,
		NodeID:        nodeID,
		Status:        "pending",
		Template:      template,
		Limits:        jsonBytesOrDefault(in.Limits, "{}"),
		Metadata:      jsonBytesOrDefault(metadata, "{}"),
	})
	if err != nil {
		return service.SandboxInstanceRef{}, fmt.Errorf("create sandbox instance: %w", err)
	}
	return service.SandboxInstanceRef{
		InstanceID:      util.UUIDToString(inst.ID),
		WorkspaceID:     util.UUIDToString(inst.WorkspaceID),
		NodeID:          util.UUIDToString(inst.NodeID),
		Template:        inst.Template,
		Status:          inst.Status,
		LocalRef:        textValue(inst.LocalRef),
		EndpointInfo:    inst.EndpointInfo,
		RuntimeMetadata: inst.Metadata,
	}, nil
}

func (a *envSandboxLifecycleDepsAdapter) EnqueueSandboxJob(ctx context.Context, workspaceID, actorUserID, nodeID, instanceID, jobType string, payload json.RawMessage) (service.SandboxLifecycleJobResult, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return service.SandboxLifecycleJobResult{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	userUUID, err := util.ParseUUID(actorUserID)
	if err != nil {
		return service.SandboxLifecycleJobResult{}, fmt.Errorf("parse actor_user_id: %w", err)
	}
	nodeUUID, err := util.ParseUUID(nodeID)
	if err != nil {
		return service.SandboxLifecycleJobResult{}, fmt.Errorf("parse node_id: %w", err)
	}
	instUUID, err := util.ParseUUID(instanceID)
	if err != nil {
		return service.SandboxLifecycleJobResult{}, fmt.Errorf("parse instance_id: %w", err)
	}
	if jobType == "delete" {
		job, err := a.h.Queries.CreateSandboxDeleteJob(ctx, db.CreateSandboxDeleteJobParams{
			WorkspaceID:     wsUUID,
			InitiatorUserID: userUUID,
			NodeID:          nodeUUID,
			InstanceID:      instUUID,
			Payload:         payload,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			job, err = a.h.Queries.GetActiveSandboxDeleteJob(ctx, instUUID)
		}
		if err != nil {
			return service.SandboxLifecycleJobResult{}, fmt.Errorf("create sandbox delete job: %w", err)
		}
		return service.SandboxLifecycleJobResult{
			JobID:      util.UUIDToString(job.ID),
			InstanceID: util.UUIDToString(job.InstanceID),
			NodeID:     util.UUIDToString(job.NodeID),
			JobType:    job.Type,
		}, nil
	}
	job, err := a.h.Queries.CreateSandboxJob(ctx, db.CreateSandboxJobParams{
		WorkspaceID:     wsUUID,
		InitiatorUserID: userUUID,
		NodeID:          nodeUUID,
		InstanceID:      instUUID,
		Type:            jobType,
		Payload:         payload,
	})
	if err != nil {
		return service.SandboxLifecycleJobResult{}, fmt.Errorf("create sandbox job: %w", err)
	}
	return service.SandboxLifecycleJobResult{
		JobID:      util.UUIDToString(job.ID),
		InstanceID: util.UUIDToString(job.InstanceID),
		NodeID:     util.UUIDToString(job.NodeID),
		JobType:    job.Type,
	}, nil
}

func (a *envSandboxLifecycleDepsAdapter) NotifySandboxJobAvailable(_ context.Context, nodeID, jobID string) error {
	if a.h.SandboxHub == nil {
		return nil
	}
	a.h.SandboxHub.NotifyJobAvailable(nodeID, jobID)
	return nil
}

func (a *envSandboxLifecycleDepsAdapter) ForceDeleteSandboxInstance(ctx context.Context, _ string, instanceID string) error {
	instUUID, err := util.ParseUUID(instanceID)
	if err != nil {
		return fmt.Errorf("parse instance_id: %w", err)
	}
	return a.h.Queries.DeleteSandboxInstance(ctx, instUUID)
}

func (a *envSandboxLifecycleDepsAdapter) ForceDeleteSandboxRuntime(ctx context.Context, workspaceID, runtimeID string) error {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace_id: %w", err)
	}
	runtimeUUID, err := util.ParseUUID(runtimeID)
	if err != nil {
		return fmt.Errorf("parse runtime_id: %w", err)
	}
	return a.h.Queries.DeleteAgentRuntimeForWorkspace(ctx, db.DeleteAgentRuntimeForWorkspaceParams{ID: runtimeUUID, WorkspaceID: wsUUID})
}

// ConfigureEphemeralSandboxManager wires the shared TaskService before any
// request or background sweeper can reach a terminal task path.
func ConfigureEphemeralSandboxManager(h *Handler) {
	if h == nil || h.TaskService == nil {
		return
	}
	lifecycle := newEnvSandboxLifecycleService(h)
	if lifecycle == nil {
		return
	}
	h.TaskService.EphemeralSandboxManager = newEphemeralSandboxManager(h, lifecycle)
	h.TaskService.EphemeralSandboxCleaner = newEphemeralSandboxCleaner(lifecycle)
}

// ephemeralSandboxCleanerAdapter implements service.EphemeralSandboxCleaner
// by delegating to the env-sandbox lifecycle service's sandboxd delete job
// path (stop+delete the Cube job, with a DB-level force-delete fallback
// when no sandboxd node is available). Not-found = success (already deleted).
type ephemeralSandboxCleanerAdapter struct {
	lifecycle service.SandboxInstanceCreator
}

func newEphemeralSandboxCleaner(lc service.SandboxInstanceCreator) *ephemeralSandboxCleanerAdapter {
	return &ephemeralSandboxCleanerAdapter{lifecycle: lc}
}

func (a *ephemeralSandboxCleanerAdapter) DeleteSandboxInstance(ctx context.Context, workspaceID, instanceID string) error {
	ref, err := a.lifecycle.GetSandboxInstanceRef(ctx, workspaceID, instanceID)
	if err != nil {
		// Not-found or transient error: best-effort, treat as already gone.
		return nil
	}
	// The sandbox's creator is the actor: terminal-task cleanup has no request
	// context, yet a "delete" job's initiator_user_id is NOT NULL and Delete
	// only force-deletes when the node is unavailable. An empty actor therefore
	// failed to enqueue and left a reachable node's Cube sandbox running.
	return a.lifecycle.DeleteSandboxInstance(ctx, ref, ref.CreatorUserID)
}

// sandboxInstanceRowToRef converts a joined sandbox_instance row to the
// service-layer SandboxInstanceRef.
func sandboxInstanceRowToRef(row db.ListSandboxInstancesByWorkspaceRow) service.SandboxInstanceRef {
	return service.SandboxInstanceRef{
		InstanceID:      util.UUIDToString(row.ID),
		WorkspaceID:     util.UUIDToString(row.WorkspaceID),
		CreatorUserID:   util.UUIDToString(row.CreatorUserID),
		NodeID:          util.UUIDToString(row.NodeID),
		Template:        row.Template,
		Status:          row.Status,
		LocalRef:        textValue(row.LocalRef),
		EndpointInfo:    row.EndpointInfo,
		RuntimeMetadata: row.Metadata,
	}
}
