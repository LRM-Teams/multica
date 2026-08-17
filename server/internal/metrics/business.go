package metrics

import (
	"math"
	"sync"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
	"github.com/prometheus/client_golang/prometheus"
)

var taskDurationBuckets = []float64{1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1200, 3600, 7200}
var freshnessHoldResolutionBuckets = []float64{0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300}
var agentDeleteDurationBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 10}

type activeTaskLabels struct {
	source      string
	runtimeMode string
}

type BusinessMetrics struct {
	taskEnqueued     *prometheus.CounterVec
	taskDispatched   *prometheus.CounterVec
	taskStarted      *prometheus.CounterVec
	taskTerminal     *prometheus.CounterVec
	taskFailed       *prometheus.CounterVec
	taskQueueWait    *prometheus.HistogramVec
	taskRunSeconds   *prometheus.HistogramVec
	taskTotalSeconds *prometheus.HistogramVec
	taskInProgress   *prometheus.GaugeVec
	taskIterations   *prometheus.HistogramVec

	llmTokens         *prometheus.CounterVec
	llmCostUSD        *prometheus.CounterVec
	llmUnpricedTokens *prometheus.CounterVec
	llmRequests       *prometheus.CounterVec

	taskQueuedExpired                      *prometheus.CounterVec
	taskLeaseExpired                       *prometheus.CounterVec
	channelAmbientGateDecisions            *prometheus.CounterVec
	channelOutputSuppressed                *prometheus.CounterVec
	channelFullExecutionWakes              *prometheus.CounterVec
	channelFullExecutionAmplificationRatio *prometheus.GaugeVec
	channelTriggerDepth                    prometheus.Histogram
	freshnessHoldResolution                *prometheus.HistogramVec
	agentDeleteDuration                    *prometheus.HistogramVec

	// Graph memory reviewer (design §7 observability).
	graphMemoryRecall         *prometheus.CounterVec
	graphMemoryExploreRounds  prometheus.Histogram
	graphMemoryJudge          *prometheus.CounterVec
	graphMemoryIngest         *prometheus.CounterVec
	graphMemoryVersionSwitch  prometheus.Counter
	graphMemoryBacktestBypass prometheus.Gauge

	activeMu    sync.Mutex
	activeTasks map[string]activeTaskLabels

	// PR3 funnel / community / commercial counters. See business_events.go
	// for the field-level docs and labels.
	events *businessEventMetrics
}

