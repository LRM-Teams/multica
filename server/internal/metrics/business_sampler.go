// Package-level note for PR4 (MUL-2947): the sampler runs at /metrics scrape
// time, hits the read replica via the existing pgxpool, and is opt-in. Every
// individual SQL statement runs in its own short read-only transaction with
// `SET LOCAL statement_timeout = '500ms'` and a hard `LIMIT 100` so a slow
// table or a hung connection cannot drag /metrics — and by extension the
// whole alerting story — down. A 5–10 second TTL cache absorbs concurrent
// scrapes from multiple Prometheus replicas.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Default knobs. Kept conservative so the very first scrape on a busy DB
// cannot stall /metrics for more than ~half a second per query, and so a
// single scrape can never spawn a flood of identical reads even if multiple
// Prometheus replicas attach to the same pod at once.
const (
	defaultSamplerCacheTTL     = 8 * time.Second
	defaultSamplerQueryTimeout = 500 * time.Millisecond
	samplerRowLimit            = 100

	// Active-user / active-workspace DB windows. Keep this DB-sampled path
	// to the short window only: PR2's counters do not carry user/workspace
	// IDs, so PromQL cannot derive distinct 1h/24h active estimates without
	// adding high-cardinality labels. Long-window actives need a separate
	// counter-derived aggregation, not expanding this sampler over history.
	windowFiveMinutes = "5m"

	// Runtime is considered online if its last_seen_at is within this
	// many seconds of `now()`. 60s matches the daemon heartbeat cadence
	// (~15s) plus headroom for redis relay lag and clock skew.
	runtimeOnlineWindowSeconds = 60

	// A running task is considered "stuck" once started_at is older
	// than this. Matches the Grafana board threshold from MUL-2328.
	stuckRunningInterval = "30 minutes"

	// Scheduled reminders count as ops-visible aggregate debt once fire_at is
	// this far past due. This metric is observational only; daemon-owned timers
	// remain the sole Reminder fire mechanism.
	reminderOverdueThreshold = time.Hour
)

// samplerWindows is the canonical list emitted on every scrape. The slice
// (rather than ranging a map) is intentional: order is stable in
// /metrics output, which keeps diffs readable and golden tests honest.
var samplerWindows = []struct {
	label string
	d     time.Duration
}{
	{windowFiveMinutes, 5 * time.Minute},
}

// BusinessSamplerOptions configures the BusinessSamplerCollector. A nil
// receiver in the registry means the sampler is disabled, which is the
// expected state for unit tests and any deployment where the operator does
// not opt in.
type BusinessSamplerOptions struct {
	// Pool is the dedicated pgxpool used by the sampler. Callers SHOULD
	// build a small pool (MaxConns 1–2) pointed at the same database as
	// the main app pool, so a sampler stall cannot starve business
	// traffic. If a caller really wants to share the main pool, they may
	// pass it here — the per-query statement_timeout still bounds the
	// blast radius.
	Pool *pgxpool.Pool

	// CacheTTL is how long a successful sample is reused before the next
	// scrape triggers a refresh. Defaults to 8s. The spec calls for
	// 5–10s; values outside that range are accepted but logged.
	CacheTTL time.Duration

	// QueryTimeout is the per-query statement_timeout pushed to Postgres
	// via SET LOCAL. Defaults to 500ms.
	QueryTimeout time.Duration
}

// samplerQuerier is the minimal pgx surface the sampler needs. Splitting it
// out lets unit tests inject a fake without spinning up a real database.
type samplerQuerier interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

