// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task 14 consolidation outcomes at the service boundary.
const (
	GraphMemoryConsolidationPublishPublished      = "published"
	GraphMemoryConsolidationPublishSkippedNoAtoms = "skipped_no_atoms"
	GraphMemoryConsolidationPublishAbortedUncited = "aborted_uncited_atoms"
)

var ErrGraphMemoryConsolidationUncitedAtoms = errors.New("graph memory consolidation left atoms uncited")

// GraphMemoryConsolidationPublishService bridges the atom-manifest
// consolidator to the DB-authoritative publication coordinator: load the
// active atom ledger, fold it into an immutable candidate, and publish the
// winner through the source-locked CAS transaction. The file-store current
// pointer moves only as a post-commit cache.
type GraphMemoryConsolidationPublishService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryConsolidationPublishService(pool *pgxpool.Pool) *GraphMemoryConsolidationPublishService {
	return &GraphMemoryConsolidationPublishService{pool: pool}
}

// GraphMemoryConsolidationPublishReport summarizes one scope cycle.
type GraphMemoryConsolidationPublishReport struct {
	Outcome          string
	Generation       int64
	CandidateVersion int
	AtomCount        int
	UncitedAtomIDs   []string
}

// PublishScope runs one consolidation+publication cycle for a single graph
// scope. backend may be nil (the caller resolves providers); scope must be
// a validated consolidate ProviderScope.
func (s *GraphMemoryConsolidationPublishService) PublishScope(
	ctx context.Context, workspaceID pgtype.UUID, graphKind, graphOwnerID string,
	backend agent.Backend, scope memorygraph.ProviderScope,
) (*GraphMemoryConsolidationPublishReport, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("graph memory consolidation publish service not configured")
	}
	gate := NewMemoryReadGate(db.New(s.pool))
	enabled, err := gate.RouteEnabled(ctx, workspaceID, MemoryRouteAtomConsolidation)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrMemoryRouteDisabled
	}
	ownerUUID, err := parseUUIDColumn("graph_owner_id", graphOwnerID)
	if err != nil {
		return nil, err
	}

	// The consolidator input is the DB-authoritative active atom manifest at
	// the current publish watermark — never staging files.
	channelScope := ""
	if memorygraph.GraphDirKind(graphKind) == memorygraph.GraphDirKindChannel {
		channelScope = graphOwnerID
	}
	atoms, _, _, err := LoadActiveAtomSnapshot(ctx, s.pool, workspaceID, channelScope, atomSnapshotLimit)
	if err != nil {
		return nil, fmt.Errorf("load atom manifest: %w", err)
	}
	if len(atoms) == 0 {
		return &GraphMemoryConsolidationPublishReport{Outcome: GraphMemoryConsolidationPublishSkippedNoAtoms}, nil
	}

	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return nil, err
	}
	storeDir, err := memorygraph.EnsureScopedDir(root, workspaceID.String(),
		memorygraph.GraphDirKind(graphKind), graphOwnerID)
	if err != nil {
		return nil, err
	}
	store := memorygraph.NewStore(storeDir)
	if err := store.Init(); err != nil {
		return nil, err
	}

	// Base version: DB authority first, file pointer only for legacy scopes.
	baseGeneration, baseVersion, _, err := ActiveGraphPublication(ctx, s.pool, workspaceID, graphKind, ownerUUID)
	if err != nil {
		return nil, err
	}
	if baseVersion == 0 {
		current, err := store.CurrentVersion()
		if err != nil {
			return nil, fmt.Errorf("resolve base version: %w", err)
		}
		baseVersion = current
	}

	manifest := make([]memorygraph.AtomManifestEntry, 0, len(atoms))
	atomBySegment := map[string]string{}
	for _, atom := range atoms {
		manifest = append(manifest, memorygraph.AtomManifestEntry{
			AtomID: atom.AtomID, SegmentID: atom.SegmentID, Body: atom.Body,
		})
		atomBySegment[atom.SegmentID] = atom.AtomID
	}

	consolidator := memorygraph.NewConsolidator(store, backend,
		memorygraph.DefaultConsolidateConfig(), scope, nil, memorygraph.NewTraceRecorder(storeDir))
	result, err := consolidator.ConsolidateAtoms(ctx, baseVersion, manifest)
	if err != nil {
		return nil, err
	}
	if len(result.UncitedAtomIDs) > 0 {
		// Non-consumption: uncited atoms are NOT covered by this candidate,
		// so publication is refused and the next cycle still sees them.
		return &GraphMemoryConsolidationPublishReport{
			Outcome:        GraphMemoryConsolidationPublishAbortedUncited,
			AtomCount:      len(atoms),
			UncitedAtomIDs: result.UncitedAtomIDs,
		}, ErrGraphMemoryConsolidationUncitedAtoms
	}

	// Publication sources: the canonical task_output of every covered
	// segment, locked FOR KEY SHARE in deterministic order.
	segmentIDs := make([]string, 0, len(atoms))
	for _, atom := range atoms {
		segmentIDs = append(segmentIDs, atom.SegmentID)
	}
	sort.Strings(segmentIDs)
	sourceRows, err := db.New(s.pool).ListSegmentTaskSources(ctx, db.ListSegmentTaskSourcesParams{
		WorkspaceID: workspaceID, SegmentIds: segmentIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve segment sources: %w", err)
	}
	sources := make([]GraphMemoryPublicationSource, 0, len(sourceRows))
	seen := map[string]bool{}
	for _, row := range sourceRows {
		key := "task_output:" + row.AgentRunID
		if seen[key] {
			continue
		}
		seen[key] = true
		sources = append(sources, GraphMemoryPublicationSource{Kind: "task_output", ID: row.AgentRunID})
	}

	coverage := make([]GraphMemoryCoverageAtom, 0, len(atoms))
	for _, atom := range atoms {
		coverage = append(coverage, GraphMemoryCoverageAtom{AtomID: atom.AtomID, SegmentID: atom.SegmentID})
	}
	provenance := make([]GraphMemoryNodeProvenance, 0, len(result.NodeAtoms))
	for nodeID, atomIDs := range result.NodeAtoms {
		segs := map[string]bool{}
		for _, atomID := range atomIDs {
			for _, atom := range atoms {
				if atom.AtomID == atomID {
					segs[atom.SegmentID] = true
				}
			}
		}
		nodeSegments := make([]string, 0, len(segs))
		for segmentID := range segs {
			nodeSegments = append(nodeSegments, segmentID)
		}
		sort.Strings(nodeSegments)
		provenance = append(provenance, GraphMemoryNodeProvenance{
			NodeID: nodeID, AtomIDs: atomIDs, SegmentIDs: nodeSegments,
		})
	}

	pub, err := PublishGraphMemoryPublication(ctx, s.pool, GraphMemoryPublicationRequest{
		WorkspaceID: workspaceID, GraphKind: graphKind, GraphOwnerID: ownerUUID,
		Store: store, CandidateVersion: result.CandidateVersion, BaseGeneration: baseGeneration,
		Sources: sources, Coverage: coverage, Provenance: provenance, PublishedBy: "atom_consolidator",
	})
	if err != nil {
		return nil, err
	}
	return &GraphMemoryConsolidationPublishReport{
		Outcome:    GraphMemoryConsolidationPublishPublished,
		Generation: pub.Generation, CandidateVersion: result.CandidateVersion,
		AtomCount: len(atoms),
	}, nil
}

