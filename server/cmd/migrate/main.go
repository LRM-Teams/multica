package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/migrations"
	"github.com/multica-ai/multica/server/internal/taskusagebackfill"
)

// preMigrationHook runs work that must happen before a specific
// migration is applied during `migrate up`. Hooks are idempotent and
// must not depend on the migration loop's session-pinned advisory lock
// — they run on the pool, not on the loop's pinned conn, so they can
// safely acquire other session-level locks (e.g. advisory lock 4246
// for the historical hourly-usage rollup).
//
// Returning an error aborts the migration run. The corresponding
// migration is NOT recorded in schema_migrations, so the next run will
// retry the hook + migration.
type preMigrationHook func(ctx context.Context, pool *pgxpool.Pool) error

// preMigrationHooks wires migration version → hook. The version key is
// the file basename without the `.up.sql` suffix, matching what
// `migrations.ExtractVersion` returns.
//
// MUL-2957: the v0.3.4 → current direct-upgrade path needs the hourly
// rollup seeded BEFORE migration 103 evaluates its fail-closed lag
// guard, because at `cmd/migrate up` time the server has not yet
// started so neither the legacy pg_cron job nor the new app scheduler
// can advance the watermark. The hook runs the same idempotent
// monthly-slice backfill for the historical pre-103 schema. Current
// `agent_usage` recovery backfills use cmd/backfill_agent_usage_hourly.
var preMigrationHooks = map[string]preMigrationHook{
	"103_drop_legacy_daily_rollups":       runHistoricalUsageHourlyHook,
	"188_agent_ascii_handle_backfill":     runAgentASCIIHandleBackfillHook,
	"190_agent_handle_truncation_repair":  runAgentASCIIHandleBackfillHook,
	"191_agent_default_handle_repair":     runAgentDefaultHandleRepairHook,
	"227_agent_delete_fk_indexes":         runAgentDeleteFKIndexesHook,
	"229_agent_delete_cascade_fk_indexes": runAgentDeleteCascadeFKIndexesHook,
	"245_research_fleet_agent_indexes":    runResearchFleetAgentIndexesHook,
}

type concurrentIndexSpec struct {
	name string
	ddl  string
}

func runAgentDeleteFKIndexesHook(ctx context.Context, pool *pgxpool.Pool) error {
	// This registry is manual: whenever a migration drops a table, remove its
	// index spec here too or the hook will fail against the post-migration schema.
	indexes := []concurrentIndexSpec{
		{"idx_agent_inbox_event_runtime", `CREATE INDEX CONCURRENTLY idx_agent_inbox_event_runtime ON agent_inbox_event (runtime_id) WHERE runtime_id IS NOT NULL`},
		{"idx_memory_curation_watermark_agent", `CREATE INDEX CONCURRENTLY idx_memory_curation_watermark_agent ON memory_curation_watermark (agent_id)`},
		{"idx_evolution_unit_feedback_event_agent", `CREATE INDEX CONCURRENTLY idx_evolution_unit_feedback_event_agent ON evolution_unit_feedback_event (agent_id)`},
		{"idx_memory_curation_evidence_cursor_agent", `CREATE INDEX CONCURRENTLY idx_memory_curation_evidence_cursor_agent ON memory_curation_evidence_cursor (agent_id)`},
		{"idx_memory_curator_target_agent", `CREATE INDEX CONCURRENTLY idx_memory_curator_target_agent ON memory_curator_target (agent_id)`},
		{"idx_task_token_agent", `CREATE INDEX CONCURRENTLY idx_task_token_agent ON task_token (agent_id)`},
		{"idx_wendy_nudge_ladder_agent", `CREATE INDEX CONCURRENTLY idx_wendy_nudge_ladder_agent ON wendy_nudge_ladder (agent_id)`},
		{"idx_voice_call_session_agent", `CREATE INDEX CONCURRENTLY idx_voice_call_session_agent ON voice_call_session (agent_id)`},
		{"idx_agent_inbox_token_agent", `CREATE INDEX CONCURRENTLY idx_agent_inbox_token_agent ON agent_inbox_token (agent_id)`},
		{"idx_agent_session_agent", `CREATE INDEX CONCURRENTLY idx_agent_session_agent ON agent_session (agent_id)`},
		{"idx_channel_agent_session_agent", `CREATE INDEX CONCURRENTLY idx_channel_agent_session_agent ON channel_agent_session (agent_id)`},
		{"idx_channel_decision_audit_agent", `CREATE INDEX CONCURRENTLY idx_channel_decision_audit_agent ON channel_decision_audit (agent_id)`},
		{"idx_chat_session_agent", `CREATE INDEX CONCURRENTLY idx_chat_session_agent ON chat_session (agent_id)`},
		{"idx_collaboration_turn_agent", `CREATE INDEX CONCURRENTLY idx_collaboration_turn_agent ON collaboration_turn (agent_id)`},
		{"idx_environment_agent_sandbox_agent", `CREATE INDEX CONCURRENTLY idx_environment_agent_sandbox_agent ON environment_agent_sandbox (agent_id)`},
		{"idx_evolution_training_example_agent", `CREATE INDEX CONCURRENTLY idx_evolution_training_example_agent ON evolution_training_example (agent_id)`},
		{"idx_team_knowledge_item_curator_agent", `CREATE INDEX CONCURRENTLY idx_team_knowledge_item_curator_agent ON team_knowledge_item (created_by_curator_agent_id)`},
		{"idx_memory_curator_profile_curator_agent", `CREATE INDEX CONCURRENTLY idx_memory_curator_profile_curator_agent ON memory_curator_profile (curator_agent_id)`},
		{"idx_memory_curation_run_curator_agent", `CREATE INDEX CONCURRENTLY idx_memory_curation_run_curator_agent ON memory_curation_run (curator_agent_id)`},
		{"idx_squad_leader", `CREATE INDEX CONCURRENTLY idx_squad_leader ON squad (leader_id)`},
		{"idx_agent_creation_draft_used_agent", `CREATE INDEX CONCURRENTLY idx_agent_creation_draft_used_agent ON agent_creation_draft (used_agent_id)`},
		{"idx_agent_workspace_source_agent", `CREATE INDEX CONCURRENTLY idx_agent_workspace_source_agent ON agent (workspace_id, source_agent_id) WHERE source_agent_id IS NOT NULL`},
	}

	return ensureConcurrentIndexes(ctx, pool, indexes)
}