// BusinessSamplerCollector implements prometheus.Collector by issuing a fixed
// set of read-only SQL queries on each scrape and exposing the results as
// gauges. See the package note above for the safety contract.
type BusinessSamplerCollector struct {
	pool         samplerQuerier
	cacheTTL     time.Duration
	queryTimeout time.Duration
	now          func() time.Time
	logger       *slog.Logger

	// refreshFn is the snapshot-producing function. Defaults to
	// refreshFromDB. Tests swap it out for a fake so they can exercise
	// caching / cardinality / emit logic without spinning up Postgres.
	refreshFn func(ctx context.Context, now time.Time) *samplerSnapshot

	// Self-introspection metrics. Registered alongside the collector via
	// Collectors() so /metrics shows query latency and error counts even
	// when the gauges themselves are stale.
	queryDuration *prometheus.HistogramVec
	queryErrors   *prometheus.CounterVec

	// Gauge descriptors. ConstMetric is preferred over a GaugeVec because
	// the values are computed once per scrape from a fresh DB read; we
	// never want stale series sticking around because a label has stopped
	// appearing in the result set.
	descActiveUsers                 *prometheus.Desc
	descActiveWorkspaces            *prometheus.Desc
	descTaskQueued                  *prometheus.Desc
	descTaskRunning                 *prometheus.Desc
	descTaskStuck                   *prometheus.Desc
	descRuntimeOnline               *prometheus.Desc
	descHeartbeatAgeHist            *prometheus.Desc
	descWorkspaceTotal              *prometheus.Desc
	descReminderScheduledOverdue    *prometheus.Desc
	descResearchRuns                *prometheus.Desc
	descResearchTasks               *prometheus.Desc
	descResearchAttempts            *prometheus.Desc
	descResearchEvidence            *prometheus.Desc
	descResearchFrontier            *prometheus.Desc
	descResearchBacklog             *prometheus.Desc
	descResearchCancellationBacklog *prometheus.Desc
	descResearchV6Health            *prometheus.Desc

	mu       sync.Mutex
	snapshot *samplerSnapshot
}

