// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// undefinedTable reports whether err is PostgreSQL 42P01: the redirect
// tables of a later migration are absent (pre-migration schema), so no
// redirect can exist and the caller may safely skip the lookup.
func undefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// Canonical memory source kinds (migration 467 closed set).
const (
	MemorySourceTaskOutput     = "task_output"
	MemorySourceComment        = "comment"
	MemorySourceChannelMessage = "channel_message"
	MemorySourceChannel        = "channel"
	MemorySourceChatSession    = "chat_session"
	MemorySourceAttachment     = "attachment"
	MemorySourceIssue          = "issue"
	MemorySourceProject        = "project"
	MemorySourceWorkspace      = "workspace"
	MemorySourceEnvDispatch    = "env_dispatch"
	MemorySourceMemoryRun      = "memory_agent_run"
)

// ErrMemorySourceRetracted is the fail-closed read-gate sentinel: the request
// cited a fenced source, so no body may be resolved for it (spec §9).
var ErrMemorySourceRetracted = errors.New("memory source retracted")

// MemorySourceRef names one canonical source of memory evidence.
type MemorySourceRef struct {
	WorkspaceID pgtype.UUID
	Kind        string
	ID          pgtype.UUID
}

// SourceKey returns the composite guard key "kind:id".
func (r MemorySourceRef) SourceKey() string {
	return r.Kind + ":" + r.ID.String()
}

// MemoryRetractionService fences canonical sources synchronously inside the
// caller's business transaction (Task 8A, spec §9): tombstone/delete, fence
// rows, and the quarantined reverse-provenance closure all commit together or
// all roll back. No HTTP success is returned if fencing fails.
type MemoryRetractionService struct{}

// NewMemoryRetractionService constructs the fence writer.
func NewMemoryRetractionService() *MemoryRetractionService {
	return &MemoryRetractionService{}
}

// RetractSourcesTx fences sources in deterministic sorted key order:
// guard rows are locked FOR UPDATE in the same order by every caller, so
// concurrent retractions of overlapping sets cannot deadlock. For each
// fenced source it records the retraction event, quarantines the complete
// currently published reverse-provenance closure, and audits the deletion —
// all on the caller's transaction, so a rollback undoes the fence too.
func (s *MemoryRetractionService) RetractSourcesTx(
	ctx context.Context, tx pgx.Tx, sources []MemorySourceRef, actor, reason string,
) error {
	if s == nil {
		return errors.New("memory retraction service not configured")
	}
	if len(sources) == 0 {
		return nil
	}
	// Deterministic order + workspace consistency: mixed-workspace input is a
	// programming error, not a fence event.
	sorted := append([]MemorySourceRef(nil), sources...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SourceKey() < sorted[j].SourceKey() })
	workspaceID := sorted[0].WorkspaceID
	keys := make([]string, 0, len(sorted))
	for _, ref := range sorted {
		if !ref.WorkspaceID.Valid || ref.WorkspaceID != workspaceID {
			return fmt.Errorf("memory retraction sources span workspaces (%s)", ref.SourceKey())
		}
		if ref.Kind == "" || !ref.ID.Valid {
			return fmt.Errorf("memory retraction source ref incomplete (%s)", ref.SourceKey())
		}
		keys = append(keys, ref.SourceKey())
	}
	return s.RetractSourceKeysTx(ctx, tx, workspaceID, keys, actor, reason)
}

