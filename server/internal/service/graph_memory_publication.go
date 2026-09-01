// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Task 14 publication outcomes. Every attempt — published or aborted —
// leaves exactly one graph_memory_publication_outcome row (aggregate
// counters only, never node bodies or payloads).
const (
	GraphMemoryPublicationOutcomePublished           = "published"
	GraphMemoryPublicationOutcomeAbortedStaleBase    = "aborted_stale_base"
	GraphMemoryPublicationOutcomeAbortedRetracted    = "aborted_retracted_source"
	GraphMemoryPublicationOutcomeAbortedManifest     = "aborted_manifest_mismatch"
	GraphMemoryPublicationOutcomeAbortedIncompleteCo = "aborted_coverage_incomplete"
)

var (
	ErrGraphMemoryPublicationStaleBase          = errors.New("graph memory publication base generation is stale")
	ErrGraphMemoryPublicationRetractedSource    = errors.New("graph memory publication source is retracted")
	ErrGraphMemoryPublicationManifestMismatch   = errors.New("graph memory publication candidate manifest changed")
	ErrGraphMemoryPublicationCoverageIncomplete = errors.New("graph memory publication coverage is incomplete")
)

// GraphMemoryPublicationSource is one contributing memory source of a
// candidate generation, keyed exactly like memory_source_guard rows.
type GraphMemoryPublicationSource struct {
	Kind string
	ID   string
}

func (s GraphMemoryPublicationSource) key() string {
	return s.Kind + ":" + s.ID
}

// GraphMemoryCoverageAtom is one atom the candidate generation consumed.
type GraphMemoryCoverageAtom struct {
	AtomID    string
	SegmentID string
}

// GraphMemoryNodeProvenance is the reverse provenance of one published
// node: the atoms (and segments) its body was derived from.
type GraphMemoryNodeProvenance struct {
	NodeID     string
	AtomIDs    []string
	SegmentIDs []string
}

// GraphMemoryPublicationRequest describes one candidate generation: an
// already-written immutable version directory plus the atom closure it
// covers. BaseGeneration is the generation the candidate planned against
// (0 = the scope's first publication); the CAS refuses everything else.
type GraphMemoryPublicationRequest struct {
	WorkspaceID      pgtype.UUID
	GraphKind        string // "project" | "channel"
	GraphOwnerID     pgtype.UUID
	Store            *memorygraph.Store
	CandidateVersion int
	BaseGeneration   int64
	Sources          []GraphMemoryPublicationSource
	Coverage         []GraphMemoryCoverageAtom
	Provenance       []GraphMemoryNodeProvenance
	PublishedBy      string
}

// GraphMemoryPublicationHooks are fault-injection points for the crash and
// deletion-race tests. A non-nil error returned from a hook simulates a
// process crash at exactly that point in the protocol.
type GraphMemoryPublicationHooks struct {
	AfterFileSync    func() error // files written, fsync not yet complete
	AfterFilePrepare func() error // fsync complete, DB transaction not started
	AfterDBCommit    func() error // generation committed, cache pointer not yet moved
}

// GraphMemoryPublicationResult reports the committed generation.
type GraphMemoryPublicationResult struct {
	Generation   int64
	GraphVersion int
	ManifestHash string
}

// PublishGraphMemoryPublication runs the DB-authoritative publication
// protocol for one graph scope:
//
//  1. digest + fsync the complete immutable candidate version (a crash here
//     leaves nothing durable anywhere);
//  2. one PostgreSQL transaction: lock every contributing
//     memory_source_guard FOR KEY SHARE in the deletion path's deterministic
//     source-key order, recheck retraction and the on-disk candidate
//     digest, insert coverage + reverse provenance, CAS the generation, and
//     write the reader-facing index row — all or nothing;
//  3. only after commit, move the file-store current pointer (a recoverable
//     cache; RebuildGraphMemoryCachePointer heals it from DB state).
//
// The file-store pointer is never reader authority: ActiveGraphPublication
// is.
func PublishGraphMemoryPublication(
	ctx context.Context, pool *pgxpool.Pool, req GraphMemoryPublicationRequest,
) (GraphMemoryPublicationResult, error) {
	return PublishGraphMemoryPublicationWithHooks(ctx, pool, req, GraphMemoryPublicationHooks{})
}

