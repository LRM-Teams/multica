package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/memorysync"
)

func TestMemorySyncStrategyADecisions(t *testing.T) {
	// Mirror the handler branch mapping so regressions are obvious.
	cases := []struct {
		existing, incoming, want string
	}{
		{"先报进度", "先报进度", memorysync.DecisionSame},
		{"先报进度", "长任务开始前先报进度并持续汇报", memorysync.DecisionMoreSpecific},
		{"紧急也要先报进度", "紧急时别报进度，直接干", memorysync.DecisionOpposed},
	}
	for _, tc := range cases {
		got := memorysync.Compare(tc.existing, tc.incoming)
		if got.Decision != tc.want {
			t.Fatalf("%q vs %q: got %s want %s (%s)", tc.existing, tc.incoming, got.Decision, tc.want, got.Reason)
		}
	}
}
