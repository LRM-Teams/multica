package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const computerWorkDigestTimeout = 30 * time.Second

// GetComputerWorkDigest returns one windowed Work Digest for a Computer the
// caller owns. Workspace members and admins cannot read another owner's
// machine journal. The digest is not stored.
func (h *Handler) GetComputerWorkDigest(w http.ResponseWriter, r *http.Request) {
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
	start, ok := parseWorkDigestTimeQuery(w, r, "start")
	if !ok {
		return
	}
	end, ok := parseWorkDigestTimeQuery(w, r, "end")
	if !ok {
		return
	}
	if !end.After(start) {
		writeError(w, http.StatusBadRequest, "end must be after start")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), computerWorkDigestTimeout)
	defer cancel()
	digest, err := h.fetchComputerWorkDigest(ctx, daemonID, protocol.ComputerWorkDigestPayload{
		RequestID: uuid.NewString(),
		Start:     start,
		End:       end,
	})
	if err != nil {
		h.writeComputerWorkDigestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, digest)
}

func parseWorkDigestTimeQuery(w http.ResponseWriter, r *http.Request, name string) (time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		writeError(w, http.StatusBadRequest, name+" is required")
		return time.Time{}, false
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return time.Time{}, false
	}
	return value, true
}

func (h *Handler) fetchComputerWorkDigest(ctx context.Context, computerID string, command protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error) {
	if h == nil || h.DaemonHub == nil {
		return protocol.WorkDigest{}, daemonws.ErrComputerOffline
	}
	rows, err := h.DB.Query(ctx, `
		SELECT workspace_id
		FROM computer_workspace_bindings
		WHERE daemon_id = $1 AND active = TRUE AND revoked_at IS NULL
		ORDER BY workspace_id`, computerID)
	if err != nil {
		return protocol.WorkDigest{}, err
	}
	defer rows.Close()
	var last error
	attempted := false
	for rows.Next() {
		var workspaceID pgtype.UUID
		if err := rows.Scan(&workspaceID); err != nil {
			return protocol.WorkDigest{}, err
		}
		attempted = true
		digest, err := h.DaemonHub.RequestComputerWorkDigest(ctx, computerID, uuidToString(workspaceID), command)
		if err == nil {
			return digest, nil
		}
		last = err
		if !errors.Is(err, daemonws.ErrComputerOffline) {
			return protocol.WorkDigest{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return protocol.WorkDigest{}, err
	}
	if !attempted || last == nil {
		return protocol.WorkDigest{}, daemonws.ErrComputerOffline
	}
	return protocol.WorkDigest{}, last
}

func (h *Handler) writeComputerWorkDigestError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, daemonws.ErrComputerOffline):
		writeCodedError(w, http.StatusServiceUnavailable, "computer_offline", "Computer is offline")
	case errors.Is(err, context.DeadlineExceeded):
		writeCodedError(w, http.StatusGatewayTimeout, "computer_work_digest_timeout", "Computer did not return a work digest in time")
	default:
		writeError(w, http.StatusBadGateway, "failed to harvest work digest")
	}
}
