// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ErrGraphProjectionRoute marks deterministic route-identity failures: the
// event-time lineage of a channel segment cannot be resolved, so the request
// dead-letters instead of guessing a write target. It never consumes a retry.
var ErrGraphProjectionRoute = errors.New("graph memory projection route failure")

const (
	// graphMemoryProjectionLeaseTTL bounds one projection claim; expired
	// leases are reclaimed by the next claim without consuming an attempt.
	graphMemoryProjectionLeaseTTL = 5 * time.Minute
	// graphMemoryProjectionMaxAttempts caps transient retries before the
	// request dead-letters.
	graphMemoryProjectionMaxAttempts = 10
	// graphMemoryProjectionBackoffBase is the first retry interval; each
	// further retry doubles it.
	graphMemoryProjectionBackoffBase = time.Minute
	// graphMemoryProjectionLastErrorCap keeps last_error bounded and
	// content-free (classes and ids only, never atom bodies).
	graphMemoryProjectionLastErrorCap = 500
	// graphMemoryProjectionSource identifies projection staging documents
	// built from published atoms (the Task 7 contract).
	graphMemoryProjectionSource = "universal_dag_atoms"
)

// GraphMemoryProjector drains graph_memory_projection_outbox (Task 8). The
// outbox is the ONLY work-discovery surface: the projector never scans
// segments, atoms, or graph directories for work, and it runs on the
// scheduler — no detached goroutine. Every decision uses the event-time
// facts frozen on the segment and the route generation frozen on the
// request; current workspace/channel state is never consulted, so a
// Legacy→Graph switch can neither resurrect legacy segments nor retract
// graph-frozen ones.
type GraphMemoryProjector struct {
	pool         *pgxpool.Pool
	root         string
	workerID     string
	leaseTTL     time.Duration
	maxAttempts  int
	backoffBase  time.Duration
	publishClock func() time.Time
}

// GraphMemoryProjectorOption customizes projector construction.
type GraphMemoryProjectorOption func(*GraphMemoryProjector)

// WithGraphMemoryProjectionRoot pins the workspaces root (tests).
func WithGraphMemoryProjectionRoot(root string) GraphMemoryProjectorOption {
	return func(p *GraphMemoryProjector) { p.root = root }
}

// WithGraphMemoryProjectionWorkerID pins the lease owner identity.
func WithGraphMemoryProjectionWorkerID(workerID string) GraphMemoryProjectorOption {
	return func(p *GraphMemoryProjector) {
		if workerID != "" {
			p.workerID = workerID
		}
	}
}

// WithGraphMemoryProjectionClock overrides the scheduling clock (tests).
func WithGraphMemoryProjectionClock(clock func() time.Time) GraphMemoryProjectorOption {
	return func(p *GraphMemoryProjector) {
		if clock != nil {
			p.publishClock = clock
		}
	}
}

// WithGraphMemoryProjectionBackoff overrides the retry backoff base (tests).
func WithGraphMemoryProjectionBackoff(base time.Duration) GraphMemoryProjectorOption {
	return func(p *GraphMemoryProjector) {
		if base > 0 {
			p.backoffBase = base
		}
	}
}

// WithGraphMemoryProjectionMaxAttempts overrides the attempt cap (tests).
func WithGraphMemoryProjectionMaxAttempts(max int) GraphMemoryProjectorOption {
	return func(p *GraphMemoryProjector) {
		if max > 0 {
			p.maxAttempts = max
		}
	}
}

func defaultGraphMemoryProjectionWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("graph-projection-%s-%d", host, os.Getpid())
}

// NewGraphMemoryProjector constructs the projector over the Task 7 outbox.
func NewGraphMemoryProjector(pool *pgxpool.Pool, options ...GraphMemoryProjectorOption) *GraphMemoryProjector {
	p := &GraphMemoryProjector{
		pool:         pool,
		workerID:     defaultGraphMemoryProjectionWorkerID(),
		leaseTTL:     graphMemoryProjectionLeaseTTL,
		maxAttempts:  graphMemoryProjectionMaxAttempts,
		backoffBase:  graphMemoryProjectionBackoffBase,
		publishClock: time.Now,
	}
	for _, option := range options {
		option(p)
	}
	return p
}

