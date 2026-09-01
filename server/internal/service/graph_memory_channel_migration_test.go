// SPDX-License-Identifier: Apache-2.0

package service

import (
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// migrationHarness: binding harness with the copy route gated on, one
// queued A→B migration, and both graph store dirs seeded with nodes.
type migrationHarness struct {
	*channelBindingHarness
	oldStore *memorygraph.Store
	newStore *memorygraph.Store
	binding  ChannelProjectBindingResult
}

func newMigrationHarness(t *testing.T) *migrationHarness {
	t.Helper()
	h := &migrationHarness{channelBindingHarness: newChannelBindingHarness(t)}
	q := db.New(h.pubPool)
	_, err := q.InsertMemoryReadPhaseGate(h.ctx, h.workspace)
	require.NoError(t, err)
	_, err = q.SetMemoryReadPhaseGate(h.ctx, db.SetMemoryReadPhaseGateParams{
		WorkspaceID: h.workspace, RetractionCanaryOk: true,
	})
	require.NoError(t, err)
	err = q.SetMemoryReadPhaseGateChannelMigration(h.ctx, db.SetMemoryReadPhaseGateChannelMigrationParams{
		WorkspaceID: h.workspace, ChannelMigrationEnabled: true,
	})
	require.NoError(t, err)

	// Seed the OLD project-A graph with the channel's nodes: a channel
	// fact node, the channel's daily node, a foreign project node, and a
	// cross-scope edge that must be dropped in the copy.
	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	oldDir, err := memorygraph.EnsureScopedDir(root, h.workspace.String(),
		memorygraph.GraphDirKindProject, h.project.String())
	require.NoError(t, err)
	oldStore := memorygraph.NewStore(oldDir)
	require.NoError(t, oldStore.Init())
	require.NoError(t, oldStore.SaveNode(1, &memorygraph.Node{
		NodeID: "node-fact", Body: "NIMBUS v2 rollout fact",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1,
		Visibility: "channel", ChannelID: h.channel.String(), AtomRefs: []string{h.atomID},
	}))
	require.NoError(t, oldStore.SaveNode(1, &memorygraph.Node{
		NodeID:    memorygraph.DailyNodeID("agent-1", h.project.String(), h.channel.String(), h.ctxTime()),
		Body:      "daily summary",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1,
		Visibility: "channel", ChannelID: h.channel.String(),
	}))
	require.NoError(t, oldStore.SaveNode(1, &memorygraph.Node{
		NodeID: "node-project-only", Body: "project-scoped knowledge",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1,
		Visibility: "project", ChannelID: "",
	}))
	require.NoError(t, oldStore.SaveEdges(1,
		[]*memorygraph.Edge{{EdgeID: "edge-in-scope", Type: "summarizes", From: "node-fact",
			To:        memorygraph.DailyNodeID("agent-1", h.project.String(), h.channel.String(), h.ctxTime()),
			CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1}},
		[]*memorygraph.Edge{{EdgeID: "edge-cross-scope", Type: "relates", From: "node-fact",
			To: "node-project-only", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1}},
	))

	binding := h.bind(t, h.projectB)
	require.True(t, binding.MigrationPending)

	newDir, err := memorygraph.EnsureScopedDir(root, h.workspace.String(),
		memorygraph.GraphDirKindProject, h.projectB.String())
	require.NoError(t, err)
	h.oldStore, h.newStore, h.binding = oldStore, memorygraph.NewStore(newDir), binding
	return h
}

func (h *migrationHarness) ctxTime() time.Time { return time.Now().UTC() }

func (h *migrationHarness) worker() *GraphMemoryChannelMigrationService {
	return NewGraphMemoryChannelMigrationService(h.pubPool)
}

// worker on the base harness lets gate-refusal cases skip the graph setup.
func (h *channelBindingHarness) worker() *GraphMemoryChannelMigrationService {
	return NewGraphMemoryChannelMigrationService(h.pubPool)
}

func (h *migrationHarness) run(t *testing.T) GraphMemoryChannelMigrationReport {
	t.Helper()
	report, err := h.worker().MigrateChannel(h.ctx, h.workspace, h.channel, h.binding.Generation)
	require.NoError(t, err)
	return report
}

func (h *migrationHarness) publishPostBindingAtom(t *testing.T, label string) {
	t.Helper()
	task := h.createTask(t, h.ctx, 1)
	setTaskMessageContent(t, h.universalDAGPublisherHarness, task, "NIMBUS "+label+" fact", `{"a":1}`, "")
	input := h.boundaryInput(task, universalDAGBoundaryFixture{
		kind: DAGBoundaryVisible, closeKind: DAGCloseMessage, endSeq: 1, actionKey: label,
	})
	input.ProjectID = h.projectB
	_, err := h.recordBoundary(h.ctx, input)
	require.NoError(t, err, "record boundary for %s", label)
	published, err := NewInteractionDAGPublisher(h.pubPool).PublishClaim(h.ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, published)
}

func (h *migrationHarness) atomIDs(t *testing.T, query string, args ...any) []string {
	t.Helper()
	rows, err := h.pubPool.Query(h.ctx, query, args...)
	require.NoError(t, err)
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	return ids
}

// The disabled gate refuses and leaves the queued state untouched: a red
// gate claims nothing.
func TestGraphMemoryChannelMigration_DisabledGateRefuses(t *testing.T) {
	h := newChannelBindingHarness(t)
	defer h.Close()
	binding := h.bind(t, h.projectB)
	require.True(t, binding.MigrationPending)

	_, err := h.worker().MigrateChannel(h.ctx, h.workspace, h.channel, binding.Generation)
	assert.ErrorIs(t, err, ErrChannelMigrationDisabled)
	assert.Equal(t, 1, h.countRows(t, `
		SELECT count(*) FROM graph_memory_channel_migration_state
		WHERE channel_id=$1 AND phase='pending'`, h.channel))
	assert.Equal(t, 0, h.countRows(t, `SELECT count(*) FROM graph_memory_migration_redirect`))
}

// The copy: channel-owned atoms at or below the watermark gain new canonical
// ids (visibility stays channel), redirects and blob refs are recorded, the
// state completes with counts, and the old ids leave the active snapshot
// while the new ids stay searchable (tombstone-by-redirect, not rewrite).
func TestGraphMemoryChannelMigration_CopiesAtomsWithRedirects(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()

	report := h.run(t)
	assert.True(t, report.CopiedAtoms > 0, "fixture atoms must be copied, got %+v", report)
	assert.Equal(t, "completed", report.Phase)

	// Redirect + new copy for the fixture atom.
	newID, err := db.New(h.pubPool).GetGraphMemoryMigrationRedirect(h.ctx,
		db.GetGraphMemoryMigrationRedirectParams{
			WorkspaceID: h.workspace, OldKind: "atom", OldID: h.atomID})
	require.NoError(t, err)
	assert.NotEqual(t, h.atomID, newID)
	var body, visibility string
	var channel pgtype.UUID
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT body, visibility, channel_id FROM graph_memory_atom
		WHERE workspace_id=$1 AND atom_id=$2`, h.workspace, newID.NewID).Scan(&body, &visibility, &channel))
	assert.Equal(t, h.channel, channel, "the copy keeps channel ownership")
	assert.Equal(t, "channel", visibility)
	assert.NotEmpty(t, body)

	// Old ref resolves through the redirect ledger (citation redirect).
	// Old ids are tombstoned out of the active snapshot; new ids remain.
	state, err := db.New(h.pubPool).GetGraphMemoryChannelMigrationState(h.ctx,
		db.GetGraphMemoryChannelMigrationStateParams{ChannelID: h.channel, BindingGeneration: h.binding.Generation})
	require.NoError(t, err)
	assert.Equal(t, "completed", state.Phase)
	assert.Equal(t, report.CopiedAtoms, int(state.CopiedAtoms))
}

// The watermark: atoms published after the binding snapshot are NOT copied
// — new writes during the copy land in the new scope only.
func TestGraphMemoryChannelMigration_RespectsWatermark(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()
	// One more published atom after the binding was queued — recorded
	// against the channel's NEW project so the segment scope validates.
	h.publishPostBindingAtom(t, "post-binding-atom")

	var total int
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT count(*) FROM graph_memory_atom
		WHERE workspace_id=$1 AND channel_id=$2 AND visibility='channel'
		  AND publish_seq <= $3 AND atom_id NOT LIKE '%:mig%'`,
		h.workspace, h.channel, h.binding.SourceWatermark).Scan(&total))
	report := h.run(t)
	assert.Equal(t, total, report.CopiedAtoms, "only atoms at or below the watermark are copied")
	redirected := h.countRows(t, `SELECT count(*) FROM graph_memory_migration_redirect WHERE old_kind='atom'`)
	assert.Equal(t, total, redirected)
}

