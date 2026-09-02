// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoryRetentionDDL mirrors migration 471 plus the migration-424 blob
// tables in the private test schema (the service mini-schema carries no
// upstream migrations).
const memoryRetentionDDL = `
CREATE TABLE IF NOT EXISTS memory_retention_policy (
    workspace_id        uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    version             bigint      NOT NULL CHECK (version > 0),
    trajectory_hot_days int         NOT NULL CHECK (trajectory_hot_days > 0 AND trajectory_hot_days <= 90),
    archive_days        int         NOT NULL CHECK (archive_days > 0 AND archive_days <= 365),
    trace_hot_days      int         NOT NULL CHECK (trace_hot_days > 0 AND trace_hot_days <= 30),
    diagnostic_thinking_days int    NOT NULL DEFAULT 30 CHECK (diagnostic_thinking_days > 0 AND diagnostic_thinking_days <= 30),
    updated_by          text        NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, version)
);
CREATE TABLE IF NOT EXISTS memory_archive_manifest (
    id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    blob_id        uuid        NOT NULL,
    object_ref     text        NOT NULL,
    key_envelope   text        NOT NULL,
    cipher_sha256  text        NOT NULL,
    size_bytes     bigint      NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    status         text        NOT NULL DEFAULT 'archived' CHECK (status IN ('archived', 'erased')),
    archived_at    timestamptz NOT NULL DEFAULT now(),
    erase_due_at   timestamptz NOT NULL,
    erased_at      timestamptz,
    UNIQUE (workspace_id, blob_id)
);
CREATE TABLE IF NOT EXISTS memory_archive_restore_lease (
    id           uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    manifest_id  uuid        NOT NULL,
    actor        text        NOT NULL,
    reason       text        NOT NULL CHECK (reason <> ''),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS memory_retention_sweep_cursor (
    workspace_id             uuid        NOT NULL PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    last_trajectory_sweep_at timestamptz,
    last_trace_sweep_at      timestamptz,
    last_archive_sweep_at    timestamptz,
    last_thinking_sweep_at   timestamptz,
    updated_at               timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS graph_memory_blob (
    id            uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    storage_url   text        NOT NULL,
    blob_sha256   text        NOT NULL DEFAULT '',
    size_bytes    bigint      NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    status        text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retired')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    retired_at    timestamptz,
    UNIQUE (workspace_id, storage_url)
);
CREATE TABLE IF NOT EXISTS graph_memory_blob_ref (
    id           uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    blob_id      uuid        NOT NULL,
    ref_kind     text        NOT NULL CHECK (ref_kind IN ('attachment', 'graph_source', 'graph_version')),
    ref_id       uuid        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    released_at  timestamptz
);
`

// fakeArchiveStore is the in-memory ArchiveObjectStore.
type fakeArchiveStore struct {
	objects map[string][]byte
}

func newFakeArchiveStore() *fakeArchiveStore {
	return &fakeArchiveStore{objects: map[string][]byte{}}
}

var errFakeArchiveMissing = errors.New("fake archive store: object missing")

func (f *fakeArchiveStore) Fetch(ctx context.Context, storageURL string) ([]byte, error) {
	data, ok := f.objects[storageURL]
	if !ok {
		return nil, errFakeArchiveMissing
	}
	return data, nil
}

func (f *fakeArchiveStore) Put(ctx context.Context, workspaceID pgtype.UUID, name string, data []byte) (string, error) {
	f.objects[name] = data
	return name, nil
}

func (f *fakeArchiveStore) Delete(ctx context.Context, objectRef string) error {
	delete(f.objects, objectRef)
	return nil
}

type retentionHarness struct {
	*exploreV2Harness
	store  *fakeArchiveStore
	cipher ArchiveCipher
	now    time.Time
}

