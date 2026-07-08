package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	return sandboxInstanceRowToRef(row), nil
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
	metadata := buildSandboxMetadata(nil, "", in.Runtime)
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

// sandboxInstanceRowToRef converts a joined sandbox_instance row to the
// service-layer SandboxInstanceRef.
func sandboxInstanceRowToRef(row db.ListSandboxInstancesByWorkspaceRow) service.SandboxInstanceRef {
	return service.SandboxInstanceRef{
		InstanceID:      util.UUIDToString(row.ID),
		WorkspaceID:     util.UUIDToString(row.WorkspaceID),
		NodeID:          util.UUIDToString(row.NodeID),
		Template:        row.Template,
		Status:          row.Status,
		LocalRef:        textValue(row.LocalRef),
		EndpointInfo:    row.EndpointInfo,
		RuntimeMetadata: row.Metadata,
	}
}