// Crash replay: a worker that died mid-copy leaves the state in copying
// with partial redirects; the rerun finishes without duplicating anything.
func TestGraphMemoryChannelMigration_ReplayAfterCrashIsIdempotent(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()

	// Simulate the crash: the state flips to copying but nothing is done.
	_, err := h.pubPool.Exec(h.ctx, `
		UPDATE graph_memory_channel_migration_state SET phase='copying'
		WHERE channel_id=$1 AND binding_generation=$2`, h.channel, h.binding.Generation)
	require.NoError(t, err)

	report := h.run(t)
	assert.Equal(t, "completed", report.Phase)
	// Exactly one copy per source atom — no duplicates from the replay.
	var sources, copies int
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT count(*) FROM graph_memory_atom
		WHERE workspace_id=$1 AND channel_id=$2 AND visibility='channel'
		  AND publish_seq <= $3 AND atom_id NOT LIKE '%:mig%'`,
		h.workspace, h.channel, h.binding.SourceWatermark).Scan(&sources))
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT count(*) FROM graph_memory_atom
		WHERE workspace_id=$1 AND channel_id=$2 AND visibility='channel'
		  AND atom_id LIKE '%:mig%'`, h.workspace, h.channel).Scan(&copies))
	assert.Equal(t, sources, copies, "replay must not duplicate copies")

	// A second full run is a no-op.
	again := h.run(t)
	assert.Equal(t, 0, again.CopiedAtoms)
	assert.Equal(t, "completed", again.Phase)
}

