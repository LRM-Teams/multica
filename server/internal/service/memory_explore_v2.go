// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ErrMemoryExploreBudgetExhausted marks a refused operation: the trajectory
// consumed one of its persisted hard budgets.
var ErrMemoryExploreBudgetExhausted = errors.New("memory explore budget exhausted")

// Ledger ceilings of the Explore v2 surface (plan Task 11 Step 2). A
// workspace may only tighten these, never loosen them.
const (
	exploreEvidenceMaxChunkBytes  = 8 * 1024 // bounded sanitized trajectory chunk
	exploreCheckpointPriorLimit   = 16       // bounded prior carried across rollover
	exploreHistoryLimit           = 64       // bounded history window
	exploreCheckpointTrajectories = 256      // bounded in-memory checkpoint ledger
)

// MemoryExploreV2Start is the gated Start result: the authorized plan and
// its atom-ledger seeds.
type MemoryExploreV2Start struct {
	Plan  MemoryExplorePlan   `json:"plan"`
	Seeds []ResolvedMemoryRef `json:"seeds"`
}

// MemoryExploreV2Neighbors is one authorized neighborhood.
type MemoryExploreV2Neighbors struct {
	Refs         []ResolvedMemoryRef `json:"refs"`
	SiblingAtoms []ResolvedMemoryRef `json:"sibling_atoms"`
	Edges        []MemoryExploreEdge `json:"edges"`
}

// MemoryExploreEdge is one bidirectional DAG edge of the neighborhood.
type MemoryExploreEdge struct {
	EdgeSeq    int64  `json:"edge_seq"`
	Type       string `json:"type"`
	SrcSegment string `json:"src_segment_id"`
	DstSegment string `json:"dst_segment_id"`
}

// MemoryExploreV2Evidence is the summary-first evidence of one segment.
type MemoryExploreV2Evidence struct {
	Ref             memorygraph.MemoryRef `json:"ref"`
	SegmentID       string                `json:"segment_id"`
	Summary         string                `json:"summary"`
	TrajectoryChunk json.RawMessage       `json:"trajectory_chunk,omitempty"`
	PublishSeq      int64                 `json:"publish_seq"`
}

// MemoryExploreV2Checkpoint is the bounded state of one trajectory.
type MemoryExploreV2Checkpoint struct {
	TrajectoryID string              `json:"trajectory_id"`
	Prior        []ResolvedMemoryRef `json:"prior"`
	RoundsUsed   int                 `json:"rounds_used"`
	SegmentsUsed int                 `json:"segments_used"`
	Focus        string              `json:"focus,omitempty"`
}

// MemoryExploreV2History is the bounded walk history.
type MemoryExploreV2History struct {
	TrajectoryID string              `json:"trajectory_id"`
	Refs         []ResolvedMemoryRef `json:"refs"`
}

// exploreCheckpoint is the mutable per-trajectory ledger entry.
type exploreCheckpoint struct {
	prior     []ResolvedMemoryRef
	distinct  map[string]bool
	rounds    int
	focus     string
	submitted bool
}

// MemoryExploreV2Service implements the gated Explore v2 methods (plan
// Task 11): Start/Explore/Redirect/Submit/Checkpoint/Evidence/History.
// Every operation rechecks the memory_explore_v2 phase gate and the Task 8A
// source fence — a gate flip or a mid-walk retraction fails the next
// operation closed. Checkpoint state is bounded in memory; the plan,
// watermarks and budgets are the durable truth (migration 468).
type MemoryExploreV2Service struct {
	pool    *pgxpool.Pool
	gate    *MemoryReadGate
	plans   *MemoryExplorePlanService
	queries *db.Queries

	mu          sync.Mutex
	checkpoints map[string]*exploreCheckpoint
}

// NewMemoryExploreV2Service constructs the service over the pool.
func NewMemoryExploreV2Service(pool *pgxpool.Pool) *MemoryExploreV2Service {
	return &MemoryExploreV2Service{
		pool: pool, gate: NewMemoryReadGate(db.New(pool)),
		plans: NewMemoryExplorePlanService(pool), queries: db.New(pool),
		checkpoints: make(map[string]*exploreCheckpoint),
	}
}