func NewBusinessMetrics() *BusinessMetrics {
	validateBusinessMetricLabels()
	m := &BusinessMetrics{
		taskEnqueued: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "enqueued_total",
			Help:      "Total agent tasks enqueued.",
		}, metricLabels("multica_agent_task_enqueued_total")),
		taskDispatched: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "dispatched_total",
			Help:      "Total agent tasks dispatched to a runtime.",
		}, metricLabels("multica_agent_task_dispatched_total")),
		taskStarted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "started_total",
			Help:      "Total agent tasks that reached running state.",
		}, metricLabels("multica_agent_task_started_total")),
		taskTerminal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "terminal_total",
			Help:      "Total agent tasks that reached a terminal state.",
		}, metricLabels("multica_agent_task_terminal_total")),
		taskFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "failed_total",
			Help:      "Total failed agent tasks by canonical failure reason.",
		}, metricLabels("multica_agent_task_failed_total")),
		taskQueueWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "queue_wait_seconds",
			Help:      "Time agent tasks spent queued before dispatch.",
			Buckets:   taskDurationBuckets,
		}, metricLabels("multica_agent_task_queue_wait_seconds")),
		taskRunSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "run_seconds",
			Help:      "Time agent tasks spent running before a terminal state.",
			Buckets:   taskDurationBuckets,
		}, metricLabels("multica_agent_task_run_seconds")),
		taskTotalSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "total_seconds",
			Help:      "Total time from agent task creation to terminal state.",
			Buckets:   taskDurationBuckets,
		}, metricLabels("multica_agent_task_total_seconds")),
		taskInProgress: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "in_progress",
			Help:      "Current agent tasks dispatched by this process and not yet terminal.",
		}, metricLabels("multica_agent_task_in_progress")),
		taskIterations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "agent_task",
			Name:      "iteration_count",
			Help:      "Retry attempt count observed when an agent task reaches a terminal state.",
			Buckets:   []float64{1, 2, 3, 4, 5, 10},
		}, metricLabels("multica_agent_task_iteration_count")),
		llmTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "llm",
			Name:      "tokens_total",
			Help:      "Total priced LLM tokens by provider, model, token type, runtime mode, and task source.",
		}, metricLabels("multica_llm_tokens_total")),
		llmCostUSD: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "llm",
			Name:      "cost_usd_total",
			Help:      "Total estimated priced LLM token cost in USD.",
		}, metricLabels("multica_llm_cost_usd_total")),
		llmUnpricedTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "llm",
			Name:      "unpriced_tokens_total",
			Help:      "Total LLM tokens for model aliases without a fixed TSR price.",
		}, metricLabels("multica_llm_unpriced_tokens_total")),
		llmRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "llm",
			Name:      "request_total",
			Help:      "Total task usage reports by normalized LLM provider and model.",
		}, metricLabels("multica_llm_request_total")),
		taskQueuedExpired: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "task",
			Name:      "queued_expired_total",
			Help:      "Total queued tasks expired by the scheduler.",
		}, metricLabels("multica_task_queued_expired_total")),
		taskLeaseExpired: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "task",
			Name:      "lease_expired_total",
			Help:      "Total dispatched or running task leases expired by the scheduler.",
		}, metricLabels("multica_task_lease_expired_total")),
		channelAmbientGateDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "channel_ambient_gate",
			Name:      "decisions_total",
			Help:      "Total Phase 0 channel ambient gate decisions by low-cardinality action and reason.",
		}, metricLabels("multica_channel_ambient_gate_decisions_total")),
		channelOutputSuppressed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "channel_output",
			Name:      "suppressed_total",
			Help:      "Total channel/DM agent task outputs suppressed before becoming visible chat, by reason.",
		}, metricLabels("multica_channel_output_suppressed_total")),
		channelFullExecutionWakes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "channel",
			Name:      "full_execution_wakes_total",
			Help:      "Total channel full-execution wakes by bounded reason.",
		}, metricLabels("multica_channel_full_execution_wakes_total")),
		channelFullExecutionAmplificationRatio: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "multica",
			Subsystem: "channel",
			Name:      "full_execution_amplification_ratio",
			Help:      "Ratio of full-execution wakes to human no-mention channel messages.",
		}, metricLabels("multica_channel_full_execution_amplification_ratio")),
		channelTriggerDepth: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "channel",
			Name:      "trigger_depth",
			Help:      "Trigger depth of committed agent channel messages.",
			Buckets:   []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 16, 32, 64},
		}),
		freshnessHoldResolution: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "multica_freshness_hold_resolution_seconds",
			Help:    "Time from a freshness decision fact to its decisive same-target resolution.",
			Buckets: freshnessHoldResolutionBuckets,
		}, metricLabels("multica_freshness_hold_resolution_seconds")),
		agentDeleteDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "multica_agent_delete_duration_seconds",
			Help:    "End-to-end server duration of user-facing Agent delete (archive) requests.",
			Buckets: agentDeleteDurationBuckets,
		}, metricLabels("multica_agent_delete_duration_seconds")),
		graphMemoryRecall: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "graph_memory",
			Name:      "recall_total",
			Help:      "Total graph memory recalls by outcome (found or miss).",
		}, metricLabels("multica_graph_memory_recall_total")),
		graphMemoryExploreRounds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "graph_memory",
			Name:      "explore_rounds",
			Help:      "Explore-agent rounds used per graph memory recall.",
			Buckets:   []float64{1, 2, 3, 4, 5, 8, 10},
		}),
		graphMemoryJudge: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "graph_memory",
			Name:      "judge_total",
			Help:      "Total graph memory judge results by pass/fail against the relevance threshold.",
		}, metricLabels("multica_graph_memory_judge_total")),
		graphMemoryIngest: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "graph_memory",
			Name:      "ingest_total",
			Help:      "Total graph memory segment ingests by outcome (ok, extractive fallback, or error).",
		}, metricLabels("multica_graph_memory_ingest_total")),
		graphMemoryVersionSwitch: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "graph_memory",
			Name:      "version_switch_total",
			Help:      "Total graph memory version switches after TTT consolidation.",
		}),
		graphMemoryBacktestBypass: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "multica",
			Subsystem: "graph_memory",
			Name:      "backtest_bypass_ratio",
			Help:      "Fraction of backtested queries accepted on graph distance alone (no full explore run) in the last consolidation.",
		}),
		activeTasks: map[string]activeTaskLabels{},
		events:      newBusinessEventMetrics(),
	}
	m.prewarmFailureReasons()
	// Prewarm the graph-memory result vecs so their families are visible in
	// the registry before the first recall/judge (same intent as
	// prewarmFailureReasons).
	for _, result := range []string{"found", "miss"} {
		m.graphMemoryRecall.WithLabelValues(result).Add(0)
	}
	for _, result := range []string{"passed", "failed"} {
		m.graphMemoryJudge.WithLabelValues(result).Add(0)
	}
	for _, result := range []string{"ok", "fallback", "error"} {
		m.graphMemoryIngest.WithLabelValues(result).Add(0)
	}
	return m
}

