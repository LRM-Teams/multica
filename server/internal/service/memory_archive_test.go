// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// Hot blobs past the trajectory window are encrypted into an archive
// object first; only after the manifest records the ciphertext hash do the
// hot body retire and its refs release.
func TestMemoryArchive_ArchiveDueEncryptsThenRetires(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	blob := h.seedHotBlob(t, "blob-hot", 91)
	h.seedHotBlob(t, "blob-fresh", 10)

	archived, err := h.archiveService().ArchiveDue(h.ctx, 16)
	require.NoError(t, err)
	assert.Equal(t, 1, archived)

	var (
		status    string
		cipherSum string
		objectRef string
		eraseDue  time.Time
	)
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT status, cipher_sha256, object_ref, erase_due_at
		FROM memory_archive_manifest WHERE blob_id=$1`, blob).
		Scan(&status, &cipherSum, &objectRef, &eraseDue))
	assert.Equal(t, "archived", status)
	assert.NotEmpty(t, cipherSum)
	// The recorded hash is the hash of the stored ciphertext object.
	ct := h.store.objects[objectRef]
	require.NotNil(t, ct)
	sum := sha256.Sum256(ct)
	assert.Equal(t, hex.EncodeToString(sum[:]), cipherSum)
	// The stored object is NOT the plaintext.
	assert.False(t, bytes.Contains(ct, []byte("plaintext of blob-hot")))
	// erase_due_at binds the bootstrap archive window at archive time.
	assert.WithinDuration(t, h.now.AddDate(0, 0, 365), eraseDue, time.Minute)

	var blobStatus string
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT status FROM graph_memory_blob WHERE id=$1`, blob).Scan(&blobStatus))
	assert.Equal(t, "retired", blobStatus)
	var openRefs int
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT count(*) FROM graph_memory_blob_ref WHERE blob_id=$1 AND released_at IS NULL`,
		blob).Scan(&openRefs))
	assert.Equal(t, 0, openRefs)
	// The fresh blob is untouched.
	var freshStatus string
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT status FROM graph_memory_blob WHERE storage_url='blob-fresh'`).Scan(&freshStatus))
	assert.Equal(t, "active", freshStatus)
}

