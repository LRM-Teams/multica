package metrics

import (
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

func TestBusinessMetricsLifecycleCountersAndGauge(t *testing.T) {
	m := NewBusinessMetrics()

	m.RecordTaskEnqueued("issue", "local")
	for i := 0; i < 100; i++ {
		m.RecordTaskDispatched("task-"+strconv.Itoa(i), "issue", "local", 2.5)
	}
	m.RecordTaskStarted("issue", "local", "codex")
	m.RecordTaskTerminal("task-0", "issue", "local", "completed", 10, 20, 1)

	if got := testutil.ToFloat64(m.taskEnqueued.WithLabelValues("issue", "local")); got != 1 {
		t.Fatalf("enqueued counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.taskDispatched.WithLabelValues("issue", "local")); got != 100 {
		t.Fatalf("dispatched counter = %v, want 100", got)
	}
	if got := testutil.ToFloat64(m.taskStarted.WithLabelValues("issue", "local", "codex")); got != 1 {
		t.Fatalf("started counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.taskTerminal.WithLabelValues("issue", "local", "completed")); got != 1 {
		t.Fatalf("terminal counter = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.taskInProgress); got != 1 {
		t.Fatalf("in_progress series count = %d, want 1 despite 100 task ids", got)
	}
	if got := testutil.ToFloat64(m.taskInProgress.WithLabelValues("issue", "local")); got != 99 {
		t.Fatalf("in_progress gauge = %v, want 99", got)
	}
	if got := testutil.CollectAndCount(m.taskQueueWait); got != 1 {
		t.Fatalf("queue wait series count = %d, want 1", got)
	}
	if got := testutil.CollectAndCount(m.taskRunSeconds); got != 1 {
		t.Fatalf("run seconds series count = %d, want 1", got)
	}
	if got := testutil.CollectAndCount(m.taskTotalSeconds); got != 1 {
		t.Fatalf("total seconds series count = %d, want 1", got)
	}
}

func TestBusinessMetricsFailureReasonUsesCanonicalClassifier(t *testing.T) {
	m := NewBusinessMetrics()

	rawError := `API Error: 429 {"error":"overloaded"}`
	m.RecordTaskFailed("issue", "local", rawError)

	wantReason := taskfailure.ReasonAgentProviderCapacityOrRateLimit.String()
	if got := testutil.ToFloat64(m.taskFailed.WithLabelValues("issue", "local", wantReason)); got != 1 {
		t.Fatalf("classified failure counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.taskFailed.WithLabelValues("issue", "local", taskfailure.ReasonAgentUnknown.String())); got != 0 {
		t.Fatalf("unknown failure counter = %v, want 0", got)
	}
}

func TestBusinessMetricsLLMPricingAndUnpricedTokens(t *testing.T) {
	m := NewBusinessMetrics()

	m.RecordLLMUsage("chat", "cloud", "codex", "gpt-5.4", 1_000_000, 2_000_000, 3_000_000, 4_000_000)

	if got := testutil.ToFloat64(m.llmTokens.WithLabelValues("openai", "gpt-5.4", "input", "cloud", "chat")); got != 1_000_000 {
		t.Fatalf("priced input tokens = %v, want 1000000", got)
	}
	if got := testutil.ToFloat64(m.llmTokens.WithLabelValues("openai", "gpt-5.4", "output", "cloud", "chat")); got != 2_000_000 {
		t.Fatalf("priced output tokens = %v, want 2000000", got)
	}
	if got := testutil.ToFloat64(m.llmCostUSD.WithLabelValues("openai", "gpt-5.4", "input", "cloud", "chat")); got != 2.5 {
		t.Fatalf("priced input cost = %v, want 2.5", got)
	}
	if got := testutil.ToFloat64(m.llmCostUSD.WithLabelValues("openai", "gpt-5.4", "output", "cloud", "chat")); got != 30 {
		t.Fatalf("priced output cost = %v, want 30", got)
	}
	if got := testutil.ToFloat64(m.llmRequests.WithLabelValues("openai", "gpt-5.4", "cloud")); got != 1 {
		t.Fatalf("priced request counter = %v, want 1", got)
	}

	m.RecordLLMUsage("issue", "local", "custom-provider", "Free Model!!", 7, 0, 0, 0)
	if got := testutil.ToFloat64(m.llmUnpricedTokens.WithLabelValues("other", "free_model_", "input")); got != 7 {
		t.Fatalf("unpriced input tokens = %v, want 7", got)
	}
	if got := testutil.ToFloat64(m.llmRequests.WithLabelValues("other", "unknown", "local")); got != 1 {
		t.Fatalf("unpriced request counter = %v, want 1", got)
	}
}

func TestBusinessMetricsChannelFullExecutionWakesAndBoundedLabels(t *testing.T) {
	m := NewBusinessMetrics()
	for _, reason := range []string{"single_claim", "coordinate", "all_probes_failed"} {
		if got := NormalizeFullExecutionWakeReason(reason); got != "other" {
			t.Fatalf("PR3-only wake reason %q normalized to %q, want other", reason, got)
		}
	}

	m.RecordChannelFullExecutionWake("explicit_mention")
	m.RecordChannelFullExecutionWake("group_command")
	m.RecordChannelFullExecutionWake("thread_reply")
	m.RecordChannelFullExecutionWake("dm")
	m.RecordChannelFullExecutionWake("legacy_full")
	m.RecordChannelFullExecutionWake("message-123")
	m.SetChannelFullExecutionAmplificationRatio(0.3)

	if got := testutil.ToFloat64(m.channelFullExecutionWakes.WithLabelValues("explicit_mention")); got != 1 {
		t.Fatalf("explicit mention wakes = %v, want 1", got)
	}
	for _, reason := range []string{"group_command", "thread_reply", "dm", "legacy_full"} {
		if got := testutil.ToFloat64(m.channelFullExecutionWakes.WithLabelValues(reason)); got != 1 {
			t.Fatalf("%s wakes = %v, want 1", reason, got)
		}
	}
	if got := testutil.ToFloat64(m.channelFullExecutionWakes.WithLabelValues("other")); got != 1 {
		t.Fatalf("normalized wakes = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.channelFullExecutionAmplificationRatio.WithLabelValues()); got != 0.3 {
		t.Fatalf("amplification ratio = %v, want 0.3", got)
	}
}

func TestBusinessMetricsFreshnessHoldResolutionUsesThreeBoundedOutcomes(t *testing.T) {
	m := NewBusinessMetrics()
	m.ObserveFreshnessHoldResolution("send_draft", 1)
	m.ObserveFreshnessHoldResolution("revised_send", 2)
	m.ObserveFreshnessHoldResolution("abandoned", 31)
	m.ObserveFreshnessHoldResolution("invalid", 99)

	family := GatherForTest(t, m)["multica_freshness_hold_resolution_seconds"]
	if family == nil {
		t.Fatal("freshness hold resolution metric family missing")
	}
	if got := len(family.Metric); got != 3 {
		t.Fatalf("freshness outcome series=%d, want exactly 3", got)
	}
	got := map[string]uint64{}
	for _, metric := range family.Metric {
		if len(metric.Label) != 1 || metric.Label[0].GetName() != "outcome" {
			t.Fatalf("freshness metric labels=%+v, want outcome only", metric.Label)
		}
		got[metric.Label[0].GetValue()] = metric.GetHistogram().GetSampleCount()
	}
	for _, outcome := range []string{"send_draft", "revised_send", "abandoned"} {
		if got[outcome] != 1 {
			t.Fatalf("freshness outcome %s count=%d, want 1; all=%+v", outcome, got[outcome], got)
		}
	}
}

func TestBusinessMetricsAgentDeleteDurationUsesBoundedResults(t *testing.T) {
	m := NewBusinessMetrics()
	m.ObserveAgentDelete("success", 0.8)
	m.ObserveAgentDelete("error", 1.2)
	m.ObserveAgentDelete("invalid", 9)

	family := GatherForTest(t, m)["multica_agent_delete_duration_seconds"]
	if family == nil {
		t.Fatal("agent delete duration metric family missing")
	}
	if got := len(family.Metric); got != 2 {
		t.Fatalf("agent delete result series=%d, want exactly 2", got)
	}
	got := map[string]uint64{}
	for _, metric := range family.Metric {
		if len(metric.Label) != 1 || metric.Label[0].GetName() != "result" {
			t.Fatalf("agent delete metric labels=%+v, want result only", metric.Label)
		}
		got[metric.Label[0].GetValue()] = metric.GetHistogram().GetSampleCount()
	}
	for _, result := range []string{"success", "error"} {
		if got[result] != 1 {
			t.Fatalf("agent delete result %s count=%d, want 1; all=%+v", result, got[result], got)
		}
	}
}

func TestBusinessMetricsChannelTriggerDepthHasNoLabels(t *testing.T) {
	m := NewBusinessMetrics()
	m.ObserveChannelTriggerDepth(0)
	m.ObserveChannelTriggerDepth(10)
	m.ObserveChannelTriggerDepth(11)
	m.ObserveChannelTriggerDepth(-1)

	family := GatherForTest(t, m)["multica_channel_trigger_depth"]
	if family == nil {
		t.Fatal("channel trigger depth metric family missing")
	}
	if got := len(family.Metric); got != 1 {
		t.Fatalf("channel trigger depth series=%d, want 1", got)
	}
	metric := family.Metric[0]
	if got := len(metric.Label); got != 0 {
		t.Fatalf("channel trigger depth labels=%+v, want none", metric.Label)
	}
	if got := metric.GetHistogram().GetSampleCount(); got != 3 {
		t.Fatalf("channel trigger depth count=%d, want 3", got)
	}
	if got := metric.GetHistogram().GetSampleSum(); got != 21 {
		t.Fatalf("channel trigger depth sum=%v, want 21", got)
	}
	var atTen uint64
	for _, bucket := range metric.GetHistogram().Bucket {
		if bucket.GetUpperBound() == 10 {
			atTen = bucket.GetCumulativeCount()
		}
	}
	if atTen != 2 {
		t.Fatalf("channel trigger depth <=10 bucket=%d, want 2", atTen)
	}
}

func TestBusinessMetricsRegistryExposesAllFamilies(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewBusinessMetrics()
	registry.MustRegister(m.Collectors()...)

	m.RecordTaskEnqueued("issue", "local")
	m.RecordTaskDispatched("task-1", "issue", "local", 1)
	m.RecordTaskStarted("issue", "local", "codex")
	m.RecordTaskTerminal("task-1", "issue", "local", "completed", 2, 3, 1)
	m.RecordTaskFailed("issue", "local", taskfailure.ReasonTimeout.String())
	m.RecordTaskQueuedExpired("issue", "local")
	m.RecordTaskLeaseExpired("issue")
	m.RecordChannelAmbientGateDecision("coalesced", "agent_active_ambient")
	m.RecordChannelOutputSuppressed("legacy_protocol_output")
	m.RecordChannelFullExecutionWake("legacy_full")
	m.SetChannelFullExecutionAmplificationRatio(0.25)
	m.ObserveChannelTriggerDepth(0)
	m.ObserveFreshnessHoldResolution("send_draft", 1)
	m.ObserveAgentDelete("success", 0.5)
	m.RecordLLMUsage("issue", "local", "codex", "gpt-5.4", 1, 1, 1, 1)
	m.RecordLLMUsage("issue", "local", "custom-provider", "custom-model", 1, 0, 0, 0)

	// PR3 funnel / community / commercial events. Drive every counter
	// with one synthetic value so the gather loop below sees the family.
	exerciseEvent(m, analytics.EventSignup, map[string]any{"signup_source": "test"})
	exerciseEvent(m, analytics.EventWorkspaceCreated, map[string]any{"source": "manual"})
	exerciseEvent(m, analytics.EventTeamInviteSent, nil)
	exerciseEvent(m, analytics.EventTeamInviteAccepted, nil)
	exerciseEvent(m, analytics.EventOnboardingStarted, map[string]any{"platform": "web"})
	exerciseEvent(m, analytics.EventOnboardingQuestionnaireSubmit, nil)
	exerciseEvent(m, analytics.EventOnboardingCompleted, map[string]any{"completion_path": "full"})
	exerciseEvent(m, analytics.EventCloudWaitlistJoined, nil)
	exerciseEvent(m, analytics.EventIssueCreated, map[string]any{"source": "manual", "platform": "web"})
	exerciseEvent(m, analytics.EventChatMessageSent, map[string]any{"platform": "web"})
	exerciseEvent(m, analytics.EventAgentCreated, map[string]any{"runtime_mode": "local", "source": "manual"})
	exerciseEvent(m, analytics.EventAutopilotCreated, map[string]any{"cadence": "manual"})
	exerciseEvent(m, analytics.EventIssueExecuted, map[string]any{"source": "manual"})
	exerciseEvent(m, analytics.EventRuntimeRegistered, map[string]any{"runtime_mode": "local", "provider": "claude"})
	exerciseEvent(m, analytics.EventRuntimeReady, map[string]any{"runtime_mode": "local", "provider": "claude", "ready_duration_ms": int64(1000)})
	exerciseEvent(m, analytics.EventRuntimeFailed, map[string]any{"runtime_mode": "local", "provider": "claude", "failure_reason": "timeout", "recoverable": true})
	exerciseEvent(m, analytics.EventRuntimeOffline, map[string]any{"runtime_mode": "local", "provider": "claude"})
	exerciseEvent(m, analytics.EventAutopilotRunStarted, map[string]any{"cadence": "manual", "trigger_kind": "manual"})
	exerciseEvent(m, analytics.EventAutopilotRunCompleted, map[string]any{"cadence": "manual", "trigger_kind": "manual"})
	exerciseEvent(m, analytics.EventAutopilotRunFailed, map[string]any{"cadence": "manual", "trigger_kind": "manual"})
	exerciseEvent(m, analytics.EventFeedbackSubmitted, map[string]any{"kind": "general", "platform": "web"})
	exerciseEvent(m, analytics.EventContactSalesSubmitted, map[string]any{"form_source": "page"})

	// Direct Record* helpers (no PostHog event source).
	m.RecordAutopilotRunSkipped("manual", "throttled")
	m.RecordWebhookDelivery("github", "dispatched")
	m.RecordGithubEventReceived("pull_request", "opened")
	m.RecordGithubPRReview("approved")
	m.ObserveGithubPRMergeSeconds(120)
	m.RecordCloudRuntimeRequest("provision", "ok", 0.5)
	m.RecordDaemonWSMessageReceived("heartbeat")

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	seen := make(map[string]bool, len(families))
	for _, family := range families {
		seen[family.GetName()] = true
	}
	for metric := range businessMetricLabels {
		if !seen[metric] {
			t.Fatalf("registry did not expose metric family %s", metric)
		}
	}
}

func exerciseEvent(m *BusinessMetrics, name string, props map[string]any) {
	if props == nil {
		props = map[string]any{}
	}
	m.IncForEvent(analytics.Event{Name: name, Properties: props})
}

// Shadow gate observability (Task 21): canary results, gate phases and
// audited transitions carry only closed-set labels — never workspace ids,
// memory content, or free-form error text.
func TestShadowGateMetricsUseBoundedLabels(t *testing.T) {
	m := NewBusinessMetrics()

	m.RecordShadowGateCanary("sequence_gap", true)
	m.RecordShadowGateCanary("sanitizer_fail_open", false)
	m.RecordShadowGateCanary("free-form-noise", true)
	m.SetShadowGatePhase("workspace", "atoms", "enabled")
	m.SetShadowGatePhase("global", "reward_shadow", "shadow")
	m.RecordShadowGateTransition("atoms", "shadow", "enabled", "manual")

	if got := testutil.ToFloat64(m.shadowGateCanary.WithLabelValues("sequence_gap")); got != 1 {
		t.Fatalf("green sequence canary gauge = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.shadowGateCanary.WithLabelValues("sanitizer_fail_open")); got != 0 {
		t.Fatalf("red sanitizer canary gauge = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.shadowGateCanary.WithLabelValues("other")); got != 1 {
		t.Fatalf("unknown canary name must normalize to other, got %v", got)
	}
	if got := testutil.ToFloat64(m.shadowGatePhase.WithLabelValues("workspace", "atoms")); got != 2 {
		t.Fatalf("enabled phase gauge = %v, want ordinal 2", got)
	}
	if got := testutil.ToFloat64(m.shadowGatePhase.WithLabelValues("global", "reward_shadow")); got != 1 {
		t.Fatalf("shadow phase gauge = %v, want ordinal 1", got)
	}
	if got := testutil.ToFloat64(m.shadowGateTransitions.WithLabelValues("atoms", "shadow", "enabled", "manual")); got != 1 {
		t.Fatalf("manual transition counter = %v, want 1", got)
	}

	family := GatherForTest(t, m)["multica_shadow_gate_canary_ok"]
	if family == nil {
		t.Fatal("shadow gate canary metric family missing")
	}
	for _, metric := range family.Metric {
		if len(metric.Label) != 1 || metric.Label[0].GetName() != "canary" {
			t.Fatalf("canary metric labels = %+v, want exactly [canary]", metric.Label)
		}
	}

	// Metrics-less deployments (nil receiver) must not panic.
	var nilMetrics *BusinessMetrics
	nilMetrics.RecordShadowGateCanary("sequence_gap", true)
	nilMetrics.SetShadowGatePhase("workspace", "atoms", "enabled")
	nilMetrics.RecordShadowGateTransition("atoms", "shadow", "enabled", "manual")
}
