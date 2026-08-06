// migrate-uploads-to-s3 is an operator recovery command for the 2026-07-30
// OSS-outage incident (Aliyun OSS account UserDisable due to unpaid
// billing). While S3_BUCKET was unset in production, attachment uploads
// fell back to LocalStorage (server/internal/storage/local.go) and were
// written under LOCAL_UPLOAD_DIR with site-relative URLs
// ("/uploads/<key>") instead of S3 object URLs.
//
// S3Storage.KeyFromURL cannot parse a LocalStorage-style "/uploads/<key>"
// URL: none of its known prefixes match, so it falls back to "everything
// after the last '/'", which drops any directory components in <key>
// (e.g. "workspaces/<ws>/<file>" becomes just "<file>"). Reactivating
// S3Storage without fixing this up would make every attachment uploaded
// during the incident window unreadable, even after its bytes are copied
// to the bucket under the right key. This tool re-derives the key from
// the LocalStorage URL (which parses correctly), uploads the bytes to S3
// under that exact key, verifies the round trip by checksum, and only
// then rewrites attachment.url to the URL S3Storage itself produced —
// so both KeyFromURL and the on-disk key agree once S3 is the active
// backend again.
//
// Usage (run with OSS credentials restored in the environment, BEFORE or
// AFTER flipping the app's own S3_BUCKET back — order does not matter,
// since this tool talks to S3/local storage directly, not through the
// running app):
//
//	DATABASE_URL=... S3_BUCKET=leagent S3_REGION=cn-beijing \
//	AWS_ENDPOINT_URL=https://s3.oss-cn-beijing.aliyuncs.com \
//	S3_FORCE_PATH_STYLE=false \
//	AWS_REQUEST_CHECKSUM_CALCULATION=when_required AWS_RESPONSE_CHECKSUM_VALIDATION=when_required \
//	AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... \
//	LOCAL_UPLOAD_DIR=/app/data/uploads \
//	  ./migrate-uploads-to-s3 --since 2026-07-30T06:20:00Z
//
// This talks to Aliyun OSS through storage.NewS3StorageFromEnv, so it
// inherits every OSS compatibility gotcha documented in .env.example's
// "S3 / CloudFront" section (bucket must pre-exist / addressing style /
// checksum trailers) — the env vars above are not optional decoration.
//
// Defaults to --dry-run (lists candidate rows only, uploads nothing,
// writes nothing). Pass --dry-run=false to actually migrate. Re-running
// after a partial failure is safe: only rows whose url still starts with
// "/uploads/" are selected, so already-migrated rows (url rewritten to
// the S3 form) are skipped on the next pass.
//
// This tool never deletes the local file — the operator removes the
// LOCAL_UPLOAD_DIR contents (or leaves them; they are inert once no
// attachment.url references them) once satisfied with the migration.
//
// After attachment.url rows are rewritten, this tool also cascades a
// denormalized avatar_url backfill (agent / user / workspace / channel) via
// internal/avatarbackfill — migrate-uploads-to-s3 historically left those
// fields on "/uploads/...", which broke author_avatar_url joins once the app
// ran without a LocalStorage /uploads/* route. Standalone re-run:
// scripts/run-backfill-avatar-urls.sh or go run ./cmd/backfill-avatar-urls.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/avatarbackfill"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/storage"
)

