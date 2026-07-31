// backfill-avatar-urls rewrites stale LocalStorage-style avatar URLs
// ("/uploads/workspaces/...") to the deployment's public OSS base URL after
// an OSS-outage fallback window. Prefer running this after
// migrate-uploads-to-s3 (which now cascades the same rewrite); this command
// is the one-click re-run path when only denormalized fields are still stale.
//
// Usage:
//
//	DATABASE_URL=... S3_PUBLIC_BASE_URL=https://leagent.s3.oss-cn-beijing.aliyuncs.com \
//	  go run ./cmd/backfill-avatar-urls --dry-run=false
//
// Defaults to --dry-run (reports counts only, writes nothing). Safe to re-run:
// only rows whose avatar URL still starts with "/uploads/" are selected.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/avatarbackfill"
	"github.com/multica-ai/multica/server/internal/logger"
)

func main() {
	logger.Init()
	if err := run(); err != nil {
		slog.Error("backfill-avatar-urls failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	dryRun := flag.Bool("dry-run", true, "report candidate counts without writing; pass --dry-run=false to apply")
	flag.Parse()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	_, err = avatarbackfill.Run(context.Background(), pool, avatarbackfill.PublicBaseURL(), *dryRun)
	return err
}
