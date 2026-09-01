// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ErrMemoryRouteDisabled is the unavailable signal of a gated-off Explore
// route: no plan is created, read, or resolved while the phase gate is red.
var ErrMemoryRouteDisabled = errors.New("memory route disabled by the workspace phase gate")

// ErrMemoryRefUnauthorized marks a ref whose target is not part of the
// caller's plan: authorization comes from the plan, never from ref fields.
var ErrMemoryRefUnauthorized = errors.New("memory ref not authorized by the explore plan")

// PinnedGraph freezes one physical graph the plan may read: the route kind
// resolved at plan time, its owning id, and the lineage generation.
type PinnedGraph struct {
	Kind       string `json:"kind"`       // "channel" | "project"
	OwnerID    string `json:"owner_id"`   // graph owner uuid
	Generation int64  `json:"generation"` // lineage generation
}

// ExploreBudgets are the server-side ledger budgets persisted with every
// plan (Task 11 Step 2 applies them as hard ceilings; workspaces may only
// tighten them).
type ExploreBudgets struct {
	Rounds           int `json:"rounds"`
	Neighbors        int `json:"neighbors"`
	DistinctSegments int `json:"distinct_segments"`
	AtomsPerResponse int `json:"atoms_per_response"`
	ToolCalls        int `json:"tool_calls"`
	Tokens           int `json:"tokens"`
	Seconds          int `json:"seconds"`
}

// DefaultExploreBudgets returns the exact server ledger budgets (Task 11
// Step 2): 6 rounds, 8 neighbors, 32 distinct segments, 8 atoms/response,
// 32 tool calls, 32000 tokens, 600 seconds.
func DefaultExploreBudgets() ExploreBudgets {
	return ExploreBudgets{
		Rounds: 6, Neighbors: 8, DistinctSegments: 32, AtomsPerResponse: 8,
		ToolCalls: 32, Tokens: 32000, Seconds: 600,
	}
}

// MemoryExplorePlan is the persisted plan ledger row (migration 468): the
// pinned graphs, the frozen watermarks, and the budgets of one trajectory.
type MemoryExplorePlan struct {
	TrajectoryID          string         `json:"trajectory_id"`
	Graphs                []PinnedGraph  `json:"graphs"`
	SegmentPublishSeqMax  int64          `json:"segment_publish_seq_max"`
	InteractionEdgeSeqMax int64          `json:"interaction_edge_seq_max"`
	Budgets               ExploreBudgets `json:"budgets"`
}

// ResolvedMemoryRef is the authorized outcome of one ref resolution.
type ResolvedMemoryRef struct {
	Ref        memorygraph.MemoryRef `json:"ref"`
	SegmentID  string                `json:"segment_id,omitempty"`
	PublishSeq int64                 `json:"publish_seq,omitempty"`
}

// MemoryExplorePlanService owns the Explore plan ledger (Task 10). Every
// method requires the memory_explore_v2 route to be green; a disabled gate
// never persists or serves a plan.
type MemoryExplorePlanService struct {
	pool *pgxpool.Pool
	gate *MemoryReadGate
}

// NewMemoryExplorePlanService constructs the plan service over the pool.
func NewMemoryExplorePlanService(pool *pgxpool.Pool) *MemoryExplorePlanService {
	return &MemoryExplorePlanService{pool: pool, gate: NewMemoryReadGate(db.New(pool))}
}

// CreatePlan validates and persists one trajectory's plan. A replayed start
// for the same trajectory is idempotent (one row, high-water marks kept,
// rollover counted). Watermarks are frozen at creation from the workspace's
// current publish/edge ceilings.
func (s *MemoryExplorePlanService) CreatePlan(
	ctx context.Context, workspaceID pgtype.UUID, trajectoryID string, graphs []PinnedGraph,
) (MemoryExplorePlan, error) {
	if s == nil || s.pool == nil {
		return MemoryExplorePlan{}, errors.New("memory explore plan service not configured")
	}
	if err := s.gate.RequireRouteEnabled(ctx, workspaceID, MemoryRouteExplore); err != nil {
		return MemoryExplorePlan{}, fmt.Errorf("%w: %v", ErrMemoryRouteDisabled, err)
	}
	if err := validateExploreTrajectoryID(trajectoryID); err != nil {
		return MemoryExplorePlan{}, err
	}
	pinned, err := validatePinnedGraphs(graphs)
	if err != nil {
		return MemoryExplorePlan{}, err
	}
	queries := db.New(s.pool)
	marks, err := queries.MemoryExploreWatermarks(ctx, workspaceID)
	if err != nil {
		return MemoryExplorePlan{}, fmt.Errorf("memory explore watermarks: %w", err)
	}
	graphsJSON, err := json.Marshal(pinned)
	if err != nil {
		return MemoryExplorePlan{}, fmt.Errorf("memory explore plan graphs: %w", err)
	}
	budgets := DefaultExploreBudgets()
	budgetsJSON, err := json.Marshal(budgets)
	if err != nil {
		return MemoryExplorePlan{}, fmt.Errorf("memory explore plan budgets: %w", err)
	}
	row, err := queries.UpsertMemoryExplorePlan(ctx, db.UpsertMemoryExplorePlanParams{
		WorkspaceID: workspaceID, TrajectoryID: trajectoryID,
		PinnedGraphs:          graphsJSON,
		SegmentPublishSeqMax:  marks.SegmentPublishSeqMax,
		InteractionEdgeSeqMax: marks.InteractionEdgeSeqMax,
		Budgets:               budgetsJSON,
	})
	if err != nil {
		return MemoryExplorePlan{}, fmt.Errorf("persist memory explore plan: %w", err)
	}
	return planFromRow(row, budgets)
}

