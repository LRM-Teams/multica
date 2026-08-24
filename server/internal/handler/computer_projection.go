package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// computerConnectionResponse is the Workspace-scoped Computer projection.
// It deliberately exists independently of Agent runtime rows: an explicitly
// connected Computer remains visible when the Workspace has zero Agents.
type computerConnectionResponse struct {
	DaemonID           string  `json:"daemon_id"`
	OwnerID            string  `json:"owner_id"`
	Connected          bool    `json:"connected"`
	LastSeen           *string `json:"last_seen_at"`
	WorkJournalEnabled bool    `json:"work_journal_enabled"`
	DeviceName         string  `json:"deviceName,omitempty"`
	OS                 string  `json:"os,omitempty"`
	CLIVersion         string  `json:"cliVersion,omitempty"`
	// Runtimes the caller may bind an agent to on this Computer, in the
	// hierarchy the product actually has: a Computer's daemon core hosts one
	// runtime per provider. Choosing where an agent runs is therefore a
	// two-level choice — machine, then provider — and this is its one source.
	//
	// Filtered by runtime visibility, which is the only level visibility
	// exists at: another member's private runtime is simply not here, so no
	// client has to re-derive who may bind to what. A Computer whose runtimes
	// are all private to someone else comes back with an empty list rather
	// than disappearing — it is a real machine in this workspace, it just has
	// nothing this caller can pick.
	Runtimes []computerRuntimeOption `json:"runtimes"`
}

// computerRuntimeOption is the provider process, named for picking. It carries
// no liveness of its own: whether work can run is a Computer-level fact
// (Connected above), decided by the daemon's Workspace Runner socket.
type computerRuntimeOption struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

func computerConnectionProjection(daemonID, ownerID string, lastSeen pgtype.Timestamptz, runnerConnected, workJournalEnabled bool, deviceName, osName, cliVersion string) computerConnectionResponse {
	var seen *string
	if lastSeen.Valid {
		value := lastSeen.Time.UTC().Format(time.RFC3339Nano)
		seen = &value
	}
	return computerConnectionResponse{
		DaemonID:           daemonID,
		OwnerID:            ownerID,
		Connected:          runnerConnected,
		LastSeen:           seen,
		WorkJournalEnabled: workJournalEnabled,
		DeviceName:         deviceName,
		OS:                 osName,
		CLIVersion:         cliVersion,
	}
}

// bindableRuntimesByDaemon groups this workspace's bindable runtimes by the
// Computer hosting them. Reuses ListVisibleAgentRuntimes so the rule for "may
// I bind here" has exactly one definition on the server, instead of each
// client filtering a flat runtime list by owner and visibility.
func (h *Handler) bindableRuntimesByDaemon(
	ctx context.Context,
	workspaceID, userID string,
) (map[string][]computerRuntimeOption, error) {
	result := map[string][]computerRuntimeOption{}
	if h == nil || h.Queries == nil || strings.TrimSpace(userID) == "" {
		return result, nil
	}
	runtimes, err := h.Queries.ListVisibleAgentRuntimes(ctx, db.ListVisibleAgentRuntimesParams{
		WorkspaceID: parseUUID(workspaceID),
		UserID:      parseUUID(userID),
	})
	if err != nil {
		return nil, err
	}
	for _, rt := range runtimes {
		daemonID := strings.TrimSpace(rt.DaemonID.String)
		if daemonID == "" {
			continue
		}
		result[daemonID] = append(result[daemonID], computerRuntimeOption{
			ID:       uuidToString(rt.ID),
			Provider: rt.Provider,
		})
	}
	return result, nil
}

// ListComputers returns every active Workspace connection, including those
// with no Agent runtimes. Membership controls visibility; row ownership is
// included only so the existing Mine/Team presentation can remain truthful.
func (h *Handler) ListComputers(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	bindable, err := h.bindableRuntimesByDaemon(r.Context(), workspaceID, requestUserID(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Computer runtimes")
		return
	}

	rows, err := h.DB.Query(r.Context(), `
SELECT b.daemon_id, b.user_id::text, h.last_seen_at,
       COALESCE(o.work_journal_enabled, FALSE),
       COALESCE(o.device_name, ''), COALESCE(o.os, ''), COALESCE(o.cli_version, '')
FROM computer_workspace_bindings b
LEFT JOIN daemon_heartbeat h
  ON h.workspace_id = b.workspace_id AND h.daemon_id = b.daemon_id
LEFT JOIN computers o
  ON o.id = b.daemon_id
WHERE b.workspace_id = $1 AND b.active = TRUE
ORDER BY b.created_at, b.daemon_id`, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Computers")
		return
	}
	defer rows.Close()

	result := make([]computerConnectionResponse, 0)
	for rows.Next() {
		var daemonID, ownerID, deviceName, osName, cliVersion string
		var lastSeen pgtype.Timestamptz
		var workJournalEnabled bool
		if err := rows.Scan(&daemonID, &ownerID, &lastSeen, &workJournalEnabled, &deviceName, &osName, &cliVersion); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read Computer connection")
			return
		}
		hb := &db.DaemonHeartbeat{LastSeenAt: lastSeen}
		connected := h.computerConnectedByRunner(daemonID, workspaceID, hb, time.Now())
		row := computerConnectionProjection(daemonID, ownerID, lastSeen, connected, workJournalEnabled, deviceName, osName, cliVersion)
		row.Runtimes = bindable[daemonID]
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Computers")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
