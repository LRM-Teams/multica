// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
)

// GraphMemoryBlobService owns physical-byte retention for graph memory
// (spec §15, A28/D32). Attachment rows and graph sources share bytes via
// durable refs; collection physically deletes only zero-ref active blobs.
type GraphMemoryBlobService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryBlobService(pool *pgxpool.Pool) *GraphMemoryBlobService {
	return &GraphMemoryBlobService{pool: pool}
}

// BlobDeleter physically removes one storage object. DeleteBlob of a
// missing object must succeed so collection is retryable/idempotent.
type BlobDeleter interface {
	DeleteBlob(ctx context.Context, storageURL string) error
}

// RegisterBlob is idempotent per (workspace, storage_url): an existing
// row for that URL returns its id without inserting a second.
func (s *GraphMemoryBlobService) RegisterBlob(ctx context.Context, workspaceID, storageURL, sha256 string, sizeBytes int64) (string, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return "", fmt.Errorf("graph memory blob: workspace id: %w", err)
	}
	var existing pgtype.UUID
	err = s.pool.QueryRow(ctx, `
		SELECT id FROM graph_memory_blob
		WHERE workspace_id = $1 AND storage_url = $2
	`, ws, storageURL).Scan(&existing)
	switch {
	case err == nil:
		return util.UUIDToString(existing), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("graph memory blob: lookup: %w", err)
	}

	var id pgtype.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO graph_memory_blob (workspace_id, storage_url, blob_sha256, size_bytes)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, ws, storageURL, sha256, sizeBytes).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			if err := s.pool.QueryRow(ctx, `
				SELECT id FROM graph_memory_blob
				WHERE workspace_id = $1 AND storage_url = $2
			`, ws, storageURL).Scan(&existing); err == nil {
				return util.UUIDToString(existing), nil
			}
		}
		return "", fmt.Errorf("graph memory blob: insert: %w", err)
	}
	return util.UUIDToString(id), nil
}

// RetainBlob validates refKind before SQL and is idempotent per open
// (blob, kind, ref) tuple.
func (s *GraphMemoryBlobService) RetainBlob(ctx context.Context, blobID, refKind, refID string) (string, error) {
	if err := validateGraphMemoryBlobRefKind(refKind); err != nil {
		return "", err
	}
	blob, err := util.ParseUUID(blobID)
	if err != nil {
		return "", fmt.Errorf("graph memory blob: blob id: %w", err)
	}
	referrer, err := util.ParseUUID(refID)
	if err != nil {
		return "", fmt.Errorf("graph memory blob: ref id: %w", err)
	}

	var ws pgtype.UUID
	err = s.pool.QueryRow(ctx, `
		SELECT workspace_id FROM graph_memory_blob WHERE id = $1
	`, blob).Scan(&ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("graph memory blob: unknown blob %s", blobID)
	}
	if err != nil {
		return "", fmt.Errorf("graph memory blob: lookup blob: %w", err)
	}

	var existing pgtype.UUID
	err = s.pool.QueryRow(ctx, `
		SELECT id FROM graph_memory_blob_ref
		WHERE blob_id = $1 AND ref_kind = $2 AND ref_id = $3 AND released_at IS NULL
	`, blob, refKind, referrer).Scan(&existing)
	switch {
	case err == nil:
		return util.UUIDToString(existing), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("graph memory blob: lookup open ref: %w", err)
	}

	var id pgtype.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO graph_memory_blob_ref (workspace_id, blob_id, ref_kind, ref_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, ws, blob, refKind, referrer).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			if err := s.pool.QueryRow(ctx, `
				SELECT id FROM graph_memory_blob_ref
				WHERE blob_id = $1 AND ref_kind = $2 AND ref_id = $3 AND released_at IS NULL
			`, blob, refKind, referrer).Scan(&existing); err == nil {
				return util.UUIDToString(existing), nil
			}
		}
		return "", fmt.Errorf("graph memory blob: retain: %w", err)
	}
	return util.UUIDToString(id), nil
}