func PublishGraphMemoryPublicationWithHooks(
	ctx context.Context, pool *pgxpool.Pool, req GraphMemoryPublicationRequest, hooks GraphMemoryPublicationHooks,
) (GraphMemoryPublicationResult, error) {
	if pool == nil {
		return GraphMemoryPublicationResult{}, errors.New("graph memory publication requires a pool")
	}
	if req.Store == nil {
		return GraphMemoryPublicationResult{}, errors.New("graph memory publication requires the candidate store")
	}
	gate := NewMemoryReadGate(db.New(pool))
	enabled, err := gate.RouteEnabled(ctx, req.WorkspaceID, MemoryRouteAtomConsolidation)
	if err != nil {
		return GraphMemoryPublicationResult{}, err
	}
	if !enabled {
		return GraphMemoryPublicationResult{}, ErrMemoryRouteDisabled
	}
	if !req.WorkspaceID.Valid || !req.GraphOwnerID.Valid {
		return GraphMemoryPublicationResult{}, errors.New("graph memory publication requires workspace and owner")
	}
	if req.CandidateVersion <= 0 {
		return GraphMemoryPublicationResult{}, errors.New("graph memory publication requires a candidate version")
	}
	if len(req.Sources) == 0 {
		return GraphMemoryPublicationResult{}, errors.New("graph memory publication requires at least one source")
	}
	if err := validateGraphMemoryPublicationCoverage(req); err != nil {
		return GraphMemoryPublicationResult{}, err
	}

	// Step 1: immutable candidate preparation. The digest computed here is
	// the identity the transaction will recheck.
	if err := req.Store.SyncVersion(req.CandidateVersion); err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("fsync candidate version: %w", err)
	}
	if hooks.AfterFileSync != nil {
		if err := hooks.AfterFileSync(); err != nil {
			return GraphMemoryPublicationResult{}, err
		}
	}
	digest, err := req.Store.VersionDigest(req.CandidateVersion)
	if err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("digest candidate version: %w", err)
	}
	if hooks.AfterFilePrepare != nil {
		if err := hooks.AfterFilePrepare(); err != nil {
			return GraphMemoryPublicationResult{}, err
		}
	}

	keys := make([]string, 0, len(req.Sources))
	for _, source := range req.Sources {
		keys = append(keys, source.key())
	}
	sort.Strings(keys)

	// Step 2: the publication transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("begin publication: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)

	// FOR KEY SHARE in the same sorted order the deletion path takes FOR
	// UPDATE: delete-first makes this publication abort; publish-first makes
	// the deletion wait for the transaction that quarantines the closure.
	locked, err := qtx.LockMemorySourceGuardsKeyShare(ctx, db.LockMemorySourceGuardsKeyShareParams{
		WorkspaceID: req.WorkspaceID, SourceKeys: keys,
	})
	if err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("lock source guards: %w", err)
	}
	for _, row := range locked {
		if row.RetractedAt.Valid {
			return graphMemoryPublicationAbort(ctx, pool, req, digest, keys,
				GraphMemoryPublicationOutcomeAbortedRetracted, ErrGraphMemoryPublicationRetractedSource)
		}
	}

	// Candidate recheck inside the lock window: the immutable version must
	// still digest to what was prepared.
	current, err := req.Store.VersionDigest(req.CandidateVersion)
	if err != nil || current != digest {
		return graphMemoryPublicationAbort(ctx, pool, req, digest, keys,
			GraphMemoryPublicationOutcomeAbortedManifest, ErrGraphMemoryPublicationManifestMismatch)
	}

	generation := req.BaseGeneration + 1
	published, err := qtx.CASPublishGraphMemoryGeneration(ctx, db.CASPublishGraphMemoryGenerationParams{
		WorkspaceID: req.WorkspaceID, GraphKind: req.GraphKind, GraphOwnerID: req.GraphOwnerID,
		BaseGeneration: req.BaseGeneration, GraphVersion: int32(req.CandidateVersion),
		FileManifestHash: digest, PublishedBy: publicationActor(req.PublishedBy),
	})
	if err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("cas publication generation: %w", err)
	}
	if published == 0 {
		return graphMemoryPublicationAbort(ctx, pool, req, digest, keys,
			GraphMemoryPublicationOutcomeAbortedStaleBase, ErrGraphMemoryPublicationStaleBase)
	}
	if err := qtx.UpsertGraphMemoryPublicationIndex(ctx, db.UpsertGraphMemoryPublicationIndexParams{
		WorkspaceID: req.WorkspaceID, GraphKind: req.GraphKind, GraphOwnerID: req.GraphOwnerID,
		ActiveGeneration: generation, GraphVersion: int32(req.CandidateVersion), FileManifestHash: digest,
	}); err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("write publication index: %w", err)
	}
	atomIDs := make([]string, 0, len(req.Coverage))
	segmentIDs := make([]string, 0, len(req.Coverage))
	segments := map[string]bool{}
	for _, atom := range req.Coverage {
		atomIDs = append(atomIDs, atom.AtomID)
		segmentIDs = append(segmentIDs, atom.SegmentID)
		segments[atom.SegmentID] = true
	}
	if _, err := qtx.InsertGraphMemoryPublicationCoverage(ctx, db.InsertGraphMemoryPublicationCoverageParams{
		WorkspaceID: req.WorkspaceID, GraphKind: req.GraphKind, GraphOwnerID: req.GraphOwnerID,
		Generation: generation, AtomIds: atomIDs, SegmentIds: segmentIDs,
	}); err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("write coverage ledger: %w", err)
	}
	for _, node := range req.Provenance {
		if err := qtx.InsertGraphMemoryPublicationProvenanceRow(ctx, db.InsertGraphMemoryPublicationProvenanceRowParams{
			WorkspaceID: req.WorkspaceID, GraphKind: req.GraphKind, GraphOwnerID: req.GraphOwnerID,
			Generation: generation, NodeID: node.NodeID, AtomIds: node.AtomIDs, SegmentIds: node.SegmentIDs,
		}); err != nil {
			return GraphMemoryPublicationResult{}, fmt.Errorf("write node provenance: %w", err)
		}
	}
	if err := qtx.InsertGraphMemoryPublicationOutcome(ctx, db.InsertGraphMemoryPublicationOutcomeParams{
		WorkspaceID: req.WorkspaceID, GraphKind: req.GraphKind, GraphOwnerID: req.GraphOwnerID,
		Generation: generation, Outcome: GraphMemoryPublicationOutcomePublished,
		GraphVersion: int32(req.CandidateVersion), FileManifestHash: digest,
		CoveredAtomCount: int32(len(atomIDs)), CoveredSegmentCount: int32(len(segments)),
		NodeCount: int32(len(req.Provenance)), SourceKeys: keys,
	}); err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("write publication outcome: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("commit publication: %w", err)
	}

	if hooks.AfterDBCommit != nil {
		if err := hooks.AfterDBCommit(); err != nil {
			return GraphMemoryPublicationResult{Generation: generation, GraphVersion: req.CandidateVersion, ManifestHash: digest}, err
		}
	}

	// Step 3: the file-store current pointer is a recoverable cache. Failure
	// here never unwinds the committed generation.
	if err := req.Store.SwitchCurrent(req.CandidateVersion); err != nil {
		fmt.Printf("[graph-memory] publication cache pointer switch failed (recoverable): %v\n", err)
	}
	return GraphMemoryPublicationResult{Generation: generation, GraphVersion: req.CandidateVersion, ManifestHash: digest}, nil
}