func newRetentionHarness(t *testing.T) *retentionHarness {
	t.Helper()
	h := &retentionHarness{exploreV2Harness: newExploreV2Harness(t, false), now: time.Now().UTC()}
	_, err := h.conn.Exec(h.ctx, memoryRetentionDDL)
	require.NoError(t, err)
	h.store = newFakeArchiveStore()
	cipher, err := NewAesGcmArchiveCipher([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	h.cipher = cipher
	// Bind the explicit bootstrap policy so archive/sweep queues see the
	// workspace (mirrors migration 471's INSERT ... SELECT for existing
	// workspaces).
	require.NoError(t, NewMemoryRetentionService(h.pubPool, nil).
		EnsureBootstrapPolicy(h.ctx, h.workspace))
	return h
}

func (h *retentionHarness) archiveService() *MemoryArchiveService {
	svc := NewMemoryArchiveService(h.pubPool, h.cipher, h.store)
	svc.now = func() time.Time { return h.now }
	return svc
}

func (h *retentionHarness) retentionService() *MemoryRetentionService {
	svc := NewMemoryRetentionService(h.pubPool, h.archiveService())
	svc.now = func() time.Time { return h.now }
	return svc
}

// seedHotBlob inserts one active blob created `age` ago with an open
// graph_source ref and registers its plaintext bytes in the fake store.
func (h *retentionHarness) seedHotBlob(t *testing.T, name string, ageDays int) pgtype.UUID {
	t.Helper()
	blob := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_blob (id, workspace_id, storage_url, blob_sha256, size_bytes, created_at)
		VALUES ($1, $2, $3, 'sha', $4, now() - ($5 || ' days')::interval)`,
		blob, h.workspace, name, int64(len(name)), fmt.Sprintf("%d", ageDays))
	require.NoError(t, err)
	_, err = h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_blob_ref (workspace_id, blob_id, ref_kind, ref_id)
		VALUES ($1, $2, 'graph_source', $3)`,
		h.workspace, blob, pgtype.UUID{Bytes: uuid.New(), Valid: true})
	require.NoError(t, err)
	h.store.objects[name] = []byte("plaintext of " + name)
	return blob
}

// ---------------------------------------------------------------------------
// Policy tests (plan Step 1).
// ---------------------------------------------------------------------------

// Bootstrap binds the explicit shadow contract 90/365/30 as version 1 —
// never a silent runtime default.
func TestMemoryRetention_BootstrapBindsExplicitVersion(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()

	svc := h.retentionService()
	policy, err := svc.CurrentPolicy(h.ctx, h.workspace)
	require.NoError(t, err)
	assert.Equal(t, int64(1), policy.Version)
	assert.Equal(t, 90, policy.TrajectoryHotDays)
	assert.Equal(t, 365, policy.ArchiveDays)
	assert.Equal(t, 30, policy.TraceHotDays)

	var updatedBy string
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT updated_by FROM memory_retention_policy WHERE workspace_id=$1 AND version=1`,
		h.workspace).Scan(&updatedBy))
	assert.Equal(t, "bootstrap", updatedBy)
}

// A workspace may shorten within the caps; the CAS version gate rejects
// concurrent updates.
func TestMemoryRetention_UpdateShortensWithCAS(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	svc := h.retentionService()

	updated, err := svc.UpdatePolicy(h.ctx, h.workspace, MemoryRetentionUpdate{
		TrajectoryHotDays: 30, ArchiveDays: 180, TraceHotDays: 14, DiagnosticThinkingDays: 30,
		ExpectedVersion: 1,
	}, "user:1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)
	assert.Equal(t, 14, updated.TraceHotDays)

	// Stale expected version conflicts and writes nothing.
	_, err = svc.UpdatePolicy(h.ctx, h.workspace, MemoryRetentionUpdate{
		TrajectoryHotDays: 10, ArchiveDays: 100, TraceHotDays: 7, DiagnosticThinkingDays: 30,
		ExpectedVersion: 1,
	}, "user:1")
	assert.ErrorIs(t, err, ErrMemoryRetentionVersion)
	var versions int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM memory_retention_policy WHERE workspace_id=$1`, h.workspace).Scan(&versions))
	assert.Equal(t, 2, versions)
}

// Platform caps reject extension attempts server-side; the DB CHECK is the
// second wall.
func TestMemoryRetention_CapExtensionRejected(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	svc := h.retentionService()

	for _, update := range []MemoryRetentionUpdate{
		{TrajectoryHotDays: 91, ArchiveDays: 365, TraceHotDays: 30, DiagnosticThinkingDays: 30,
			ExpectedVersion: 1},
		{TrajectoryHotDays: 90, ArchiveDays: 366, TraceHotDays: 30, ExpectedVersion: 1},
		{TrajectoryHotDays: 90, ArchiveDays: 365, TraceHotDays: 31, ExpectedVersion: 1},
	} {
		_, err := svc.UpdatePolicy(h.ctx, h.workspace, update, "user:1")
		assert.ErrorIs(t, err, ErrMemoryRetentionCap, "update %+v must be rejected", update)
	}
	_, err := svc.UpdatePolicy(h.ctx, h.workspace, MemoryRetentionUpdate{
		TrajectoryHotDays: 0, ArchiveDays: 365, TraceHotDays: 30, DiagnosticThinkingDays: 30,
		ExpectedVersion: 1,
	}, "user:1")
	assert.ErrorIs(t, err, ErrMemoryRetentionDaysGlobal)
}

// Policy history is append-only: rollback (re-lengthening within caps)
// appends a new version for NEW data without deleting history.
func TestMemoryRetention_RollbackAppendsVersion(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	svc := h.retentionService()

	_, err := svc.UpdatePolicy(h.ctx, h.workspace, MemoryRetentionUpdate{
		TrajectoryHotDays: 30, ArchiveDays: 90, TraceHotDays: 14, DiagnosticThinkingDays: 30,
		ExpectedVersion: 1,
	}, "user:1")
	require.NoError(t, err)
	rolled, err := svc.UpdatePolicy(h.ctx, h.workspace, MemoryRetentionUpdate{
		TrajectoryHotDays: 60, ArchiveDays: 200, TraceHotDays: 20, DiagnosticThinkingDays: 30,
		ExpectedVersion: 2,
	}, "user:1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), rolled.Version)

	var rows []int64
	r, err := h.conn.Query(h.ctx,
		`SELECT version FROM memory_retention_policy WHERE workspace_id=$1 ORDER BY version`, h.workspace)
	require.NoError(t, err)
	for r.Next() {
		var v int64
		require.NoError(t, r.Scan(&v))
		rows = append(rows, v)
	}
	r.Close()
	assert.Equal(t, []int64{1, 2, 3}, rows)
}
