// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var errPublicationCrash = errors.New("simulated publication crash")

type publicationHarness struct {
	*exploreV2Harness
	store        *memorygraph.Store
	storeDir     string
	candidateVer int
	sources      []GraphMemoryPublicationSource
	coverage     []GraphMemoryCoverageAtom
	provenance   []GraphMemoryNodeProvenance
}

// The base publisher harness chain now applies 469 (idempotently), so the
// publication harness inherits the ledger tables and the gate column.
func applyGraphMemoryPublicationMigration(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	applyUniversalDAGPublicationMigration(t, ctx, conn)
}

func newPublicationHarness(t *testing.T) *publicationHarness {
	t.Helper()
	h := &publicationHarness{exploreV2Harness: newExploreV2Harness(t, false)}
	applyGraphMemoryPublicationMigration(t, h.ctx, h.conn)
	h.enableConsolidationRoute(t)

	h.storeDir = t.TempDir()
	h.store = memorygraph.NewStore(h.storeDir)
	require.NoError(t, h.store.Init())
	h.candidateVer = 2
	_, err := h.store.CreateVersionFrom(1, "ttt")
	require.NoError(t, err)
	require.NoError(t, h.store.SaveNode(h.candidateVer, &memorygraph.Node{
		NodeID: "node-nimbus", Body: "NIMBUS launch facts",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: h.candidateVer, UpdatedVersion: h.candidateVer,
		Visibility: "channel", ChannelID: h.channel.String(),
	}))

	h.sources = []GraphMemoryPublicationSource{{Kind: h.taskRef.Kind, ID: h.taskRef.ID.String()}}
	h.coverage = []GraphMemoryCoverageAtom{{AtomID: h.atomID, SegmentID: h.segment}}
	h.provenance = []GraphMemoryNodeProvenance{{
		NodeID: "node-nimbus", AtomIDs: []string{h.atomID}, SegmentIDs: []string{h.segment},
	}}
	return h
}

func (h *publicationHarness) request() GraphMemoryPublicationRequest {
	return GraphMemoryPublicationRequest{
		WorkspaceID: h.workspace, GraphKind: "channel", GraphOwnerID: h.channel,
		Store: h.store, CandidateVersion: h.candidateVer, BaseGeneration: 0,
		Sources: h.sources, Coverage: h.coverage, Provenance: h.provenance,
	}
}

func (h *publicationHarness) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, h.conn.QueryRow(h.ctx, query, args...).Scan(&n))
	return n
}

// The happy path is one PostgreSQL transaction: CAS generation, index
// pointer, coverage ledger, reverse provenance and outcome commit together,
// and only then does the file-store current pointer move (recoverable
// cache).
func TestGraphMemoryPublication_PublishesGenerationWithCompleteLedgers(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()

	res, err := PublishGraphMemoryPublication(h.ctx, h.pubPool, h.request())
	require.NoError(t, err)
	assert.EqualValues(t, 1, res.Generation)
	require.NotEmpty(t, res.ManifestHash)

	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_index WHERE active_generation=1`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_coverage`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_provenance WHERE node_id='node-nimbus'`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome WHERE outcome='published'`))

	// The cache pointer moved after commit and matches the published version.
	current, err := h.store.CurrentVersion()
	require.NoError(t, err)
	assert.Equal(t, h.candidateVer, current)

	// Reader authority comes from the DB, and the digest still matches disk.
	gen, version, digest, err := ActiveGraphPublication(h.ctx, h.pubPool, h.workspace, "channel", h.channel)
	require.NoError(t, err)
	assert.EqualValues(t, 1, gen)
	assert.Equal(t, h.candidateVer, version)
	onDisk, err := h.store.VersionDigest(h.candidateVer)
	require.NoError(t, err)
	assert.Equal(t, onDisk, digest)
}

