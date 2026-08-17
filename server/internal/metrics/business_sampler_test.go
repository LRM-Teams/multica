package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newTestSampler builds a BusinessSamplerCollector wired to a fake refresh
// function. We bypass NewBusinessSamplerCollector's nil-pool short-circuit
// by passing a dummy pgxpool — the fake refreshFn never calls into it. This
// keeps the real DB code path off the hot path of every unit test while
// still exercising the production registration / emit logic.
func newTestSampler(t *testing.T, refresh func(ctx context.Context, now time.Time) *samplerSnapshot) *BusinessSamplerCollector {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://disabled@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatalf("create dummy pool: %v", err)
	}
	t.Cleanup(pool.Close)

	c := NewBusinessSamplerCollector(&BusinessSamplerOptions{
		Pool:         pool,
		CacheTTL:     50 * time.Millisecond,
		QueryTimeout: 100 * time.Millisecond,
	})
	if c == nil {
		t.Fatalf("NewBusinessSamplerCollector returned nil")
	}
	c.refreshFn = refresh
	return c
}

func filledSnapshot(now time.Time) *samplerSnapshot {
	snap := newSamplerSnapshot(now)
	snap.activeUsers[windowFiveMinutes] = 7
	snap.activeWorkspaces[windowFiveMinutes] = 3

	snap.taskQueued["chat"] = 5
	snap.taskQueued["issue"] = 2

	snap.taskRunning[taskRunningKey{source: "chat", runtimeMode: "cloud"}] = 3
	snap.taskRunning[taskRunningKey{source: "issue", runtimeMode: "local"}] = 1

	snap.taskStuck["issue"] = 1

	snap.runtimeOnline[runtimeOnlineKey{runtimeMode: "local", provider: "claude"}] = 4
	snap.runtimeOnline[runtimeOnlineKey{runtimeMode: "cloud", provider: "kiro"}] = 2

	snap.heartbeatAge["local"] = samplerHistogram{
		count:   3,
		sum:     45,
		buckets: bucketsFor([]float64{5, 15, 30}),
	}
	snap.heartbeatAge["cloud"] = samplerHistogram{
		count:   1,
		sum:     2,
		buckets: bucketsFor([]float64{2}),
	}

	snap.workspaceTotal = 250
	snap.workspaceTotalKnown = true

	// Platform-ops aggregate for #73 Phase A (overdue scheduled reminders).
	snap.reminderScheduledOverdue = 4

	snap.researchRuns[researchMetricKey{first: "running", second: "research-run-v1"}] = 2
	snap.researchRuns[researchMetricKey{first: "running", second: "research-run-v3"}] = 1
	snap.researchRuns[researchMetricKey{first: "running", second: "research-run-v4"}] = 1
	snap.researchRuns[researchMetricKey{first: "running", second: "research-run-v5"}] = 1
	snap.researchRuns[researchMetricKey{first: "completed", second: "research-run-v1"}] = 9
	snap.researchTasks[researchMetricKey{first: "verify", second: "running"}] = 3
	snap.researchTasks[researchMetricKey{first: "synthesize", second: "ready"}] = 1
	snap.researchAttempts[researchMetricKey{first: "failed", second: "task_timeout"}] = 2
	snap.researchAttempts[researchMetricKey{first: "succeeded", second: "none"}] = 12
	snap.researchEvidence[researchMetricKey{first: "source_snapshot", second: "verified"}] = 17
	snap.researchEvidence[researchMetricKey{first: "claim_evidence", second: "pending"}] = 4
	snap.researchFrontierOpen = 6
	snap.researchFrontierOldestAge = 185
	snap.researchProjectionPending = 2
	snap.researchProjectionOldestAge = 14
	snap.researchCancellationPending = 3
	snap.researchCancellationOldestAge = 27
	return snap
}

func bucketsFor(observations []float64) map[float64]uint64 {
	buckets := make(map[float64]uint64, len(heartbeatAgeBuckets))
	for _, b := range heartbeatAgeBuckets {
		buckets[b] = 0
	}
	for _, o := range observations {
		for _, b := range heartbeatAgeBuckets {
			if o <= b {
				buckets[b]++
			}
		}
	}
	return buckets
}