// PublishPromotion publishes one ALLOWED promotion decision as a derived
// project node through the Task 14 coordinator: an immutable candidate
// version carrying the stamped node, evidence-atom coverage, reverse
// provenance, and the evidence task_output sources locked FOR KEY SHARE.
// Deletion of any evidence source between the proposal and this commit
// aborts the publication (nothing is consumed).
func (s *GraphMemoryConsolidationPublishService) PublishPromotion(
	ctx context.Context, req PromotionRequest, decision PromotionDecision,
) (*GraphMemoryConsolidationPublishReport, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("graph memory consolidation publish service not configured")
	}
	if !decision.Allowed || decision.DerivedNode == nil {
		return nil, errors.New("promotion publication requires an allowed decision")
	}
	ownerUUID, err := parseUUIDColumn("graph_owner_id", req.ProjectID.String())
	if err != nil {
		return nil, err
	}

	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return nil, err
	}
	storeDir, err := memorygraph.EnsureScopedDir(root, req.WorkspaceID.String(),
		memorygraph.GraphDirKindProject, req.ProjectID.String())
	if err != nil {
		return nil, err
	}
	store := memorygraph.NewStore(storeDir)
	if err := store.Init(); err != nil {
		return nil, err
	}
	baseGeneration, baseVersion, _, err := ActiveGraphPublication(ctx, s.pool, req.WorkspaceID, "project", ownerUUID)
	if err != nil {
		return nil, err
	}
	if baseVersion == 0 {
		current, err := store.CurrentVersion()
		if err != nil {
			return nil, fmt.Errorf("resolve base version: %w", err)
		}
		baseVersion = current
	}
	candidate, err := store.CreateVersionFrom(baseVersion, "promotion")
	if err != nil {
		return nil, err
	}
	node := *decision.DerivedNode
	node.CreatedVersion = candidate
	node.UpdatedVersion = candidate
	if node.ObservedAt.IsZero() {
		node.ObservedAt = time.Now().UTC()
	}
	if err := store.SaveNode(candidate, &node); err != nil {
		return nil, err
	}

	q := db.New(s.pool)
	coverage := []GraphMemoryCoverageAtom{}
	provenance := []GraphMemoryNodeProvenance{}
	sources := []GraphMemoryPublicationSource{}
	if len(node.AtomRefs) > 0 {
		atoms, err := q.ListGraphMemoryAtomsByIDs(ctx, db.ListGraphMemoryAtomsByIDsParams{
			WorkspaceID: req.WorkspaceID, AtomIds: node.AtomRefs,
		})
		if err != nil {
			return nil, fmt.Errorf("resolve promotion evidence atoms: %w", err)
		}
		segmentIDs := make([]string, 0, len(atoms))
		for _, atom := range atoms {
			coverage = append(coverage, GraphMemoryCoverageAtom{AtomID: atom.AtomID, SegmentID: atom.SegmentID})
			segmentIDs = append(segmentIDs, atom.SegmentID)
		}
		provenance = append(provenance, GraphMemoryNodeProvenance{
			NodeID: node.NodeID, AtomIDs: node.AtomRefs, SegmentIDs: segmentIDs,
		})
		sort.Strings(segmentIDs)
		sourceRows, err := q.ListSegmentTaskSources(ctx, db.ListSegmentTaskSourcesParams{
			WorkspaceID: req.WorkspaceID, SegmentIds: segmentIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("resolve promotion evidence sources: %w", err)
		}
		seen := map[string]bool{}
		for _, row := range sourceRows {
			key := "task_output:" + row.AgentRunID
			if seen[key] {
				continue
			}
			seen[key] = true
			sources = append(sources, GraphMemoryPublicationSource{Kind: "task_output", ID: row.AgentRunID})
		}
	}
	for _, e := range decision.Evidence {
		if e.Kind == PromotionEvidenceCompletedOutcome {
			key := "task_output:" + e.RefID
			if !containsPromotionSource(sources, key) {
				sources = append(sources, GraphMemoryPublicationSource{Kind: "task_output", ID: e.RefID})
			}
		}
	}

	pub, err := PublishGraphMemoryPublication(ctx, s.pool, GraphMemoryPublicationRequest{
		WorkspaceID: req.WorkspaceID, GraphKind: "project", GraphOwnerID: ownerUUID,
		Store: store, CandidateVersion: candidate, BaseGeneration: baseGeneration,
		Sources: sources, Coverage: coverage, Provenance: provenance,
		PublishedBy: "promotion:" + PromotionPolicyVersion,
	})
	if err != nil {
		return nil, err
	}
	return &GraphMemoryConsolidationPublishReport{
		Outcome:    GraphMemoryConsolidationPublishPublished,
		Generation: pub.Generation, CandidateVersion: candidate,
		AtomCount: len(coverage),
	}, nil
}

func containsPromotionSource(sources []GraphMemoryPublicationSource, key string) bool {
	for _, s := range sources {
		if s.Kind+":"+s.ID == key {
			return true
		}
	}
	return false
}
