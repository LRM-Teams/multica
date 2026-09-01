// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Channel-owned Graph migration worker (plan Task 16, spec §12): drains the
// copy ledger the binding service queues. Each migration copies the
// channel's own artifacts to the new write owner — channel-owned active
// atoms (new canonical ids, visibility unchanged), channel-visible graph
// nodes, the channel's daily nodes (ids rebuilt for the new owner), and
// edges whose endpoints both moved. Blob bytes are never duplicated (the
// ledger gains refs), the interaction DAG / training / audit ledgers are
// never copied or rewritten, and old canonical refs become unsearchable
// tombstones that resolve through the redirect ledger.

// ErrChannelMigrationDisabled is the red-gate refusal: the copy route is
// DB-default OFF until an operator approves it.
var ErrChannelMigrationDisabled = errors.New("channel migration gate disabled")

// GraphMemoryChannelMigrationReport is one executed migration.
type GraphMemoryChannelMigrationReport struct {
	WorkspaceID       string
	ChannelID         string
	BindingGeneration int64
	Phase             string
	CopiedAtoms       int
	CopiedNodes       int
	CopiedEdges       int
}

// migrationBatchLimit bounds one atom-copy pass; replay drains the rest.
const migrationBatchLimit = 512

type GraphMemoryChannelMigrationService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryChannelMigrationService(pool *pgxpool.Pool) *GraphMemoryChannelMigrationService {
	return &GraphMemoryChannelMigrationService{pool: pool}
}