// A publication that planned against a superseded base generation is
// refused: the CAS affects zero rows, an abort outcome is recorded, and no
// ledger row moves.
func TestGraphMemoryPublication_StaleBaseAbortsWithoutConsuming(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	_, err := PublishGraphMemoryPublication(h.ctx, h.pubPool, h.request())
	require.NoError(t, err)

	_, err = PublishGraphMemoryPublication(h.ctx, h.pubPool, h.request())
	require.ErrorIs(t, err, ErrGraphMemoryPublicationStaleBase)
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication WHERE current_generation=1`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome WHERE outcome='aborted_stale_base'`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_coverage`))
}

// A retracted (deleted) source aborts the publication before any ledger
// write: the deleted body never becomes visible through a new generation.
func TestGraphMemoryPublication_RetractedSourceAborts(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{h.taskRef}, "user:1", "deleted before publication"))
	require.NoError(t, tx.Commit(h.ctx))

	_, err = PublishGraphMemoryPublication(h.ctx, h.pubPool, h.request())
	require.ErrorIs(t, err, ErrGraphMemoryPublicationRetractedSource)
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_index`))
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_coverage`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome WHERE outcome='aborted_retracted_source'`))
}

// The candidate manifest is rechecked inside the publication transaction: a
// version mutated after prepare is refused.
func TestGraphMemoryPublication_ManifestMismatchAborts(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	hooks := GraphMemoryPublicationHooks{
		AfterFilePrepare: func() error {
			node := &memorygraph.Node{
				NodeID: "node-tamper", Body: "mutated after prepare",
				CreatedBy: memorygraph.CreatorIngester, CreatedVersion: h.candidateVer, UpdatedVersion: h.candidateVer,
			}
			return h.store.SaveNode(h.candidateVer, node)
		},
	}
	_, err := PublishGraphMemoryPublicationWithHooks(h.ctx, h.pubPool, h.request(), hooks)
	require.ErrorIs(t, err, ErrGraphMemoryPublicationManifestMismatch)
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
	assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome WHERE outcome='aborted_manifest_mismatch'`))
}

// Crash windows: before the fsync completes nothing is durable anywhere;
// after file prepare but before the DB commit nothing is claimed; after the
// DB commit but before the cache pointer the DB still serves the new
// generation (the pointer is a rebuildable projection).
func TestGraphMemoryPublication_CrashWindows(t *testing.T) {
	t.Run("crash before fsync", func(t *testing.T) {
		h := newPublicationHarness(t)
		defer h.Close()
		hooks := GraphMemoryPublicationHooks{AfterFileSync: func() error { return errPublicationCrash }}
		_, err := PublishGraphMemoryPublicationWithHooks(h.ctx, h.pubPool, h.request(), hooks)
		require.ErrorIs(t, err, errPublicationCrash)
		assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
		assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome`))
	})
	t.Run("crash after file prepare before db commit", func(t *testing.T) {
		h := newPublicationHarness(t)
		defer h.Close()
		hooks := GraphMemoryPublicationHooks{AfterFilePrepare: func() error { return errPublicationCrash }}
		_, err := PublishGraphMemoryPublicationWithHooks(h.ctx, h.pubPool, h.request(), hooks)
		require.ErrorIs(t, err, errPublicationCrash)
		assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
		assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome`))
	})
	t.Run("crash after db commit before cache pointer", func(t *testing.T) {
		h := newPublicationHarness(t)
		defer h.Close()
		hooks := GraphMemoryPublicationHooks{AfterDBCommit: func() error { return errPublicationCrash }}
		_, err := PublishGraphMemoryPublicationWithHooks(h.ctx, h.pubPool, h.request(), hooks)
		require.ErrorIs(t, err, errPublicationCrash)
		// DB authority holds the complete generation.
		assert.Equal(t, 1, h.countRows(t, `SELECT count(*) FROM graph_memory_publication WHERE current_generation=1`))
		gen, version, _, err := ActiveGraphPublication(h.ctx, h.pubPool, h.workspace, "channel", h.channel)
		require.NoError(t, err)
		assert.EqualValues(t, 1, gen)
		assert.Equal(t, h.candidateVer, version)
		// The stale cache pointer is still recoverable: republishing the
		// pointer from DB state heals it.
		require.NoError(t, RebuildGraphMemoryCachePointer(h.ctx, h.pubPool, h.request()))
		current, err := h.store.CurrentVersion()
		require.NoError(t, err)
		assert.Equal(t, h.candidateVer, current)
	})
}

// The coverage ledger must be exact: provenance citing atoms outside the
// declared coverage is refused instead of silently under-covering.
func TestGraphMemoryPublication_PartialCoverageRefused(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	req := h.request()
	req.Provenance = []GraphMemoryNodeProvenance{{
		NodeID: "node-nimbus", AtomIDs: []string{"atom-not-in-coverage"}, SegmentIDs: []string{h.segment},
	}}
	_, err := PublishGraphMemoryPublication(h.ctx, h.pubPool, req)
	require.ErrorIs(t, err, ErrGraphMemoryPublicationCoverageIncomplete)
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
}

// A disabled atom_consolidation gate refuses publication.
func TestGraphMemoryPublication_DisabledGateRefuses(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	h.disableConsolidationRoute(t)
	_, err := PublishGraphMemoryPublication(h.ctx, h.pubPool, h.request())
	require.ErrorIs(t, err, ErrMemoryRouteDisabled)
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
}

// Deleting a source after the files are prepared but before the publication
// transaction takes its locks makes the publication abort: delete-first
// wins, the deleted body is never visible.
func TestGraphMemoryPublication_DeletionBetweenPrepareAndCommitAborts(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	hooks := GraphMemoryPublicationHooks{AfterFilePrepare: func() error {
		tx, err := h.pubPool.Begin(h.ctx)
		if err != nil {
			return err
		}
		if err := NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
			[]MemorySourceRef{h.taskRef}, "user:1", "deleted during publication"); err != nil {
			tx.Rollback(h.ctx)
			return err
		}
		return tx.Commit(h.ctx)
	}}
	_, err := PublishGraphMemoryPublicationWithHooks(h.ctx, h.pubPool, h.request(), hooks)
	require.ErrorIs(t, err, ErrGraphMemoryPublicationRetractedSource)
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_publication`))
}

