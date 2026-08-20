package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// graphMemoryLineageCurrent is the active route of one channel; null when
// the channel has never used the graph (spec §10).
type graphMemoryLineageCurrent struct {
	GraphKind    string `json:"graph_kind"`
	GraphOwnerID string `json:"graph_owner_id"`
	Generation   int64  `json:"generation"`
}

type graphMemoryLineageEntry struct {
	Generation   int64  `json:"generation"`
	GraphKind    string `json:"graph_kind"`
	GraphOwnerID string `json:"graph_owner_id"`
	ValidFrom    string `json:"valid_from"`
	ValidTo      string `json:"valid_to,omitempty"`
}

type graphMemoryLineageResponse struct {
	WorkspaceID string                     `json:"workspace_id"`
	ChannelID   string                     `json:"channel_id"`
	RoutingMode string                     `json:"routing_mode"`
	Current     *graphMemoryLineageCurrent `json:"current"`
	Lineage     []graphMemoryLineageEntry  `json:"lineage"`
}

// GetGraphMemoryChannelLineage serves
// GET /api/workspaces/{id}/graph-memory/channels/{channelId}/lineage
// (spec §10): the channel's current graph route plus its full generation
// lineage. A channel that never resolved a route yields a stable empty
// answer, not an error.
func (h *Handler) GetGraphMemoryChannelLineage(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	workspaceUUID := parseUUID(workspaceID)
	channelID, err := parseUUIDLoose(chi.URLParam(r, "channelId"))
	if err != nil || !channelID.Valid {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}

	var (
		channelKind string
		systemKey   pgtype.Text
	)
	err = h.DB.QueryRow(r.Context(), `
		SELECT kind, system_key
		FROM channel
		WHERE id = $1 AND workspace_id = $2`, channelID, workspaceUUID).Scan(&channelKind, &systemKey)
	if errors.Is(err, pgx.ErrNoRows) {
		writeGraphMemoryLineageNotFound(w)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load graph memory channel")
		return
	}
	// Ordinary group channels are private; the workspace-wide #general channel
	// has a system key and remains visible to every workspace member.
	if channelKind == "group" && !systemKey.Valid &&
		!h.channelUserIsMember(r.Context(), workspaceID, channelID, member.UserID) &&
		!h.isWorkspaceOwnerOrAdmin(r.Context(), workspaceID, member.UserID) {
		writeGraphMemoryLineageNotFound(w)
		return
	}

	resp := graphMemoryLineageResponse{
		WorkspaceID: workspaceID,
		ChannelID:   uuidToString(channelID),
		Lineage:     []graphMemoryLineageEntry{},
	}

	var (
		routingMode string
		graphKind   string
		graphOwner  pgtype.UUID
		generation  int64
	)
	err = h.DB.QueryRow(r.Context(), `
		SELECT r.routing_mode, r.current_graph_kind, r.current_graph_owner_id, r.generation
		FROM graph_memory_channel_route r
		JOIN channel c ON c.id = r.channel_id AND c.workspace_id = r.workspace_id
		WHERE r.workspace_id = $1 AND r.channel_id = $2
		  AND (
			(r.current_graph_kind = 'channel' AND r.current_graph_owner_id = c.id)
			OR (r.current_graph_kind = 'project' AND r.current_graph_owner_id = c.project_id
				AND EXISTS (
					SELECT 1 FROM project p
					WHERE p.id = r.current_graph_owner_id AND p.workspace_id = r.workspace_id
				))
		  )`, workspaceUUID, channelID).Scan(&routingMode, &graphKind, &graphOwner, &generation)
	switch {
	case err == nil:
		resp.RoutingMode = routingMode
		resp.Current = &graphMemoryLineageCurrent{
			GraphKind:    graphKind,
			GraphOwnerID: uuidToString(graphOwner),
			Generation:   generation,
		}
	case errors.Is(err, pgx.ErrNoRows):
		var routeExists bool
		if err := h.DB.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM graph_memory_channel_route
				WHERE workspace_id = $1 AND channel_id = $2
			)`, workspaceUUID, channelID).Scan(&routeExists); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to verify graph memory channel route")
			return
		}
		if routeExists {
			writeGraphMemoryLineageNotFound(w)
			return
		}
	default:
		writeError(w, http.StatusInternalServerError, "failed to load graph memory channel route")
		return
	}

	var invalidLineage bool
	err = h.DB.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM graph_memory_channel_lineage l
			WHERE l.workspace_id = $1 AND l.channel_id = $2
			  AND NOT (
				(l.graph_kind = 'channel' AND l.graph_owner_id = $2)
				OR (l.graph_kind = 'project' AND EXISTS (
					SELECT 1 FROM project p
					WHERE p.id = l.graph_owner_id AND p.workspace_id = l.workspace_id
				))
			  )
		)`, workspaceUUID, channelID).Scan(&invalidLineage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify graph memory channel lineage")
		return
	}
	if invalidLineage {
		writeGraphMemoryLineageNotFound(w)
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT generation, graph_kind, graph_owner_id, valid_from, valid_to
		FROM graph_memory_channel_lineage l
		WHERE l.workspace_id = $1 AND l.channel_id = $2
		  AND (
			(l.graph_kind = 'channel' AND l.graph_owner_id = $2)
			OR (l.graph_kind = 'project' AND EXISTS (
				SELECT 1 FROM project p
				WHERE p.id = l.graph_owner_id AND p.workspace_id = l.workspace_id
			))
		  )
		ORDER BY generation`, workspaceUUID, channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load graph memory channel lineage")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var (
			entry   graphMemoryLineageEntry
			ownerID pgtype.UUID
			validAt pgtype.Timestamptz
			validTo pgtype.Timestamptz
		)
		if err := rows.Scan(&entry.Generation, &entry.GraphKind, &ownerID, &validAt, &validTo); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read graph memory channel lineage")
			return
		}
		entry.GraphOwnerID = uuidToString(ownerID)
		entry.ValidFrom = validAt.Time.UTC().Format(time.RFC3339)
		if validTo.Valid {
			entry.ValidTo = validTo.Time.UTC().Format(time.RFC3339)
		}
		resp.Lineage = append(resp.Lineage, entry)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read graph memory channel lineage")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeGraphMemoryLineageNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "channel not found")
}
