package agent

import "testing"

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