// RetractSourceKeysTx is the key-based core of the fence: register, lock
// FOR UPDATE in sorted order, record the event, fence, quarantine the full
// provenance closure, and audit — one atomic unit on the caller's tx.
func (s *MemoryRetractionService) RetractSourceKeysTx(
	ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID, keys []string, actor, reason string,
) error {
	if s == nil {
		return errors.New("memory retraction service not configured")
	}
	if tx == nil {
		return errors.New("memory retraction requires the caller's transaction")
	}
	if len(keys) == 0 {
		return nil
	}
	if actor == "" {
		return errors.New("memory retraction requires an actor")
	}
	if !workspaceID.Valid {
		return errors.New("memory retraction requires a workspace")
	}
	sort.Strings(keys)
	qtx := db.New(tx)

	// Register the sources first: a business delete of a source that never
	// published anything (no guard row yet) still fences it.
	if err := qtx.UpsertMemorySourceGuards(ctx, db.UpsertMemorySourceGuardsParams{
		WorkspaceID: workspaceID, SourceKeys: keys,
	}); err != nil {
		return fmt.Errorf("register memory source guards: %w", err)
	}
	// Lock the guard rows in the same sorted order every retraction uses.
	locked, err := qtx.LockMemorySourceGuardsForUpdate(ctx, db.LockMemorySourceGuardsForUpdateParams{
		WorkspaceID: workspaceID, SourceKeys: keys,
	})
	if err != nil {
		return fmt.Errorf("lock memory source guards: %w", err)
	}
	if len(locked) != len(keys) {
		return fmt.Errorf("memory source guards vanished mid-retraction (%d/%d locked)",
			len(locked), len(keys))
	}

	event, err := qtx.InsertRetractionRegistry(ctx, db.InsertRetractionRegistryParams{
		WorkspaceID: workspaceID, Actor: actor, Reason: reason, SourceCount: int32(len(keys)),
	})
	if err != nil {
		return fmt.Errorf("record retraction event: %w", err)
	}
	if _, err := qtx.FenceMemorySourceGuard(ctx, db.FenceMemorySourceGuardParams{
		WorkspaceID: workspaceID, RetractedBy: actor, Reason: reason, SourceKeys: keys,
	}); err != nil {
		return fmt.Errorf("fence memory source guards: %w", err)
	}

	// Quarantine the complete published reverse-provenance closure.
	consumers, err := qtx.ProvenanceConsumersForSources(ctx, db.ProvenanceConsumersForSourcesParams{
		WorkspaceID: workspaceID, SourceKeys: keys,
	})
	if err != nil {
		return fmt.Errorf("resolve provenance closure: %w", err)
	}
	consumerKeys := make([]string, 0, len(consumers))
	retractedAtoms := make([]string, 0, len(consumers))
	for _, consumer := range consumers {
		consumerKeys = append(consumerKeys, consumer.ConsumerKind+":"+consumer.ConsumerID)
		if consumer.ConsumerKind == "graph_memory_atom" {
			retractedAtoms = append(retractedAtoms, consumer.ConsumerID)
		}
	}
	// Task 16: deletion resolves BOTH canonical refs of a migrated atom.
	// A source published before a channel migration leaves atoms under the
	// old id and a copy under the new one; the redirect closure fences them
	// together whichever id the provenance walk entered through. The lookup
	// runs in a savepoint: pre-migration schemas have no redirect table, and
	// the savepoint rollback keeps the enclosing retraction transaction
	// usable instead of poisoning it with 25P02.
	if len(retractedAtoms) > 0 {
		if sp, spErr := tx.Begin(ctx); spErr == nil {
			qsp := db.New(sp)
			closureErr := func() error {
				extra, err := qsp.ListGraphMemoryMigrationRedirectsByNewID(ctx, db.ListGraphMemoryMigrationRedirectsByNewIDParams{
					WorkspaceID: workspaceID, NewKind: "atom", NewIds: retractedAtoms,
				})
				if err != nil {
					return err
				}
				for _, redirect := range extra {
					consumerKeys = append(consumerKeys, "graph_memory_atom:"+redirect.OldID)
					retractedAtoms = append(retractedAtoms, redirect.OldID)
				}
				forward, err := qsp.ListGraphMemoryMigrationRedirectsByOldID(ctx, db.ListGraphMemoryMigrationRedirectsByOldIDParams{
					WorkspaceID: workspaceID, OldKind: "atom", OldIds: retractedAtoms,
				})
				if err != nil {
					return err
				}
				for _, redirect := range forward {
					consumerKeys = append(consumerKeys, "graph_memory_atom:"+redirect.NewID)
				}
				return nil
			}()
			if closureErr == nil {
				_ = sp.Commit(ctx)
			} else {
				_ = sp.Rollback(ctx)
				if !undefinedTable(closureErr) {
					return fmt.Errorf("redirect closure: %w", closureErr)
				}
			}
		}
	}
	// Task 14: a deletion that lands after a publication committed finds the
	// newly published nodes through the coverage/provenance ledgers and
	// quarantines them in the same atomic unit — the deleted body is never
	// visible through the new generation.
	if len(retractedAtoms) > 0 {
		nodes, err := qtx.PublishedNodesCoveringAtoms(ctx, db.PublishedNodesCoveringAtomsParams{
			WorkspaceID: workspaceID, AtomIds: retractedAtoms,
		})
		if err != nil {
			return fmt.Errorf("resolve published node closure: %w", err)
		}
		for _, node := range nodes {
			consumerKeys = append(consumerKeys, "graph_node:"+node.NodeID)
		}
	}
	if len(consumerKeys) > 0 {
		if _, err := qtx.InsertQuarantinedPendingRecompute(ctx, db.InsertQuarantinedPendingRecomputeParams{
			WorkspaceID: workspaceID, RetractionID: event.ID, ConsumerKeys: consumerKeys,
		}); err != nil {
			return fmt.Errorf("quarantine provenance closure: %w", err)
		}
	}
	if _, err := qtx.InsertMemoryDeletionAudit(ctx, db.InsertMemoryDeletionAuditParams{
		WorkspaceID: workspaceID, RetractionID: event.ID,
		SourceKeys: keys, QuarantinedCount: int32(len(consumerKeys)),
	}); err != nil {
		return fmt.Errorf("audit memory deletion: %w", err)
	}
	return nil
}

