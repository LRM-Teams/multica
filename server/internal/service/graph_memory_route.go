package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GraphRouteResolution is the server's authoritative current write target
// for one channel (spec §4).
type GraphRouteResolution struct {
	RoutingMode  string // "standalone" | "project_lineage"
	GraphKind    string // "project" | "channel"
	GraphOwnerID string
	Generation   int64
}

// graphRouteState is the persisted route row (found=false when absent).
type graphRouteState struct {
	found       bool
	routingMode string
	graphKind   string
	ownerID     string
	generation  int64
}

// resolveChannelRouteTransition is the pure transition table of spec §4.
// boundProjectID is the server's current channel binding ("" = unbound).
// It returns the next route state, whether the current lineage generation
// closes, and whether a new lineage row is appended.
func resolveChannelRouteTransition(cur graphRouteState, channelID, boundProjectID string) (next graphRouteState, closeCurrent, appendLineage bool) {
	bound := strings.TrimSpace(boundProjectID) != ""
	if !cur.found {
		if !bound {
			return graphRouteState{true, "standalone", "channel", channelID, 1}, false, true
		}
		return graphRouteState{true, "project_lineage", "project", boundProjectID, 1}, false, true
	}
	if cur.routingMode == "standalone" {
		return cur, false, false // permanent: keeps its channel graph (§4.2)
	}
	// project_lineage
	switch {
	case bound && cur.graphKind == "project" && cur.ownerID == boundProjectID:
		return cur, false, false
	case bound && cur.graphKind == "project" && cur.ownerID != boundProjectID:
		return graphRouteState{true, "project_lineage", "project", boundProjectID, cur.generation + 1}, true, true // §4.3
	case !bound && cur.graphKind == "project":
		return graphRouteState{true, "project_lineage", "channel", channelID, cur.generation + 1}, true, true // §4.4 temporary
	case bound && cur.graphKind == "channel":
		return graphRouteState{true, "project_lineage", "project", boundProjectID, cur.generation + 1}, true, true // §4.4 rebind
	default:
		return cur, false, false
	}
}

// ResolveChannelRoute locks the channel row and its route row in one
// transaction, reads the server's current binding (never a caller-supplied
// project id), applies the transition, and persists route + lineage
// atomically. Concurrent calls serialize on the channel row lock, so
// repeated resolution yields exactly one active generation.
func ResolveChannelRoute(ctx context.Context, pool *pgxpool.Pool, workspaceID, channelID string) (GraphRouteResolution, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return GraphRouteResolution{}, fmt.Errorf("graph_scope_unresolved: %w", err)
	}
	chUUID, err := util.ParseUUID(channelID)
	if err != nil {
		return GraphRouteResolution{}, fmt.Errorf("graph_scope_unresolved: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return GraphRouteResolution{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	binding, err := q.GetGraphMemoryChannelBindingForUpdate(ctx, db.GetGraphMemoryChannelBindingForUpdateParams{ID: chUUID, WorkspaceID: wsUUID})
	if err != nil {
		return GraphRouteResolution{}, fmt.Errorf("graph_scope_unresolved: channel binding: %w", err)
	}
	boundProjectID := util.UUIDToString(binding) // "" when the binding is NULL

	cur := graphRouteState{}
	route, err := q.GetGraphMemoryChannelRouteForUpdate(ctx, db.GetGraphMemoryChannelRouteForUpdateParams{ChannelID: chUUID, WorkspaceID: wsUUID})
	switch {
	case err == nil:
		cur = graphRouteState{
			found:       true,
			routingMode: route.RoutingMode,
			graphKind:   route.CurrentGraphKind,
			ownerID:     util.UUIDToString(route.CurrentGraphOwnerID),
			generation:  route.Generation,
		}
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return GraphRouteResolution{}, err
	}

	next, closeCurrent, appendLineage := resolveChannelRouteTransition(cur, channelID, boundProjectID)
	if closeCurrent {
		if err := q.CloseGraphMemoryChannelLineage(ctx, db.CloseGraphMemoryChannelLineageParams{ChannelID: chUUID, Generation: cur.generation}); err != nil {
			return GraphRouteResolution{}, fmt.Errorf("graph_lineage_conflict: close generation: %w", err)
		}
	}
	if !cur.found || closeCurrent {
		ownerUUID, err := util.ParseUUID(next.ownerID)
		if err != nil {
			return GraphRouteResolution{}, fmt.Errorf("graph_scope_unresolved: route owner: %w", err)
		}
		if err := q.UpsertGraphMemoryChannelRoute(ctx, db.UpsertGraphMemoryChannelRouteParams{
			WorkspaceID:         wsUUID,
			ChannelID:           chUUID,
			RoutingMode:         next.routingMode,
			CurrentGraphKind:    next.graphKind,
			CurrentGraphOwnerID: ownerUUID,
			Generation:          next.generation,
		}); err != nil {
			return GraphRouteResolution{}, err
		}
	}
	if appendLineage {
		ownerUUID, err := util.ParseUUID(next.ownerID)
		if err != nil {
			return GraphRouteResolution{}, fmt.Errorf("graph_scope_unresolved: lineage owner: %w", err)
		}
		if err := q.AppendGraphMemoryChannelLineage(ctx, db.AppendGraphMemoryChannelLineageParams{
			WorkspaceID: wsUUID, ChannelID: chUUID, Generation: next.generation,
			GraphKind: next.graphKind, GraphOwnerID: ownerUUID,
		}); err != nil {
			return GraphRouteResolution{}, fmt.Errorf("graph_lineage_conflict: append lineage: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return GraphRouteResolution{}, err
	}
	return GraphRouteResolution{
		RoutingMode:  next.routingMode,
		GraphKind:    next.graphKind,
		GraphOwnerID: next.ownerID,
		Generation:   next.generation,
	}, nil
}