func runResearchFleetAgentIndexesHook(
	ctx context.Context,
	pool *pgxpool.Pool,
) error {
	indexes := []concurrentIndexSpec{
		{
			"idx_research_fleet_lead_agent",
			`CREATE INDEX CONCURRENTLY idx_research_fleet_lead_agent ON research_fleet (lead_agent_id) WHERE lead_agent_id IS NOT NULL`,
		},
		{
			"idx_research_graph_node_actor_agent",
			`CREATE INDEX CONCURRENTLY idx_research_graph_node_actor_agent ON research_graph_node (actor_agent_id) WHERE actor_agent_id IS NOT NULL`,
		},
		{
			"idx_research_message_target_agent",
			`CREATE INDEX CONCURRENTLY idx_research_message_target_agent ON research_message (target_agent_id) WHERE target_agent_id IS NOT NULL`,
		},
	}

	return ensureConcurrentIndexes(ctx, pool, indexes)
}

func runAgentDeleteCascadeFKIndexesHook(ctx context.Context, pool *pgxpool.Pool) error {
	// Deleting an archived agent cascades through inbox/session/history rows.
	// Every FK that points at any table in that recursive delete closure needs
	// a supporting index: PostgreSQL enforces CASCADE/SET NULL with a child
	// lookup per deleted parent row. Missing even one turns a bounded teardown
	// into repeated full-table scans (notably agent_inbox_event.parent_task_id).
	indexes := []concurrentIndexSpec{
		{"idx_agent_inbox_event_agent_session", `CREATE INDEX CONCURRENTLY idx_agent_inbox_event_agent_session ON agent_inbox_event (agent_session_id) WHERE agent_session_id IS NOT NULL`},
		{"idx_agent_inbox_event_chat_session", `CREATE INDEX CONCURRENTLY idx_agent_inbox_event_chat_session ON agent_inbox_event (chat_session_id) WHERE chat_session_id IS NOT NULL`},
		{"idx_agent_inbox_event_parent_task", `CREATE INDEX CONCURRENTLY idx_agent_inbox_event_parent_task ON agent_inbox_event (parent_task_id) WHERE parent_task_id IS NOT NULL`},
		{"idx_agent_inbox_event_source_chat_message", `CREATE INDEX CONCURRENTLY idx_agent_inbox_event_source_chat_message ON agent_inbox_event (source_chat_message_id) WHERE source_chat_message_id IS NOT NULL`},
		{"idx_agent_inbox_event_terminal_delivery", `CREATE INDEX CONCURRENTLY idx_agent_inbox_event_terminal_delivery ON agent_inbox_event (terminal_delivery_id) WHERE terminal_delivery_id IS NOT NULL`},
		{"idx_agent_inbox_token_delivery", `CREATE INDEX CONCURRENTLY idx_agent_inbox_token_delivery ON agent_inbox_token (delivery_id) WHERE delivery_id IS NOT NULL`},
		{"idx_agent_memory_curation_candidate_run", `CREATE INDEX CONCURRENTLY idx_agent_memory_curation_candidate_run ON agent_memory_curation_candidate (run_id) WHERE run_id IS NOT NULL`},
		{"idx_agent_memory_write_event_task", `CREATE INDEX CONCURRENTLY idx_agent_memory_write_event_task ON agent_memory_write_event (task_id) WHERE task_id IS NOT NULL`},
		{"idx_agent_reminder_fired_task", `CREATE INDEX CONCURRENTLY idx_agent_reminder_fired_task ON agent_reminder (fired_task_id) WHERE fired_task_id IS NOT NULL`},
		{"idx_agent_reminder_occurrence_fired_task", `CREATE INDEX CONCURRENTLY idx_agent_reminder_occurrence_fired_task ON agent_reminder_occurrence (fired_task_id) WHERE fired_task_id IS NOT NULL`},
		{"idx_agent_session_last_acked_event", `CREATE INDEX CONCURRENTLY idx_agent_session_last_acked_event ON agent_session (last_acked_event_id) WHERE last_acked_event_id IS NOT NULL`},
		{"idx_channel_decision_audit_inbox_event", `CREATE INDEX CONCURRENTLY idx_channel_decision_audit_inbox_event ON channel_decision_audit (inbox_event_id) WHERE inbox_event_id IS NOT NULL`},
		{"idx_channel_message_attachment_workspace_attachment", `CREATE INDEX CONCURRENTLY idx_channel_message_attachment_workspace_attachment ON channel_message_attachment (workspace_id, attachment_id) WHERE attachment_id IS NOT NULL`},
		{"idx_channel_voice_transcription_attachment", `CREATE INDEX CONCURRENTLY idx_channel_voice_transcription_attachment ON channel_voice_transcription (attachment_id) WHERE attachment_id IS NOT NULL`},
		{"idx_env_dispatch_run_root_task", `CREATE INDEX CONCURRENTLY idx_env_dispatch_run_root_task ON env_dispatch_run (root_task_id) WHERE root_task_id IS NOT NULL`},
		{"idx_evolution_unit_feedback_event_task", `CREATE INDEX CONCURRENTLY idx_evolution_unit_feedback_event_task ON evolution_unit_feedback_event (task_id) WHERE task_id IS NOT NULL`},
		{"idx_lark_user_binding_installation_workspace", `CREATE INDEX CONCURRENTLY idx_lark_user_binding_installation_workspace ON lark_user_binding (installation_id, workspace_id)`},
		{"idx_memory_curation_watermark_last_run", `CREATE INDEX CONCURRENTLY idx_memory_curation_watermark_last_run ON memory_curation_watermark (last_run_id) WHERE last_run_id IS NOT NULL`},
		{"idx_work_node_linked_task", `CREATE INDEX CONCURRENTLY idx_work_node_linked_task ON work_node (linked_task_id) WHERE linked_task_id IS NOT NULL`},
	}

	return ensureConcurrentIndexes(ctx, pool, indexes)
}

