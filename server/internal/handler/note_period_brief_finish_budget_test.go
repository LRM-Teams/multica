package handler

import (
	"testing"
	"time"
)

func TestNotePeriodBriefFinishWaitBudgetCoversRetryWave(t *testing.T) {
	t.Parallel()
	prev := notePeriodBriefCollectorMaxWait
	notePeriodBriefCollectorMaxWait = 15 * time.Minute
	t.Cleanup(func() { notePeriodBriefCollectorMaxWait = prev })

	got := notePeriodBriefFinishWaitBudget()
	want := 2*notePeriodBriefCollectorMaxWait + time.Minute
	if got != want {
		t.Fatalf("finish wait budget = %s, want %s (two ceilings + cushion)", got, want)
	}
	// Regression: maxWait+1m alone aborts a late retry (see collecting stuck
	// after Windows 503 retry while Linux already had a pack).
	if got <= notePeriodBriefCollectorMaxWait+time.Minute {
		t.Fatalf("finish wait budget %s must exceed a single wave (%s)", got, notePeriodBriefCollectorMaxWait+time.Minute)
	}
}
