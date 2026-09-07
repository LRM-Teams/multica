// SPDX-License-Identifier: Apache-2.0

package service

// Pattern → Graph projection (spec §12.4/§12.5, plan Slice 3.1). The
// graph node with NodeRole=pattern is the scope-safe, versioned projection
// of a canonical ledger PatternRecord; the ledger stays the authority.
// The projection body carries semantics only — never evidence counts,
// scores, or vote tallies — so a hidden or downgraded node cannot leak
// aggregates (spec §12.5 retraction test). Retracted sources hide the
// projection entirely.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/skillevolution"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// patternPlaneNamespace seeds the deterministic per-workspace owner UUID
// of the pattern plane graph. The plane is a dedicated project-scope
// graph (layout has no evolution kind; DirForScope requires a UUID owner)
// whose owner is derived from the workspace id, never a user.
var patternPlaneNamespace = uuid.NameSpaceURL

// PatternPlaneOwnerID derives the stable owner UUID of one workspace's
// pattern plane graph.
func PatternPlaneOwnerID(workspaceID string) (string, error) {
	if _, err := uuid.Parse(strings.TrimSpace(workspaceID)); err != nil {
		return "", fmt.Errorf("pattern plane owner: invalid workspace id %q", workspaceID)
	}
	return uuid.NewSHA1(patternPlaneNamespace, []byte("multica:skill-evolution-pattern-plane:"+workspaceID)).String(), nil
}

// PatternPlaneStore opens (creating on first use) the workspace's pattern
// plane graph store. Readers — SkillEvolutionRefResolver and the curator
// surface — bind to the same store.
func PatternPlaneStore(workspacesRoot, workspaceID string) (*memorygraph.Store, error) {
	ownerID, err := PatternPlaneOwnerID(workspaceID)
	if err != nil {
		return nil, err
	}
	dir, err := memorygraph.EnsureScopedDir(workspacesRoot, workspaceID, memorygraph.GraphDirKindProject, ownerID)
	if err != nil {
		return nil, err
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		return nil, err
	}
	return store, nil
}

// PatternProjectionRequest is one pattern revision to project. NodeID of
// the projection equals the pattern id: SkillEvolutionRef{Kind: pattern,
// ID: pattern_id} resolves to it through the evolution-plane resolver.
type PatternProjectionRequest struct {
	WorkspaceID string
	Pattern     skillevolution.PatternRecord
	// SourceTaskIDs are the durable run ids behind the pattern's evidence.
	// Retraction of any of them hides the projection.
	SourceTaskIDs []string
	// ConflictsWith are pattern ids this pattern records a conflicts_with
	// relation to (spec §12.5 merge conflicts). Both endpoints must be
	// pattern projections.
	ConflictsWith []string
}

func (r PatternProjectionRequest) Validate() error {
	if _, err := uuid.Parse(strings.TrimSpace(r.WorkspaceID)); err != nil {
		return fmt.Errorf("pattern projection: invalid workspace id")
	}
	if r.Pattern.WorkspaceID != "" && r.Pattern.WorkspaceID != r.WorkspaceID {
		return fmt.Errorf("pattern projection: pattern belongs to another workspace")
	}
	if err := r.Pattern.Validate(); err != nil {
		return err
	}
	for _, taskID := range r.SourceTaskIDs {
		if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
			return fmt.Errorf("pattern projection: source task id %q is not a uuid", taskID)
		}
	}
	return nil
}

// SkillPatternProjectionService projects pattern ledger revisions into
// the pattern plane graph under the graph mutation lock. Projection is
// idempotent per (pattern id, revision): re-projecting the same revision
// produces the same node body and edges.
type SkillPatternProjectionService struct {
	pool      *pgxpool.Pool
	mutations *GraphMutationCoordinator
	gates     SkillEvolutionFeatureGates
	now       func() time.Time
}

func NewSkillPatternProjectionService(pool *pgxpool.Pool, mutations *GraphMutationCoordinator, gates SkillEvolutionFeatureGates) *SkillPatternProjectionService {
	return &SkillPatternProjectionService{
		pool: pool, mutations: mutations, gates: gates, now: time.Now,
	}
}