// graphMemoryProjectionClaim is one leased projection request.
type graphMemoryProjectionClaim struct {
	WorkspaceID     string
	SegmentID       string
	Attempts        int32
	RouteGeneration int64
	RequestHash     string
}

// graphMemoryProjectionAtom is the durable per-atom projection record inside
// the staging document. It carries only atom contract fields — never raw
// message bodies (the atom body is the sanitized extract).
type graphMemoryProjectionAtom struct {
	AtomID            string  `json:"atom_id"`
	Body              string  `json:"body"`
	Kind              string  `json:"kind"`
	SourceTool        string  `json:"source_tool,omitempty"`
	ToolTrustClass    string  `json:"tool_trust_class"`
	SourceMessageSeqs []int32 `json:"source_message_seqs"`
	ArtifactRef       string  `json:"artifact_ref,omitempty"`
}

// graphMemoryProjectionDocument is the staging content one projection writes.
type graphMemoryProjectionDocument struct {
	SegmentID  string                      `json:"segment_id"`
	AgentRunID string                      `json:"agent_run_id,omitempty"`
	PublishSeq int64                       `json:"publish_seq"`
	Source     string                      `json:"source"`
	Atoms      []graphMemoryProjectionAtom `json:"atoms"`
}

// ProjectClaim leases and drives at most limit projection requests to their
// next state. It returns how many rows were claimed; rows that end in retry
// or dead_letter still count.
func (p *GraphMemoryProjector) ProjectClaim(ctx context.Context, limit int) (int, error) {
	if p == nil || p.pool == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 1
	}
	now := p.publishClock().UTC()
	claims, err := p.claim(ctx, int32(limit), now)
	if err != nil {
		return 0, err
	}
	for _, claim := range claims {
		if err := p.process(ctx, claim, now); err != nil {
			return len(claims), err
		}
	}
	return len(claims), nil
}

