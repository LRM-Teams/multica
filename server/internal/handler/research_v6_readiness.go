package handler

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type researchV6DirectorReadinessError struct {
	status    int
	code      string
	message   string
	retryable bool
}

func (h *Handler) researchV6DirectorReadiness(ctx context.Context, workspaceID, directorID pgtype.UUID) *researchV6DirectorReadinessError {
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID: directorID, WorkspaceID: workspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return &researchV6DirectorReadinessError{status: 404, code: "research.v6.director_not_found", message: "research Director was not found"}
	}
	if err != nil {
		return &researchV6DirectorReadinessError{status: 500, code: "research.v6.director_lookup_failed", message: "failed to inspect research Director", retryable: true}
	}
	if agent.ArchivedAt.Valid {
		return &researchV6DirectorReadinessError{status: 409, code: "research.v6.director_archived", message: "research Director is archived"}
	}
	if !agent.RuntimeID.Valid {
		return &researchV6DirectorReadinessError{status: 409, code: "research.v6.director_runtime_unavailable", message: "research Director has no runtime", retryable: true}
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID: agent.RuntimeID, WorkspaceID: workspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return &researchV6DirectorReadinessError{status: 409, code: "research.v6.director_runtime_unavailable", message: "research Director runtime was not found", retryable: true}
	}
	if err != nil {
		return &researchV6DirectorReadinessError{status: 500, code: "research.v6.director_runtime_lookup_failed", message: "failed to inspect research Director runtime", retryable: true}
	}
	if runtimeConnectivity(runtime, time.Now()) != runtimeConnectivityOnline {
		return &researchV6DirectorReadinessError{status: 409, code: "research.v6.director_runtime_offline", message: "research Director runtime is offline", retryable: true}
	}
	if !agentRuntimeHasCapability(runtime, protocol.DaemonCapabilityResearchRunV6) {
		return &researchV6DirectorReadinessError{status: 409, code: "research.v6.director_runtime_incompatible", message: "research Director runtime must be upgraded for Research V6"}
	}
	return nil
}