func ensureConcurrentIndexes(ctx context.Context, pool *pgxpool.Pool, indexes []concurrentIndexSpec) error {
	for _, index := range indexes {
		var valid bool
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE((
				SELECT i.indisvalid AND i.indisready
				FROM pg_class c
				JOIN pg_index i ON i.indexrelid = c.oid
				WHERE c.relnamespace = 'public'::regnamespace
				  AND c.relname = $1
			), false)
		`, index.name).Scan(&valid); err != nil {
			return fmt.Errorf("inspect concurrent index %s: %w", index.name, err)
		}
		if valid {
			continue
		}

		// An interrupted CREATE INDEX CONCURRENTLY can leave an invalid index
		// with the final name. Drop only that invalid remnant, then retry.
		drop := fmt.Sprintf(
			"DROP INDEX CONCURRENTLY IF EXISTS %s",
			pgx.Identifier{index.name}.Sanitize(),
		)
		if _, err := pool.Exec(ctx, drop); err != nil {
			return fmt.Errorf("drop invalid concurrent index %s: %w", index.name, err)
		}
		if _, err := pool.Exec(ctx, index.ddl); err != nil {
			return fmt.Errorf("create concurrent index %s: %w", index.name, err)
		}
	}
	return nil
}

func runHistoricalUsageHourlyHook(ctx context.Context, pool *pgxpool.Pool) error {
	res, err := taskusagebackfill.Hook(ctx, pool, taskusagebackfill.HookOptions{})
	if err != nil {
		return fmt.Errorf("historical usage pre-103 hook: %w", err)
	}
	if res.Skipped != "" {
		slog.Info("historical usage hourly rollup hook: skipped",
			"reason", res.Skipped,
			"watermark_stamped", res.WatermarkStamped)
		return nil
	}
	slog.Info("historical usage hourly rollup hook: backfill complete",
		"slices", res.SlicesProcessed,
		"rows_touched", res.RowsTouched,
		"from", res.From.Format("2006-01-02T15:04:05Z07:00"),
		"to", res.To.Format("2006-01-02T15:04:05Z07:00"))
	return nil
}

// migrationAdvisoryLockKey is the int64 identifier used with Postgres
// pg_advisory_lock to serialize the migration loop across concurrent
// runners (multi-replica backend Deployment, scale-up, or a manual
// `migrate up` overlapping with pod startup). The exact value is
// arbitrary — it just needs to be stable across every process that runs
// migrations against the same database. See GitHub multica-ai/multica#3647.
const migrationAdvisoryLockKey int64 = 7244554146635925501

// defaultSchemaMigrationsTable is the unqualified name of the bookkeeping
// table that tracks which migrations have been applied. Tests override
// this so a concurrent-race harness can run against the same shared
// Postgres without colliding with the production table.
const defaultSchemaMigrationsTable = "schema_migrations"

// runOptions carries everything runMigrations needs that is not the
// pool itself. Tests use it to inject a hermetic migrations directory,
// a unique per-test bookkeeping table, and a unique advisory-lock key
// that doesn't collide with any other migration runner sharing the same
// Postgres instance.
type runOptions struct {
	// Direction is "up" or "down".
	Direction string
	// Files is the ordered list of .sql files to apply. Production callers
	// pass migrations.Files(direction); tests pass a curated set written
	// to a t.TempDir().
	Files []string
	// SchemaMigrationsTable is the bookkeeping table to read/write.
	// May be schema-qualified (e.g. "migrate_test_xyz.schema_migrations").
	// Empty means defaultSchemaMigrationsTable.
	SchemaMigrationsTable string
	// AdvisoryLockKey is the int64 used with pg_advisory_lock. Zero means
	// migrationAdvisoryLockKey. Tests pass a unique key per run so
	// concurrent test workers do not block on the production migration
	// runner if it happens to share the database.
	AdvisoryLockKey int64
	// Hooks maps migration version → pre-migration hook. The hook
	// receives the pool (not the loop's pinned conn) so it can take
	// its own session-level locks. nil or missing entries mean "no
	// hook" and the migration runs straight through. Production main()
	// passes preMigrationHooks; tests leave this nil.
	Hooks map[string]preMigrationHook
}

func newMigrationPool(ctx context.Context, dbURL string, noticeOutput io.Writer) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, err
	}

	noticeLogger := log.New(noticeOutput, "", 0)
	config.ConnConfig.OnNotice = func(_ *pgconn.PgConn, notice *pgconn.Notice) {
		severity := notice.SeverityUnlocalized
		if severity == "" {
			severity = notice.Severity
		}
		noticeLogger.Printf("  %s  %s", strings.ToLower(severity), notice.Message)
	}

	return pgxpool.NewWithConfig(ctx, config)
}

func main() {
	logger.Init()

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run ./cmd/migrate <up|down>")
		os.Exit(1)
	}

	direction := os.Args[1]
	if direction != "up" && direction != "down" {
		fmt.Println("Usage: go run ./cmd/migrate <up|down>")
		os.Exit(1)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := newMigrationPool(ctx, dbURL, os.Stdout)
	if err != nil {
		slog.Error("unable to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		slog.Error("unable to ping database", "error", err)
		os.Exit(1)
	}

	files, err := migrations.Files(direction)
	if err != nil {
		slog.Error("failed to find migration files", "error", err)
		os.Exit(1)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction: direction,
		Files:     files,
		Hooks:     preMigrationHooks,
	}); err != nil {
		slog.Error("migration run failed", "error", err)
		os.Exit(1)
	}

	fmt.Println("Done.")
}

// runMigrations applies (direction="up") or rolls back (direction="down")
// the given file list against the supplied pool, serialized through a
// Postgres session-level advisory lock so multiple concurrent runners
// (multi-replica startup, scale-up, manual migrate overlap) take turns
// instead of racing each other.
//
// It is safe to invoke concurrently from multiple goroutines or
// processes against the same database with the same options: every
// caller blocks on pg_advisory_lock, and once it is their turn the
// already-applied EXISTS check turns each finished migration into a
// no-op skip. See GitHub multica-ai/multica#3647 / MUL-2923.
func runMigrations(ctx context.Context, pool *pgxpool.Pool, opts runOptions) error {
	switch opts.Direction {
	case "up", "down":
		// ok
	default:
		return fmt.Errorf("invalid direction %q (want \"up\" or \"down\")", opts.Direction)
	}

	table := opts.SchemaMigrationsTable
	if table == "" {
		table = defaultSchemaMigrationsTable
	}
	tableIdent, err := quoteQualifiedIdentifier(table)
	if err != nil {
		return fmt.Errorf("invalid schema migrations table %q: %w", table, err)
	}
	lockKey := opts.AdvisoryLockKey
	if lockKey == 0 {
		lockKey = migrationAdvisoryLockKey
	}

	// pg_advisory_lock is scoped to a single session, so we must pin one
	// *pgxpool.Conn for the whole run — calling pool.Exec would attach the
	// lock to a random connection that pgxpool could hand back out before
	// the loop finishes, making the lock effectively a no-op. We use the
	// blocking pg_advisory_lock (not pg_try_*) so a late-arriving runner
	// queues behind the current one instead of crash-looping; once it
	// acquires the lock the EXISTS checks below turn finished migrations
	// into no-op skips.
	//
	// We deliberately do NOT wrap the loop in a single transaction: the
	// repo already ships migrations using CREATE INDEX CONCURRENTLY,
	// which Postgres rejects inside a transaction block.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	// Best-effort explicit unlock on the success path. On error returns
	// the defer still runs; on os.Exit error paths in main() it does not,
	// but session-level advisory locks are released automatically when
	// the connection closes at process exit, so the next runner is never
	// permanently blocked.
	defer func() {
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey); err != nil {
			slog.Warn("failed to release migration advisory lock", "error", err)
		}
	}()

	// Create migrations tracking table.
	if _, err := conn.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`, tableIdent)); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	existsSQL := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE version = $1)", tableIdent)
	insertSQL := fmt.Sprintf("INSERT INTO %s (version) VALUES ($1)", tableIdent)
	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE version = $1", tableIdent)

	for _, file := range opts.Files {
		version := migrations.ExtractVersion(file)

		var exists bool
		if err := conn.QueryRow(ctx, existsSQL, version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %q: %w", version, err)
		}

		if opts.Direction == "up" {
			if exists {
				fmt.Printf("  skip  %s (already applied)\n", version)
				continue
			}
		} else {
			if !exists {
				fmt.Printf("  skip  %s (not applied)\n", version)
				continue
			}
		}

		sql, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", file, err)
		}

		// Run any pre-migration hook before the SQL file. Hooks
		// receive the *pgxpool.Pool (not the loop's pinned conn), so
		// they can acquire other session-level locks without
		// colliding with migrationAdvisoryLockKey. Hook failures
		// abort the run before schema_migrations is updated, so the
		// same version retries cleanly on the next invocation.
		if opts.Direction == "up" {
			if hook, ok := opts.Hooks[version]; ok && hook != nil {
				slog.Info("running pre-migration hook", "version", version)
				if err := hook(ctx, pool); err != nil {
					return fmt.Errorf("pre-migration hook for %q: %w", version, err)
				}
			}
		}

		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %q: %w", file, err)
		}

		if opts.Direction == "up" {
			_, err = conn.Exec(ctx, insertSQL, version)
		} else {
			_, err = conn.Exec(ctx, deleteSQL, version)
		}
		if err != nil {
			return fmt.Errorf("record migration %q: %w", version, err)
		}

		fmt.Printf("  %s  %s\n", opts.Direction, version)
	}

	return nil
}

// quoteQualifiedIdentifier safely quotes either an unqualified table
// name ("foo") or a schema-qualified name ("schema.foo") for embedding
// into a SQL statement. Postgres does not let parametrized queries
// supply identifiers, so we have to interpolate, but pgx.Identifier
// does the right escaping (double-quotes, embedded-quote handling).
//
// The accepted shape is exactly one or two dot-separated components.
// Names containing more than one dot are rejected outright rather than
// silently sanitized into a "schema"."b.c" reference, which is valid
// SQL but almost certainly not what the caller meant.
func quoteQualifiedIdentifier(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty identifier")
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return "", fmt.Errorf("identifier %q has more than one dot; only schema.table is supported", name)
	}
	for _, p := range parts {
		if p == "" {
			return "", fmt.Errorf("empty component in %q", name)
		}
	}
	return pgx.Identifier(parts).Sanitize(), nil
}
