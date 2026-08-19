package handler

import (
	"encoding/json"
	"net/http"
	"strings"
)

// resolveComputerByMachineIDRequest is the POST body for reclaiming a Computer
// identity after a local identity rebuild.
type resolveComputerByMachineIDRequest struct {
	// MachineID is the OS-level persistent machine fingerprint (e.g.
	// /etc/machine-id, IOPlatformUUID, MachineGuid). It is independent of
	// ~/.multica, so it survives an identity wipe.
	MachineID string `json:"machine_id"`
}

// ResolveComputerByMachineID returns the Computer (daemon_id) this user already
// owns on the same physical machine, proven by the OS machine fingerprint.
//
// When `~/.multica` is wiped (or identity evidence lost), setup would normally
// mint a brand-new computer_id, orphaning every agent still pinned to the old
// identity's runtimes. This endpoint lets setup reclaim the existing identity
// instead: if the server knows a computer_identity_owner row for this user with
// the same machine_id, and that identity still has an active Workspace binding
// (i.e. it was not deliberately removed), the client reuses that computer_id
// and no orphan is created at all (LRM-1570).
//
// Safety: returns at most one id, and only when the match is unambiguous. A
// machine shared by multiple OS users each has their own identity row, so the
// lookup is always scoped to the requesting user; ambiguity (or a deliberately
// removed Computer) yields empty, and the client falls back to minting fresh.
func (h *Handler) ResolveComputerByMachineID(w http.ResponseWriter, r *http.Request) {
	var req resolveComputerByMachineIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	machineID := strings.TrimSpace(req.MachineID)
	if machineID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "machine_id is required"})
		return
	}
	userID := requestUserID(r)
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "workspace required"})
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	rows, err := h.DB.Query(r.Context(), `
SELECT DISTINCT o.daemon_id
  FROM computer_identity_owner o
  JOIN computer_workspace_bindings b
    ON b.daemon_id = o.daemon_id
 WHERE o.user_id = $1 AND o.machine_id = $2
   AND b.active = TRUE AND b.revoked_at IS NULL
   AND NOT EXISTS (
     SELECT 1 FROM daemon_registration_tombstone t
      WHERE t.workspace_id = $3 AND t.daemon_id = o.daemon_id
   )`,
		userID, machineID, parseUUID(workspaceID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to resolve Computer identity"})
		return
	}
	defer rows.Close()
	var computerIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to resolve Computer identity"})
			return
		}
		computerIDs = append(computerIDs, id)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to resolve Computer identity"})
		return
	}
	// Only a unique identity may be reclaimed; ambiguous or removed → empty.
	resolved := ""
	if len(computerIDs) == 1 {
		resolved = computerIDs[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{"computer_id": resolved})
}