// Scope discipline: another channel's atoms and project-visible atoms are
// never copied.
func TestGraphMemoryChannelMigration_SkipsForeignScope(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()
	// A project-visible atom in the workspace (visibility=project).
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_atom (
			workspace_id, atom_id, segment_id, body, kind, tool_trust_class,
			content_hash, visibility, project_id, publish_seq
		) VALUES ($1, 'atom-project', $2, 'project knowledge', 'fact', 'none',
			'sha256:proj', 'project', $3, 1)`,
		h.workspace, h.segment, h.project)
	require.NoError(t, err)

	report := h.run(t)
	assert.Equal(t, 1, h.countRows(t, `
		SELECT count(*) FROM graph_memory_atom
		WHERE workspace_id=$1 AND visibility='project'`, h.workspace),
		"project-visible atoms are never copied or rewritten")
	_, err = db.New(h.pubPool).GetGraphMemoryMigrationRedirect(h.ctx,
		db.GetGraphMemoryMigrationRedirectParams{WorkspaceID: h.workspace, OldKind: "atom", OldID: "atom-project"})
	assert.Error(t, err, "project-visible atoms are never migrated")
	assert.True(t, report.CopiedAtoms > 0)
}

// Deletion through either canonical ref: retracting the source quarantines
// the old AND the new atom copies (redirect closure).
func TestGraphMemoryChannelMigration_DeletionCoversBothRefs(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()
	h.run(t)

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	defer tx.Rollback(h.ctx)
	require.NoError(t, NewMemoryRetractionService().RetractSourcesTx(h.ctx, tx,
		[]MemorySourceRef{h.taskRef}, "user:1", "channel deleted"))
	require.NoError(t, tx.Commit(h.ctx))

	newID, err := db.New(h.pubPool).GetGraphMemoryMigrationRedirect(h.ctx,
		db.GetGraphMemoryMigrationRedirectParams{WorkspaceID: h.workspace, OldKind: "atom", OldID: h.atomID})
	require.NoError(t, err)
	for _, atomID := range []string{h.atomID, newID.NewID} {
		assert.Equal(t, 1, h.countRows(t, `
			SELECT count(*) FROM quarantined_pending_recompute
			WHERE workspace_id=$1 AND consumer_kind='graph_memory_atom' AND consumer_id=$2`,
			h.workspace, atomID), "both canonical copies must be fenced for atom %s", atomID)
	}
}

// Non-copy guarantees: the interaction DAG, its edges, and the old
// publication ledger are not duplicated or rewritten by the migration.
func TestGraphMemoryChannelMigration_DoesNotCopyDAGOrAudit(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()
	var segments, edgesBefore, publications int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM interaction_dag_segment`).Scan(&segments))
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM interaction_dag_edge`).Scan(&edgesBefore))
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM graph_memory_publication_outcome`).Scan(&publications))

	h.run(t)

	assert.Equal(t, segments, h.countRows(t, `SELECT count(*) FROM interaction_dag_segment`),
		"segments are never copied or rewritten")
	var edgesAfter int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM interaction_dag_edge`).Scan(&edgesAfter))
	assert.Equal(t, edgesBefore, edgesAfter, "DAG edges are untouched")
	assert.Equal(t, publications, h.countRows(t, `SELECT count(*) FROM graph_memory_publication_outcome`),
		"the old graph's audit ledger is not rewritten")
}

// The graph copy: channel-visible nodes and the channel's daily nodes move
// (daily ids rebuilt for the new owner identity, with a node redirect);
// project-only nodes and cross-scope edges do not.
func TestGraphMemoryChannelMigration_CopiesGraphWithScopeDiscipline(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()
	report := h.run(t)
	require.Equal(t, "completed", report.Phase)
	assert.True(t, report.CopiedNodes >= 2, "channel fact + daily node must move, got %+v", report)
	assert.Equal(t, 1, report.CopiedEdges, "only the in-scope edge survives")

	version, err := h.newStore.CurrentVersion()
	require.NoError(t, err)
	nodes, err := h.newStore.LoadNodes(version)
	require.NoError(t, err)
	byID := map[string]*memorygraph.Node{}
	for _, n := range nodes {
		byID[n.NodeID] = n
	}
	assert.Contains(t, byID, "node-fact", "channel-visible node moves with the same id")
	newDaily := memorygraph.DailyNodeID("agent-1", h.projectB.String(), h.channel.String(), h.ctxTime())
	assert.Contains(t, byID, newDaily, "the daily node is rebuilt for the new owner identity")
	assert.NotContains(t, byID, "node-project-only", "project-only knowledge does not move")

	// The daily node's old id redirects to the new one.
	newID, err := db.New(h.pubPool).GetGraphMemoryMigrationRedirect(h.ctx,
		db.GetGraphMemoryMigrationRedirectParams{
			WorkspaceID: h.workspace, OldKind: "node",
			OldID: memorygraph.DailyNodeID("agent-1", h.project.String(), h.channel.String(), h.ctxTime())})
	require.NoError(t, err)
	assert.Equal(t, newDaily, newID.NewID)

	// AtomRefs are remapped to the new canonical atom ids.
	newAtom, err := db.New(h.pubPool).GetGraphMemoryMigrationRedirect(h.ctx,
		db.GetGraphMemoryMigrationRedirectParams{WorkspaceID: h.workspace, OldKind: "atom", OldID: h.atomID})
	require.NoError(t, err)
	require.NotNil(t, byID["node-fact"])
	assert.Equal(t, []string{newAtom.NewID}, byID["node-fact"].AtomRefs,
		"copied nodes cite the new canonical atoms")

	// The cross-scope edge was dropped: only the in-scope edge exists.
	hier, rel, err := h.newStore.LoadEdges(version)
	require.NoError(t, err)
	assert.Len(t, hier, 1)
	assert.Empty(t, rel, "cross-scope edges are dropped in the copy")
}

// RunPending drains the queue end to end.
func TestGraphMemoryChannelMigration_RunPendingDrainsQueue(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()
	reports, err := h.worker().RunPending(h.ctx, 10)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, "completed", reports[0].Phase)
	assert.Equal(t, 0, h.countRows(t, `
		SELECT count(*) FROM graph_memory_channel_migration_state WHERE phase='pending'`))
}

// Tombstone: after the copy the searchable projection holds the new
// canonical id only — the old atom never reaches a retriever, so searches
// cannot surface both copies.
func TestGraphMemoryChannelMigration_SearchTombstonesOldAtom(t *testing.T) {
	h := newMigrationHarness(t)
	defer h.Close()
	h.run(t)

	atoms, _, retracted, err := LoadActiveAtomSnapshot(h.ctx, h.pubPool, h.workspace, util.UUIDToString(h.channel), 64)
	require.NoError(t, err)

	newID := h.atomID + ":mig" + strconv.FormatInt(h.binding.Generation, 10)
	ids := map[string]bool{}
	for _, atom := range atoms {
		ids[atom.AtomID] = true
	}
	assert.False(t, ids[h.atomID], "old id must be tombstoned out of the snapshot")
	assert.True(t, ids[newID], "new canonical id must be searchable")
	assert.True(t, retracted[h.atomID], "old id is re-asserted through the exclusion set")
	assert.False(t, retracted[newID], "the new copy stays searchable")
}
