package analytics

import "testing"

func TestRuntimeReadyOmitsUnmeasuredDuration(t *testing.T) {
	ev := RuntimeReady("user-1", "workspace-1", "runtime-1", "daemon-1", "codex", 0)
	if _, ok := ev.Properties["ready_duration_ms"]; ok {
		t.Fatalf("ready_duration_ms should be omitted until it is measured")
	}

	ev = RuntimeReady("user-1", "workspace-1", "runtime-1", "daemon-1", "codex", 123)
	if got := ev.Properties["ready_duration_ms"]; got != int64(123) {
		t.Fatalf("ready_duration_ms = %v, want 123", got)
	}
}

func TestFailedEventsUseRecoverable(t *testing.T) {
	runEv := RuntimeFailed("user-1", "workspace-1", "daemon-1", "codex", "task failed", "task_error", false)
	if got := runEv.Properties["recoverable"]; got != false {
		t.Fatalf("runtime recoverable = %v, want false", got)
	}
	if _, ok := runEv.Properties["will_retry"]; ok {
		t.Fatalf("runtime failure should not emit will_retry")
	}
}

func TestIsMetricsOnly(t *testing.T) {
	// Operational / execution-lifecycle events are Prometheus-only and must
	// not be shipped to PostHog.
	for _, name := range []string{
		EventRuntimeRegistered, EventRuntimeReady, EventRuntimeFailed, EventRuntimeOffline,
		EventAutopilotRunStarted, EventAutopilotRunCompleted, EventAutopilotRunFailed,
	} {
		if !IsMetricsOnly(name) {
			t.Errorf("IsMetricsOnly(%q) = false, want true (operational event must stay out of PostHog)", name)
		}
	}
	// Product-behaviour events must still reach PostHog.
	for _, name := range []string{
		EventSignup, EventWorkspaceCreated, EventIssueCreated, EventIssueExecuted,
		EventChatMessageSent, EventAgentCreated, EventAutopilotCreated,
	} {
		if IsMetricsOnly(name) {
			t.Errorf("IsMetricsOnly(%q) = true, want false (product event must reach PostHog)", name)
		}
	}
}