// TestBusinessSamplerCollectorEmitsExpectedMetrics asserts every metric
// family from the PR4 spec is present on /metrics with the expected
// values, AND that we always emit a known-source/runtime-mode zero series
// so dashboards don't show "no data" right after a restart.
func TestBusinessSamplerCollectorEmitsExpectedMetrics(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	c := newTestSampler(t, func(ctx context.Context, refreshAt time.Time) *samplerSnapshot {
		return filledSnapshot(refreshAt)
	})
	c.now = func() time.Time { return now }

	registry := prometheus.NewRegistry()
	registry.MustRegister(c.Collectors()...)

	rec := httptest.NewRecorder()
	NewHandler(registry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()

	wantSubstrings := []string{
		`multica_active_users{window="5m"} 7`,
		`multica_active_workspaces{window="5m"} 3`,
		`multica_agent_task_queued{source="chat"} 5`,
		`multica_agent_task_queued{source="issue"} 2`,
		// Zero series for sources that didn't appear in the snapshot.
		`multica_agent_task_queued{source="autopilot"} 0`,
		`multica_agent_task_queued{source="other"} 0`,
		`multica_agent_task_running{runtime_mode="cloud",source="chat"} 3`,
		`multica_agent_task_running{runtime_mode="local",source="issue"} 1`,
		`multica_agent_task_stuck_total{source="issue"} 1`,
		`multica_runtime_online{provider="claude",runtime_mode="local"} 4`,
		`multica_runtime_online{provider="kiro",runtime_mode="cloud"} 2`,
		`multica_runtime_heartbeat_age_seconds_count{runtime_mode="local"} 3`,
		`multica_runtime_heartbeat_age_seconds_sum{runtime_mode="local"} 45`,
		`multica_workspace_total 250`,
		`multica_reminder_scheduled_overdue 4`,
		`multica_research_runs{orchestrator_version="research-run-v1",status="running"} 2`,
		`multica_research_runs{orchestrator_version="research-run-v3",status="running"} 1`,
		`multica_research_runs{orchestrator_version="research-run-v4",status="running"} 1`,
		`multica_research_runs{orchestrator_version="research-run-v5",status="running"} 1`,
		`multica_research_tasks{kind="verify",status="running"} 3`,
		`multica_research_attempts{failure_class="task_timeout",status="failed"} 2`,
		`multica_research_evidence_artifacts{artifact="source_snapshot",verification_status="verified"} 17`,
		`multica_research_frontier{measure="open"} 6`,
		`multica_research_frontier{measure="oldest_age_seconds"} 185`,
		`multica_research_projection_backlog{measure="pending"} 2`,
		`multica_research_projection_backlog{measure="oldest_age_seconds"} 14`,
		`multica_research_cancellation_backlog{measure="pending"} 3`,
		`multica_research_cancellation_backlog{measure="oldest_age_seconds"} 27`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q\nbody:\n%s", want, body)
		}
	}
	for _, removed := range []string{
		`multica_active_users{window="1h"}`,
		`multica_active_users{window="24h"}`,
		`multica_active_workspaces{window="1h"}`,
		`multica_active_workspaces{window="24h"}`,
	} {
		if strings.Contains(body, removed) {
			t.Errorf("metrics body still exposes removed long DB window %q\nbody:\n%s", removed, body)
		}
	}
}

// TestBusinessSamplerEmitsZeroReminderOverdueOnEmptySnapshot ensures the
// platform-ops gauge always appears (including 0) so scrapes/alerters never
// treat a healthy fleet as "missing series".
func TestBusinessSamplerEmitsZeroReminderOverdueOnEmptySnapshot(t *testing.T) {
	c := newTestSampler(t, func(ctx context.Context, refreshAt time.Time) *samplerSnapshot {
		return newSamplerSnapshot(refreshAt)
	})
	registry := prometheus.NewRegistry()
	registry.MustRegister(c.Collectors()...)
	rec := httptest.NewRecorder()
	NewHandler(registry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `multica_reminder_scheduled_overdue 0`) {
		t.Fatalf("empty snapshot must emit zero overdue gauge\n%s", body)
	}
}

// TestBusinessSamplerSelfIntrospectionHistogramIsExposed observes a value
// directly on the query-duration histogram and asserts it surfaces on
// /metrics. Prometheus elides histograms with zero observations, so we have
// to seed one before scraping.
func TestBusinessSamplerSelfIntrospectionHistogramIsExposed(t *testing.T) {
	c := newTestSampler(t, func(ctx context.Context, refreshAt time.Time) *samplerSnapshot {
		return newSamplerSnapshot(refreshAt)
	})
	c.queryDuration.WithLabelValues("active_users").Observe(0.012)

	registry := prometheus.NewRegistry()
	registry.MustRegister(c.Collectors()...)

	rec := httptest.NewRecorder()
	NewHandler(registry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `multica_business_sampler_query_seconds_count{name="active_users"} 1`) {
		t.Fatalf("query duration histogram missing\n%s", body)
	}
}

