package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/runtimecleanup"
)

type output struct {
	DryRun    bool                    `json:"dry_run"`
	Apply     bool                    `json:"apply"`
	StartedAt time.Time               `json:"started_at"`
	Before    runtimecleanup.Summary  `json:"before"`
	Deleted   *int64                  `json:"deleted,omitempty"`
	After     *runtimecleanup.Summary `json:"after,omitempty"`
}

func main() {
	logger.Init()
	if err := run(); err != nil {
		slog.Error("runtime system message cleanup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		apply              = flag.Bool("apply", false, "physically delete matching legacy runtime current-state channel messages; default is dry-run preview only")
		confirm            = flag.String("confirm", "", "required with --apply; must equal "+runtimecleanup.ApplyConfirmation)
		workspaceID        = flag.String("workspace-id", "", "optional workspace UUID filter")
		channelID          = flag.String("channel-id", "", "optional channel UUID filter")
		sampleLimit        = flag.Int("sample-limit", 20, "number of candidate samples to include in preview output")
		maxDelete          = flag.Int64("max-delete", 0, "refuse --apply when matched rows exceed this count (0 = no count cap)")
		allowThreadCascade = flag.Bool("allow-thread-cascade", false, "allow deleting a candidate that is a thread root with replies; default refuses to avoid accidental reply cascade")
	)
	flag.Parse()

	if *apply && *confirm != runtimecleanup.ApplyConfirmation {
		return fmt.Errorf("--apply requires --confirm=%s", runtimecleanup.ApplyConfirmation)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	opts := runtimecleanup.CleanupOptions{
		WorkspaceID:        *workspaceID,
		ChannelID:          *channelID,
		SampleLimit:        *sampleLimit,
		MaxDelete:          *maxDelete,
		AllowThreadCascade: *allowThreadCascade,
	}

	before, err := runtimecleanup.PreviewLegacyRuntimeSystemMessages(ctx, pool, opts)
	if err != nil {
		return err
	}

	result := output{
		DryRun:    !*apply,
		Apply:     *apply,
		StartedAt: time.Now().UTC(),
		Before:    before,
	}

	if *apply {
		deleted, err := runtimecleanup.DeleteLegacyRuntimeSystemMessages(ctx, pool, opts)
		if err != nil {
			return err
		}
		result.Deleted = &deleted

		after, err := runtimecleanup.PreviewLegacyRuntimeSystemMessages(context.Background(), pool, opts)
		if err != nil {
			return err
		}
		result.After = &after
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("write cleanup report: %w", err)
	}
	return nil
}
