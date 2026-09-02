// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// channelBindingHarness: the explore-v2 mini-schema plus the Task 16
// migration layer (route tables from 392 + migration 470's binding guard,
// copy ledger, redirect ledger, and phase gate) and a second project in the
// fixture workspace so A→B rebinds are exercisable.
type channelBindingHarness struct {
	*exploreV2Harness
	projectB pgtype.UUID
}

// applyGraphMemoryChannelMigration applies migration 470 in the harness's
// private schema. The route tables (upstream migration 392) are not part of
// the service mini-schema, so they are created here first — production
// already has them. The gate column doubling as the applied marker keeps
// repeat harness layers safe.
func applyGraphMemoryChannelMigration(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	if _, err := conn.Exec(ctx, `
		ALTER TABLE channel ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
		CREATE TABLE IF NOT EXISTS graph_memory_channel_route (
		  workspace_id          uuid        NOT NULL,
		  channel_id            uuid        NOT NULL PRIMARY KEY,
		  routing_mode          text        NOT NULL CHECK (routing_mode IN ('standalone', 'project_lineage')),
		  current_graph_kind    text        NOT NULL CHECK (current_graph_kind IN ('project', 'channel')),
		  current_graph_owner_id uuid       NOT NULL,
		  generation            bigint      NOT NULL,
		  created_at            timestamptz NOT NULL DEFAULT now(),
		  updated_at            timestamptz NOT NULL DEFAULT now()
		);
		CREATE TABLE IF NOT EXISTS graph_memory_channel_lineage (
		  workspace_id   uuid        NOT NULL,
		  channel_id     uuid        NOT NULL,
		  generation     bigint      NOT NULL,
		  graph_kind     text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
		  graph_owner_id uuid        NOT NULL,
		  valid_from     timestamptz NOT NULL DEFAULT now(),
		  valid_to       timestamptz,
		  PRIMARY KEY (channel_id, generation)
		);`); err != nil {
		t.Fatalf("create route tables in private schema: %v", err)
	}
	var applied bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'memory_read_phase_gate'
			  AND column_name = 'channel_migration_enabled')`).Scan(&applied); err != nil {
		t.Fatalf("probe migration 470 marker: %v", err)
	}
	if applied {
		return
	}
	if _, err := conn.Exec(ctx, graphMemoryChannelMigrationDDL); err != nil {
		t.Fatalf("apply migration 470 in private schema: %v", err)
	}
}

// graphMemoryChannelMigrationDDL is the body of migration 470 (the guard
// trigger, ledgers, and gate column), kept in sync with
// migrations/470_graph_memory_channel_migration.up.sql for private-schema
// harnesses.
const graphMemoryChannelMigrationDDL = `
ALTER TABLE memory_read_phase_gate
    ADD COLUMN channel_migration_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE memory_read_phase_gate
    DROP CONSTRAINT memory_read_phase_gate_transition_check;
ALTER TABLE memory_read_phase_gate
    ADD CONSTRAINT memory_read_phase_gate_transition_check CHECK (
        (atoms_enabled OR search_v2_enabled OR explore_enabled
         OR citations_enabled OR atom_consolidation_enabled
         OR channel_migration_enabled)
        <= retraction_canary_ok
    );
CREATE TABLE graph_memory_channel_binding (
    id             uuid NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid NOT NULL,
    channel_id     uuid NOT NULL,
    generation     bigint NOT NULL CHECK (generation > 0),
    old_project_id uuid,
    new_project_id uuid,
    route_kind     text NOT NULL CHECK (route_kind IN ('project', 'channel')),
    route_owner_id uuid NOT NULL,
    route_generation bigint NOT NULL,
    source_watermark bigint NOT NULL DEFAULT 0,
    actor          text NOT NULL DEFAULT '',
    txid           bigint NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT graph_memory_channel_binding_gen_unique UNIQUE (channel_id, generation)
);
CREATE INDEX graph_memory_channel_binding_channel_idx
    ON graph_memory_channel_binding (channel_id, generation DESC);