// TestBusinessSamplerCollectorCachesSnapshot asserts the TTL cache absorbs
// concurrent scrapes: two Collect calls inside the TTL window must trigger
// exactly one refresh, and a third call after the TTL elapses must trigger
// a second.
func TestBusinessSamplerCollectorCachesSnapshot(t *testing.T) {
	var refreshCount atomic.Int32
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	current := now

	c := newTestSampler(t, func(ctx context.Context, refreshAt time.Time) *samplerSnapshot {
		refreshCount.Add(1)
		return filledSnapshot(refreshAt)
	})
	c.cacheTTL = 100 * time.Millisecond
	c.now = func() time.Time { return current }

	registry := prometheus.NewRegistry()
	registry.MustRegister(c.Collectors()...)

	rec := httptest.NewRecorder()
	NewHandler(registry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	rec = httptest.NewRecorder()
	NewHandler(registry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if got := refreshCount.Load(); got != 1 {
		t.Fatalf("refresh count after two cached scrapes = %d, want 1", got)
	}

	// Advance past the TTL — the next scrape must refresh again.
	current = current.Add(150 * time.Millisecond)
	rec = httptest.NewRecorder()
	NewHandler(registry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if got := refreshCount.Load(); got != 2 {
		t.Fatalf("refresh count after TTL expiry = %d, want 2", got)
	}
}

// TestBusinessSamplerCollectorBoundedCardinality is the cardinality canary.
// Even with a malicious snapshot that mentions many distinct labels, the
// sampler must collapse them into the BusinessMetrics whitelist plus
// known-window zeros. This protects /metrics from a per-runtime or
// per-workspace explosion.
func TestBusinessSamplerCollectorBoundedCardinality(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	c := newTestSampler(t, func(ctx context.Context, refreshAt time.Time) *samplerSnapshot {
		// We can't inject raw rogue labels through the snapshot directly
		// (the maps are typed), so we exercise the normalization path by
		// pre-normalizing here exactly the way refreshFromDB would.
		snap := newSamplerSnapshot(refreshAt)
		for i := 0; i < 50; i++ {
			snap.taskQueued[NormalizeTaskSource("provider-from-user-input-"+string(rune('A'+i%26)))] += 1
		}
		for i := 0; i < 50; i++ {
			snap.runtimeOnline[runtimeOnlineKey{
				runtimeMode: NormalizeRuntimeMode("rogue-mode"),
				provider:    NormalizeRuntimeProvider("attacker-provider"),
			}] += 1
		}
		for i := 0; i < 50; i++ {
			snap.researchRuns[researchMetricKey{
				first:  normalizeResearchRunStatus("rogue-status"),
				second: normalizeResearchOrchestratorVersion("user-version-" + string(rune('A'+i%26))),
			}] += 1
			snap.researchAttempts[researchMetricKey{
				first:  normalizeResearchAttemptStatus("rogue-status"),
				second: normalizeResearchFailureClass("raw-agent-error-" + string(rune('A'+i%26))),
			}] += 1
			snap.researchEvidence[researchMetricKey{
				first:  normalizeResearchArtifact("rogue-artifact"),
				second: normalizeResearchVerificationStatus("rogue-verification"),
			}] += 1
		}
		return snap
	})
	c.now = func() time.Time { return now }

	registry := prometheus.NewRegistry()
	registry.MustRegister(c.Collectors()...)

	if got := testutil.CollectAndCount(c, "multica_agent_task_queued"); got != len(knownSourceLabels()) {
		t.Fatalf("agent_task_queued series = %d, want %d", got, len(knownSourceLabels()))
	}
	expectedRunning := len(knownSourceLabels()) * len(knownRuntimeModeLabels())
	if got := testutil.CollectAndCount(c, "multica_agent_task_running"); got != expectedRunning {
		t.Fatalf("agent_task_running series = %d, want %d", got, expectedRunning)
	}
	if got := testutil.CollectAndCount(c, "multica_runtime_online"); got != 1 {
		t.Fatalf("runtime_online series = %d, want 1 (collapsed by normalizers)", got)
	}
	if got := testutil.CollectAndCount(c, "multica_research_runs"); got != 1 {
		t.Fatalf("research_runs series = %d, want 1 (collapsed by normalizers)", got)
	}
	if got := testutil.CollectAndCount(c, "multica_research_attempts"); got != 1 {
		t.Fatalf("research_attempts series = %d, want 1 (collapsed by normalizers)", got)
	}
	if got := testutil.CollectAndCount(c, "multica_research_evidence_artifacts"); got != 1 {
		t.Fatalf("research_evidence series = %d, want 1 (collapsed by normalizers)", got)
	}
	if got := testutil.CollectAndCount(c, "multica_research_frontier"); got != 2 {
		t.Fatalf("research_frontier series = %d, want 2 fixed measures", got)
	}
}

// TestBusinessSamplerCollectorDisabledWithoutOptions exercises the opt-in
// path in the registry: no BusinessSampler option means no sampler-related
// series leak into /metrics, and existing collectors stay registered.
func TestBusinessSamplerCollectorDisabledWithoutOptions(t *testing.T) {
	registry := NewRegistry(RegistryOptions{})
	if registry.Sampler != nil {
		t.Fatalf("Sampler must be nil when BusinessSampler option is absent")
	}
	rec := httptest.NewRecorder()
	NewHandler(registry.Gatherer).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, forbidden := range []string{
		"multica_active_users",
		"multica_agent_task_queued",
		"multica_runtime_online",
		"multica_business_sampler_query_seconds",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("/metrics leaked sampler family %q when sampler disabled", forbidden)
		}
	}
}

// TestBusinessSamplerCollectorDBHangIsolation simulates a hung database
// using a pgxpool pointed at an unreachable host. The sampler must not
// panic, must not block /metrics indefinitely, and must record the
// resulting failure on the query error counter — that's the
// statement_timeout / connection-acquire safety net the spec asks for.
func TestBusinessSamplerCollectorDBHangIsolation(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://hang@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatalf("create unreachable pool: %v", err)
	}
	defer pool.Close()

	c := NewBusinessSamplerCollector(&BusinessSamplerOptions{
		Pool:         pool,
		CacheTTL:     time.Second,
		QueryTimeout: 50 * time.Millisecond,
	})
	if c == nil {
		t.Fatalf("NewBusinessSamplerCollector returned nil")
	}

	// Use the real refreshFromDB path so we exercise Acquire / SET LOCAL
	// statement_timeout / error counter wiring.
	registry := prometheus.NewRegistry()
	registry.MustRegister(c.Collectors()...)

	done := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		NewHandler(registry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		close(done)
	}()

	select {
	case <-done:
		// Success — the scrape completed despite no reachable DB.
	case <-time.After(5 * time.Second):
		t.Fatal("metrics scrape blocked > 5s on unreachable DB")
	}

	// At least one error must have been recorded against the
	// connection-acquire path. We don't assert exact name to avoid
	// coupling to internal labels; we just want a positive count.
	if got := testutil.CollectAndCount(c.queryErrors); got == 0 {
		t.Fatal("expected at least one query_errors_total series after DB hang")
	}
}

// TestNewBusinessSamplerCollectorNilPool covers the explicit opt-out path.
func TestNewBusinessSamplerCollectorNilPool(t *testing.T) {
	if c := NewBusinessSamplerCollector(nil); c != nil {
		t.Fatalf("NewBusinessSamplerCollector(nil) = %p, want nil", c)
	}
	if c := NewBusinessSamplerCollector(&BusinessSamplerOptions{}); c != nil {
		t.Fatalf("NewBusinessSamplerCollector with nil Pool = %p, want nil", c)
	}
}

// TestSamplerHistogramBucketing exercises the in-memory bucketing logic so
// a regression in heartbeat-age accounting is caught before it ships to
// dashboards.
func TestSamplerHistogramBucketing(t *testing.T) {
	buckets := bucketsFor([]float64{0.5, 5, 30, 75})
	expectations := map[float64]uint64{
		1: 1, 5: 2, 15: 2, 30: 3, 60: 3, 120: 4, 300: 4, 600: 4,
	}
	for b, want := range expectations {
		if got := buckets[b]; got != want {
			t.Errorf("bucket le=%g count = %d, want %d", b, got, want)
		}
	}
}

func TestResearchMetricNormalizersPreserveBoundedEmptyFailureClass(t *testing.T) {
	if got := normalizeResearchFailureClass(""); got != "none" {
		t.Fatalf("empty failure class = %q, want none", got)
	}
	if got := normalizeResearchFailureClass("none"); got != "none" {
		t.Fatalf("SQL-normalized failure class = %q, want none", got)
	}
	if got := normalizeResearchFailureClass("provider returned user-controlled text"); got != "other" {
		t.Fatalf("unbounded failure class = %q, want other", got)
	}
}