// requireOpen fails closed unless the explore route is green.
func (s *MemoryExploreV2Service) requireOpen(ctx context.Context, workspaceID pgtype.UUID) error {
	if s == nil || s.pool == nil {
		return errors.New("memory explore v2 service not configured")
	}
	if err := s.gate.RequireRouteEnabled(ctx, workspaceID, MemoryRouteExplore); err != nil {
		return fmt.Errorf("%w: %v", ErrMemoryRouteDisabled, err)
	}
	return nil
}

func (s *MemoryExploreV2Service) checkpointKey(workspaceID pgtype.UUID, trajectoryID string) string {
	return workspaceID.String() + "/" + trajectoryID
}

// checkpointFor returns (creating when absent) the bounded checkpoint entry.
func (s *MemoryExploreV2Service) checkpointFor(workspaceID pgtype.UUID, trajectoryID string) *exploreCheckpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.checkpointKey(workspaceID, trajectoryID)
	cp, ok := s.checkpoints[key]
	if !ok {
		if len(s.checkpoints) >= exploreCheckpointTrajectories {
			// Bounded ledger: drop the first (oldest by Go map iteration is
			// unspecified, so this is a size cap, not an LRU — the durable
			// truth is the plan row).
			for k := range s.checkpoints {
				delete(s.checkpoints, k)
				break
			}
		}
		cp = &exploreCheckpoint{distinct: make(map[string]bool)}
		s.checkpoints[key] = cp
	}
	return cp
}

// Start pins the plan (Task 10) and seeds the walk from the atom ledger at
// the frozen watermark. A replayed start reuses the persisted plan.
func (s *MemoryExploreV2Service) Start(
	ctx context.Context, workspaceID pgtype.UUID, trajectoryID string, graphs []PinnedGraph,
) (MemoryExploreV2Start, error) {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return MemoryExploreV2Start{}, err
	}
	plan, err := s.plans.CreatePlan(ctx, workspaceID, trajectoryID, graphs)
	if err != nil {
		return MemoryExploreV2Start{}, err
	}
	channelID := pinnedChannelID(plan)
	rows, err := s.queries.ListStagingAtomsAtWatermark(ctx, db.ListStagingAtomsAtWatermarkParams{
		WorkspaceID: workspaceID, PublishSeqMax: plan.SegmentPublishSeqMax,
		ChannelID: channelID, LimitRows: int32(plan.Budgets.AtomsPerResponse),
	})
	if err != nil {
		return MemoryExploreV2Start{}, fmt.Errorf("memory explore seeds: %w", err)
	}
	seeds := make([]ResolvedMemoryRef, 0, len(rows))
	for _, row := range rows {
		seeds = append(seeds, ResolvedMemoryRef{
			Ref: memorygraph.MemoryRef{
				Kind: memorygraph.MemoryRefStagingAtom, AtomID: row.AtomID,
				SegmentID: row.SegmentID, ChannelID: row.ChannelID,
			},
			SegmentID: row.SegmentID, PublishSeq: row.AtomPublishSeq,
		})
	}
	cp := s.checkpointFor(workspaceID, trajectoryID)
	s.mu.Lock()
	cp.rounds = 1
	s.mu.Unlock()
	return MemoryExploreV2Start{Plan: plan, Seeds: seeds}, nil
}