CREATE TABLE graph_memory_channel_migration_state (
    workspace_id uuid NOT NULL,
    channel_id   uuid NOT NULL,
    binding_generation bigint NOT NULL,
    phase        text NOT NULL CHECK (phase IN ('pending', 'copying', 'completed', 'aborted')),
    source_watermark bigint NOT NULL DEFAULT 0,
    copied_atoms  integer NOT NULL DEFAULT 0,
    copied_nodes  integer NOT NULL DEFAULT 0,
    copied_edges  integer NOT NULL DEFAULT 0,
    error         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, binding_generation)
);
CREATE INDEX graph_memory_channel_migration_pending_idx
    ON graph_memory_channel_migration_state (phase, created_at)
    WHERE phase IN ('pending', 'copying');
CREATE TABLE graph_memory_migration_redirect (
    workspace_id uuid NOT NULL,
    old_kind     text NOT NULL CHECK (old_kind IN ('atom', 'node', 'edge')),
    old_id       text NOT NULL,
    new_kind     text NOT NULL CHECK (new_kind IN ('atom', 'node', 'edge')),
    new_id       text NOT NULL,
    binding_generation bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, old_kind, old_id),
    CONSTRAINT graph_memory_migration_redirect_pair CHECK (old_kind != new_kind OR old_id != new_id)
);
CREATE INDEX graph_memory_migration_redirect_new_idx
    ON graph_memory_migration_redirect (workspace_id, new_kind, new_id);
CREATE TABLE graph_memory_migration_blob_ref (
    workspace_id uuid NOT NULL,
    channel_id   uuid NOT NULL,
    binding_generation bigint NOT NULL,
    blob_ref     text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, channel_id, binding_generation, blob_ref)
);
CREATE OR REPLACE FUNCTION graph_memory_channel_binding_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $guard$
BEGIN
    IF NEW.project_id IS DISTINCT FROM OLD.project_id THEN
        IF NOT EXISTS (
            SELECT 1 FROM graph_memory_channel_binding b
            WHERE b.channel_id = NEW.id
              AND b.txid = pg_current_xact_id()::text::bigint
        ) THEN
            RAISE EXCEPTION
                'channel project binding must go through ChannelProjectBindingService (channel %)',
                NEW.id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$guard$;
CREATE TRIGGER graph_memory_channel_binding_guard_trigger
BEFORE UPDATE OF project_id ON channel
FOR EACH ROW EXECUTE FUNCTION graph_memory_channel_binding_guard();
`

func newChannelBindingHarness(t *testing.T) *channelBindingHarness {
	t.Helper()
	h := &channelBindingHarness{exploreV2Harness: newExploreV2Harness(t, false)}
	applyGraphMemoryChannelMigration(t, h.ctx, h.conn)
	h.projectB = pgtype.UUID{Bytes: [16]byte{2, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 2}, Valid: true}
	_, err := h.conn.Exec(h.ctx, `INSERT INTO project(id,workspace_id) VALUES ($1,$2)`,
		h.projectB, h.workspace)
	require.NoError(t, err)
	return h
}

func (h *channelBindingHarness) binding() *ChannelProjectBindingService {
	return NewChannelProjectBindingService(h.pubPool)
}

func (h *channelBindingHarness) bind(t *testing.T, project pgtype.UUID) ChannelProjectBindingResult {
	t.Helper()
	result, err := h.binding().SetChannelProject(h.ctx, ChannelProjectBindingParams{
		WorkspaceID: h.workspace, ChannelID: h.channel,
		NewProjectID: project, Actor: "binding-test",
	})
	require.NoError(t, err)
	return result
}

func (h *channelBindingHarness) routeRow(t *testing.T) (mode, kind, owner string, generation int64) {
	t.Helper()
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT routing_mode, current_graph_kind, current_graph_owner_id::text, generation
		FROM graph_memory_channel_route WHERE channel_id=$1`, h.channel).
		Scan(&mode, &kind, &owner, &generation))
	return
}

