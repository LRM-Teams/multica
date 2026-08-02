package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type ephemeralSandboxManager struct {
	h         *Handler
	lifecycle service.SandboxInstanceCreator
}

func newEphemeralSandboxManager(h *Handler, lifecycle service.SandboxInstanceCreator) *ephemeralSandboxManager {
	if h == nil || h.Queries == nil || lifecycle == nil {
		return nil
	}
	return &ephemeralSandboxManager{h: h, lifecycle: lifecycle}
}

func (m *ephemeralSandboxManager) PrepareRetry(ctx context.Context, task db.AgentInboxEvent) (*service.EphemeralRetryResources, error) {
	marker, ok := service.ExtractEphemeralSandbox(task.Context)
	if !ok {
		return nil, fmt.Errorf("prepare retry: ephemeral sandbox marker missing")
	}
	agent, err := m.h.Queries.GetAgent(ctx, task.AgentID)
	if err != nil {
		return nil, fmt.Errorf("prepare retry: get task agent: %w", err)
	}
	workspaceID := util.UUIDToString(agent.WorkspaceID)
	oldRef, err := m.lifecycle.GetSandboxInstanceRef(ctx, workspaceID, marker.SandboxInstanceID)
	if err != nil {
		return nil, fmt.Errorf("prepare retry: lookup old sandbox: %w", err)
	}
	actorUserID := marker.ActorUserID
	if actorUserID == "" {
		actorUserID = oldRef.CreatorUserID
	}
	if actorUserID == "" {
		return nil, fmt.Errorf("prepare retry: sandbox actor is empty")
	}

	runtimeID, daemonID, err := (&envDispatchDepsAdapter{h: m.h}).PrecreateAgentRuntime(
		ctx, workspaceID, actorUserID, util.UUIDToString(task.AgentID),
	)
	if err != nil {
		return nil, fmt.Errorf("prepare retry: precreate runtime: %w", err)
	}
	runtimeUUID, err := util.ParseUUID(runtimeID)
	if err != nil {
		return nil, m.compensateRuntime(ctx, workspaceID, runtimeID, fmt.Errorf("prepare retry: parse runtime id: %w", err))
	}

	newRef, err := m.lifecycle.CreateSandboxInstance(ctx, service.CreateSandboxInstanceInput{
		WorkspaceID:   workspaceID,
		Template:      oldRef.Template,
		DaemonEnabled: true,
		RuntimeEnv:    map[string]string{"MULTICA_DAEMON_ID": daemonID},
	}, actorUserID)
	if err != nil {
		return nil, m.compensateRuntime(ctx, workspaceID, runtimeID, fmt.Errorf("prepare retry: create replacement sandbox: %w", err))
	}

	return &service.EphemeralRetryResources{
		RuntimeID:   runtimeUUID,
		Context:     mergeEphemeralSandboxContext(task.Context, newRef.InstanceID, actorUserID),
		WorkspaceID: workspaceID,
		SandboxRef:  newRef,
		ActorUserID: actorUserID,
	}, nil
}

func (m *ephemeralSandboxManager) compensateRuntime(ctx context.Context, workspaceID, runtimeID string, cause error) error {
	err := (&envDispatchDepsAdapter{h: m.h}).DeleteAgentRuntime(
		context.WithoutCancel(ctx), workspaceID, runtimeID,
	)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("delete replacement runtime: %w", err))
	}
	return cause
}

func (m *ephemeralSandboxManager) Reclaim(ctx context.Context, resources *service.EphemeralRetryResources) error {
	if resources == nil {
		return nil
	}
	var result error
	if resources.SandboxRef.InstanceID != "" {
		if err := m.lifecycle.DeleteSandboxInstance(ctx, resources.SandboxRef, resources.ActorUserID); err != nil &&
			!errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, service.ErrSandboxInstanceNotFound) {
			result = errors.Join(result, fmt.Errorf("delete replacement sandbox: %w", err))
		}
	}
	if resources.RuntimeID.Valid && resources.WorkspaceID != "" {
		if err := (&envDispatchDepsAdapter{h: m.h}).DeleteAgentRuntime(
			ctx, resources.WorkspaceID, util.UUIDToString(resources.RuntimeID),
		); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			result = errors.Join(result, fmt.Errorf("delete replacement runtime: %w", err))
		}
	}
	return result
}

func (m *ephemeralSandboxManager) Cleanup(ctx context.Context, task db.AgentInboxEvent) error {
	marker, ok := service.ExtractEphemeralSandbox(task.Context)
	if !ok {
		return nil
	}
	agent, err := m.h.Queries.GetAgent(ctx, task.AgentID)
	if err != nil {
		return fmt.Errorf("cleanup ephemeral sandbox: get task agent: %w", err)
	}
	workspaceID := util.UUIDToString(agent.WorkspaceID)

	if task.RuntimeID.Valid {
		hasOther, err := m.h.Queries.HasOtherActiveTaskForRuntime(ctx, db.HasOtherActiveTaskForRuntimeParams{
			RuntimeID: task.RuntimeID, ExcludeTask: task.ID,
		})
		if err != nil {
			return fmt.Errorf("cleanup ephemeral sandbox: check active tasks: %w", err)
		}
		if hasOther {
			return nil
		}
	}

	ref, err := m.lifecycle.GetSandboxInstanceRef(ctx, workspaceID, marker.SandboxInstanceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, service.ErrSandboxInstanceNotFound) {
			if task.RuntimeID.Valid {
				return m.h.Queries.SetAgentRuntimeOffline(ctx, db.SetAgentRuntimeOfflineParams{
					ID:            task.RuntimeID,
					OfflineReason: pgtype.Text{String: "sandbox_teardown", Valid: true},
				})
			}
			return nil
		}
		return fmt.Errorf("cleanup ephemeral sandbox: lookup sandbox: %w", err)
	}
	if task.RuntimeID.Valid {
		if err := m.h.Queries.SetAgentRuntimeOffline(ctx, db.SetAgentRuntimeOfflineParams{
			ID:            task.RuntimeID,
			OfflineReason: pgtype.Text{String: "sandbox_teardown", Valid: true},
		}); err != nil {
			return fmt.Errorf("cleanup ephemeral sandbox: set runtime offline: %w", err)
		}
	}
	actorUserID := marker.ActorUserID
	if actorUserID == "" {
		actorUserID = ref.CreatorUserID
	}
	if actorUserID == "" {
		return fmt.Errorf("cleanup ephemeral sandbox: actor is empty")
	}
	if err := m.lifecycle.DeleteSandboxInstance(ctx, ref, actorUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, service.ErrSandboxInstanceNotFound) {
			return nil
		}
		return fmt.Errorf("cleanup ephemeral sandbox: delete sandbox: %w", err)
	}
	return nil
}