func main() {
	logger.Init()
	if err := run(); err != nil {
		slog.Error("migrate-uploads-to-s3 failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		since  = flag.String("since", "", "RFC3339 timestamp; only migrate attachment rows created at or after this time (required)")
		dryRun = flag.Bool("dry-run", true, "list candidate rows without uploading or writing anything; pass --dry-run=false to migrate for real")
	)
	flag.Parse()

	if *since == "" {
		return fmt.Errorf("--since is required (e.g. --since 2026-07-30T06:20:00Z)")
	}
	sinceTime, err := time.Parse(time.RFC3339, *since)
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	s3 := storage.NewS3StorageFromEnv()
	if s3 == nil {
		return fmt.Errorf("S3_BUCKET not set — restore OSS credentials in the environment before running this tool")
	}
	local := storage.NewLocalStorageFromEnv()
	if local == nil {
		return fmt.Errorf("LOCAL_UPLOAD_DIR could not be initialized — check the directory exists and is readable")
	}

	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT id, url, filename, content_type
		FROM attachment
		WHERE created_at >= $1
		  AND url LIKE '/uploads/%'
		ORDER BY created_at ASC
	`, sinceTime)
	if err != nil {
		return fmt.Errorf("query candidate attachments: %w", err)
	}
	type candidate struct {
		id, url, filename, contentType string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.url, &c.filename, &c.contentType); err != nil {
			rows.Close()
			return fmt.Errorf("scan candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate candidates: %w", err)
	}

	slog.Info("found candidates", "count", len(candidates), "since", sinceTime, "dry_run", *dryRun)

	var migrated, failed int
	if len(candidates) == 0 {
		slog.Info("no attachment rows to migrate")
	} else {
		for _, c := range candidates {
			key := local.KeyFromURL(c.url)
			if *dryRun {
				slog.Info("would migrate", "id", c.id, "key", key)
				continue
			}

			reader, err := local.GetReader(ctx, key)
			if err != nil {
				slog.Error("read local file failed — leaving row unmigrated", "id", c.id, "key", key, "error", err)
				failed++
				continue
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				slog.Error("read local file body failed — leaving row unmigrated", "id", c.id, "key", key, "error", err)
				failed++
				continue
			}
			localSum := sha256.Sum256(data)

			newURL, err := s3.Upload(ctx, key, data, c.contentType, c.filename)
			if err != nil {
				slog.Error("s3 upload failed — leaving row unmigrated", "id", c.id, "key", key, "error", err)
				failed++
				continue
			}

			verifyReader, err := s3.GetReader(ctx, key)
			if err != nil {
				slog.Error("post-upload verify read failed — row uploaded but NOT marked migrated, investigate before re-running", "id", c.id, "key", key, "error", err)
				failed++
				continue
			}
			verifyData, err := io.ReadAll(verifyReader)
			verifyReader.Close()
			if err != nil {
				slog.Error("post-upload verify read body failed — row uploaded but NOT marked migrated, investigate before re-running", "id", c.id, "key", key, "error", err)
				failed++
				continue
			}
			verifySum := sha256.Sum256(verifyData)
			if hex.EncodeToString(localSum[:]) != hex.EncodeToString(verifySum[:]) {
				slog.Error("checksum mismatch after upload — row NOT marked migrated, investigate before re-running", "id", c.id, "key", key)
				failed++
				continue
			}

			if _, err := pool.Exec(ctx, `UPDATE attachment SET url = $1 WHERE id = $2`, newURL, c.id); err != nil {
				slog.Error("bytes verified on S3 but DB url update failed — re-run will retry (bytes are already correct, safe to retry)", "id", c.id, "key", key, "error", err)
				failed++
				continue
			}

			slog.Info("migrated", "id", c.id, "key", key, "new_url", newURL)
			migrated++
		}
	}

	slog.Info("attachment migrate done", "migrated", migrated, "failed", failed, "dry_run", *dryRun)

	// Cascade denormalized avatar_url refresh even when no attachment rows
	// remain (avatars may still be stale). Dry-run reports counts only.
	if _, err := avatarbackfill.Run(ctx, pool, avatarbackfill.PublicBaseURL(), *dryRun); err != nil {
		return fmt.Errorf("avatar URL cascade after attachment migrate: %w", err)
	}

	if failed > 0 {
		return fmt.Errorf("%d row(s) failed to migrate — see errors above, safe to re-run (already-migrated rows are skipped)", failed)
	}
	return nil
}