// RunPending drains up to limit queued (or stuck-copying) migrations.
func (s *GraphMemoryChannelMigrationService) RunPending(ctx context.Context, limit int32) ([]GraphMemoryChannelMigrationReport, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("channel migration service not configured")
	}
	if limit <= 0 {
		limit = 1
	}
	states, err := db.New(s.pool).ListGraphMemoryChannelMigrationsByPhase(ctx, db.ListGraphMemoryChannelMigrationsByPhaseParams{
		Phases: []string{"pending", "copying"}, LimitCount: limit,
	})
	if err != nil {
		return nil, err
	}
	reports := make([]GraphMemoryChannelMigrationReport, 0, len(states))
	for _, state := range states {
		report, err := s.MigrateChannel(ctx, state.WorkspaceID, state.ChannelID, state.BindingGeneration)
		if err != nil {
			if errors.Is(err, ErrChannelMigrationDisabled) {
				continue // red gate claims nothing, not even an error
			}
			return reports, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// MigrateChannel executes (or replays) one migration idempotently: atom
// copies are keyed by the redirect ledger, the graph copy by a
// migration-stamped version, and a completed state is a no-op.
func (s *GraphMemoryChannelMigrationService) MigrateChannel(
	ctx context.Context, workspaceID, channelID pgtype.UUID, bindingGeneration int64,
) (GraphMemoryChannelMigrationReport, error) {
	if s == nil || s.pool == nil {
		return GraphMemoryChannelMigrationReport{}, errors.New("channel migration service not configured")
	}
	report := GraphMemoryChannelMigrationReport{
		WorkspaceID: workspaceID.String(), ChannelID: channelID.String(),
		BindingGeneration: bindingGeneration,
	}
	open, err := db.New(s.pool).ChannelMigrationGateOpen(ctx, workspaceID)
	if err != nil {
		return report, err
	}
	if !open {
		return report, ErrChannelMigrationDisabled
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)
	state, err := q.GetGraphMemoryChannelMigrationStateForUpdate(ctx, db.GetGraphMemoryChannelMigrationStateForUpdateParams{
		ChannelID: channelID, BindingGeneration: bindingGeneration,
	})
	if err != nil {
		return report, fmt.Errorf("channel migration state: %w", err)
	}
	if state.Phase == "completed" {
		// Already-finished migration: nothing to copy this run. The report
		// counts what THIS invocation copied, so totals stay zero.
		report.Phase = "completed"
		return report, nil
	}
	binding, err := q.GetGraphMemoryChannelBindingByGeneration(ctx, db.GetGraphMemoryChannelBindingByGenerationParams{
		ChannelID: channelID, Generation: bindingGeneration,
	})
	if err != nil {
		return report, fmt.Errorf("channel migration binding: %w", err)
	}
	if err := q.ClaimGraphMemoryChannelMigration(ctx, db.ClaimGraphMemoryChannelMigrationParams{
		ChannelID: channelID, BindingGeneration: bindingGeneration,
	}); err != nil {
		return report, err
	}

	// Atom copies: per-atom atomic flip — the new row and the old id's
	// redirect land in one transaction, so readers never see both.
	atomRedirects, copiedAtoms, err := s.copyAtoms(ctx, q, workspaceID, channelID, bindingGeneration, binding.SourceWatermark)
	if err != nil {
		s.markAborted(ctx, channelID, bindingGeneration, err)
		return report, err
	}

	// Graph copy: channel-visible nodes + the channel's daily nodes, edges
	// with both endpoints in scope, into a migration-stamped version of the
	// new owner's store.
	copiedNodes, copiedEdges, err := s.copyGraph(ctx, workspaceID, channelID, binding, bindingGeneration, atomRedirects)
	if err != nil {
		s.markAborted(ctx, channelID, bindingGeneration, err)
		return report, err
	}

	if err := q.FinishGraphMemoryChannelMigration(ctx, db.FinishGraphMemoryChannelMigrationParams{
		ChannelID: channelID, BindingGeneration: bindingGeneration,
		CopiedAtoms: int32(copiedAtoms), CopiedNodes: int32(copiedNodes), CopiedEdges: int32(copiedEdges),
	}); err != nil {
		return report, err
	}
	if err := tx.Commit(ctx); err != nil {
		return report, err
	}
	report.Phase, report.CopiedAtoms, report.CopiedNodes, report.CopiedEdges =
		"completed", copiedAtoms, copiedNodes, copiedEdges
	return report, nil
}

// copyAtoms copies channel-owned active atoms at or below the watermark.
// Already-redirected ids are skipped, so a crash replay never duplicates.
func (s *GraphMemoryChannelMigrationService) copyAtoms(
	ctx context.Context, q *db.Queries, workspaceID, channelID pgtype.UUID,
	bindingGeneration, watermark int64,
) (map[string]string, int, error) {
	redirects := map[string]string{}
	copied := 0
	for {
		rows, err := q.ListGraphMemoryChannelAtomsForMigration(ctx, db.ListGraphMemoryChannelAtomsForMigrationParams{
			WorkspaceID: workspaceID, ChannelID: channelID,
			PublishSeq: watermark, Limit: migrationBatchLimit,
		})
		if err != nil {
			return nil, 0, err
		}
		if len(rows) == 0 {
			return redirects, copied, nil
		}
		for _, atom := range rows {
			newID := fmt.Sprintf("%s:mig%d", atom.AtomID, bindingGeneration)
			if err := q.InsertMigratedGraphMemoryAtom(ctx, db.InsertMigratedGraphMemoryAtomParams{
				WorkspaceID: workspaceID, AtomID: newID, SegmentID: atom.SegmentID,
				Body: atom.Body, Kind: atom.Kind, SourceMessageSeqs: atom.SourceMessageSeqs,
				SourceTool: atom.SourceTool, ToolTrustClass: atom.ToolTrustClass,
				ContentHash: atom.ContentHash, ChannelID: channelID, PublishSeq: atom.PublishSeq,
			}); err != nil {
				return nil, 0, fmt.Errorf("copy atom %s: %w", atom.AtomID, err)
			}
			if err := q.UpsertGraphMemoryMigrationRedirect(ctx, db.UpsertGraphMemoryMigrationRedirectParams{
				WorkspaceID: workspaceID, OldKind: "atom", OldID: atom.AtomID,
				NewKind: "atom", NewID: newID, BindingGeneration: bindingGeneration,
			}); err != nil {
				return nil, 0, err
			}
			if atom.ArtifactRef.Valid && atom.ArtifactRef.String != "" {
				if err := q.InsertGraphMemoryMigrationBlobRef(ctx, db.InsertGraphMemoryMigrationBlobRefParams{
					WorkspaceID: workspaceID, ChannelID: channelID,
					BindingGeneration: bindingGeneration, BlobRef: atom.ArtifactRef.String,
				}); err != nil {
					return nil, 0, err
				}
			}
			redirects[atom.AtomID] = newID
			copied++
		}
		if len(rows) < migrationBatchLimit {
			return redirects, copied, nil
		}
	}
}

// copyGraph moves the channel's graph artifacts between store scopes. A
// crash replay finds the migration-stamped version instead of stacking a
// second candidate.
func (s *GraphMemoryChannelMigrationService) copyGraph(
	ctx context.Context, workspaceID, channelID pgtype.UUID,
	binding db.GraphMemoryChannelBinding, bindingGeneration int64, atomRedirects map[string]string,
) (int, int, error) {
	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return 0, 0, err
	}
	oldKind, oldOwner := migrationOldScope(binding, channelID)
	oldDir, err := memorygraph.DirForScope(root, workspaceID.String(), oldKind, oldOwner)
	if err != nil {
		return 0, 0, err
	}
	oldStore := memorygraph.NewStore(oldDir)
	if err := oldStore.Init(); err != nil {
		return 0, 0, err
	}
	newDir, err := memorygraph.EnsureScopedDir(root, workspaceID.String(),
		memorygraph.GraphDirKind(binding.RouteKind), binding.RouteOwnerID.String())
	if err != nil {
		return 0, 0, err
	}
	newStore := memorygraph.NewStore(newDir)
	if err := newStore.Init(); err != nil {
		return 0, 0, err
	}

	// Replay: a migration-stamped version already exists — nothing to copy.
	stamp := migrationVersionStamp(bindingGeneration)
	if version := findMigrationVersion(newStore, stamp); version > 0 {
		nodes, edges := countMigrationCopy(newStore, version)
		return nodes, edges, nil
	}
	oldVersion, err := oldStore.CurrentVersion()
	if err != nil {
		return 0, 0, err
	}
	nodes, err := oldStore.LoadNodes(oldVersion)
	if err != nil {
		return 0, 0, err
	}
	hier, rel, err := oldStore.LoadEdges(oldVersion)
	if err != nil {
		return 0, 0, err
	}

	targetProject := ""
	if binding.RouteKind == "project" {
		targetProject = binding.RouteOwnerID.String()
	}
	channelStr := channelID.String()
	copiedIDs := map[string]bool{}
	var copiedNodes []*memorygraph.Node
	nodeRedirect := map[string]string{}
	for _, node := range nodes {
		migrated, ok := migrateNodeForChannel(node, channelStr, targetProject, atomRedirects)
		if !ok {
			continue
		}
		copiedIDs[migrated.NodeID] = true
		if migrated.NodeID != node.NodeID {
			nodeRedirect[node.NodeID] = migrated.NodeID
		}
		copiedNodes = append(copiedNodes, migrated)
	}
	if len(copiedNodes) == 0 {
		return 0, 0, nil
	}
	candidate, err := newStore.CreateVersionFrom(latestVersionOr(newStore, oldVersion), memorygraph.CreatorMigration)
	if err != nil {
		return 0, 0, err
	}
	for _, node := range copiedNodes {
		if err := newStore.SaveNode(candidate, node); err != nil {
			return 0, 0, err
		}
	}
	var copiedHier, copiedRel []*memorygraph.Edge
	for _, edge := range append(hier, rel...) {
		from, to := edge.From, edge.To
		if nodeRedirect[from] != "" {
			from = nodeRedirect[from]
		}
		if nodeRedirect[to] != "" {
			to = nodeRedirect[to]
		}
		if !copiedIDs[from] || !copiedIDs[to] {
			continue // cross-scope edges never move (spec §12)
		}
		clone := *edge
		clone.From, clone.To = from, to
		if strings.HasPrefix(edge.Type, "summarizes") {
			copiedHier = append(copiedHier, &clone)
		} else {
			copiedRel = append(copiedRel, &clone)
		}
	}
	if err := newStore.SaveEdges(candidate, copiedHier, copiedRel); err != nil {
		return 0, 0, err
	}
	if err := newStore.SaveManifest(candidate, &memorygraph.Manifest{
		Version: candidate, ParentVersion: oldVersion, CreatedBy: memorygraph.CreatorMigration,
		Notes: stamp, NodeCount: len(copiedNodes),
		HierEdgeCount: len(copiedHier), RelEdgeCount: len(copiedRel),
	}); err != nil {
		return 0, 0, err
	}
	if err := newStore.SwitchCurrent(candidate); err != nil {
		return 0, 0, err
	}

	// Node redirects (daily-node identity changes) land after the version
	// is durable; atom redirects already committed with their rows.
	qtx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = qtx.Rollback(ctx) }()
	q := db.New(qtx)
	for oldID, newID := range nodeRedirect {
		if err := q.UpsertGraphMemoryMigrationRedirect(ctx, db.UpsertGraphMemoryMigrationRedirectParams{
			WorkspaceID: workspaceID, OldKind: "node", OldID: oldID,
			NewKind: "node", NewID: newID, BindingGeneration: bindingGeneration,
		}); err != nil {
			return 0, 0, err
		}
	}
	if err := qtx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return len(copiedNodes), len(copiedHier) + len(copiedRel), nil
}