// RetractIssueSourcesTx fences the complete canonical-source closure of one
// issue delete: the issue itself, its task outputs, its comments, and its
// attachments (issue-level and comment-level), resolved set-based on the
// caller's transaction so the FK cascade and the fence commit together.
func (s *MemoryRetractionService) RetractIssueSourcesTx(
	ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID, issueID pgtype.UUID, actor, reason string,
) error {
	if s == nil {
		return errors.New("memory retraction service not configured")
	}
	if tx == nil {
		return errors.New("memory retraction requires the caller's transaction")
	}
	if !workspaceID.Valid || !issueID.Valid {
		return errors.New("issue retraction requires workspace and issue ids")
	}
	keys, err := db.New(tx).ListUniversalDAGIssueSourceKeys(ctx, db.ListUniversalDAGIssueSourceKeysParams{
		WorkspaceID: workspaceID, IssueID: issueID,
	})
	if err != nil {
		return fmt.Errorf("resolve issue source closure: %w", err)
	}
	return s.RetractSourceKeysTx(ctx, tx, workspaceID, keys, actor, reason)
}

// RetractWorkspaceSourcesTx is the set-based workspace bulk fence (Task 8A
// Step 3): every guard row of the workspace is fenced, the complete
// provenance closure is quarantined, and one aggregate audit row is written —
// all inside the caller's workspace-delete transaction. Idempotent sources
// already retracted keep their original attribution.
func (s *MemoryRetractionService) RetractWorkspaceSourcesTx(
	ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID, actor, reason string,
) error {
	if s == nil {
		return errors.New("memory retraction service not configured")
	}
	if tx == nil {
		return errors.New("memory retraction requires the caller's transaction")
	}
	if !workspaceID.Valid {
		return errors.New("workspace retraction requires a workspace id")
	}
	if actor == "" {
		return errors.New("memory retraction requires an actor")
	}
	qtx := db.New(tx)
	if err := qtx.RegisterWorkspaceMemorySourceGuard(ctx, db.RegisterWorkspaceMemorySourceGuardParams{
		WorkspaceID: workspaceID, WorkspaceIDText: workspaceID.String(),
	}); err != nil {
		return fmt.Errorf("register workspace memory source guard: %w", err)
	}
	fenced, err := qtx.FenceWorkspaceMemorySourceGuards(ctx, db.FenceWorkspaceMemorySourceGuardsParams{
		WorkspaceID: workspaceID, RetractedBy: actor, Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("fence workspace memory sources: %w", err)
	}
	if fenced == 0 {
		return nil // every source was already retracted: nothing new to audit
	}
	event, err := qtx.InsertRetractionRegistry(ctx, db.InsertRetractionRegistryParams{
		WorkspaceID: workspaceID, Actor: actor, Reason: reason, SourceCount: int32(fenced),
	})
	if err != nil {
		return fmt.Errorf("record retraction event: %w", err)
	}
	quarantined, err := qtx.QuarantineWorkspaceProvenance(ctx, db.QuarantineWorkspaceProvenanceParams{
		WorkspaceID: workspaceID, RetractionID: event.ID,
	})
	if err != nil {
		return fmt.Errorf("quarantine provenance closure: %w", err)
	}
	if err := qtx.InsertWorkspaceDeletionAudit(ctx, db.InsertWorkspaceDeletionAuditParams{
		WorkspaceID: workspaceID, RetractionID: event.ID,
		WorkspaceIDText: workspaceID.String(), QuarantinedCount: int32(quarantined),
	}); err != nil {
		return fmt.Errorf("audit memory deletion: %w", err)
	}
	return nil
}

// MemoryReadGate is the fail-closed fence every memory reader consults before
// resolving a body (Task 8A Step 4): DB rows, graph files, blobs, archives,
// citations, Search, Explore, offline export, and Mixed-RL trajectories. A
// retracted source returns ErrMemorySourceRetracted — the caller surfaces
// content_retracted and never stores or forwards a provider payload for it.
type MemoryReadGate struct {
	queries *db.Queries
}

// NewMemoryReadGate constructs the gate over the pool-backed queries.
func NewMemoryReadGate(queries *db.Queries) *MemoryReadGate {
	return &MemoryReadGate{queries: queries}
}

// AuthorizeResolve fails closed when any cited source is fenced.
func (g *MemoryReadGate) AuthorizeResolve(ctx context.Context, workspaceID pgtype.UUID, refs []MemorySourceRef) error {
	if g == nil || g.queries == nil {
		// An unwired gate cannot prove safety: fail closed.
		return fmt.Errorf("%w: read gate not configured", ErrMemorySourceRetracted)
	}
	if len(refs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(refs))
	for _, ref := range refs {
		if !ref.WorkspaceID.Valid {
			continue
		}
		keys = append(keys, ref.Kind+":"+ref.ID.String())
	}
	if len(keys) == 0 {
		return nil
	}
	retracted, err := g.queries.RetractedMemorySources(ctx, db.RetractedMemorySourcesParams{
		WorkspaceID: workspaceID, SourceKeys: keys,
	})
	if err != nil {
		return fmt.Errorf("read gate check: %w", err)
	}
	if len(retracted) > 0 {
		return fmt.Errorf("%w: %s:%s", ErrMemorySourceRetracted, retracted[0].SourceKind, retracted[0].SourceID)
	}
	return nil
}

// MemoryReadRoute names an externally reachable memory route (Task 8A
// Step 5): all are DB-default disabled until the phase gate turns green.
type MemoryReadRoute string

const (
	MemoryRouteAtoms     MemoryReadRoute = "atoms"
	MemoryRouteSearchV2  MemoryReadRoute = "search_v2"
	MemoryRouteExplore   MemoryReadRoute = "explore"
	MemoryRouteCitations MemoryReadRoute = "citations"
	// MemoryRouteAtomConsolidation gates the Task 14 publication path: the
	// scheduler and the manual consolidation service cannot claim work (and
	// PublishGraphMemoryPublication refuses) while it is red.
	MemoryRouteAtomConsolidation MemoryReadRoute = "atom_consolidation"
)

// RouteEnabled reports whether one external memory route is reachable. A
// missing gate row means disabled — the DB default.
func (g *MemoryReadGate) RouteEnabled(ctx context.Context, workspaceID pgtype.UUID, route MemoryReadRoute) (bool, error) {
	if g == nil || g.queries == nil {
		return false, fmt.Errorf("read gate not configured")
	}
	gate, err := g.queries.GetMemoryReadPhaseGate(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read phase gate: %w", err)
	}
	switch route {
	case MemoryRouteAtoms:
		return gate.AtomsEnabled, nil
	case MemoryRouteSearchV2:
		return gate.SearchV2Enabled, nil
	case MemoryRouteExplore:
		return gate.ExploreEnabled, nil
	case MemoryRouteCitations:
		return gate.CitationsEnabled, nil
	case MemoryRouteAtomConsolidation:
		return gate.AtomConsolidationEnabled, nil
	default:
		return false, fmt.Errorf("unknown memory read route %q", route)
	}
}

// RequireRouteEnabled is the fail-closed variant used by external entry
// points: the route is unreachable unless the gate row is green.
func (g *MemoryReadGate) RequireRouteEnabled(ctx context.Context, workspaceID pgtype.UUID, route MemoryReadRoute) error {
	enabled, err := g.RouteEnabled(ctx, workspaceID, route)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("memory route %q is disabled by the workspace phase gate", route)
	}
	return nil
}