func (m *BusinessMetrics) Collectors() []prometheus.Collector {
	return append([]prometheus.Collector{
		m.taskEnqueued,
		m.taskDispatched,
		m.taskStarted,
		m.taskTerminal,
		m.taskFailed,
		m.taskQueueWait,
		m.taskRunSeconds,
		m.taskTotalSeconds,
		m.taskInProgress,
		m.taskIterations,
		m.llmTokens,
		m.llmCostUSD,
		m.llmUnpricedTokens,
		m.llmRequests,
		m.taskQueuedExpired,
		m.taskLeaseExpired,
		m.channelAmbientGateDecisions,
		m.channelOutputSuppressed,
		m.channelFullExecutionWakes,
		m.channelFullExecutionAmplificationRatio,
		m.channelTriggerDepth,
		m.freshnessHoldResolution,
		m.agentDeleteDuration,
		m.graphMemoryRecall,
		m.graphMemoryExploreRounds,
		m.graphMemoryJudge,
		m.graphMemoryIngest,
		m.graphMemoryVersionSwitch,
		m.graphMemoryBacktestBypass,
	}, m.events.collectors()...)
}

func (m *BusinessMetrics) ObserveAgentDelete(result string, seconds float64) {
	if m == nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	switch result {
	case "success", "error":
		m.agentDeleteDuration.WithLabelValues(result).Observe(seconds)
	}
}

func (m *BusinessMetrics) RecordTaskEnqueued(source, runtimeMode string) {
	if m == nil {
		return
	}
	m.taskEnqueued.WithLabelValues(NormalizeTaskSource(source), NormalizeRuntimeMode(runtimeMode)).Inc()
}

func (m *BusinessMetrics) RecordChannelAmbientGateDecision(action, reason string) {
	if m == nil {
		return
	}
	m.channelAmbientGateDecisions.WithLabelValues(
		NormalizeAmbientGateAction(action),
		NormalizeAmbientGateReason(reason),
	).Inc()
}

func (m *BusinessMetrics) RecordChannelOutputSuppressed(reason string) {
	if m == nil {
		return
	}
	m.channelOutputSuppressed.WithLabelValues(NormalizeChannelOutputSuppressedReason(reason)).Inc()
}

func (m *BusinessMetrics) RecordChannelFullExecutionWake(reason string) {
	if m == nil {
		return
	}
	m.channelFullExecutionWakes.WithLabelValues(NormalizeFullExecutionWakeReason(reason)).Inc()
}

func (m *BusinessMetrics) SetChannelFullExecutionAmplificationRatio(ratio float64) {
	if m == nil || ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return
	}
	m.channelFullExecutionAmplificationRatio.WithLabelValues().Set(ratio)
}

// ObserveChannelTriggerDepth records the depth of a committed agent message.
// This metric intentionally has no labels: channel and actor identifiers are
// high cardinality and belong only in the accompanying structured log.
func (m *BusinessMetrics) ObserveChannelTriggerDepth(depth int) {
	if m == nil || depth < 0 {
		return
	}
	m.channelTriggerDepth.Observe(float64(depth))
}

func (m *BusinessMetrics) ObserveFreshnessHoldResolution(outcome string, seconds float64) {
	if m == nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	switch outcome {
	case "send_draft", "revised_send", "abandoned":
		m.freshnessHoldResolution.WithLabelValues(outcome).Observe(seconds)
	}
}