// Replays archive nothing: the manifest guard is idempotent per blob.
func TestMemoryArchive_ArchiveDueIdempotent(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	h.seedHotBlob(t, "blob-hot", 120)

	_, err := h.archiveService().ArchiveDue(h.ctx, 16)
	require.NoError(t, err)
	again, err := h.archiveService().ArchiveDue(h.ctx, 16)
	require.NoError(t, err)
	assert.Equal(t, 0, again)

	var manifests int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM memory_archive_manifest`).Scan(&manifests))
	assert.Equal(t, 1, manifests)
}

// Restore requires an explicit reason, audits actor/reason/TTL, and only
// streams live manifests; erased objects never return.
func TestMemoryArchive_RestoreLeaseAuditAndExpiry(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	blob := h.seedHotBlob(t, "blob-hot", 91)
	_, err := h.archiveService().ArchiveDue(h.ctx, 16)
	require.NoError(t, err)
	var manifestID pgtype.UUID
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT id FROM memory_archive_manifest WHERE blob_id=$1`, blob).Scan(&manifestID))
	svc := h.archiveService()

	// Empty reason is refused outright.
	_, err = svc.RestoreEvidence(h.ctx, RestoreRequest{
		WorkspaceID: h.workspace, ManifestID: manifestID, Actor: "user:1", Reason: "  ",
	})
	assert.ErrorIs(t, err, ErrRestoreReasonRequired)

	lease, err := svc.RestoreEvidence(h.ctx, RestoreRequest{
		WorkspaceID: h.workspace, ManifestID: manifestID,
		Actor: "user:1", Reason: "incident investigation",
	})
	require.NoError(t, err)
	assert.WithinDuration(t, h.now.Add(memoryRestoreLeaseTTL), lease.ExpiresAt, time.Second)

	// The lease row is the audit record.
	var actor, reason string
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT actor, reason FROM memory_archive_restore_lease WHERE id=$1`,
		pgtype.UUID{Bytes: uuid.MustParse(lease.ID), Valid: true}).Scan(&actor, &reason))
	assert.Equal(t, "user:1", actor)
	assert.Equal(t, "incident investigation", reason)

	// Decrypt round-trips the plaintext under the live lease.
	plain, err := svc.Decrypt(h.ctx, lease)
	require.NoError(t, err)
	body, err := io.ReadAll(plain)
	require.NoError(t, err)
	assert.Equal(t, "plaintext of blob-hot", string(body))

	// An expired lease refuses to open server-side.
	lease.ExpiresAt = h.now.Add(-time.Second)
	_, err = svc.Decrypt(h.ctx, lease)
	assert.ErrorIs(t, err, ErrArchiveLeaseExpired)

	// A cross-workspace envelope does not open (workspace-scoped keys).
	foreign := lease
	foreign.WorkspaceID = pgtype.UUID{Bytes: uuid.MustParse("03000000-0000-4000-8000-000000000003"), Valid: true}
	foreign.ExpiresAt = h.now.Add(time.Minute)
	_, err = svc.Decrypt(h.ctx, foreign)
	assert.Error(t, err)

	// Unknown manifest and erased manifest never restore.
	_, err = svc.RestoreEvidence(h.ctx, RestoreRequest{
		WorkspaceID: h.workspace,
		ManifestID:  pgtype.UUID{Bytes: uuid.MustParse("04000000-0000-4000-8000-000000000004"), Valid: true},
		Actor:       "user:1", Reason: "x",
	})
	assert.ErrorIs(t, err, ErrArchiveManifestNotFound)
}

// EraseDue deletes the ciphertext object then flips the manifest; a live
// restore lease is a legal ref and defers the erase.
func TestMemoryArchive_EraseDueHonorsLeases(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	blob := h.seedHotBlob(t, "blob-hot", 400)
	svc := h.archiveService()
	// Bind a 30-day archive window so the manifest is immediately due.
	_, err := h.retentionService().UpdatePolicy(h.ctx, h.workspace, MemoryRetentionUpdate{
		TrajectoryHotDays: 90, ArchiveDays: 30, TraceHotDays: 30, DiagnosticThinkingDays: 30,
		ExpectedVersion: 1,
	}, "user:1")
	require.NoError(t, err)
	_, err = svc.ArchiveDue(h.ctx, 16)
	require.NoError(t, err)
	var manifestID pgtype.UUID
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT id FROM memory_archive_manifest WHERE blob_id=$1`, blob).Scan(&manifestID))

	// A live lease defers erasure.
	_, err = svc.RestoreEvidence(h.ctx, RestoreRequest{
		WorkspaceID: h.workspace, ManifestID: manifestID, Actor: "user:1", Reason: "hold",
	})
	require.NoError(t, err)
	erased, err := svc.EraseDue(h.ctx, 16)
	require.NoError(t, err)
	assert.Equal(t, 0, erased)

	// Past the lease AND past the 30-day archive window the object is
	// cryptographically erased.
	var objectRef string
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT object_ref FROM memory_archive_manifest WHERE id=$1`, manifestID).Scan(&objectRef))
	h.now = h.now.AddDate(0, 0, 31)
	// Age the lease row on the DB clock too (the guard uses now()).
	_, err = h.conn.Exec(h.ctx,
		`UPDATE memory_archive_restore_lease SET expires_at = now() - interval '1 minute'`)
	require.NoError(t, err)
	erased, err = svc.EraseDue(h.ctx, 16)
	require.NoError(t, err)
	assert.Equal(t, 1, erased)

	var status string
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT status FROM memory_archive_manifest WHERE id=$1`, manifestID).Scan(&status))
	assert.Equal(t, "erased", status)
	// The ciphertext object is gone from the store.
	_, missing := h.store.objects[objectRef]
	assert.False(t, missing, "ciphertext object must be deleted")
	_, err = svc.RestoreEvidence(h.ctx, RestoreRequest{
		WorkspaceID: h.workspace, ManifestID: manifestID, Actor: "user:1", Reason: "later",
	})
	assert.ErrorIs(t, err, ErrArchiveContentErased)
}