// migrationOldScope resolves the pre-switch write owner of one binding:
// the old project's graph when the channel was bound, else the channel's
// own graph.
func migrationOldScope(binding db.GraphMemoryChannelBinding, channelID pgtype.UUID) (memorygraph.GraphDirKind, string) {
	if binding.OldProjectID.Valid {
		return memorygraph.GraphDirKindProject, binding.OldProjectID.String()
	}
	return memorygraph.GraphDirKindChannel, channelID.String()
}

// migrateNodeForChannel decides whether one node belongs to the migrating
// channel and rebuilds it for the new owner identity: channel-visible
// channel nodes keep their id (AtomRefs remapped); the channel's daily
// nodes get a new id for the target scope; everything else stays.
func migrateNodeForChannel(node *memorygraph.Node, channelID, targetProject string, atomRedirects map[string]string) (*memorygraph.Node, bool) {
	if node.Visibility == "channel" && node.ChannelID == channelID {
		clone := *node
		// A daily node's id embeds its owning scope (spec §6): the id is
		// rebuilt for the target identity even though the node itself is
		// channel-visible.
		if migrated, ok := remapDailyNodeID(node.NodeID, channelID, targetProject); ok {
			clone.NodeID = migrated
		}
		clone.AtomRefs = remapAtomRefs(node.AtomRefs, atomRedirects)
		return &clone, true
	}
	// Daily nodes that predate visibility tagging still carry the channel
	// in their id; they belong to the channel and move with it.
	if migrated, ok := remapDailyNodeID(node.NodeID, channelID, targetProject); ok {
		clone := *node
		clone.NodeID = migrated
		clone.Visibility = "channel"
		clone.ChannelID = channelID
		clone.AtomRefs = remapAtomRefs(node.AtomRefs, atomRedirects)
		return &clone, true
	}
	return nil, false
}