func (h *channelBindingHarness) currentChannelProject(t *testing.T) pgtype.UUID {
	t.Helper()
	var pid pgtype.UUID
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT project_id FROM channel WHERE id=$1`, h.channel).Scan(&pid))
	return pid
}

// The settings A→B rebind (the fixture channel ships bound to project A):
// the rebind CAS-advances the binding generation, opens the project-B route
// with a queued migration at the captured watermark, and re-rebinding back
// to A closes B's lineage and opens A's.
func TestChannelProjectBinding_SettingsRebindAtoB(t *testing.T) {
	h := newChannelBindingHarness(t)
	defer h.Close()
	watermark := int64(0)
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT COALESCE(MAX(publish_seq),0) FROM graph_memory_atom WHERE channel_id=$1`,
		h.channel).Scan(&watermark))
	require.Positive(t, watermark, "fixture must publish channel atoms first")

	rebind := h.bind(t, h.projectB)
	assert.Equal(t, int64(1), rebind.Generation, "the first service-mediated change is generation 1")
	assert.Equal(t, h.projectB, h.currentChannelProject(t))
	assert.True(t, rebind.RouteOwnerMoved)
	assert.True(t, rebind.MigrationPending)
	assert.Equal(t, watermark, rebind.SourceWatermark)
	mode, kind, owner, gen := h.routeRow(t)
	assert.Equal(t, "project_lineage", mode)
	assert.Equal(t, "project", kind)
	assert.Equal(t, h.projectB.String(), owner)
	assert.Equal(t, int64(1), gen)
	// A migration is queued at the captured watermark.
	assert.Equal(t, 1, h.countRows(t, `
		SELECT count(*) FROM graph_memory_channel_migration_state
		WHERE channel_id=$1 AND binding_generation=1 AND phase='pending'
		  AND source_watermark=$2`, h.channel, watermark))

	back := h.bind(t, h.project)
	assert.Equal(t, int64(2), back.Generation)
	assert.Equal(t, h.project, h.currentChannelProject(t))
	_, kind, owner, gen = h.routeRow(t)
	assert.Equal(t, "project", kind)
	assert.Equal(t, h.project.String(), owner)
	assert.Equal(t, int64(2), gen)
	// B's lineage generation is closed, A's re-opened.
	assert.Equal(t, 1, h.countRows(t, `
		SELECT count(*) FROM graph_memory_channel_lineage
		WHERE channel_id=$1 AND generation=1 AND valid_to IS NOT NULL`, h.channel))
	assert.Equal(t, 1, h.countRows(t, `
		SELECT count(*) FROM graph_memory_channel_lineage
		WHERE channel_id=$1 AND generation=2 AND valid_to IS NULL`, h.channel))
	assert.Equal(t, 2, h.countRows(t, `
		SELECT count(*) FROM graph_memory_channel_binding WHERE channel_id=$1`, h.channel))
}

// Unbind from a live project-lineage route opens the temporary channel-owned
// generation and queues a migration back to the channel graph. (Unbinding a
// channel whose route was never resolved lands in permanent standalone
// instead — pinned by the standalone test.)
func TestChannelProjectBinding_UnbindQueuesReverseMigration(t *testing.T) {
	h := newChannelBindingHarness(t)
	defer h.Close()
	_, err := ResolveChannelRoute(h.ctx, h.pubPool, h.workspace.String(), h.channel.String())
	require.NoError(t, err)

	unbound := h.bind(t, pgtype.UUID{})
	assert.Equal(t, int64(1), unbound.Generation)
	assert.False(t, h.currentChannelProject(t).Valid)
	_, kind, owner, gen := h.routeRow(t)
	assert.Equal(t, "channel", kind)
	assert.Equal(t, h.channel.String(), owner)
	assert.Equal(t, int64(2), gen)
	assert.True(t, unbound.RouteOwnerMoved)
	assert.True(t, unblockedPending(t, h))
}

