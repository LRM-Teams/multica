// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// atomSnapshotLimit bounds the active-atom snapshot installed into a
// retriever (recall seeding and the external v2 search). The atom ledger is
// workspace-bounded by construction; this is a defensive ceiling.
const atomSnapshotLimit = 512

// LoadActiveAtomSnapshot resolves the active atom ledger of one workspace
// scope (channel exact-match, or project-scoped atoms when channelID is
// empty) at the workspace's current publish watermark. Retracted atoms are
// excluded by the Task 8A quarantine closure and also returned as the
// re-assertion set for InstallAtomSnapshot, so the retriever's local
// exclusions stay real rather than trusting the loader alone.
func LoadActiveAtomSnapshot(
	ctx context.Context, pool *pgxpool.Pool, workspaceID pgtype.UUID, channelID string, limit int32,
) (atoms []memorygraph.AtomDoc, publishSeqMax int64, retracted map[string]bool, err error) {
	if pool == nil {
		return nil, 0, nil, fmt.Errorf("atom snapshot loader requires a pool")
	}
	if limit <= 0 {
		limit = atomSnapshotLimit
	}
	q := db.New(pool)
	publishSeqMax, err = q.MaxAtomPublishSeq(ctx, workspaceID)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("atom snapshot watermark: %w", err)
	}
	rows, err := q.ListActiveAtomSnapshot(ctx, db.ListActiveAtomSnapshotParams{
		WorkspaceID: workspaceID, PublishSeqMax: publishSeqMax,
		ScopeChannelID: channelID, LimitRows: limit,
	})
	if err != nil {
		return nil, 0, nil, fmt.Errorf("atom snapshot rows: %w", err)
	}
	atoms = make([]memorygraph.AtomDoc, 0, len(rows))
	for _, row := range rows {
		createdAt := time.Time{}
		if row.CreatedAt.Valid {
			createdAt = row.CreatedAt.Time
		}
		atoms = append(atoms, memorygraph.AtomDoc{
			AtomID: row.AtomID, SegmentID: row.SegmentID, Body: row.Body,
			PublishSeq: row.AtomPublishSeq, CreatedAt: createdAt, ChannelID: row.ChannelID,
		})
	}
	ids, err := q.ListQuarantinedAtomIDs(ctx, workspaceID)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("atom snapshot retraction set: %w", err)
	}
	retracted = make(map[string]bool, len(ids))
	for _, id := range ids {
		retracted[id] = true
	}
	// Task 16 tombstone: once a channel migration copied an atom, the old
	// id is no longer canonical — the "<atom>:mig<gen>" copy is. Redirected
	// ids are dropped from the snapshot and re-asserted through the
	// exclusion set, so no retriever can surface both copies. Schemas
	// without the redirect table (pre-migration harnesses) leave the
	// snapshot untouched.
	if len(atoms) > 0 {
		snapshotIDs := make([]string, 0, len(atoms))
		for _, atom := range atoms {
			snapshotIDs = append(snapshotIDs, atom.AtomID)
		}
		if oldIDs, rErr := q.ListGraphMemoryMigrationRedirectOldIDs(ctx, db.ListGraphMemoryMigrationRedirectOldIDsParams{
			WorkspaceID: workspaceID, OldKind: "atom", OldIds: snapshotIDs,
		}); rErr == nil && len(oldIDs) > 0 {
			tombstoned := make(map[string]bool, len(oldIDs))
			for _, id := range oldIDs {
				tombstoned[id] = true
			}
			kept := atoms[:0]
			for _, atom := range atoms {
				if tombstoned[atom.AtomID] {
					retracted[atom.AtomID] = true
					continue
				}
				kept = append(kept, atom)
			}
			atoms = kept
		}
	}
	return atoms, publishSeqMax, retracted, nil
}

// RecallShadowComparison is the aggregate-only comparison recorded while the
// atom_search gate is red: counters, never query text, ids or bodies.
type RecallShadowComparison struct {
	Adopted  bool
	Legacy   int
	Graph    int
	AtomHits int
}

// Describe renders the counters for logs (aggregate only by construction).
func (c RecallShadowComparison) Describe() string {
	return fmt.Sprintf("adopted=%t legacy=%d graph=%d atoms=%d", c.Adopted, c.Legacy, c.Graph, c.AtomHits)
}

// GraphMemoryAtomSearchSeeder is the Task 13 production seed retriever: the
// legacy hybrid retriever, plus the class-aware SearchAt channel adopted
// only while the workspace's atom_search gate is green. With the gate red
// (or no pool wired) it is behaviorally identical to GraphMemoryHybridSeeder
// — the legacy inject/v1 semantics are preserved — and records only the
// aggregate shadow comparison.
type GraphMemoryAtomSearchSeeder struct {
	pool *pgxpool.Pool

	mu     sync.Mutex
	shadow RecallShadowComparison
}