// ProjectPattern writes one pattern revision as a NodeRole=pattern node
// (plus conflicts_with edges) into the workspace pattern plane. If any
// source task is retracted, the node is written HIDDEN: ValidTo is set,
// the body is replaced by a neutral placeholder, and no semantic content
// or aggregates survive.
func (s *SkillPatternProjectionService) ProjectPattern(ctx context.Context, req PatternProjectionRequest) error {
	if s == nil || s.pool == nil {
		return errors.New("pattern projection service not configured")
	}
	if !s.gates.PatternConsolidation {
		return errors.New("pattern projection is disabled by feature gates")
	}
	if err := req.Validate(); err != nil {
		return err
	}
	workspaceUUID, err := utilParseUUID("workspace", req.WorkspaceID)
	if err != nil {
		return err
	}

	hidden := false
	if len(req.SourceTaskIDs) > 0 {
		gate := NewMemoryReadGate(db.New(s.pool))
		refs := make([]MemorySourceRef, 0, len(req.SourceTaskIDs))
		for _, taskID := range req.SourceTaskIDs {
			id, err := utilParseUUID("source task", taskID)
			if err != nil {
				return err
			}
			refs = append(refs, MemorySourceRef{
				WorkspaceID: workspaceUUID, Kind: MemorySourceTaskOutput, ID: id,
			})
		}
		if err := gate.AuthorizeResolve(ctx, workspaceUUID, refs); err != nil {
			if !errors.Is(err, ErrMemorySourceRetracted) {
				return fmt.Errorf("pattern projection: source gate: %w", err)
			}
			hidden = true
		}
	}

	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return err
	}
	store, err := PatternPlaneStore(root, req.WorkspaceID)
	if err != nil {
		return fmt.Errorf("pattern projection: plane store: %w", err)
	}
	ownerID, err := PatternPlaneOwnerID(req.WorkspaceID)
	if err != nil {
		return err
	}

	return s.mutations.WithGraphLock(ctx, req.WorkspaceID, string(memorygraph.GraphDirKindProject), ownerID, func(ctx context.Context) error {
		base, err := store.CurrentVersion()
		if err != nil {
			return err
		}
		candidate, err := store.CreateVersionFrom(base, memorygraph.CreatorConsolidator)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		node := patternProjectionNode(req, hidden, now, candidate)
		if err := store.SaveNode(candidate, node); err != nil {
			return err
		}
		if !hidden {
			hier, rel, err := loadPatternPlaneEdges(store, base)
			if err != nil {
				return err
			}
			rel = upsertConflictEdges(rel, req.Pattern.PatternID, req.ConflictsWith, candidate, now)
			if err := store.SaveEdges(candidate, hier, rel); err != nil {
				return err
			}
		}
		return store.SwitchCurrent(candidate)
	})
}

// patternProjectionNode maps a ledger revision to its graph projection.
// The body is the ONLY content surface and it is aggregate-free by
// construction: no evidence counts, no scores, no vote tallies — hidden
// or downgraded nodes must not leak them.
func patternProjectionNode(req PatternProjectionRequest, hidden bool, now time.Time, version int) *memorygraph.Node {
	if hidden {
		validTo := now
		return &memorygraph.Node{
			NodeID:         req.Pattern.PatternID,
			Role:           memorygraph.NodeRolePattern,
			Visibility:     "project",
			Body:           "hidden: source retracted",
			Epistemic:      "asserted",
			ObservedAt:     now,
			ValidFrom:      &now,
			ValidTo:        &validTo,
			TemporalStatus: "closed",
			Tags:           []string{"pattern", "hidden"},
			CreatedBy:      memorygraph.CreatorConsolidator,
			CreatedVersion: version,
			UpdatedVersion: version,
		}
	}
	body := strings.Join([]string{
		"kind: " + string(req.Pattern.PatternKind),
		"status: " + string(req.Pattern.Status),
		"problem: " + req.Pattern.Problem,
		"applicability: " + req.Pattern.Applicability,
		"root_cause: " + req.Pattern.RootCauseSummary,
		"recommended_action: " + req.Pattern.RecommendedAction,
		fmt.Sprintf("revision: %d", req.Pattern.Revision),
	}, "\n")
	return &memorygraph.Node{
		NodeID:         req.Pattern.PatternID,
		Role:           memorygraph.NodeRolePattern,
		Visibility:     "project",
		Body:           body,
		Epistemic:      "asserted",
		ObservedAt:     now,
		TemporalStatus: "ongoing",
		Tags:           []string{"pattern", string(req.Pattern.PatternKind), string(req.Pattern.Status)},
		CreatedBy:      memorygraph.CreatorConsolidator,
		CreatedVersion: version,
		UpdatedVersion: version,
		PolicyVersion:  req.Pattern.PolicyVersion,
		SourceTaskIDs:  req.SourceTaskIDs,
	}
}