// Explore resolves one ref and walks its authorized neighborhood: the
// Atom→Segment edge, sibling atoms of the segment, and the bidirectional
// DAG edges under the frozen edge watermark. The source fence is rechecked.
func (s *MemoryExploreV2Service) Explore(
	ctx context.Context, workspaceID pgtype.UUID, trajectoryID string, ref memorygraph.MemoryRef,
) (MemoryExploreV2Neighbors, error) {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return MemoryExploreV2Neighbors{}, err
	}
	plan, err := s.plans.GetPlan(ctx, workspaceID, trajectoryID)
	if err != nil {
		return MemoryExploreV2Neighbors{}, err
	}
	resolved, err := s.plans.ResolveRef(ctx, workspaceID, plan, ref)
	if err != nil {
		return MemoryExploreV2Neighbors{}, err
	}
	cp := s.checkpointFor(workspaceID, trajectoryID)
	if err := s.chargeSegment(cp, resolved.SegmentID, plan); err != nil {
		return MemoryExploreV2Neighbors{}, err
	}

	out := MemoryExploreV2Neighbors{Refs: []ResolvedMemoryRef{resolved}}
	siblings, err := s.queries.ListSiblingAtoms(ctx, db.ListSiblingAtomsParams{
		WorkspaceID: workspaceID, SegmentID: resolved.SegmentID,
		ExceptAtomID: ref.AtomID, LimitRows: int32(plan.Budgets.AtomsPerResponse),
	})
	if err != nil {
		return MemoryExploreV2Neighbors{}, fmt.Errorf("memory explore siblings: %w", err)
	}
	for _, atomID := range siblings {
		out.SiblingAtoms = append(out.SiblingAtoms, ResolvedMemoryRef{
			Ref: memorygraph.MemoryRef{
				Kind: memorygraph.MemoryRefStagingAtom, AtomID: atomID,
				SegmentID: resolved.SegmentID, ChannelID: ref.ChannelID,
			},
			SegmentID: resolved.SegmentID, PublishSeq: resolved.PublishSeq,
		})
	}
	edges, err := s.queries.ListDAGEdgesAroundSegment(ctx, db.ListDAGEdgesAroundSegmentParams{
		WorkspaceID: workspaceID, EdgeSeqMax: plan.InteractionEdgeSeqMax,
		SegmentID: resolved.SegmentID, LimitRows: int32(plan.Budgets.Neighbors),
	})
	if err != nil {
		return MemoryExploreV2Neighbors{}, fmt.Errorf("memory explore edges: %w", err)
	}
	for _, edge := range edges {
		out.Edges = append(out.Edges, MemoryExploreEdge{
			EdgeSeq: edge.EdgeSeq, Type: edge.Type,
			SrcSegment: edge.SrcSegmentID, DstSegment: edge.DstSegmentID,
		})
	}
	s.recordRef(cp, resolved)
	return out, nil
}

// chargeSegment enforces the distinct-segment ceiling of the plan.
func (s *MemoryExploreV2Service) chargeSegment(cp *exploreCheckpoint, segmentID string, plan MemoryExplorePlan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !cp.distinct[segmentID] {
		if len(cp.distinct) >= plan.Budgets.DistinctSegments {
			return fmt.Errorf("%w: distinct segments", ErrMemoryExploreBudgetExhausted)
		}
		cp.distinct[segmentID] = true
	}
	return nil
}

// consumeDistinctSegment is the test seam for the ceiling.
func (s *MemoryExploreV2Service) consumeDistinctSegment(ctx context.Context, workspaceID pgtype.UUID, trajectoryID, segmentID string) error {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return err
	}
	plan, err := s.plans.GetPlan(ctx, workspaceID, trajectoryID)
	if err != nil {
		return err
	}
	return s.chargeSegment(s.checkpointFor(workspaceID, trajectoryID), segmentID, plan)
}

// recordRef appends to the bounded prior.
func (s *MemoryExploreV2Service) recordRef(cp *exploreCheckpoint, ref ResolvedMemoryRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp.prior = append(cp.prior, ref)
	if len(cp.prior) > exploreCheckpointPriorLimit {
		cp.prior = append([]ResolvedMemoryRef(nil), cp.prior[len(cp.prior)-exploreCheckpointPriorLimit:]...)
	}
}

// Redirect changes the walk focus; gated.
func (s *MemoryExploreV2Service) Redirect(ctx context.Context, workspaceID pgtype.UUID, trajectoryID, focus string) error {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return err
	}
	if _, err := s.plans.GetPlan(ctx, workspaceID, trajectoryID); err != nil {
		return err
	}
	cp := s.checkpointFor(workspaceID, trajectoryID)
	s.mu.Lock()
	cp.focus = focus
	cp.rounds++
	s.mu.Unlock()
	return nil
}