// NewGraphMemoryAtomSearchSeeder constructs the gated seeder. A nil pool
// degrades to the legacy seeder (used by CLI/test wiring without a DB).
func NewGraphMemoryAtomSearchSeeder(pool *pgxpool.Pool) *GraphMemoryAtomSearchSeeder {
	return &GraphMemoryAtomSearchSeeder{pool: pool}
}

// LastShadowComparison returns the most recent aggregate comparison.
func (s *GraphMemoryAtomSearchSeeder) LastShadowComparison() RecallShadowComparison {
	if s == nil {
		return RecallShadowComparison{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shadow
}

func (s *GraphMemoryAtomSearchSeeder) recordShadow(c RecallShadowComparison) {
	s.mu.Lock()
	s.shadow = c
	s.mu.Unlock()
}

// Seeds resolves the round-0 candidates. Gate red: identical to the legacy
// hybrid seeder. Gate green: SearchAt over the graph channel (current nodes
// only) fused with the active-atom staging channel; the returned seeds are
// graph node ids (the explorer walks graph nodes — staging ids never seed
// the walk). Any snapshot/gate read failure falls back to the legacy path:
// recall stays failure-nonfatal.
func (s *GraphMemoryAtomSearchSeeder) Seeds(
	ctx context.Context, workspaceID, dir string, version int, query string, view memorygraph.GraphView,
) ([]string, error) {
	legacyIDs, legacyErr := GraphMemoryHybridSeeder{}.Seeds(ctx, workspaceID, dir, version, query, view)
	if legacyErr != nil {
		return nil, legacyErr
	}
	if s == nil || s.pool == nil {
		return legacyIDs, nil
	}
	wsUUID, err := parseAtomSeederWorkspace(workspaceID)
	if err != nil {
		return legacyIDs, nil
	}
	enabled, err := NewMemoryReadGate(db.New(s.pool)).RouteEnabled(ctx, wsUUID, MemoryRouteAtoms)
	if err != nil || !enabled {
		// Shadow-disabled: aggregate comparison only, never content.
		s.recordShadow(RecallShadowComparison{Legacy: len(legacyIDs)})
		return legacyIDs, nil
	}
	atoms, watermark, retracted, err := LoadActiveAtomSnapshot(ctx, s.pool, wsUUID, view.ChannelID, atomSnapshotLimit)
	if err != nil {
		// Failure-nonfatal: adoption must never break recall.
		return legacyIDs, nil
	}
	retr, err := newAtomScopedRetriever(ctx, dir, version, view, atoms, watermark, retracted)
	if err != nil {
		return legacyIDs, nil
	}
	hits, err := retr.SearchAt(ctx, query, view, watermark)
	if err != nil {
		return legacyIDs, nil
	}
	seeds := make([]string, 0, len(hits))
	atomHits := 0
	for _, hit := range hits {
		if hit.Class == memorygraph.SearchGraphNode {
			seeds = append(seeds, hit.Ref.NodeID)
		} else {
			atomHits++
		}
	}
	s.recordShadow(RecallShadowComparison{Adopted: true, Legacy: len(legacyIDs), Graph: len(seeds), AtomHits: atomHits})
	return seeds, nil
}

// newAtomScopedRetriever builds a retriever over the pinned version with the
// active atom snapshot installed (shared by the seeder, the recall executor
// and the external v2 search).
func newAtomScopedRetriever(
	ctx context.Context, dir string, version int, view memorygraph.GraphView,
	atoms []memorygraph.AtomDoc, watermark int64, retracted map[string]bool,
) (*memorygraph.HybridRetriever, error) {
	store := memorygraph.NewStore(dir)
	cfg := memorygraph.DefaultRetrievalConfig()
	cfg.View = view
	retr := memorygraph.NewHybridRetriever(store, nil, cfg)
	if err := retr.RebuildForVersion(ctx, version); err != nil {
		return nil, err
	}
	retr.InstallAtomSnapshot(atoms, watermark, retracted)
	return retr, nil
}

// parseAtomSeederWorkspace parses the canonical workspace identity; an
// unparseable id keeps the seeder on the legacy path (failure-nonfatal).
func parseAtomSeederWorkspace(workspaceID string) (pgtype.UUID, error) {
	return parseUUIDColumn("workspace_id", workspaceID)
}

// installActiveAtomSnapshotIfAdopted installs the active-atom ledger into a
// built retriever only while the workspace's atom_search gate is green; gate
// reads and snapshot failures are silently non-fatal (legacy graph-only
// behavior).
func installActiveAtomSnapshotIfAdopted(
	ctx context.Context, pool *pgxpool.Pool, workspaceID pgtype.UUID, channelID string, retr *memorygraph.HybridRetriever,
) {
	if pool == nil {
		return
	}
	enabled, err := NewMemoryReadGate(db.New(pool)).RouteEnabled(ctx, workspaceID, MemoryRouteAtoms)
	if err != nil || !enabled {
		return
	}
	atoms, watermark, retracted, err := LoadActiveAtomSnapshot(ctx, pool, workspaceID, channelID, atomSnapshotLimit)
	if err != nil {
		return
	}
	retr.InstallAtomSnapshot(atoms, watermark, retracted)
}