func unblockedPending(t *testing.T, h *channelBindingHarness) bool {
	t.Helper()
	var pending bool
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT EXISTS (SELECT 1 FROM graph_memory_channel_migration_state
			WHERE channel_id=$1 AND phase='pending')`, h.channel).Scan(&pending))
	return pending
}

// A standalone channel (resolved unbound first) is permanent: binding a
// project later never moves its write owner, so no migration is queued.
func TestChannelProjectBinding_StandaloneStaysStandalone(t *testing.T) {
	h := newChannelBindingHarness(t)
	defer h.Close()
	// Unbind the fixture channel first so the first resolution is standalone.
	// The guard requires the service path even for this harness setup bind.
	_, err := h.binding().SetChannelProject(h.ctx, ChannelProjectBindingParams{
		WorkspaceID: h.workspace, ChannelID: h.channel, Actor: "binding-test",
	})
	require.NoError(t, err)
	_, err = ResolveChannelRoute(h.ctx, h.pubPool, h.workspace.String(), h.channel.String())
	require.NoError(t, err)
	mode, kind, owner, _ := h.routeRow(t)
	assert.Equal(t, "standalone", mode)
	assert.Equal(t, "channel", kind)
	assert.Equal(t, h.channel.String(), owner)

	result := h.bind(t, h.project)
	assert.Equal(t, h.project, h.currentChannelProject(t))
	mode, kind, owner, _ = h.routeRow(t)
	assert.Equal(t, "standalone", mode)
	assert.Equal(t, "channel", kind)
	assert.Equal(t, h.channel.String(), owner)
	assert.False(t, result.RouteOwnerMoved)
	assert.False(t, result.MigrationPending)
	assert.Equal(t, 0, h.countRows(t,
		`SELECT count(*) FROM graph_memory_channel_migration_state WHERE channel_id=$1`, h.channel))
}

// The CAS expectation: a rebind that lost a concurrent race fails with
// ErrChannelBindingConflict and writes nothing — neither a binding
// generation nor a channel update. (The fixture channel is bound to
// h.project at seed time.)
func TestChannelProjectBinding_ConcurrentCASRebindConflicts(t *testing.T) {
	h := newChannelBindingHarness(t)
	defer h.Close()

	_, err := h.binding().SetChannelProject(h.ctx, ChannelProjectBindingParams{
		WorkspaceID: h.workspace, ChannelID: h.channel, NewProjectID: h.projectB,
		ExpectedOldProjectID: pgtype.UUID{}, ExpectedOldSet: true, // lost the race: old is h.project
		Actor: "racing-writer",
	})
	assert.ErrorIs(t, err, ErrChannelBindingConflict)
	assert.Equal(t, h.project, h.currentChannelProject(t), "the channel must keep the winner's binding")
	assert.Equal(t, 0, h.countRows(t,
		`SELECT count(*) FROM graph_memory_channel_binding WHERE channel_id=$1`, h.channel))

	// With the correct expectation the same call succeeds.
	_, err = h.binding().SetChannelProject(h.ctx, ChannelProjectBindingParams{
		WorkspaceID: h.workspace, ChannelID: h.channel, NewProjectID: h.projectB,
		ExpectedOldProjectID: h.project, ExpectedOldSet: true, Actor: "settings",
	})
	assert.NoError(t, err)
	assert.Equal(t, h.projectB, h.currentChannelProject(t))
}

// Re-saving the identical binding consumes nothing: no generation, no
// migration, no channel write. (The fixture channel ships bound to
// h.project.)
func TestChannelProjectBinding_IdempotentResameConsumesNothing(t *testing.T) {
	h := newChannelBindingHarness(t)
	defer h.Close()

	result := h.bind(t, h.project)
	assert.Equal(t, int64(0), result.Generation)
	assert.False(t, result.MigrationPending)
	assert.Equal(t, 0, h.countRows(t,
		`SELECT count(*) FROM graph_memory_channel_binding WHERE channel_id=$1`, h.channel))
}

// The migration-470 guard: any UPDATE of channel.project_id outside a
// transaction that wrote a binding row is rejected in the private schema
// exactly as in production.
func TestChannelProjectBinding_DirectSQLWriterRejected(t *testing.T) {
	h := newChannelBindingHarness(t)
	defer h.Close()
	_, err := h.conn.Exec(h.ctx,
		`UPDATE channel SET project_id=$1 WHERE id=$2`, h.projectB, h.channel)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ChannelProjectBindingService")
	assert.Equal(t, h.project, h.currentChannelProject(t))

	// Non-binding UPDATEs (updated_at bumps) stay untouched by the guard.
	_, err = h.conn.Exec(h.ctx, `UPDATE channel SET updated_at=now() WHERE id=$1`, h.channel)
	assert.NoError(t, err)
}

// The goal-bootstrap composition: the service runs inside a caller-owned
// transaction that also writes unrelated rows; a failure later in that
// transaction rolls the binding back with everything else.
func TestChannelProjectBinding_ComposesWithCallerTransaction(t *testing.T) {
	h := newChannelBindingHarness(t)
	defer h.Close()

	tx, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	defer tx.Rollback(h.ctx)
	_, err = h.binding().SetChannelProjectTx(h.ctx, tx, ChannelProjectBindingParams{
		WorkspaceID: h.workspace, ChannelID: h.channel, NewProjectID: h.projectB,
		ExpectedOldProjectID: h.project, ExpectedOldSet: true, Actor: "bootstrap",
	})
	require.NoError(t, err)
	_, err = tx.Exec(h.ctx, `SELECT 1`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(h.ctx))
	assert.Equal(t, h.projectB, h.currentChannelProject(t))
	assert.Equal(t, 1, h.countRows(t,
		`SELECT count(*) FROM graph_memory_channel_binding WHERE channel_id=$1`, h.channel))

	// And the all-or-nothing side: a caller failure after the service call
	// leaves no binding rows behind.
	tx2, err := h.pubPool.Begin(h.ctx)
	require.NoError(t, err)
	defer tx2.Rollback(h.ctx)
	_, err = h.binding().SetChannelProjectTx(h.ctx, tx2, ChannelProjectBindingParams{
		WorkspaceID: h.workspace, ChannelID: h.channel, NewProjectID: h.project,
		ExpectedOldProjectID: h.projectB, ExpectedOldSet: true, Actor: "bootstrap",
	})
	require.NoError(t, err)
	_ = tx2.Rollback(h.ctx) // caller aborts for its own reasons
	assert.Equal(t, h.projectB, h.currentChannelProject(t))
	assert.Equal(t, 1, h.countRows(t,
		`SELECT count(*) FROM graph_memory_channel_binding WHERE channel_id=$1`, h.channel))
}

// The structural zero-bypass pin: no production Go code outside the binding
// service (and the test tree) may UPDATE channel.project_id — the guard
// trigger makes any straggler fail loudly in every schema.
func TestChannelProjectBinding_NoDirectProductionWriters(t *testing.T) {
	if testing.Short() {
		t.Skip("source scan in short mode")
	}
	hits := scanSourceForChannelProjectUpdates(t, "../../internal", "../../cmd")
	for _, hit := range hits {
		t.Errorf("direct channel.project_id writer outside the binding service: %s", hit)
	}
}

// scanSourceForChannelProjectUpdates greps the production tree for UPDATE
// statements against channel's project_id. Allowed: channel_project_binding.go
// (the service's own authorized UPDATE) and _test.go files.
func scanSourceForChannelProjectUpdates(t *testing.T, dirs ...string) []string {
	t.Helper()
	var hits []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for i, line := range strings.Split(string(data), "\n") {
				if strings.Contains(strings.ToLower(line), "update channel set project_id") {
					if strings.HasSuffix(path, "channel_project_binding.go") {
						continue // the service's own authorized UPDATE
					}
					hits = append(hits, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
	return hits
}

var _ = mustUUID