// Publish-first: a deletion that runs after a publication committed finds
// the new closure through the coverage/provenance ledgers and atomically
// quarantines the newly published node.
func TestGraphMemoryPublication_DeletionAfterPublishQuarantinesNode(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	_, err := PublishGraphMemoryPublication(h.ctx, h.pubPool, h.request())
	require.NoError(t, err)

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{h.taskRef}, "user:1", "deleted after publication"))
	require.NoError(t, tx.Commit(h.ctx))

	quarantined := h.countRows(t, `
		SELECT count(*) FROM quarantined_pending_recompute
		WHERE consumer_kind='graph_node' AND consumer_id='node-nimbus'`)
	assert.Equal(t, 1, quarantined, "the newly published node must be quarantined atomically")
}

// Concurrent readers only ever observe complete generations: the index row
// and its coverage commit together.
func TestGraphMemoryPublication_ConcurrentReadersSeeCompleteGenerations(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	_, err := PublishGraphMemoryPublication(h.ctx, h.pubPool, h.request())
	require.NoError(t, err)

	var incomplete atomic.Int32
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			gen, _, _, err := ActiveGraphPublication(context.Background(), h.pubPool, h.workspace, "channel", h.channel)
			if err == nil && gen > 0 {
				var covered int
				_ = h.pubPool.QueryRow(context.Background(),
					`SELECT count(*) FROM graph_memory_publication_coverage WHERE generation=$1`, gen).Scan(&covered)
				if covered == 0 {
					incomplete.Add(1)
				}
			}
		}
	}()

	req := h.request()
	req.BaseGeneration = 1
	req.CandidateVersion = 3
	_, cerr := h.store.CreateVersionFrom(h.candidateVer, "ttt")
	require.NoError(t, cerr)
	_, err = PublishGraphMemoryPublication(h.ctx, h.pubPool, req)
	require.NoError(t, err)
	close(stop)
	<-done
	assert.Equal(t, int32(0), incomplete.Load(), "a reader observed a generation without its coverage ledger")
}