// Submit finalizes the trajectory; gated.
func (s *MemoryExploreV2Service) Submit(ctx context.Context, workspaceID pgtype.UUID, trajectoryID string) error {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return err
	}
	if _, err := s.plans.GetPlan(ctx, workspaceID, trajectoryID); err != nil {
		return err
	}
	cp := s.checkpointFor(workspaceID, trajectoryID)
	s.mu.Lock()
	cp.submitted = true
	s.mu.Unlock()
	return nil
}

// Checkpoint returns the bounded state. After a rollover (a newer plan with
// higher watermarks) the prior is carried and later operations re-resolve
// against the latest authorized plan.
func (s *MemoryExploreV2Service) Checkpoint(
	ctx context.Context, workspaceID pgtype.UUID, trajectoryID string,
) (MemoryExploreV2Checkpoint, error) {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return MemoryExploreV2Checkpoint{}, err
	}
	if _, err := s.plans.GetPlan(ctx, workspaceID, trajectoryID); err != nil {
		return MemoryExploreV2Checkpoint{}, err
	}
	cp := s.checkpointFor(workspaceID, trajectoryID)
	s.mu.Lock()
	defer s.mu.Unlock()
	prior := append([]ResolvedMemoryRef(nil), cp.prior...)
	if len(prior) > exploreCheckpointPriorLimit {
		prior = prior[len(prior)-exploreCheckpointPriorLimit:]
	}
	return MemoryExploreV2Checkpoint{
		TrajectoryID: trajectoryID, Prior: prior,
		RoundsUsed: cp.rounds, SegmentsUsed: len(cp.distinct), Focus: cp.focus,
	}, nil
}

// Evidence serves the summary-first evidence of one ref's segment: the
// closing event plus a bounded chunk of the sanitized trajectory. The
// source fence is rechecked; retracted sources never yield a chunk.
func (s *MemoryExploreV2Service) Evidence(
	ctx context.Context, workspaceID pgtype.UUID, trajectoryID string, ref memorygraph.MemoryRef,
) (MemoryExploreV2Evidence, error) {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return MemoryExploreV2Evidence{}, err
	}
	plan, err := s.plans.GetPlan(ctx, workspaceID, trajectoryID)
	if err != nil {
		return MemoryExploreV2Evidence{}, err
	}
	resolved, err := s.plans.ResolveRef(ctx, workspaceID, plan, ref)
	if err != nil {
		return MemoryExploreV2Evidence{}, err
	}
	row, err := s.queries.GetSegmentEvidence(ctx, db.GetSegmentEvidenceParams{
		WorkspaceID: workspaceID, SegmentID: resolved.SegmentID,
	})
	if err != nil {
		return MemoryExploreV2Evidence{}, fmt.Errorf("memory explore evidence: %w", err)
	}
	summary := ""
	if row.ClosingEvent.Valid {
		summary = row.ClosingEvent.String
	}
	var publishSeq int64
	if row.PublishSeq.Valid {
		publishSeq = row.PublishSeq.Int64
	}
	// Summary-first: canonical segments carry no closing event — their
	// atoms are the summary until a reviewer writes one.
	if summary == "" {
		bodies, bodyErr := s.queries.ListSegmentAtomBodies(ctx, db.ListSegmentAtomBodiesParams{
			WorkspaceID: workspaceID, SegmentID: resolved.SegmentID, LimitRows: 8,
		})
		if bodyErr == nil {
			for _, b := range bodies {
				if len(summary) > 0 {
					summary += " "
				}
				summary += b.Body
				if len(summary) >= 2048 {
					break
				}
			}
		}
	}
	ev := MemoryExploreV2Evidence{
		Ref: ref, SegmentID: row.SegmentID, Summary: summary, PublishSeq: publishSeq,
	}
	if len(row.Trajectory) > 0 {
		chunk := row.Trajectory
		if len(chunk) > exploreEvidenceMaxChunkBytes {
			chunk = chunk[:exploreEvidenceMaxChunkBytes]
		}
		ev.TrajectoryChunk = json.RawMessage(chunk)
	}
	s.recordRef(s.checkpointFor(workspaceID, trajectoryID), resolved)
	return ev, nil
}