// graphMemoryPublicationAbort rolls the publication transaction back and
// records the abort outcome in its own committed transaction, so the ledger
// shows refused candidates too.
func graphMemoryPublicationAbort(
	ctx context.Context, pool *pgxpool.Pool, req GraphMemoryPublicationRequest,
	digest string, keys []string, outcome string, cause error,
) (GraphMemoryPublicationResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("begin abort ledger: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := db.New(tx).InsertGraphMemoryPublicationOutcome(ctx, db.InsertGraphMemoryPublicationOutcomeParams{
		WorkspaceID: req.WorkspaceID, GraphKind: req.GraphKind, GraphOwnerID: req.GraphOwnerID,
		Generation: req.BaseGeneration + 1, Outcome: outcome,
		GraphVersion: int32(req.CandidateVersion), FileManifestHash: digest,
		CoveredAtomCount: 0, CoveredSegmentCount: 0, NodeCount: int32(len(req.Provenance)),
		SourceKeys: keys,
	}); err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("record abort outcome: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return GraphMemoryPublicationResult{}, fmt.Errorf("commit abort ledger: %w", err)
	}
	return GraphMemoryPublicationResult{}, cause
}

// validateGraphMemoryPublicationCoverage enforces exact coverage: every
// atom cited by node provenance must be declared in the coverage ledger.
func validateGraphMemoryPublicationCoverage(req GraphMemoryPublicationRequest) error {
	declared := make(map[string]bool, len(req.Coverage))
	for _, atom := range req.Coverage {
		if atom.AtomID == "" || atom.SegmentID == "" {
			return fmt.Errorf("%w: empty atom row", ErrGraphMemoryPublicationCoverageIncomplete)
		}
		declared[atom.AtomID] = true
	}
	for _, node := range req.Provenance {
		for _, atomID := range node.AtomIDs {
			if !declared[atomID] {
				return fmt.Errorf("%w: node %s cites undeclared atom %s",
					ErrGraphMemoryPublicationCoverageIncomplete, node.NodeID, atomID)
			}
		}
	}
	return nil
}