// ActiveGraphPublication falls back to the file-store current pointer for
// scopes that predate publication (zero generation, file version).
func TestGraphMemoryPublication_FallsBackToFilePointerForLegacyScopes(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()
	// A scope that predates publication keeps its file-store pointer as the
	// only signal: the fallback reads the scoped store layout, not the DB.
	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	legacyDir, err := memorygraph.EnsureScopedDir(root, h.workspace.String(),
		memorygraph.GraphDirKindChannel, h.channel.String())
	require.NoError(t, err)
	legacy := memorygraph.NewStore(legacyDir)
	require.NoError(t, legacy.Init())

	gen, version, digest, err := ActiveGraphPublication(h.ctx, h.pubPool, h.workspace, "channel", h.channel)
	require.NoError(t, err)
	assert.EqualValues(t, 0, gen)
	current, err := legacy.CurrentVersion()
	require.NoError(t, err)
	assert.Equal(t, current, version)
	assert.Empty(t, digest)
}

func (h *publicationHarness) enableConsolidationRoute(t *testing.T) {
	t.Helper()
	q := db.New(h.pubPool)
	_, err := q.InsertMemoryReadPhaseGate(h.ctx, h.workspace)
	require.NoError(t, err)
	_, err = q.SetMemoryReadPhaseGate(h.ctx, db.SetMemoryReadPhaseGateParams{
		WorkspaceID: h.workspace, RetractionCanaryOk: true, AtomConsolidationEnabled: true,
	})
	require.NoError(t, err)
}

func (h *publicationHarness) disableConsolidationRoute(t *testing.T) {
	t.Helper()
	_, err := db.New(h.pubPool).SetMemoryReadPhaseGate(h.ctx, db.SetMemoryReadPhaseGateParams{
		WorkspaceID: h.workspace, RetractionCanaryOk: true,
	})
	require.NoError(t, err)
}

// The Task 14 scheduler trigger input: uncovered counts the scope's active
// atoms the active publication generation does not cover yet, total counts
// every active atom of the scope. Staging files are never replayed.
func TestActiveGraphUncoveredAtomCounts(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()

	var channelAtoms int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM graph_memory_atom WHERE workspace_id=$1 AND channel_id=$2`,
		h.workspace, h.channel).Scan(&channelAtoms))
	require.GreaterOrEqual(t, channelAtoms, 1)

	// Before any publication every active channel atom is uncovered.
	uncovered, total, err := ActiveGraphUncoveredAtomCounts(h.ctx, h.pubPool, h.workspace, "channel", h.channel)
	require.NoError(t, err)
	assert.EqualValues(t, channelAtoms, uncovered)
	assert.EqualValues(t, channelAtoms, total)

	// Channel atoms never count toward a project scope.
	projectOwner, err := util.ParseUUID("11111111-2222-3333-4444-555555555555")
	require.NoError(t, err)
	pUncovered, pTotal, err := ActiveGraphUncoveredAtomCounts(h.ctx, h.pubPool, h.workspace, "project", projectOwner)
	require.NoError(t, err)
	assert.EqualValues(t, 0, pUncovered)
	assert.EqualValues(t, 0, pTotal)

	// Publishing the active generation folds every covered atom to zero.
	_, err = PublishGraphMemoryPublication(h.ctx, h.pubPool, h.request())
	require.NoError(t, err)
	uncovered, total, err = ActiveGraphUncoveredAtomCounts(h.ctx, h.pubPool, h.workspace, "channel", h.channel)
	require.NoError(t, err)
	assert.EqualValues(t, 0, uncovered)
	assert.EqualValues(t, channelAtoms, total)

	// A quarantined (retracted) atom leaves both counts entirely.
	retraction := pgtype.UUID{Bytes: [16]byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}, Valid: true}
	_, err = h.conn.Exec(h.ctx, `INSERT INTO quarantined_pending_recompute
		(workspace_id, retraction_id, consumer_kind, consumer_id)
		VALUES ($1, $2, 'graph_memory_atom', $3)`, h.workspace, retraction, h.atomID)
	require.NoError(t, err)
	uncovered, total, err = ActiveGraphUncoveredAtomCounts(h.ctx, h.pubPool, h.workspace, "channel", h.channel)
	require.NoError(t, err)
	assert.EqualValues(t, 0, uncovered)
	assert.EqualValues(t, channelAtoms-1, total)
}