// NewBusinessSamplerCollector builds the collector. Returns nil when opts
// is nil or has a nil Pool — that signals "sampler disabled" to the
// registry without forcing every test to provide a stub.
func NewBusinessSamplerCollector(opts *BusinessSamplerOptions) *BusinessSamplerCollector {
	if opts == nil || opts.Pool == nil {
		return nil
	}
	cacheTTL := opts.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultSamplerCacheTTL
	}
	queryTimeout := opts.QueryTimeout
	if queryTimeout <= 0 {
		queryTimeout = defaultSamplerQueryTimeout
	}
	c := &BusinessSamplerCollector{
		pool:         opts.Pool,
		cacheTTL:     cacheTTL,
		queryTimeout: queryTimeout,
		now:          time.Now,
		logger:       slog.Default(),

		queryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "business_sampler",
			Name:      "query_seconds",
			Help:      "Per-query duration of the BusinessSamplerCollector. The `name` label is one of the fixed query identifiers and never user-controlled.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}, []string{"name"}),
		queryErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "business_sampler",
			Name:      "query_errors_total",
			Help:      "Per-query error count. Includes statement_timeout cancellations, which are the expected outcome of a hung database and the smoking gun for SET LOCAL working as intended.",
		}, []string{"name"}),

		descActiveUsers: prometheus.NewDesc(
			"multica_active_users",
			"Distinct users with chat / task activity in the rolling window. Sampled from the database; stale up to the sampler cache TTL.",
			[]string{"window"}, nil),
		descActiveWorkspaces: prometheus.NewDesc(
			"multica_active_workspaces",
			"Distinct workspaces with chat / task activity in the rolling window. Sampled from the database.",
			[]string{"window"}, nil),
		descTaskQueued: prometheus.NewDesc(
			"multica_agent_task_queued",
			"Current agent_inbox_event rows in `queued` status by inferred source. Sampled from the database.",
			[]string{"source"}, nil),
		descTaskRunning: prometheus.NewDesc(
			"multica_agent_task_running",
			"Current agent_inbox_event rows in `dispatched` or `running` status by inferred source and runtime mode. Sampled from the database.",
			[]string{"source", "runtime_mode"}, nil),
		descTaskStuck: prometheus.NewDesc(
			"multica_agent_task_stuck_total",
			"Current `running` agent_inbox_event rows whose started_at is older than the stuck threshold. Sampled from the database.",
			[]string{"source"}, nil),
		descRuntimeOnline: prometheus.NewDesc(
			"multica_runtime_online",
			"Count of agent_runtime rows with last_seen_at within the online heartbeat window. Sampled from the database.",
			[]string{"runtime_mode", "provider"}, nil),
		descHeartbeatAgeHist: prometheus.NewDesc(
			"multica_runtime_heartbeat_age_seconds",
			"Distribution of (now() - agent_runtime.last_seen_at) for runtimes considered online by the sampler.",
			[]string{"runtime_mode"}, nil),
		descWorkspaceTotal: prometheus.NewDesc(
			"multica_workspace_total",
			"Lifetime workspace row count. Useful for sizing alerts and dashboards.",
			nil, nil),
		// Platform-ops aggregate for task #73 Phase A. No labels: a single
		// global count is enough to detect "reminder fire path stopped
		// globally" without high-cardinality series. Alerter (PR2) reads
		// this gauge; user-facing per-reminder Activity is #67.
		descReminderScheduledOverdue: prometheus.NewDesc(
			"multica_reminder_scheduled_overdue",
			"Count of agent_reminder rows still status=scheduled with fire_at older than the overdue threshold (1h, aligned with #67). Platform-ops aggregate; sampled from the database.",
			nil, nil),
		descResearchRuns: prometheus.NewDesc(
			"multica_research_runs",
			"Current research sessions by durable run status and bounded orchestrator version.",
			[]string{"status", "orchestrator_version"}, nil),
		descResearchTasks: prometheus.NewDesc(
			"multica_research_tasks",
			"Current durable research tasks by task kind and status.",
			[]string{"kind", "status"}, nil),
		descResearchAttempts: prometheus.NewDesc(
			"multica_research_attempts",
			"Current durable research task attempts by status and bounded failure class.",
			[]string{"status", "failure_class"}, nil),
		descResearchEvidence: prometheus.NewDesc(
			"multica_research_evidence_artifacts",
			"Current normalized research evidence artifacts by type and verification status.",
			[]string{"artifact", "verification_status"}, nil),
		descResearchFrontier: prometheus.NewDesc(
			"multica_research_frontier",
			"Research question frontier size and age. The measure label is bounded to open or oldest_age_seconds.",
			[]string{"measure"}, nil),
		descResearchBacklog: prometheus.NewDesc(
			"multica_research_projection_backlog",
			"Unprojected research event count and oldest event age. The measure label is bounded to pending or oldest_age_seconds.",
			[]string{"measure"}, nil),
		descResearchCancellationBacklog: prometheus.NewDesc(
			"multica_research_cancellation_backlog",
			"Research attempt cancellations awaiting durable inbox cleanup, including oldest pending age.",
			[]string{"measure"}, nil),
		descResearchV6Health: prometheus.NewDesc(
			"multica_research_v6_health",
			"Operational conditions for persisted V6 runs. Labels are a closed, operator-actionable set.",
			[]string{"condition"}, nil),
	}
	c.refreshFn = c.refreshFromDB
	return c
}

// Collectors returns every prometheus.Collector that the registry must mount
// to expose this sampler — the gauges (the receiver itself) plus the query
// duration histogram and error counter.
func (c *BusinessSamplerCollector) Collectors() []prometheus.Collector {
	if c == nil {
		return nil
	}
	return []prometheus.Collector{c, c.queryDuration, c.queryErrors}
}