// remapDailyNodeID rebuilds a daily node id for the target scope when the
// id belongs to the migrating channel (spec §6 identity).
func remapDailyNodeID(nodeID, channelID, targetProject string) (string, bool) {
	if !strings.HasPrefix(nodeID, "daily:") {
		return "", false
	}
	parts := strings.Split(nodeID, ":")
	if len(parts) != 5 {
		return "", false
	}
	agent, oldProject, oldChannel, day := parts[1], parts[2], parts[3], parts[4]
	if oldChannel != channelID {
		return "", false
	}
	newProject := targetProject
	if newProject == "" {
		newProject = "none"
	}
	if newProject == oldProject {
		return "", false // already the target identity: nothing to rename
	}
	return fmt.Sprintf("daily:%s:%s:%s:%s", agent, newProject, oldChannel, day), true
}

func remapAtomRefs(refs []string, redirects map[string]string) []string {
	if len(refs) == 0 {
		return refs
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if mapped, ok := redirects[ref]; ok {
			out = append(out, mapped)
		} else {
			out = append(out, ref)
		}
	}
	return out
}

func migrationVersionStamp(bindingGeneration int64) string {
	return "channel-migration:" + strconv.FormatInt(bindingGeneration, 10)
}

func findMigrationVersion(store *memorygraph.Store, stamp string) int {
	versions, err := store.ListVersions()
	if err != nil {
		return 0
	}
	for _, version := range versions {
		manifest, err := store.LoadManifest(version)
		if err == nil && manifest.Notes == stamp {
			return version
		}
	}
	return 0
}

func countMigrationCopy(store *memorygraph.Store, version int) (int, int) {
	nodes, err := store.LoadNodes(version)
	if err != nil {
		return 0, 0
	}
	hier, rel, err := store.LoadEdges(version)
	if err != nil {
		return len(nodes), 0
	}
	return len(nodes), len(hier) + len(rel)
}

func latestVersionOr(store *memorygraph.Store, fallback int) int {
	if version, err := store.CurrentVersion(); err == nil && version > 0 {
		return version
	}
	return fallback
}

func (s *GraphMemoryChannelMigrationService) markAborted(
	ctx context.Context, channelID pgtype.UUID, bindingGeneration int64, cause error,
) {
	_, _ = s.pool.Exec(ctx, `
		UPDATE graph_memory_channel_migration_state
		SET phase='aborted', error=$3, updated_at=now()
		WHERE channel_id=$1 AND binding_generation=$2`,
		channelID, bindingGeneration, cause.Error())
}