// History returns the bounded authorized walk of one trajectory.
func (s *MemoryExploreV2Service) History(
	ctx context.Context, workspaceID pgtype.UUID, trajectoryID string,
) (MemoryExploreV2History, error) {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return MemoryExploreV2History{}, err
	}
	if _, err := s.plans.GetPlan(ctx, workspaceID, trajectoryID); err != nil {
		return MemoryExploreV2History{}, err
	}
	cp := s.checkpointFor(workspaceID, trajectoryID)
	s.mu.Lock()
	defer s.mu.Unlock()
	refs := append([]ResolvedMemoryRef(nil), cp.prior...)
	if len(refs) > exploreHistoryLimit {
		refs = refs[len(refs)-exploreHistoryLimit:]
	}
	return MemoryExploreV2History{TrajectoryID: trajectoryID, Refs: refs}, nil
}

// pinnedChannelID returns the first pinned channel graph owner ("" when the
// plan pins project graphs only).
func pinnedChannelID(plan MemoryExplorePlan) string {
	for _, g := range plan.Graphs {
		if g.Kind == "channel" {
			return g.OwnerID
		}
	}
	return ""
}

// ResolveGraphMemoryAgentProtocol negotiates the Graph Memory Agent protocol
// generation (Task 12): generation 2 requires BOTH sides — the daemon/agent
// advertised the memory_explore_v2 capability AND the workspace's
// memory_explore_v2 phase gate is green. A capability alone never authorizes
// a disabled server path, and a red gate never falls back to exposing v2
// payloads through the v1 surface (v1 keeps working either way).
func ResolveGraphMemoryAgentProtocol(ctx context.Context, capabilities []string, conn db.DBTX, workspaceID pgtype.UUID) int {
	if conn == nil {
		return 1
	}
	advertised := false
	for _, c := range capabilities {
		if c == "memory_explore_v2" {
			advertised = true
			break
		}
	}
	if !advertised {
		return 1
	}
	enabled, err := NewMemoryReadGate(db.New(conn)).RouteEnabled(ctx, workspaceID, MemoryRouteExplore)
	if err != nil || !enabled {
		return 1
	}
	return 2
}

// SearchExternal is the external v2 Search (plan Task 13): the class-aware
// SearchAt channel over the channel's scoped graph plus the active atom
// ledger at the current watermark. Gated on the explore route; the Task 8A
// fence is re-asserted by SearchAt's retraction set.
func (s *MemoryExploreV2Service) SearchExternal(
	ctx context.Context, workspaceID pgtype.UUID, channelID, query string,
) ([]memorygraph.SearchHit, error) {
	if err := s.requireOpen(ctx, workspaceID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("memory explore v2 search query is required")
	}
	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return nil, err
	}
	route, err := ResolveChannelRoute(ctx, s.pool, workspaceID.String(), channelID)
	if err != nil {
		return nil, err
	}
	storeDir, err := memorygraph.EnsureScopedDir(root, workspaceID.String(),
		memorygraph.GraphDirKind(route.GraphKind), route.GraphOwnerID)
	if err != nil {
		return nil, err
	}
	store := memorygraph.NewStore(storeDir)
	if err := store.Init(); err != nil {
		return nil, err
	}
	version, err := activeGraphVersionForStore(ctx, s.pool, workspaceID, string(memorygraph.GraphDirKind(route.GraphKind)), route.GraphOwnerID, store)
	if err != nil {
		return nil, err
	}
	atoms, watermark, retracted, err := LoadActiveAtomSnapshot(ctx, s.pool, workspaceID, channelID, atomSnapshotLimit)
	if err != nil {
		return nil, err
	}
	view := memorygraph.GraphView{ChannelID: channelID}
	if route.GraphKind == string(memorygraph.GraphDirKindProject) {
		view = memorygraph.GraphView{AllowProject: true, ChannelID: channelID}
	}
	retr, err := newAtomScopedRetriever(ctx, storeDir, version, view, atoms, watermark, retracted)
	if err != nil {
		return nil, err
	}
	return retr.SearchAt(ctx, query, view, watermark)
}