func loadPatternPlaneEdges(store *memorygraph.Store, version int) (hier, rel []*memorygraph.Edge, err error) {
	return store.LoadEdges(version)
}

// upsertConflictEdges adds conflicts_with edges between one pattern and
// its conflict partners, deduplicating by deterministic edge id. Edges are
// directed from the surviving pattern to each partner.
func upsertConflictEdges(rel []*memorygraph.Edge, patternID string, partners []string, version int, now time.Time) []*memorygraph.Edge {
	sorted := append([]string(nil), partners...)
	sort.Strings(sorted)
	wanted := map[string]bool{}
	for _, partner := range sorted {
		if partner == "" || partner == patternID {
			continue
		}
		id := "pattern-conflict:" + minID(patternID, partner) + ":" + maxID(patternID, partner)
		wanted[id] = true
		if !containsEdgeID(rel, id) {
			rel = append(rel, &memorygraph.Edge{
				EdgeID:         id,
				Type:           memorygraph.EdgeTypeConflictsWith,
				From:           patternID,
				To:             partner,
				Epistemic:      "asserted",
				CreatedBy:      memorygraph.CreatorConsolidator,
				CreatedVersion: version,
				Reason:         "pattern merge conflict: no overwrite (spec 12.5)",
			})
		}
	}
	// Reclaim this pattern's own pair space: conflict edges involving
	// this pattern that the revision no longer claims are dropped; pairs
	// between other patterns are untouched (their owners' projections
	// manage them, and a partner that still claims the pair re-adds it
	// on its next projection).
	kept := rel[:0]
	for _, edge := range rel {
		if edge.Type == memorygraph.EdgeTypeConflictsWith &&
			(edge.From == patternID || edge.To == patternID) &&
			!wanted[edge.EdgeID] {
			continue
		}
		kept = append(kept, edge)
	}
	return kept
}

func containsEdgeID(edges []*memorygraph.Edge, id string) bool {
	for _, edge := range edges {
		if edge.EdgeID == id {
			return true
		}
	}
	return false
}

func minID(a, b string) string {
	if a < b {
		return a
	}
	return b
}

func maxID(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func utilParseUUID(field, value string) (pgtype.UUID, error) {
	parsed, err := util.ParseUUID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("pattern projection: %s: %w", field, err)
	}
	return parsed, nil
}

// SkillPatternAggregateKind is the outbox aggregate this dispatcher
// consumes; pattern revision events are emitted by the consolidation
// pipeline alongside the ledger append.
const SkillPatternAggregateKind = "skill_pattern"

// SkillEvolutionOutboxDispatcher drains the evolution outbox: it
// re-materializes each pattern aggregate's LATEST revision into the
// pattern plane and marks the event dispatched. Dispatch is at-least-once
// and the projection is idempotent, so redelivery is safe. Unknown
// aggregate kinds stay pending for their future handlers instead of being
// dead-lettered or dropped.
type SkillEvolutionOutboxDispatcher struct {
	pool       *pgxpool.Pool
	ledger     *PostgresSkillEvolutionLedger
	projection *SkillPatternProjectionService
	now        func() time.Time
}

func NewSkillEvolutionOutboxDispatcher(
	pool *pgxpool.Pool, ledger *PostgresSkillEvolutionLedger, projection *SkillPatternProjectionService,
) *SkillEvolutionOutboxDispatcher {
	return &SkillEvolutionOutboxDispatcher{pool: pool, ledger: ledger, projection: projection, now: time.Now}
}

