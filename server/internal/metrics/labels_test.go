package metrics

import "testing"

func TestBusinessMetricLabelsRejectHighCardinalityNames(t *testing.T) {
	for metric, labels := range businessMetricLabels {
		for _, label := range labels {
			if _, forbidden := forbiddenMetricLabels[label]; forbidden {
				t.Fatalf("metric %s uses forbidden label %s", metric, label)
			}
		}
	}
}

func TestNormalizeLabelsCollapseUnknownValues(t *testing.T) {
	if got := NormalizeRuntimeProvider("provider-from-user-input"); got != "other" {
		t.Fatalf("NormalizeRuntimeProvider unknown = %q, want other", got)
	}
	if got := NormalizeRuntimeMode("workspace-123"); got != "unknown" {
		t.Fatalf("NormalizeRuntimeMode unknown = %q, want unknown", got)
	}
	if got := NormalizeTaskSource("agent_radar"); got != "agent_radar" {
		t.Fatalf("NormalizeTaskSource agent_radar = %q, want agent_radar", got)
	}
	if got := NormalizeRuntimeStatus("unexpected"); got != "unknown" {
		t.Fatalf("NormalizeRuntimeStatus unknown = %q, want unknown", got)
	}
	if got := NormalizeAgentCapacity("saturated"); got != "saturated" {
		t.Fatalf("NormalizeAgentCapacity saturated = %q, want saturated", got)
	}
	if got := NormalizeTaskSource("task-123"); got != "other" {
		t.Fatalf("NormalizeTaskSource unknown = %q, want other", got)
	}
}