// GetPlan serves one persisted plan; the route must be green.
func (s *MemoryExplorePlanService) GetPlan(
	ctx context.Context, workspaceID pgtype.UUID, trajectoryID string,
) (MemoryExplorePlan, error) {
	if s == nil || s.pool == nil {
		return MemoryExplorePlan{}, errors.New("memory explore plan service not configured")
	}
	if err := s.gate.RequireRouteEnabled(ctx, workspaceID, MemoryRouteExplore); err != nil {
		return MemoryExplorePlan{}, fmt.Errorf("%w: %v", ErrMemoryRouteDisabled, err)
	}
	row, err := db.New(s.pool).GetMemoryExplorePlan(ctx, db.GetMemoryExplorePlanParams{
		WorkspaceID: workspaceID, TrajectoryID: trajectoryID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryExplorePlan{}, fmt.Errorf("%w: %q", ErrMemoryExploreTrajectoryNotFound, trajectoryID)
		}
		return MemoryExplorePlan{}, fmt.Errorf("load memory explore plan: %w", err)
	}
	return planFromRow(row, DefaultExploreBudgets())
}

// ResolveRef turns one validated ref into an authorized resolution
// (Task 10 Step 2): the ref's owning graph must be pinned by the plan
// (authorization comes from the plan, never from the ref), and the source
// is rechecked against the Task 8A retraction registry on every resolve.
func (s *MemoryExplorePlanService) ResolveRef(
	ctx context.Context, workspaceID pgtype.UUID, plan MemoryExplorePlan, ref memorygraph.MemoryRef,
) (ResolvedMemoryRef, error) {
	if s == nil || s.pool == nil {
		return ResolvedMemoryRef{}, errors.New("memory explore plan service not configured")
	}
	if err := memorygraph.ValidateMemoryRef(ref); err != nil {
		return ResolvedMemoryRef{}, err
	}
	if err := s.gate.RequireRouteEnabled(ctx, workspaceID, MemoryRouteExplore); err != nil {
		return ResolvedMemoryRef{}, fmt.Errorf("%w: %v", ErrMemoryRouteDisabled, err)
	}
	switch ref.Kind {
	case memorygraph.MemoryRefStagingAtom:
		return s.resolveStagingAtom(ctx, workspaceID, plan, ref)
	case memorygraph.MemoryRefGraphNode:
		// Graph nodes are authorized by scope: a channel node needs its
		// channel graph pinned; a project node needs a project graph
		// pinned. The node body itself is read through the store, never
		// through this ledger.
		if ref.ChannelID != "" {
			if !planPins(plan, "channel", ref.ChannelID) {
				return ResolvedMemoryRef{}, fmt.Errorf("%w: channel graph not pinned", ErrMemoryRefUnauthorized)
			}
			return ResolvedMemoryRef{Ref: ref}, nil
		}
		if !planPinsAny(plan, "project") {
			return ResolvedMemoryRef{}, fmt.Errorf("%w: no project graph pinned", ErrMemoryRefUnauthorized)
		}
		return ResolvedMemoryRef{Ref: ref}, nil
	default:
		return ResolvedMemoryRef{}, fmt.Errorf("memory ref: unknown kind %q", ref.Kind)
	}
}

