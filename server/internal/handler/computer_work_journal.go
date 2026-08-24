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

const computerWorkJournalTimeout = 15 * time.Second

type patchComputerWorkJournalRequest struct {
	Enabled *bool `json:"enabled"`
}

// PatchComputerWorkJournal lets the Computer Owner turn Machine Work Journal
// on or off for that machine. The resident file is authoritative; this
// handler only asks the live Binding and projects the confirmed bit.
func (h *Handler) PatchComputerWorkJournal(w http.ResponseWriter, r *http.Request) {
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
	var req patchComputerWorkJournalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), computerWorkJournalTimeout)
	defer cancel()
	enabled, err := h.setComputerWorkJournal(ctx, daemonID, *req.Enabled)
	if err != nil {
		h.writeComputerWorkDigestError(w, err)
		return
	}
	if _, err := h.DB.Exec(ctx, `
UPDATE computers SET work_journal_enabled = $2 WHERE id = $1`, daemonID, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to project work journal setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (h *Handler) setComputerWorkJournal(ctx context.Context, computerID string, enabled bool) (bool, error) {
	if h == nil || h.DaemonHub == nil {
		return false, daemonws.ErrComputerOffline
	}
	rows, err := h.DB.Query(ctx, `
		SELECT workspace_id
		FROM computer_workspace_bindings
		WHERE daemon_id = $1 AND active = TRUE AND revoked_at IS NULL
		ORDER BY workspace_id`, computerID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	command := protocol.ComputerWorkJournalPayload{RequestID: uuid.NewString(), Enabled: enabled}
	var last error
	attempted := false
	for rows.Next() {
		var workspaceID pgtype.UUID
		if err := rows.Scan(&workspaceID); err != nil {
			return false, err
		}
		attempted = true
		got, err := h.DaemonHub.RequestComputerWorkJournal(ctx, computerID, uuidToString(workspaceID), command)
		if err == nil {
			return got, nil
		}
		last = err
		if !errors.Is(err, daemonws.ErrComputerOffline) {
			return false, err
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if !attempted || last == nil {
		return false, daemonws.ErrComputerOffline
	}
	return false, last
}