// ActiveGraphUncoveredAtomCounts is the Task 14 scheduler trigger input:
// how many active atoms of the scope the active publication generation has
// not covered yet, and the scope's total active atoms (the failure-backoff
// watermark). Staging files are never replayed.
func ActiveGraphUncoveredAtomCounts(
	ctx context.Context, pool *pgxpool.Pool, workspaceID pgtype.UUID, graphKind string, graphOwnerID pgtype.UUID,
) (uncovered, total int64, err error) {
	if pool == nil {
		return 0, 0, errors.New("uncovered atom counts require a pool")
	}
	scopeChannel := ""
	if memorygraph.GraphDirKind(graphKind) == memorygraph.GraphDirKindChannel {
		scopeChannel = graphOwnerID.String()
	}
	row, err := db.New(pool).CountScopeUncoveredActiveAtoms(ctx, db.CountScopeUncoveredActiveAtomsParams{
		WorkspaceID: workspaceID, GraphKind: graphKind, GraphOwnerID: graphOwnerID, ScopeChannelID: scopeChannel,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("count uncovered atoms: %w", err)
	}
	return row.UncoveredCount, row.TotalCount, nil
}

// activeGraphPublicationRow reads the reader-authority index row with any
// DBTX-bound query handle; found is false for scopes that predate
// publication, where the recoverable file pointer is the fallback
// authority.
func activeGraphPublicationRow(
	ctx context.Context, queries *db.Queries, workspaceID pgtype.UUID, graphKind string, graphOwnerID pgtype.UUID,
) (generation int64, version int, manifestHash string, found bool, err error) {
	row, err := queries.GetGraphMemoryPublicationIndex(ctx, db.GetGraphMemoryPublicationIndexParams{
		WorkspaceID: workspaceID, GraphKind: graphKind, GraphOwnerID: graphOwnerID,
	})
	if err == nil {
		return row.ActiveGeneration, int(row.GraphVersion), row.FileManifestHash, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, "", false, nil
	}
	return 0, 0, "", false, fmt.Errorf("read publication index: %w", err)
}

// ActiveGraphPublication is the reader-authoritative resolution of one graph
// scope: the DB generation wins; a scope that predates publication (no row)
// falls back to the file-store current pointer with generation 0.
func ActiveGraphPublication(
	ctx context.Context, pool *pgxpool.Pool, workspaceID pgtype.UUID, graphKind string, graphOwnerID pgtype.UUID,
) (generation int64, version int, manifestHash string, err error) {
	if pool == nil {
		return 0, 0, "", errors.New("active graph publication requires a pool")
	}
	generation, version, manifestHash, found, err := activeGraphPublicationRow(ctx, db.New(pool), workspaceID, graphKind, graphOwnerID)
	if err != nil || found {
		return generation, version, manifestHash, err
	}
	// Legacy scope: the recoverable file pointer is the only signal.
	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return 0, 0, "", err
	}
	storeDir, err := memorygraph.DirForScope(root, workspaceID.String(),
		memorygraph.GraphDirKind(graphKind), graphOwnerID.String())
	if err != nil {
		return 0, 0, "", err
	}
	store := memorygraph.NewStore(storeDir)
	current, err := store.CurrentVersion()
	if err != nil {
		return 0, 0, "", err
	}
	return 0, current, "", nil
}