// Describe implements prometheus.Collector.
func (c *BusinessSamplerCollector) Describe(ch chan<- *prometheus.Desc) {
	if c == nil {
		return
	}
	for _, d := range []*prometheus.Desc{
		c.descActiveUsers,
		c.descActiveWorkspaces,
		c.descTaskQueued,
		c.descTaskRunning,
		c.descTaskStuck,
		c.descRuntimeOnline,
		c.descHeartbeatAgeHist,
		c.descWorkspaceTotal,
		c.descReminderScheduledOverdue,
		c.descResearchRuns,
		c.descResearchTasks,
		c.descResearchAttempts,
		c.descResearchEvidence,
		c.descResearchFrontier,
		c.descResearchBacklog,
		c.descResearchCancellationBacklog,
		c.descResearchV6Health,
	} {
		ch <- d
	}
}

// Collect implements prometheus.Collector. It returns the cached snapshot
// when fresh; otherwise it triggers a refresh under the mutex so concurrent
// scrapes share one DB round-trip. A refresh failure is logged and the last
// known snapshot is reused — this is the "metric briefly stale, sampler
// does not crash" behavior the PR4 spec requires under DB hangs.
func (c *BusinessSamplerCollector) Collect(ch chan<- prometheus.Metric) {
	if c == nil || c.pool == nil {
		return
	}
	snap := c.maybeRefresh()
	if snap == nil {
		return
	}
	c.emit(ch, snap)
}

func (c *BusinessSamplerCollector) maybeRefresh() *samplerSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.snapshot != nil && now.Sub(c.snapshot.takenAt) < c.cacheTTL {
		return c.snapshot
	}

	// Bound the entire refresh to N×queryTimeout so an in-flight scrape
	// can never block forever even if SET LOCAL is somehow ignored by a
	// misconfigured Postgres.
	ctx, cancel := context.WithTimeout(context.Background(), 10*c.queryTimeout)
	defer cancel()

	next := c.refreshFn(ctx, now)
	if next != nil {
		c.snapshot = next
	}
	return c.snapshot
}