func (p *GraphMemoryProjector) claim(ctx context.Context, limit int32, now time.Time) ([]graphMemoryProjectionClaim, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin projection claim: %w", err)
	}
	defer tx.Rollback(ctx)
	rows, err := db.New(tx).ClaimGraphMemoryProjectionOutbox(ctx, db.ClaimGraphMemoryProjectionOutboxParams{
		MaxRows:        limit,
		LeaseOwner:     pgtype.Text{String: p.workerID, Valid: true},
		LeaseExpiresAt: pgtype.Timestamptz{Time: now.Add(p.leaseTTL), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("claim projection outbox: %w", err)
	}
	claims := make([]graphMemoryProjectionClaim, 0, len(rows))
	for _, row := range rows {
		claims = append(claims, graphMemoryProjectionClaim{
			WorkspaceID:     row.WorkspaceID.String(),
			SegmentID:       row.SegmentID,
			Attempts:        row.Attempts,
			RouteGeneration: row.RouteGeneration.Int64,
			RequestHash:     row.RequestHash,
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit projection claim: %w", err)
	}
	return claims, nil
}

// process drives one claim to completion. The outbox row stays locked for the
// whole projection so concurrent projectors cannot duplicate writes; the
// graph write itself is idempotent (immutable staging) for crash recovery.
func (p *GraphMemoryProjector) process(ctx context.Context, claim graphMemoryProjectionClaim, now time.Time) error {
	workspaceID, err := util.ParseUUID(claim.WorkspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace %s: %w", claim.WorkspaceID, err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin projection outcome %s: %w", claim.SegmentID, err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)
	row, err := qtx.GetGraphMemoryProjectionForUpdate(ctx, db.GetGraphMemoryProjectionForUpdateParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lock projection row %s: %w", claim.SegmentID, err)
	}
	if !p.ownsLease(row, now) {
		return nil
	}

	if err := p.project(ctx, qtx, workspaceID, claim, row); err != nil {
		tx.Rollback(ctx)
		return p.fail(ctx, workspaceID, claim, err, now)
	}
	if _, err := qtx.CompleteGraphMemoryProjection(ctx, db.CompleteGraphMemoryProjectionParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
	}); err != nil {
		return fmt.Errorf("complete projection %s: %w", claim.SegmentID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit projection outcome %s: %w", claim.SegmentID, err)
	}
	return nil
}

// project performs the event-time projection: re-validate the frozen
// eligibility facts, resolve the write target(s) from event-time scope and
// lineage only, and write the atom document into the scoped graph staging.
func (p *GraphMemoryProjector) project(
	ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID,
	claim graphMemoryProjectionClaim, row db.GetGraphMemoryProjectionForUpdateRow,
) error {
	facts, err := qtx.GetUniversalDAGSegmentProjectionFacts(ctx, db.GetUniversalDAGSegmentProjectionFactsParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: segment %s missing", ErrGraphProjectionRoute, claim.SegmentID)
		}
		return fmt.Errorf("load segment facts %s: %w", claim.SegmentID, err)
	}
	if facts.PublishStatus.String != string(SegmentPublished) {
		// The request outran the publish transaction; retry until it lands.
		return fmt.Errorf("segment %s not published yet (%s)", claim.SegmentID, facts.PublishStatus.String)
	}
	if !graphMemoryProjectionEligible(facts) {
		// Ineligible (legacy, unscoped, derivative, metadata-only, or an
		// explicit request for a segment with no atoms): idempotent skip.
		return nil
	}
	atoms, err := qtx.ListGraphMemoryAtomsBySegment(ctx, db.ListGraphMemoryAtomsBySegmentParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
	})
	if err != nil {
		return fmt.Errorf("load atoms %s: %w", claim.SegmentID, err)
	}
	if len(atoms) == 0 {
		return nil
	}

	wsID := claim.WorkspaceID
	routeGeneration := claim.RouteGeneration
	if routeGeneration == 0 {
		routeGeneration = facts.RouteGenerationAtEvent.Int64
	}
	for _, group := range groupProjectionAtoms(facts, atoms) {
		kind, ownerID := group.kind, group.ownerID
		if group.visibility == "channel" {
			// Route identity validation: the channel's event-time lineage
			// row decides the physical graph — under project_lineage routing
			// that may be the bound project's graph, at the frozen
			// generation. An unresolvable lineage is terminal, never a guess.
			route, err := ResolveChannelRouteAtGeneration(ctx, p.pool, wsID, group.ownerID, routeGeneration)
			if err != nil {
				return err
			}
			kind, ownerID = memorygraph.GraphDirKind(route.GraphKind), route.GraphOwnerID
		}
		meta := memorygraph.SegmentMeta{
			WorkspaceID:       wsID,
			Visibility:        group.visibility,
			LineageGeneration: routeGeneration,
		}
		if group.visibility == "channel" {
			meta.ChannelID = group.ownerID
		} else {
			meta.ProjectID = group.ownerID
		}
		doc := graphMemoryProjectionDocument{
			SegmentID:  claim.SegmentID,
			AgentRunID: facts.AgentRunID.String(),
			PublishSeq: facts.PublishSeq.Int64,
			Source:     graphMemoryProjectionSource,
			Atoms:      group.atoms,
		}
		if err := p.writeProjection(wsID, kind, ownerID, claim.SegmentID, doc, meta); err != nil {
			return err
		}
	}
	return nil
}

// graphMemoryProjectionEligible re-validates the frozen event-time facts
// (defense in depth on top of the Task 7 publish-time gate).
func graphMemoryProjectionEligible(facts db.GetUniversalDAGSegmentProjectionFactsRow) bool {
	return facts.MemoryTypeAtEvent == "graph" &&
		facts.GraphProjectionEligibleAtEvent &&
		!facts.Derivative &&
		facts.CloseActionKind.String != string(DAGCloseMetadataOnly) &&
		(facts.ChannelIDAtEvent.Valid || facts.ProjectIDAtEvent.Valid)
}

// graphMemoryProjectionGroup is one write target with its atoms.
type graphMemoryProjectionGroup struct {
	visibility string
	kind       memorygraph.GraphDirKind
	ownerID    string
	atoms      []graphMemoryProjectionAtom
}

// groupProjectionAtoms partitions atoms by visibility and resolves each
// group's write target from event-time scope only. Channel atoms stay
// exact-channel: they only ever land in the channel's own lineage graph,
// never in a project graph the segment also references.
func groupProjectionAtoms(
	facts db.GetUniversalDAGSegmentProjectionFactsRow,
	atoms []db.ListGraphMemoryAtomsBySegmentRow,
) []graphMemoryProjectionGroup {
	toAtom := func(a db.ListGraphMemoryAtomsBySegmentRow) graphMemoryProjectionAtom {
		return graphMemoryProjectionAtom{
			AtomID: a.AtomID, Body: a.Body, Kind: a.Kind,
			SourceTool: a.SourceTool, ToolTrustClass: a.ToolTrustClass,
			SourceMessageSeqs: a.SourceMessageSeqs, ArtifactRef: a.ArtifactRef.String,
		}
	}
	var groups []graphMemoryProjectionGroup
	if facts.ChannelIDAtEvent.Valid {
		groups = append(groups, graphMemoryProjectionGroup{
			visibility: "channel",
			kind:       memorygraph.GraphDirKindChannel,
			ownerID:    facts.ChannelIDAtEvent.String(),
		})
	}
	if facts.ProjectIDAtEvent.Valid {
		groups = append(groups, graphMemoryProjectionGroup{
			visibility: "project",
			kind:       memorygraph.GraphDirKindProject,
			ownerID:    facts.ProjectIDAtEvent.String(),
		})
	}
	// Atoms inherit their own visibility from Task 7; only channel and
	// project rows exist, so anything else is ignored rather than guessed.
	for _, atom := range atoms {
		for i := range groups {
			if groups[i].visibility == atom.Visibility {
				groups[i].atoms = append(groups[i].atoms, toAtom(atom))
			}
		}
	}
	out := groups[:0]
	for _, group := range groups {
		if len(group.atoms) > 0 {
			out = append(out, group)
		}
	}
	return out
}

// writeProjection writes the atom document and scope sidecar into one scoped
// graph's staging area. Staging is immutable, so an existing document is an
// idempotent success (crash between write and row completion converges on
// retry).
func (p *GraphMemoryProjector) writeProjection(
	wsID string, kind memorygraph.GraphDirKind, ownerID, segmentID string,
	doc graphMemoryProjectionDocument, meta memorygraph.SegmentMeta,
) error {
	root := p.root
	if root == "" {
		resolved, err := graphMemoryWorkspacesRoot()
		if err != nil {
			return err
		}
		root = resolved
	}
	dir, err := memorygraph.EnsureScopedDir(root, wsID, kind, ownerID)
	if err != nil {
		return fmt.Errorf("ensure graph dir: %w", err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	if _, err := store.ReadStagingSegment(segmentID); err == nil {
		return nil
	}
	content, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode projection document: %w", err)
	}
	if err := store.WriteStagingSegment(segmentID, content); err != nil {
		if _, readErr := store.ReadStagingSegment(segmentID); readErr == nil {
			return nil
		}
		return fmt.Errorf("write staging segment: %w", err)
	}
	if err := store.WriteStagingSegmentMeta(segmentID, &meta); err != nil {
		if _, readErr := store.ReadStagingSegmentMeta(segmentID); readErr == nil {
			return nil
		}
		return fmt.Errorf("write staging segment meta: %w", err)
	}
	return nil
}

func (p *GraphMemoryProjector) ownsLease(row db.GetGraphMemoryProjectionForUpdateRow, now time.Time) bool {
	return row.Status == "processing" &&
		row.LeaseOwner.Valid && row.LeaseOwner.String == p.workerID &&
		row.LeaseExpiresAt.Valid && row.LeaseExpiresAt.Time.After(now)
}

// fail applies the classified failure outcome in a fresh transaction with
// lease re-verification. Route-identity failures are terminal and never
// consume a retry; transient failures retry with exponential backoff and
// dead-letter at the attempt cap.
func (p *GraphMemoryProjector) fail(ctx context.Context, workspaceID pgtype.UUID, claim graphMemoryProjectionClaim, cause error, now time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin projection failure %s: %w", claim.SegmentID, err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)
	row, err := qtx.GetGraphMemoryProjectionForUpdate(ctx, db.GetGraphMemoryProjectionForUpdateParams{
		WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lock projection row for failure %s: %w", claim.SegmentID, err)
	}
	if !p.ownsLease(row, now) {
		return nil
	}
	lastError := boundedGraphProjectionError(cause)
	deadLetter := func(attempts int32) error {
		if _, err := qtx.FailGraphMemoryProjection(ctx, db.FailGraphMemoryProjectionParams{
			TerminalStatus: "dead_letter", Attempts: attempts,
			LastError:   pgtype.Text{String: lastError, Valid: true},
			WorkspaceID: workspaceID, SegmentID: claim.SegmentID,
		}); err != nil {
			return fmt.Errorf("dead-letter projection %s: %w", claim.SegmentID, err)
		}
		return tx.Commit(ctx)
	}
	if errors.Is(cause, ErrGraphProjectionRoute) {
		// Deterministic route failures never consume a retry.
		return deadLetter(row.Attempts)
	}
	if int(row.Attempts)+1 >= p.maxAttempts {
		return deadLetter(row.Attempts + 1)
	}
	backoff := p.backoff(int(row.Attempts) + 1)
	if _, err := qtx.RetryGraphMemoryProjection(ctx, db.RetryGraphMemoryProjectionParams{
		Attempts:      row.Attempts + 1,
		NextAttemptAt: pgtype.Timestamptz{Time: now.Add(backoff), Valid: true},
		LastError:     pgtype.Text{String: lastError, Valid: true},
		WorkspaceID:   workspaceID, SegmentID: claim.SegmentID,
		CurrentAttempts: row.Attempts,
	}); err != nil {
		return fmt.Errorf("retry projection %s: %w", claim.SegmentID, err)
	}
	return tx.Commit(ctx)
}

// backoff returns the wait before attempt n (1-based).
func (p *GraphMemoryProjector) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > p.maxAttempts {
		attempt = p.maxAttempts
	}
	return p.backoffBase * time.Duration(1<<uint(attempt-1))
}

// boundedGraphProjectionError reduces an error to a bounded, content-free
// summary: a class token plus the segment identity, never atom bodies.
func boundedGraphProjectionError(err error) string {
	class := "transient"
	if errors.Is(err, ErrGraphProjectionRoute) {
		class = "route"
	}
	detail := err.Error()
	if len(detail) > graphMemoryProjectionLastErrorCap {
		detail = detail[:graphMemoryProjectionLastErrorCap]
	}
	return class + ": " + detail
}

// ResolveChannelRouteAtGeneration returns the event-time route identity for
// one channel generation: the lineage row the segment was frozen with,
// regardless of later route transitions. Read-only — unlike
// ResolveChannelRoute it never mutates the route or lineage tables.
func ResolveChannelRouteAtGeneration(ctx context.Context, pool *pgxpool.Pool, workspaceID, channelID string, generation int64) (GraphRouteResolution, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return GraphRouteResolution{}, fmt.Errorf("%w: workspace: %v", ErrGraphProjectionRoute, err)
	}
	chUUID, err := util.ParseUUID(channelID)
	if err != nil {
		return GraphRouteResolution{}, fmt.Errorf("%w: channel: %v", ErrGraphProjectionRoute, err)
	}
	if generation <= 0 {
		return GraphRouteResolution{}, fmt.Errorf("%w: channel %s has no route generation", ErrGraphProjectionRoute, channelID)
	}
	lineage, err := db.New(pool).GetGraphMemoryLineageAtGeneration(ctx, db.GetGraphMemoryLineageAtGenerationParams{
		WorkspaceID: wsUUID, ChannelID: chUUID, Generation: generation,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GraphRouteResolution{}, fmt.Errorf("%w: channel %s generation %d not in lineage", ErrGraphProjectionRoute, channelID, generation)
		}
		return GraphRouteResolution{}, fmt.Errorf("load lineage: %w", err)
	}
	return GraphRouteResolution{
		RoutingMode:  "project_lineage",
		GraphKind:    lineage.GraphKind,
		GraphOwnerID: lineage.GraphOwnerID.String(),
		Generation:   lineage.Generation,
	}, nil
}
