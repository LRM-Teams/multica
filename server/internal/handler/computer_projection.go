package handler

import (
	"net/http"
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

// ListComputers returns every active Workspace connection, including those
// with no Agent runtimes. Membership controls visibility; row ownership is
// included only so the existing Mine/Team presentation can remain truthful.
func (h *Handler) ListComputers(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT b.daemon_id, b.user_id::text, h.last_seen_at,
       COALESCE(o.work_journal_enabled, FALSE),
       COALESCE(o.device_name, ''), COALESCE(o.os, ''), COALESCE(o.cli_version, '')
FROM computer_workspace_bindings b
LEFT JOIN daemon_heartbeat h
  ON h.workspace_id = b.workspace_id AND h.daemon_id = b.daemon_id
LEFT JOIN computer_identity_owner o
  ON o.daemon_id = b.daemon_id
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
		result = append(result, computerConnectionProjection(daemonID, ownerID, lastSeen, connected, workJournalEnabled, deviceName, osName, cliVersion))
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Computers")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
