package handler

import (
	"strings"
	"testing"
)

func TestPeriodBriefFailureIsPermanentNoAPIKey(t *testing.T) {
	t.Parallel()
	permanent, kind, why := periodBriefFailureIsPermanent("pi exited: No API key", "")
	if !permanent {
		t.Fatalf("No API key must be permanent")
	}
	if kind != "config" {
		t.Fatalf("kind = %q", kind)
	}
	if why == "" {
		t.Fatal("expected abandon why")
	}
}

func TestPeriodBriefFailureIsPermanentRuntimeOfflineRetryable(t *testing.T) {
	t.Parallel()
	permanent, _, _ := periodBriefFailureIsPermanent("runtime went away", "runtime_offline")
	if permanent {
		t.Fatal("runtime_offline should be retryable")
	}
}

func TestFormatNotePeriodBriefPacksFailedOmitsBody(t *testing.T) {
	t.Parallel()
	got := formatNotePeriodBriefPacks([]notePeriodBriefPackResult{{
		AgentID:    "agent-1",
		PageID:     "page-1",
		Status:     "failed",
		Retryable:  false,
		Detail:     "No API key",
		AbandonWhy: "missing API key",
		Content:    "should never appear in board",
		Title:      "采集包 leak",
	}})
	if !strings.Contains(got, "调用采集 Agent 失败了") {
		t.Fatalf("expected explicit collector failure: %s", got)
	}
	if strings.Contains(got, "should never appear in board") {
		t.Fatalf("failed collector must not expose pack body: %s", got)
	}
	if strings.Contains(got, "Stub awaiting") {
		t.Fatalf("failed collector must not expose stub: %s", got)
	}
}

func TestClassifyPeriodBriefCollectorOutcomeReady(t *testing.T) {
	t.Parallel()
	d := classifyPeriodBriefCollectorOutcome("running", "", "", true, false)
	if d.Status != "ready" || d.Retryable {
		t.Fatalf("%+v", d)
	}
}

func TestClassifyPeriodBriefCollectorOutcomeFailedStillReadyWhenPackWritten(t *testing.T) {
	t.Parallel()
	d := classifyPeriodBriefCollectorOutcome("failed", "api_invalid_request", "Unknown parameter: 'input[86].status'", true, false)
	if d.Status != "ready" || d.Retryable {
		t.Fatalf("pack --note-write must win over a later poisoned API failure: %+v", d)
	}
}

func TestClassifyPeriodBriefCollectorOutcomeFailedConfig(t *testing.T) {
	t.Parallel()
	d := classifyPeriodBriefCollectorOutcome("failed", "", "No API key configured", false, false)
	if d.Status != "failed" || d.Retryable {
		t.Fatalf("%+v", d)
	}
}

func TestClassifyPeriodBriefCollectorOutcomeFailedTransient(t *testing.T) {
	t.Parallel()
	d := classifyPeriodBriefCollectorOutcome("failed", "runtime_offline", "daemon disconnected", false, false)
	if d.Status != "failed" || !d.Retryable {
		t.Fatalf("%+v", d)
	}
}

func TestClassifyPeriodBriefCollectorOutcomeEmptyCompleted(t *testing.T) {
	t.Parallel()
	d := classifyPeriodBriefCollectorOutcome("completed", "", "", false, false)
	if d.Status != "empty" || !d.Retryable {
		t.Fatalf("%+v", d)
	}
}

func TestClassifyPeriodBriefCollectorOutcomeStalled(t *testing.T) {
	t.Parallel()
	d := classifyPeriodBriefCollectorOutcome("running", "", "", false, true)
	if d.Status != "stalled" || !d.Retryable {
		t.Fatalf("%+v", d)
	}
}