func (m *BusinessMetrics) RecordTaskDispatched(taskID, source, runtimeMode string, queueWaitSeconds float64) {
	if m == nil {
		return
	}
	source = NormalizeTaskSource(source)
	runtimeMode = NormalizeRuntimeMode(runtimeMode)
	m.taskDispatched.WithLabelValues(source, runtimeMode).Inc()
	if queueWaitSeconds >= 0 {
		m.taskQueueWait.WithLabelValues(source, runtimeMode).Observe(queueWaitSeconds)
	}
	m.markTaskInProgress(taskID, source, runtimeMode)
}

func (m *BusinessMetrics) RecordTaskStarted(source, runtimeMode, provider string) {
	if m == nil {
		return
	}
	m.taskStarted.WithLabelValues(
		NormalizeTaskSource(source),
		NormalizeRuntimeMode(runtimeMode),
		NormalizeRuntimeProvider(provider),
	).Inc()
}

func (m *BusinessMetrics) RecordTaskTerminal(taskID, source, runtimeMode, terminalStatus string, runSeconds, totalSeconds float64, attempt int32) {
	if m == nil {
		return
	}
	source = NormalizeTaskSource(source)
	runtimeMode = NormalizeRuntimeMode(runtimeMode)
	terminalStatus = NormalizeTerminalStatus(terminalStatus)
	m.taskTerminal.WithLabelValues(source, runtimeMode, terminalStatus).Inc()
	if runSeconds >= 0 {
		m.taskRunSeconds.WithLabelValues(source, runtimeMode, terminalStatus).Observe(runSeconds)
	}
	if totalSeconds >= 0 {
		m.taskTotalSeconds.WithLabelValues(source, runtimeMode, terminalStatus).Observe(totalSeconds)
	}
	if attempt < 1 {
		attempt = 1
	}
	m.taskIterations.WithLabelValues(source, terminalStatus).Observe(float64(attempt))
	m.clearTaskInProgress(taskID)
}

func (m *BusinessMetrics) RecordTaskFailed(source, runtimeMode, failureReason string) {
	if m == nil {
		return
	}
	m.taskFailed.WithLabelValues(
		NormalizeTaskSource(source),
		NormalizeRuntimeMode(runtimeMode),
		NormalizeFailureReason(failureReason),
	).Inc()
}

func (m *BusinessMetrics) RecordTaskQueuedExpired(source, runtimeMode string) {
	if m == nil {
		return
	}
	m.taskQueuedExpired.WithLabelValues(NormalizeTaskSource(source), NormalizeRuntimeMode(runtimeMode)).Inc()
}

func (m *BusinessMetrics) RecordTaskLeaseExpired(source string) {
	if m == nil {
		return
	}
	m.taskLeaseExpired.WithLabelValues(NormalizeTaskSource(source)).Inc()
}

func (m *BusinessMetrics) RecordLLMUsage(source, runtimeMode, rawProvider, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) {
	if m == nil {
		return
	}
	source = NormalizeTaskSource(source)
	runtimeMode = NormalizeRuntimeMode(runtimeMode)
	price, priced := PriceForModelAlias(modelAlias)
	if !priced {
		provider := NormalizeRuntimeProvider(rawProvider)
		alias := NormalizeModelAlias(modelAlias)
		m.recordUnpricedTokens(provider, alias, "input", inputTokens)
		m.recordUnpricedTokens(provider, alias, "output", outputTokens)
		m.recordUnpricedTokens(provider, alias, "cache_read", cacheReadTokens)
		m.recordUnpricedTokens(provider, alias, "cache_write", cacheWriteTokens)
		m.llmRequests.WithLabelValues(provider, "unknown", runtimeMode).Inc()
		return
	}

	m.recordPricedTokens(price.Provider, price.Model, "input", runtimeMode, source, inputTokens, tokenCostUSD(inputTokens, price.InputPerM))
	m.recordPricedTokens(price.Provider, price.Model, "output", runtimeMode, source, outputTokens, tokenCostUSD(outputTokens, price.OutputPerM))
	m.recordPricedTokens(price.Provider, price.Model, "cache_read", runtimeMode, source, cacheReadTokens, tokenCostUSD(cacheReadTokens, price.CacheReadPerM))
	m.recordPricedTokens(price.Provider, price.Model, "cache_write", runtimeMode, source, cacheWriteTokens, tokenCostUSD(cacheWriteTokens, price.CacheWritePerM))
	m.llmRequests.WithLabelValues(price.Provider, price.Model, runtimeMode).Inc()
}