// resolveStagingAtom loads the atom's owning segment, checks the plan's
// pinned graphs against the segment's frozen event-time scope, and rechecks
// the retraction registry for the segment's task_output source.
func (s *MemoryExplorePlanService) resolveStagingAtom(
	ctx context.Context, workspaceID pgtype.UUID, plan MemoryExplorePlan, ref memorygraph.MemoryRef,
) (ResolvedMemoryRef, error) {
	queries := db.New(s.pool)
	row, err := queries.ResolveStagingAtomForRef(ctx, db.ResolveStagingAtomForRefParams{
		WorkspaceID: workspaceID, AtomID: ref.AtomID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResolvedMemoryRef{}, fmt.Errorf("%w: atom %s not found", ErrMemoryRefUnauthorized, ref.AtomID)
		}
		return ResolvedMemoryRef{}, fmt.Errorf("resolve staging atom: %w", err)
	}
	if row.ChannelIDAtEvent != "" {
		if !planPins(plan, "channel", row.ChannelIDAtEvent) {
			return ResolvedMemoryRef{}, fmt.Errorf("%w: channel graph not pinned", ErrMemoryRefUnauthorized)
		}
	} else if !planPinsAny(plan, "project") {
		return ResolvedMemoryRef{}, fmt.Errorf("%w: no project graph pinned", ErrMemoryRefUnauthorized)
	}
	// Retraction recheck on every resolve (Task 8A, spec §9).
	sourceUUID, err := util.ParseUUID(row.SourceID)
	if err != nil {
		return ResolvedMemoryRef{}, fmt.Errorf("resolve staging atom source: %w", err)
	}
	if err := s.gate.AuthorizeResolve(ctx, workspaceID, []MemorySourceRef{
		{WorkspaceID: workspaceID, Kind: MemorySourceTaskOutput, ID: sourceUUID},
	}); err != nil {
		return ResolvedMemoryRef{}, err
	}
	resolved := ResolvedMemoryRef{Ref: ref, SegmentID: row.SegmentID}
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(publish_seq),0) FROM interaction_dag_segment WHERE workspace_id=$1 AND segment_id=$2`,
		workspaceID, row.SegmentID).Scan(&resolved.PublishSeq); err != nil {
		return ResolvedMemoryRef{}, fmt.Errorf("resolve staging atom watermark: %w", err)
	}
	return resolved, nil
}

// planPins reports whether the plan pinned the graph of one kind+owner.
func planPins(plan MemoryExplorePlan, kind, ownerID string) bool {
	for _, g := range plan.Graphs {
		if g.Kind == kind && strings.EqualFold(g.OwnerID, ownerID) {
			return true
		}
	}
	return false
}

func planPinsAny(plan MemoryExplorePlan, kind string) bool {
	for _, g := range plan.Graphs {
		if g.Kind == kind {
			return true
		}
	}
	return false
}

// validateExploreTrajectoryID enforces the ledger's trajectory shape
// (mirrors migration 468's CHECK; validation happens before any write).
func validateExploreTrajectoryID(trajectoryID string) error {
	trimmed := strings.TrimSpace(trajectoryID)
	if len(trimmed) < 1 || len(trimmed) > 128 {
		return fmt.Errorf("memory explore plan: trajectory_id must be 1-128 non-space bytes")
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("memory explore plan: trajectory_id contains control characters")
		}
	}
	return nil
}

// validatePinnedGraphs checks every pinned graph is well-formed and dedupes
// kind+owner pairs deterministically.
func validatePinnedGraphs(graphs []PinnedGraph) ([]PinnedGraph, error) {
	if len(graphs) == 0 {
		return nil, fmt.Errorf("memory explore plan: at least one pinned graph is required")
	}
	seen := make(map[string]bool, len(graphs))
	out := make([]PinnedGraph, 0, len(graphs))
	for _, g := range graphs {
		switch g.Kind {
		case "channel", "project":
		default:
			return nil, fmt.Errorf("memory explore plan: unknown graph kind %q", g.Kind)
		}
		if _, err := util.ParseUUID(g.OwnerID); err != nil {
			return nil, fmt.Errorf("memory explore plan: graph owner %q is not a uuid", g.OwnerID)
		}
		if g.Generation < 1 {
			return nil, fmt.Errorf("memory explore plan: graph generation must be >= 1")
		}
		key := g.Kind + ":" + strings.ToLower(g.OwnerID)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, g)
	}
	return out, nil
}

// planFromRow decodes one ledger row into a MemoryExplorePlan; budgets fall
// back to the defaults when the stored jsonb is missing fields.
func planFromRow(row db.MemoryExplorePlan, fallbackBudgets ExploreBudgets) (MemoryExplorePlan, error) {
	plan := MemoryExplorePlan{
		TrajectoryID:          row.TrajectoryID,
		SegmentPublishSeqMax:  row.SegmentPublishSeqMax,
		InteractionEdgeSeqMax: row.InteractionEdgeSeqMax,
		Budgets:               fallbackBudgets,
	}
	if len(row.PinnedGraphs) > 0 {
		if err := json.Unmarshal(row.PinnedGraphs, &plan.Graphs); err != nil {
			return MemoryExplorePlan{}, fmt.Errorf("decode pinned graphs: %w", err)
		}
	}
	if len(row.Budgets) > 0 {
		if err := json.Unmarshal(row.Budgets, &plan.Budgets); err != nil {
			return MemoryExplorePlan{}, fmt.Errorf("decode budgets: %w", err)
		}
	}
	return plan, nil
}

// ErrMemoryExploreTrajectoryNotFound marks a GetPlan miss: no persisted plan
// exists for the trajectory in this workspace.
var ErrMemoryExploreTrajectoryNotFound = errors.New("memory explore trajectory not found")