// Shortening the archive window tightens every existing manifest's erase
// deadline (LEAST) — never extended past its originally bound date.
func TestMemoryRetention_ShorteningTightensExistingManifests(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	blob := h.seedHotBlob(t, "blob-hot", 91)
	_, err := h.archiveService().ArchiveDue(h.ctx, 16)
	require.NoError(t, err)

	var originalDue time.Time
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT erase_due_at FROM memory_archive_manifest WHERE blob_id=$1`, blob).Scan(&originalDue))

	// Re-lengthening first (rollback within caps) must NOT extend the
	// manifest; then shortening tightens it to now-30d.
	svc := h.retentionService()
	_, err = svc.UpdatePolicy(h.ctx, h.workspace, MemoryRetentionUpdate{
		TrajectoryHotDays: 90, ArchiveDays: 365, TraceHotDays: 30, DiagnosticThinkingDays: 30,
		ExpectedVersion: 1,
	}, "user:1")
	require.NoError(t, err)
	var afterRollback time.Time
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT erase_due_at FROM memory_archive_manifest WHERE blob_id=$1`, blob).Scan(&afterRollback))
	assert.WithinDuration(t, originalDue, afterRollback, time.Second)

	_, err = svc.UpdatePolicy(h.ctx, h.workspace, MemoryRetentionUpdate{
		TrajectoryHotDays: 90, ArchiveDays: 30, TraceHotDays: 30, DiagnosticThinkingDays: 30,
		ExpectedVersion: 2,
	}, "user:1")
	require.NoError(t, err)
	var tightened time.Time
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT erase_due_at FROM memory_archive_manifest WHERE blob_id=$1`, blob).Scan(&tightened))
	assert.True(t, tightened.Before(afterRollback))
	assert.WithinDuration(t, h.now.AddDate(0, 0, -30), tightened, time.Minute)
}

// SweepDue deletes whole trace windows past the workspace's trace hot
// window and keeps fresh ones; the sweep cursor records the pass.
func TestMemoryRetention_SweepDueTraceWindows(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	_, err := h.retentionService().CurrentPolicy(h.ctx, h.workspace)
	require.NoError(t, err)

	// A store with one stale and one fresh query-log window.
	dir, err := memorygraph.EnsureScopedDir(root, h.workspace.String(),
		memorygraph.GraphDirKindProject, "50000000-0000-4000-8000-000000000005")
	require.NoError(t, err)
	store := memorygraph.NewStore(dir)
	require.NoError(t, store.Init())
	require.NoError(t, store.AppendQueryLog("w-old", &memorygraph.QueryLogEntry{
		TraceID: "t-old", Query: "q", Timestamp: h.now.AddDate(0, 0, -45),
	}))
	require.NoError(t, store.AppendQueryLog("w-fresh", &memorygraph.QueryLogEntry{
		TraceID: "t-new", Query: "q", Timestamp: h.now.AddDate(0, 0, -2),
	}))

	actions, err := h.retentionService().SweepDue(h.ctx, 16)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, actions, 1)

	windows, err := store.ListQueryLogWindows()
	require.NoError(t, err)
	assert.Equal(t, []string{"w-fresh"}, windows)

	var cursor time.Time
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT last_trace_sweep_at FROM memory_retention_sweep_cursor WHERE workspace_id=$1`,
		h.workspace).Scan(&cursor))
	assert.False(t, cursor.IsZero())
}

// The Task 8A fence reaches archive restore: a retracted source's archived
// body never streams again (spec AC 62), while the manifest row itself
// stays for audit until the erase sweep.
func TestMemoryArchive_RestoreFencedByRetraction(t *testing.T) {
	h := newRetentionHarness(t)
	defer h.Close()
	blob := h.seedHotBlob(t, "blob-hot", 91)
	// Retract the graph source that retains the blob (fence row with the
	// ref's id).
	var refID string
	require.NoError(t, h.conn.QueryRow(h.ctx, `
		SELECT ref_id::text FROM graph_memory_blob_ref WHERE blob_id=$1 LIMIT 1`, blob).Scan(&refID))
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO memory_source_guard (workspace_id, source_kind, source_id, retracted_at, retracted_by, reason)
		VALUES ($1, 'task_output', $2, now(), 'user:1', 'gdpr')`, h.workspace, refID)
	require.NoError(t, err)

	_, err = h.archiveService().ArchiveDue(h.ctx, 16)
	require.NoError(t, err)
	var manifestID pgtype.UUID
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT id FROM memory_archive_manifest WHERE blob_id=$1`, blob).Scan(&manifestID))

	_, err = h.archiveService().RestoreEvidence(h.ctx, RestoreRequest{
		WorkspaceID: h.workspace, ManifestID: manifestID,
		Actor: "user:1", Reason: "incident investigation",
	})
	assert.ErrorIs(t, err, ErrArchiveSourceRetracted)
	var leases int
	require.NoError(t, h.conn.QueryRow(h.ctx,
		`SELECT count(*) FROM memory_archive_restore_lease`).Scan(&leases))
	assert.Equal(t, 0, leases, "fenced restore must not mint a lease")
}
