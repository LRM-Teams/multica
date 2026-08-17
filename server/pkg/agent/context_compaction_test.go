package agent

import (
	"encoding/json"
	"testing"
)

func TestShouldProactivelyCompactAtSixtyPercent(t *testing.T) {
	percentBelow := 59.9
	percentAt := 60.0
	tokens, window := int64(60), int64(100)

	tests := []struct {
		name  string
		stats *RuntimeTokenStats
		want  bool
	}{
		{name: "missing telemetry", stats: nil, want: false},
		{name: "below threshold", stats: &RuntimeTokenStats{ContextPercent: &percentBelow}, want: false},
		{name: "at threshold", stats: &RuntimeTokenStats{ContextPercent: &percentAt}, want: true},
		{name: "derived threshold", stats: &RuntimeTokenStats{ContextTokens: &tokens, ContextWindow: &window}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldProactivelyCompact(tt.stats); got != tt.want {
				t.Fatalf("shouldProactivelyCompact() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCodexRuntimeStatsUseCurrentThreadOccupancy(t *testing.T) {
	c := &codexClient{}
	c.updateRuntimeStats(map[string]any{
		"tokenUsage": map[string]any{
			"total":              map[string]any{"totalTokens": float64(120_000)},
			"modelContextWindow": float64(200_000),
		},
	})
	stats := c.currentRuntimeStats()
	if stats == nil || stats.ContextTokens == nil || *stats.ContextTokens != 120_000 {
		t.Fatalf("context tokens = %+v, want 120000", stats)
	}
	if stats.ContextWindow == nil || *stats.ContextWindow != 200_000 {
		t.Fatalf("context window = %+v, want 200000", stats)
	}
	if !shouldProactivelyCompact(stats) {
		t.Fatal("60% occupied Codex thread should compact")
	}
}

func TestClaudeACPUsageUpdateTracksContextOccupancy(t *testing.T) {
	c := &acpClient{}
	c.handleUsageUpdate(json.RawMessage(`{"sessionUpdate":"usage_update","used":120000,"size":200000}`))
	stats := c.currentRuntimeStats()
	if stats == nil || stats.ContextTokens == nil || *stats.ContextTokens != 120_000 {
		t.Fatalf("context tokens = %+v, want 120000", stats)
	}
	if stats.ContextWindow == nil || *stats.ContextWindow != 200_000 {
		t.Fatalf("context window = %+v, want 200000", stats)
	}
	if !shouldProactivelyCompact(stats) {
		t.Fatal("60% occupied Claude session should compact")
	}
}

func TestShouldProactivelyCompactSkipsIneffectiveRepeat(t *testing.T) {
	percentHigh := 70.0
	percentStillHigh := 68.0
	percentLow := 40.0
	stats := &RuntimeTokenStats{ContextPercent: &percentHigh}
	var st compactionAttemptState

	if !shouldProactivelyCompactAt(stats, &st) {
		t.Fatal("first crossing of 60% should compact")
	}
	st.recordAttempt(true, stats)
	if !shouldProactivelyCompactAt(stats, &st) {
		t.Fatal("failed compact may be retried while occupancy stays high")
	}

	st.recordAttempt(false, &RuntimeTokenStats{ContextPercent: &percentStillHigh})
	if shouldProactivelyCompactAt(stats, &st) {
		t.Fatal("ineffective compact must not run again until occupancy falls")
	}
	low := &RuntimeTokenStats{ContextPercent: &percentLow}
	if shouldProactivelyCompactAt(low, &st) {
		t.Fatal("below 60% must not compact")
	}
	st.recordAttempt(false, low)
	if !shouldProactivelyCompactAt(stats, &st) {
		t.Fatal("after occupancy falls under the resume line, 60% may compact again")
	}
}