// ReleaseBlobRefsFor marks every open ref of the given referrer released.
// Zero matches is a no-op success.
func (s *GraphMemoryBlobService) ReleaseBlobRefsFor(ctx context.Context, refKind, refID string) error {
	if err := validateGraphMemoryBlobRefKind(refKind); err != nil {
		return err
	}
	referrer, err := util.ParseUUID(refID)
	if err != nil {
		return fmt.Errorf("graph memory blob: ref id: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE graph_memory_blob_ref
		SET released_at = COALESCE(released_at, now())
		WHERE ref_kind = $1 AND ref_id = $2 AND released_at IS NULL
	`, refKind, referrer)
	if err != nil {
		return fmt.Errorf("graph memory blob: release: %w", err)
	}
	return nil
}

// AttachmentBytesRetained is true when a graph_memory_blob row exists for
// the URL: registered bytes are owned by retention and handlers must not
// physically delete them directly.
func (s *GraphMemoryBlobService) AttachmentBytesRetained(ctx context.Context, workspaceID, storageURL string) (bool, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return false, fmt.Errorf("graph memory blob: workspace id: %w", err)
	}
	var retained bool
	err = s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM graph_memory_blob
			WHERE workspace_id = $1 AND storage_url = $2
		)
	`, ws, storageURL).Scan(&retained)
	if err != nil {
		return false, fmt.Errorf("graph memory blob: retained: %w", err)
	}
	return retained, nil
}

// AttachmentURLShared is true when any other attachment row in the
// workspace references the same URL (clone-shared bytes).
func (s *GraphMemoryBlobService) AttachmentURLShared(ctx context.Context, workspaceID, exceptAttachmentID, storageURL string) (bool, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return false, fmt.Errorf("graph memory blob: workspace id: %w", err)
	}
	except, err := util.ParseUUID(exceptAttachmentID)
	if err != nil {
		return false, fmt.Errorf("graph memory blob: attachment id: %w", err)
	}
	var shared bool
	err = s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM attachment
			WHERE workspace_id = $1 AND url = $2 AND id <> $3
		)
	`, ws, storageURL, except).Scan(&shared)
	if err != nil {
		return false, fmt.Errorf("graph memory blob: shared url: %w", err)
	}
	return shared, nil
}

// CollectZeroRefBlobs physically deletes active blobs that currently have
// zero open refs (oldest first, up to limit). Each candidate is locked,
// rechecked, deleted, then marked retired in one transaction. Deleter
// failure rolls back (blob stays active) and is joined while other blobs
// continue. Retired blobs are never collected again.
func (s *GraphMemoryBlobService) CollectZeroRefBlobs(ctx context.Context, d BlobDeleter, limit int) (int, error) {
	if limit < 1 {
		return 0, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, storage_url FROM graph_memory_blob
		WHERE status = 'active'
		  AND NOT EXISTS (
		    SELECT 1 FROM graph_memory_blob_ref
		    WHERE blob_id = graph_memory_blob.id AND released_at IS NULL
		  )
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("graph memory blob: list zero-ref: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id  pgtype.UUID
		url string
	}
	var list []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.url); err != nil {
			return 0, fmt.Errorf("graph memory blob: scan zero-ref: %w", err)
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	collected := 0
	var joined error
	for _, c := range list {
		ok, err := s.collectOne(ctx, d, c.id, c.url)
		if err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		if ok {
			collected++
		}
	}
	return collected, joined
}

func (s *GraphMemoryBlobService) collectOne(ctx context.Context, d BlobDeleter, blobID pgtype.UUID, storageURL string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("graph memory blob: begin collect: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, util.UUIDToString(blobID)); err != nil {
		return false, fmt.Errorf("graph memory blob: lock: %w", err)
	}

	var open bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM graph_memory_blob_ref
			WHERE blob_id = $1 AND released_at IS NULL
		)
	`, blobID).Scan(&open); err != nil {
		return false, fmt.Errorf("graph memory blob: recheck refs: %w", err)
	}
	if open {
		return false, nil
	}

	var status string
	err = tx.QueryRow(ctx, `
		SELECT status FROM graph_memory_blob WHERE id = $1
	`, blobID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graph memory blob: recheck status: %w", err)
	}
	if status != "active" {
		return false, nil
	}

	if err := d.DeleteBlob(ctx, storageURL); err != nil {
		return false, fmt.Errorf("graph memory blob: delete %s: %w", util.UUIDToString(blobID), err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE graph_memory_blob
		SET status = 'retired', retired_at = now()
		WHERE id = $1 AND status = 'active'
	`, blobID)
	if err != nil {
		return false, fmt.Errorf("graph memory blob: retire: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("graph memory blob: commit collect: %w", err)
	}
	return true, nil
}

func validateGraphMemoryBlobRefKind(kind string) error {
	switch kind {
	case "attachment", "graph_source", "graph_version":
		return nil
	}
	return fmt.Errorf("graph memory blob: invalid ref kind %q", kind)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