// RebuildGraphMemoryCachePointer heals the file-store current pointer from
// DB-authoritative state after a crash between the publication commit and
// the cache switch. It is idempotent and never creates a generation.
func RebuildGraphMemoryCachePointer(ctx context.Context, pool *pgxpool.Pool, req GraphMemoryPublicationRequest) error {
	row, err := db.New(pool).GetGraphMemoryPublicationIndex(ctx, db.GetGraphMemoryPublicationIndexParams{
		WorkspaceID: req.WorkspaceID, GraphKind: req.GraphKind, GraphOwnerID: req.GraphOwnerID,
	})
	if err != nil {
		return fmt.Errorf("read publication index: %w", err)
	}
	if int(row.GraphVersion) != req.CandidateVersion {
		return fmt.Errorf("active version %d does not match candidate %d", row.GraphVersion, req.CandidateVersion)
	}
	return req.Store.SwitchCurrent(req.CandidateVersion)
}

func publicationActor(name string) string {
	if name == "" {
		return "consolidator"
	}
	return name
}

// activeGraphVersionForStore resolves the version readers must use for one
// scope: the DB-authoritative publication wins; the file-store current
// pointer answers only for scopes that never published (legacy stores).
func activeGraphVersionForStore(
	ctx context.Context, pool *pgxpool.Pool, workspaceID pgtype.UUID, graphKind, ownerID string,
	store *memorygraph.Store,
) (int, error) {
	owner, err := parseUUIDColumn("graph_owner_id", ownerID)
	if err != nil {
		// Unparsable owner keeps the legacy file-pointer path.
		return store.CurrentVersion()
	}
	if pool == nil {
		return store.CurrentVersion()
	}
	// The DB index row is reader authority; a scope that predates
	// publication falls back to the CALLER's store, whose root is the one
	// the caller already resolved (never a re-resolved env root).
	_, version, _, found, err := activeGraphPublicationRow(ctx, db.New(pool), workspaceID, graphKind, owner)
	if err != nil {
		return 0, err
	}
	if !found || version <= 0 {
		return store.CurrentVersion()
	}
	return version, nil
}