// emit walks a snapshot and writes one prometheus.Metric per (desc, labels)
// pair. Pure data-shaping; no DB or locking.
func (c *BusinessSamplerCollector) emit(ch chan<- prometheus.Metric, snap *samplerSnapshot) {
	for _, w := range samplerWindows {
		ch <- prometheus.MustNewConstMetric(
			c.descActiveUsers, prometheus.GaugeValue, snap.activeUsers[w.label], w.label)
		ch <- prometheus.MustNewConstMetric(
			c.descActiveWorkspaces, prometheus.GaugeValue, snap.activeWorkspaces[w.label], w.label)
	}

	// Always emit at least one zero-valued series for queued/running per
	// known source so dashboards don't show "no data" right after a
	// process restart. Real values overwrite the zero; missing sources
	// stay at zero.
	for _, source := range knownSourceLabels() {
		ch <- prometheus.MustNewConstMetric(
			c.descTaskQueued, prometheus.GaugeValue, snap.taskQueued[source], source)
		ch <- prometheus.MustNewConstMetric(
			c.descTaskStuck, prometheus.GaugeValue, snap.taskStuck[source], source)
	}
	for _, source := range knownSourceLabels() {
		for _, mode := range knownRuntimeModeLabels() {
			key := taskRunningKey{source: source, runtimeMode: mode}
			ch <- prometheus.MustNewConstMetric(
				c.descTaskRunning, prometheus.GaugeValue, snap.taskRunning[key], source, mode)
		}
	}

	for key, val := range snap.runtimeOnline {
		ch <- prometheus.MustNewConstMetric(
			c.descRuntimeOnline, prometheus.GaugeValue, val, key.runtimeMode, key.provider)
	}

	for mode, hist := range snap.heartbeatAge {
		ch <- prometheus.MustNewConstHistogram(
			c.descHeartbeatAgeHist,
			hist.count,
			hist.sum,
			hist.buckets,
			mode,
		)
	}

	if snap.workspaceTotalKnown {
		ch <- prometheus.MustNewConstMetric(
			c.descWorkspaceTotal, prometheus.GaugeValue, snap.workspaceTotal)
	}

	// Always emit (including 0) so scrapes/alerters never see "no data"
	// after a clean start or empty fleet — same rationale as known-source
	// zero series for task queues.
	ch <- prometheus.MustNewConstMetric(
		c.descReminderScheduledOverdue, prometheus.GaugeValue, snap.reminderScheduledOverdue)

	for key, value := range snap.researchRuns {
		ch <- prometheus.MustNewConstMetric(
			c.descResearchRuns, prometheus.GaugeValue, value, key.first, key.second)
	}
	for key, value := range snap.researchTasks {
		ch <- prometheus.MustNewConstMetric(
			c.descResearchTasks, prometheus.GaugeValue, value, key.first, key.second)
	}
	for key, value := range snap.researchAttempts {
		ch <- prometheus.MustNewConstMetric(
			c.descResearchAttempts, prometheus.GaugeValue, value, key.first, key.second)
	}
	for key, value := range snap.researchEvidence {
		ch <- prometheus.MustNewConstMetric(
			c.descResearchEvidence, prometheus.GaugeValue, value, key.first, key.second)
	}
	ch <- prometheus.MustNewConstMetric(
		c.descResearchFrontier, prometheus.GaugeValue, snap.researchFrontierOpen, "open")
	ch <- prometheus.MustNewConstMetric(
		c.descResearchFrontier, prometheus.GaugeValue, snap.researchFrontierOldestAge, "oldest_age_seconds")
	ch <- prometheus.MustNewConstMetric(
		c.descResearchBacklog, prometheus.GaugeValue, snap.researchProjectionPending, "pending")
	ch <- prometheus.MustNewConstMetric(
		c.descResearchBacklog, prometheus.GaugeValue, snap.researchProjectionOldestAge, "oldest_age_seconds")
	ch <- prometheus.MustNewConstMetric(
		c.descResearchCancellationBacklog, prometheus.GaugeValue, snap.researchCancellationPending, "pending")
	ch <- prometheus.MustNewConstMetric(
		c.descResearchCancellationBacklog, prometheus.GaugeValue, snap.researchCancellationOldestAge, "oldest_age_seconds")
	for _, condition := range []string{"awaiting_director", "director_unavailable", "lost_attempt", "report_object_missing"} {
		ch <- prometheus.MustNewConstMetric(c.descResearchV6Health, prometheus.GaugeValue, snap.researchV6Health[condition], condition)
	}
}

// knownSourceLabels enumerates the source values we always emit a zero for.
// Pulled from the existing BusinessMetrics whitelist so the sampler never
// invents a new bucket.
func knownSourceLabels() []string {
	return []string{"chat", "issue", "autopilot", "autopilot_issue", "quick_create", "manual", "api", "other"}
}

func knownRuntimeModeLabels() []string {
	return []string{"local", "cloud", "unknown"}
}

// heartbeatAgeBuckets matches the Grafana board's runtime-health view: a few
// seconds for healthy heartbeats, then quickly out to "definitely stale".
var heartbeatAgeBuckets = []float64{1, 5, 15, 30, 60, 120, 300, 600}

// taskRunningKey is the composite gauge label key for the running counter.
// Defined here because it is shared between the snapshot and the emit path.
type taskRunningKey struct {
	source      string
	runtimeMode string
}

type runtimeOnlineKey struct {
	runtimeMode string
	provider    string
}

type researchMetricKey struct {
	first  string
	second string
}

func normalizeResearchRunStatus(raw string) string {
	switch raw {
	case "drafting", "running", "awaiting_user_confirm", "completed", "archived", "paused", "failed", "cancelled":
		return raw
	default:
		return "other"
	}
}

func normalizeResearchOrchestratorVersion(raw string) string {
	switch raw {
	case "research-run-v1", "research-run-v2", "research-run-v3", "research-run-v4", "research-run-v5", "research-run-v6", "legacy":
		return raw
	default:
		return "other"
	}
}