// AuthorizeRunSources is the shared run-scoped read gate (Task 8A Step 4):
// every canonical task_output source that fed the run must be unfenced
// before any frozen snapshot or offline trajectory body resolves.
func AuthorizeRunSources(ctx context.Context, queries *db.Queries, workspaceID pgtype.UUID, runID pgtype.UUID) error {
	if queries == nil {
		return fmt.Errorf("%w: read gate not configured", ErrMemorySourceRetracted)
	}
	present, err := queries.UniversalDAGSegmentTablePresent(ctx)
	if err != nil {
		return fmt.Errorf("read gate probe: %w", err)
	}
	if !present {
		// The canonical segment table is not on this search path: the schema
		// predates migration 454 and has no published sources to fence.
		return nil
	}
	sources, err := queries.ListUniversalDAGTaskSourcesForRun(ctx, db.ListUniversalDAGTaskSourcesForRunParams{
		WorkspaceID: workspaceID, RunID: runID,
	})
	if err != nil {
		return fmt.Errorf("read gate run sources: %w", err)
	}
	if len(sources) == 0 {
		return nil
	}
	refs := make([]MemorySourceRef, 0, len(sources))
	for _, taskID := range sources {
		refs = append(refs, MemorySourceRef{WorkspaceID: workspaceID, Kind: MemorySourceTaskOutput, ID: taskID})
	}
	return NewMemoryReadGate(queries).AuthorizeResolve(ctx, workspaceID, refs)
}

// MemorySourceRefForTask builds the canonical task_output ref of one agent
// run (segments publish under the task's id).
func MemorySourceRefForTask(workspaceID pgtype.UUID, taskID pgtype.UUID) MemorySourceRef {
	return MemorySourceRef{WorkspaceID: workspaceID, Kind: MemorySourceTaskOutput, ID: taskID}
}