// DispatchClaim projects up to limit pending events for one workspace and
// returns the number of events it fully handled (unknown kinds are
// neither handled nor failed).
func (d *SkillEvolutionOutboxDispatcher) DispatchClaim(ctx context.Context, workspaceID string, limit int) (int, error) {
	if d == nil || d.pool == nil || d.ledger == nil || d.projection == nil {
		return 0, errors.New("skill evolution outbox dispatcher not configured")
	}
	if limit <= 0 {
		limit = 32
	}
	events, err := d.ledger.ListPendingOutboxEvents(ctx, workspaceID, limit)
	if err != nil {
		return 0, fmt.Errorf("skill evolution outbox: list: %w", err)
	}
	handled := 0
	for _, event := range events {
		if event.AggregateKind != SkillPatternAggregateKind {
			continue
		}
		if err := d.projectPatternEvent(ctx, workspaceID, event); err != nil {
			if noteErr := d.ledger.NoteOutboxEventFailure(ctx, workspaceID, event.ID, err.Error()); noteErr != nil {
				return handled, fmt.Errorf("skill evolution outbox: note failure: %v (cause: %w)", noteErr, err)
			}
			return handled, fmt.Errorf("skill evolution outbox: pattern %s: %w", event.AggregateID, err)
		}
		ok, err := d.ledger.MarkOutboxEventDispatched(ctx, workspaceID, event.ID)
		if err != nil {
			return handled, fmt.Errorf("skill evolution outbox: mark dispatched: %w", err)
		}
		if !ok {
			continue // a concurrent dispatcher claimed it; its projection is equivalent
		}
		handled++
	}
	return handled, nil
}

// projectPatternEvent re-projects the latest ledger revision of one
// pattern. Dispatching the LATEST revision (not the event's payload)
// keeps redelivery convergent: stale queued events cannot resurrect an
// older projection over a newer one.
func (d *SkillEvolutionOutboxDispatcher) projectPatternEvent(ctx context.Context, workspaceID string, event skillevolution.OutboxEvent) error {
	record, err := d.ledger.LatestPatternRevision(ctx, workspaceID, event.AggregateID)
	if err != nil {
		if errors.Is(err, skillevolution.ErrLedgerNotFound) {
			// The pattern was merged away or never committed; nothing to
			// project. Dispatching is correct: the aggregate no longer
			// exists as a live pattern.
			return nil
		}
		return err
	}
	evidence, err := d.ledger.ListPatternEvidence(ctx, workspaceID, event.AggregateID, record.Revision)
	if err != nil {
		return err
	}
	sourceTasks := patternEvidenceSourceTasks(evidence)
	conflicts := patternEvidenceConflicts(evidence)
	return d.projection.ProjectPattern(ctx, PatternProjectionRequest{
		WorkspaceID:   workspaceID,
		Pattern:       record,
		SourceTaskIDs: sourceTasks,
		ConflictsWith: conflicts,
	})
}

// patternEvidenceSourceTasks maps evaluation-run evidence refs to their
// durable run ids (the projection's retraction surface).
func patternEvidenceSourceTasks(evidence []skillevolution.SkillEvolutionRef) []string {
	tasks := make([]string, 0, len(evidence))
	seen := map[string]bool{}
	for _, ref := range evidence {
		if ref.Kind != skillevolution.RefEvaluationRun || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		tasks = append(tasks, ref.ID)
	}
	sort.Strings(tasks)
	return tasks
}

// patternEvidenceConflicts extracts recorded conflicts_with pattern ids
// from pattern-typed evidence refs.
func patternEvidenceConflicts(evidence []skillevolution.SkillEvolutionRef) []string {
	conflicts := make([]string, 0, len(evidence))
	seen := map[string]bool{}
	for _, ref := range evidence {
		if ref.Kind != skillevolution.RefPattern || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		conflicts = append(conflicts, ref.ID)
	}
	sort.Strings(conflicts)
	return conflicts
}