func normalizeResearchTaskKind(raw string) string {
	switch raw {
	case "plan", "discover", "deep_read", "verify", "counter_search", "replan", "synthesize", "quality_gate", "citation_audit":
		return raw
	default:
		return "other"
	}
}

func normalizeResearchTaskStatus(raw string) string {
	switch raw {
	case "pending", "ready", "dispatching", "running", "succeeded", "failed", "blocked", "obsolete", "cancelled":
		return raw
	default:
		return "other"
	}
}

func normalizeResearchAttemptStatus(raw string) string {
	switch raw {
	case "dispatching", "running", "succeeded", "failed", "cancelled", "lost":
		return raw
	default:
		return "other"
	}
}

func normalizeResearchFailureClass(raw string) string {
	switch raw {
	case "", "none":
		return "none"
	case "dispatch_failed", "context_load_failed", "missing_structured_result", "task_timeout", "inbox_missing", "goal_steered":
		return raw
	default:
		return "other"
	}
}

func normalizeResearchArtifact(raw string) string {
	switch raw {
	case "source_snapshot", "observation", "claim_evidence":
		return raw
	default:
		return "other"
	}
}

func normalizeResearchVerificationStatus(raw string) string {
	switch raw {
	case "pending", "verified", "rejected":
		return raw
	default:
		return "other"
	}
}

// samplerHistogram is the in-memory representation of a single
// prometheus.ConstHistogram for one runtime_mode. We bucketise in Go
// because Postgres does not return histogram-shaped data directly.
type samplerHistogram struct {
	count   uint64
	sum     float64
	buckets map[float64]uint64
}

// samplerSnapshot is the cached output of one full refresh. All maps are
// pre-allocated so the emit path can read them lock-free once the receiver
// has handed it back.
type samplerSnapshot struct {
	takenAt time.Time

	activeUsers      map[string]float64
	activeWorkspaces map[string]float64

	taskQueued  map[string]float64
	taskRunning map[taskRunningKey]float64
	taskStuck   map[string]float64

	runtimeOnline map[runtimeOnlineKey]float64
	heartbeatAge  map[string]samplerHistogram

	researchRuns     map[researchMetricKey]float64
	researchTasks    map[researchMetricKey]float64
	researchAttempts map[researchMetricKey]float64
	researchEvidence map[researchMetricKey]float64

	researchFrontierOpen          float64
	researchFrontierOldestAge     float64
	researchProjectionPending     float64
	researchProjectionOldestAge   float64
	researchCancellationPending   float64
	researchCancellationOldestAge float64
	researchV6Health              map[string]float64

	workspaceTotal      float64
	workspaceTotalKnown bool

	// reminderScheduledOverdue is a global count (no labels). Defaults to 0
	// so emit always has a value even if the query failed this scrape.
	reminderScheduledOverdue float64
}

func newSamplerSnapshot(t time.Time) *samplerSnapshot {
	return &samplerSnapshot{
		takenAt:          t,
		activeUsers:      map[string]float64{},
		activeWorkspaces: map[string]float64{},
		taskQueued:       map[string]float64{},
		taskRunning:      map[taskRunningKey]float64{},
		taskStuck:        map[string]float64{},
		runtimeOnline:    map[runtimeOnlineKey]float64{},
		heartbeatAge:     map[string]samplerHistogram{},
		researchRuns:     map[researchMetricKey]float64{},
		researchTasks:    map[researchMetricKey]float64{},
		researchAttempts: map[researchMetricKey]float64{},
		researchEvidence: map[researchMetricKey]float64{},
		researchV6Health: map[string]float64{},
	}
}