func (m *BusinessMetrics) recordPricedTokens(provider, model, tokenType, runtimeMode, source string, tokens int64, cost float64) {
	if tokens <= 0 {
		return
	}
	tokenType = NormalizeTokenType(tokenType)
	m.llmTokens.WithLabelValues(provider, model, tokenType, runtimeMode, source).Add(float64(tokens))
	if cost > 0 {
		m.llmCostUSD.WithLabelValues(provider, model, tokenType, runtimeMode, source).Add(cost)
	}
}

func (m *BusinessMetrics) recordUnpricedTokens(provider, modelAlias, tokenType string, tokens int64) {
	if tokens <= 0 {
		return
	}
	m.llmUnpricedTokens.WithLabelValues(provider, modelAlias, NormalizeTokenType(tokenType)).Add(float64(tokens))
}

func (m *BusinessMetrics) RecordGraphMemoryRecall(found bool, rounds int) {
	if m == nil {
		return
	}
	result := "miss"
	if found {
		result = "found"
	}
	m.graphMemoryRecall.WithLabelValues(result).Inc()
}

// ObserveGraphExploreRounds records the exploration rounds of one recall.
func (m *BusinessMetrics) ObserveGraphExploreRounds(rounds int) {
	if m == nil || rounds < 0 {
		return
	}
	m.graphMemoryExploreRounds.Observe(float64(rounds))
}

// RecordGraphJudge records one judge outcome against the relevance
// threshold τ (design §5.3).
func (m *BusinessMetrics) RecordGraphJudge(passed bool) {
	if m == nil {
		return
	}
	result := "failed"
	if passed {
		result = "passed"
	}
	m.graphMemoryJudge.WithLabelValues(result).Inc()
}

// RecordGraphMemoryIngest records one segment-ingest outcome (design §5.1,
// review R14): "ok" (LLM summary), "fallback" (deterministic extractive
// summary after an LLM failure), or "error" (staging write failure).
func (m *BusinessMetrics) RecordGraphMemoryIngest(result string) {
	if m == nil {
		return
	}
	switch result {
	case "ok", "fallback", "error":
		m.graphMemoryIngest.WithLabelValues(result).Inc()
	}
}

// RecordGraphVersionSwitch records one current-pointer switch after a TTT
// consolidation selected a new winner version (design §5.4 step 6).
func (m *BusinessMetrics) RecordGraphVersionSwitch() {
	if m == nil {
		return
	}
	m.graphMemoryVersionSwitch.Inc()
}

// RecordGraphBacktestBypass records the backtest bypass rate of one
// consolidation (design §7: 回测免跑率).
func (m *BusinessMetrics) RecordGraphBacktestBypass(ratio float64) {
	if m == nil || ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return
	}
	m.graphMemoryBacktestBypass.Set(ratio)
}

func (m *BusinessMetrics) markTaskInProgress(taskID, source, runtimeMode string) {
	if taskID == "" {
		m.taskInProgress.WithLabelValues(source, runtimeMode).Inc()
		return
	}
	m.activeMu.Lock()
	defer m.activeMu.Unlock()
	if _, ok := m.activeTasks[taskID]; ok {
		return
	}
	m.activeTasks[taskID] = activeTaskLabels{source: source, runtimeMode: runtimeMode}
	m.taskInProgress.WithLabelValues(source, runtimeMode).Inc()
}

func (m *BusinessMetrics) clearTaskInProgress(taskID string) {
	if taskID == "" {
		return
	}
	m.activeMu.Lock()
	labels, ok := m.activeTasks[taskID]
	if ok {
		delete(m.activeTasks, taskID)
	}
	m.activeMu.Unlock()
	if ok {
		m.taskInProgress.WithLabelValues(labels.source, labels.runtimeMode).Dec()
	}
}

func (m *BusinessMetrics) prewarmFailureReasons() {
	for _, source := range []string{"issue", "chat", "autopilot", "autopilot_issue", "quick_create", "other"} {
		for _, runtimeMode := range []string{"local", "cloud", "unknown"} {
			for _, reason := range taskfailure.AllReasons() {
				m.taskFailed.WithLabelValues(source, runtimeMode, reason.String()).Add(0)
			}
		}
	}
}
