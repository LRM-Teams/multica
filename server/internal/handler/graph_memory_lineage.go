package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	channelID, err := parseUUIDLoose(chi.URLParam(r, "channelId"))
	if err != nil || !channelID.Valid {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	resp := graphMemoryLineageResponse{
		WorkspaceID: workspaceID,
		ChannelID:   uuidToString(channelID),
		Lineage:     []graphMemoryLineageEntry{},
	}
	route, err := h.Queries.GetGraphMemoryChannelRoute(r.Context(), db.GetGraphMemoryChannelRouteParams{
		ChannelID: channelID, WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load graph memory channel route")
		return
	}
	if err == nil {
		resp.RoutingMode = route.RoutingMode
		resp.Current = &graphMemoryLineageCurrent{
			GraphKind:    route.CurrentGraphKind,
			GraphOwnerID: uuidToString(route.CurrentGraphOwnerID),
			Generation:   route.Generation,
		}
	}
	rows, err := h.Queries.ListGraphMemoryChannelLineage(r.Context(), db.ListGraphMemoryChannelLineageParams{
		WorkspaceID: parseUUID(workspaceID), ChannelID: channelID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load graph memory channel lineage")
		return
	}
	for _, row := range rows {
		entry := graphMemoryLineageEntry{
			Generation:   row.Generation,
			GraphKind:    row.GraphKind,
			GraphOwnerID: uuidToString(row.GraphOwnerID),
			ValidFrom:    row.ValidFrom.Time.UTC().Format(time.RFC3339),
		}
		if row.ValidTo.Valid {
			entry.ValidTo = row.ValidTo.Time.UTC().Format(time.RFC3339)
		}
		resp.Lineage = append(resp.Lineage, entry)
	}
	writeJSON(w, http.StatusOK, resp)
}