// refreshFromDB runs every sampler query in sequence on a single acquired
// connection. Each query is wrapped in its own short read-only transaction
// with SET LOCAL statement_timeout, so a hang on one statement cannot
// poison the others. Errors are logged-and-continued: a partial snapshot is
// strictly better than a missing one.
func (c *BusinessSamplerCollector) refreshFromDB(ctx context.Context, now time.Time) *samplerSnapshot {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		c.queryErrors.WithLabelValues("acquire").Inc()
		c.logger.Warn("business sampler: acquire connection failed", "error", err)
		// Reuse the last snapshot if any; mark nothing fresh.
		return c.snapshot
	}
	defer conn.Release()

	snap := newSamplerSnapshot(now)

	c.runQuery(ctx, conn, "active_users", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryActiveUsers(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "active_workspaces", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryActiveWorkspaces(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "task_queued", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryTaskQueued(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "task_running", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryTaskRunning(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "task_stuck", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryTaskStuck(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "runtime_online", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryRuntimeOnline(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "runtime_heartbeat_age", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryRuntimeHeartbeatAge(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "workspace_total", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryWorkspaceTotal(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "reminder_scheduled_overdue", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryReminderScheduledOverdue(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "research_execution", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryResearchExecution(ctx, tx, snap)
	})
	c.runQuery(ctx, conn, "research_integrity", func(ctx context.Context, tx pgx.Tx) error {
		return c.queryResearchIntegrity(ctx, tx, snap)
	})

	return snap
}

// runQuery wraps one logical sampler query: BEGIN READ ONLY, SET LOCAL
// statement_timeout, run, COMMIT. Failures are recorded on the error
// counter and the query duration histogram, but never propagate; the
// snapshot is left with whatever default value (typically 0) the caller
// pre-seeded.
func (c *BusinessSamplerCollector) runQuery(
	ctx context.Context,
	conn *pgxpool.Conn,
	name string,
	body func(ctx context.Context, tx pgx.Tx) error,
) {
	queryCtx, cancel := context.WithTimeout(ctx, c.queryTimeout+50*time.Millisecond)
	defer cancel()

	start := c.now()
	defer func() {
		c.queryDuration.WithLabelValues(name).Observe(c.now().Sub(start).Seconds())
	}()

	tx, err := conn.BeginTx(queryCtx, pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
		IsoLevel:   pgx.ReadCommitted,
	})
	if err != nil {
		c.queryErrors.WithLabelValues(name).Inc()
		c.logger.Warn("business sampler: begin tx failed", "name", name, "error", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			// Best-effort rollback; the connection is already going
			// back to the pool in the caller's defer.
			_ = tx.Rollback(context.Background())
		}
	}()

	// SET LOCAL is the only knob we trust: it is automatically scoped to
	// the surrounding transaction even if the connection is later reused
	// by another caller.
	timeoutMs := int(c.queryTimeout / time.Millisecond)
	if _, err := tx.Exec(queryCtx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeoutMs)); err != nil {
		c.queryErrors.WithLabelValues(name).Inc()
		c.logger.Warn("business sampler: SET LOCAL statement_timeout failed", "name", name, "error", err)
		return
	}

	if err := body(queryCtx, tx); err != nil {
		c.queryErrors.WithLabelValues(name).Inc()
		// Statement timeouts surface as pgx errors with code 57014; we
		// log them at INFO because they are an expected steady-state
		// outcome on a degraded DB, not a bug to alert on.
		level := slog.LevelWarn
		if isStatementTimeout(err) {
			level = slog.LevelInfo
		}
		c.logger.Log(ctx, level, "business sampler: query failed", "name", name, "error", err)
		return
	}

	if err := tx.Commit(queryCtx); err != nil {
		c.queryErrors.WithLabelValues(name).Inc()
		c.logger.Warn("business sampler: commit failed", "name", name, "error", err)
		return
	}
	committed = true
}

// isStatementTimeout returns true when err looks like the canonical
// statement_timeout cancellation. Used purely to choose a log level.
func isStatementTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// pgx surfaces SQLSTATE 57014 ("query_canceled") on statement_timeout.
	// We don't import the pgconn type just to type-assert here; a
	// substring check is good enough for a log-level decision.
	msg := err.Error()
	return contains(msg, "57014") || contains(msg, "canceling statement due to statement timeout")
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
