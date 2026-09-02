package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const computerCollectRootsTimeout = 8 * time.Second

type computerCollectRootsResponse struct {
	Roots []string `json:"roots"`
}

type patchComputerCollectRootsRequest struct {
	Roots []string `json:"roots"`
}

// GetComputerCollectRoots returns the Computer-local Period Work collect roots.
// An empty list means heuristic SCAN_ROOTS. The resident file is authoritative.
func (h *Handler) GetComputerCollectRoots(w http.ResponseWriter, r *http.Request) {
	h.handleComputerCollectRoots(w, r, false, nil)
}

// PatchComputerCollectRoots writes the Computer-local Period Work collect roots.
// An empty list clears the override so collectors fall back to heuristic SCAN_ROOTS.
func (h *Handler) PatchComputerCollectRoots(w http.ResponseWriter, r *http.Request) {
	var req patchComputerCollectRootsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Roots == nil {
		req.Roots = []string{}
	}
	h.handleComputerCollectRoots(w, r, true, req.Roots)
}

func (h *Handler) handleComputerCollectRoots(w http.ResponseWriter, r *http.Request, set bool, roots []string) {
	daemonID := strings.TrimSpace(chi.URLParam(r, "daemonId"))
	if daemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}
	if err := h.authorizeComputerOwnerRequest(r.Context(), r, daemonID); err != nil {
		if errors.Is(err, errComputerConnectionUnauthorized) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to authorize Computer owner")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), computerCollectRootsTimeout)
	defer cancel()
	got, err := h.requestComputerCollectRoots(ctx, daemonID, set, roots)
	if err != nil {
		h.writeComputerCollectRootsError(w, err)
		return
	}
	if got == nil {
		got = []string{}
	}
	writeJSON(w, http.StatusOK, computerCollectRootsResponse{Roots: got})
}

func (h *Handler) requestComputerCollectRoots(ctx context.Context, computerID string, set bool, roots []string) ([]string, error) {
	if h == nil || h.DaemonHub == nil {
		return nil, daemonws.ErrComputerOffline
	}
	rows, err := h.DB.Query(ctx, `
		SELECT workspace_id
		FROM computer_workspace_bindings
		WHERE daemon_id = $1 AND active = TRUE AND revoked_at IS NULL
		ORDER BY workspace_id`, computerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	command := protocol.ComputerCollectRootsPayload{
		RequestID: uuid.NewString(),
		Set:       set,
		Roots:     roots,
	}
	var last error
	attempted := false
	for rows.Next() {
		var workspaceID pgtype.UUID
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		attempted = true
		got, err := h.DaemonHub.RequestComputerCollectRoots(ctx, computerID, uuidToString(workspaceID), command)
		if err == nil {
			return got, nil
		}
		last = err
		if !errors.Is(err, daemonws.ErrComputerOffline) {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !attempted || last == nil {
		return nil, daemonws.ErrComputerOffline
	}
	return nil, last
}

func (h *Handler) writeComputerCollectRootsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, daemonws.ErrComputerOffline):
		writeCodedError(w, http.StatusServiceUnavailable, "computer_offline", "Computer is offline")
	case errors.Is(err, context.DeadlineExceeded):
		writeCodedError(w, http.StatusGatewayTimeout, "computer_collect_roots_timeout", "This computer did not answer. If it is online, update Multica on that machine and restart Computer.")
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

func (h *Handler) loadPeriodBriefCollectRootsForAgent(ctx context.Context, agentRuntimeID pgtype.UUID) []string {
	if h == nil || !agentRuntimeID.Valid {
		return nil
	}
	rt, err := h.Queries.GetAgentRuntime(ctx, agentRuntimeID)
	if err != nil || !rt.DaemonID.Valid {
		return nil
	}
	loadCtx, cancel := context.WithTimeout(ctx, computerCollectRootsTimeout)
	defer cancel()
	roots, err := h.requestComputerCollectRoots(loadCtx, strings.TrimSpace(rt.DaemonID.String), false, nil)
	if err != nil {
		return nil
	}
	return roots
}
