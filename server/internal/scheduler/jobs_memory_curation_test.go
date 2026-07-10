package scheduler

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/memorycuration"
)

func TestMemoryCurationJobsUseStableNames(t *testing.T) {
	jobs := MemoryCurationJobs(nil)
	want := []string{
		JobNameMemoryL1DailyRecord,
		JobNameMemoryL2ReviewExtract,
		JobNameMemoryL3Promote,
		JobNameMemoryL4Curator,
	}
	if len(jobs) != len(want) {
		t.Fatalf("len(jobs) = %d, want %d", len(jobs), len(want))
	}
	for i, job := range jobs {
		if job.Name != want[i] {
			t.Fatalf("job[%d].Name = %q, want %q", i, job.Name, want[i])
		}
		if job.Cadence != time.Hour {
			t.Fatalf("job[%d].Cadence = %s, want 1h", i, job.Cadence)
		}
		if err := job.validate(); err != nil {
			t.Fatalf("job[%d] did not validate: %v", i, err)
		}
	}
}

func TestMemoryCurationJobsUseBeijingHours(t *testing.T) {
	jobs := MemoryCurationJobs(nil)
	loc, err := time.LoadLocation(memorycuration.DefaultTimezone)
	if err != nil {
		t.Fatal(err)
	}
	plan := time.Date(2026, 7, 9, 1, 0, 0, 0, loc).UTC()
	res, err := jobs[0].Handler(t.Context(), HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result["reason"] == "outside_stage_hour" {
		t.Fatalf("handler result = %#v, want Beijing 01:00 to pass hour gate", res.Result)
	}
	plan = time.Date(2026, 7, 9, 0, 0, 0, 0, loc).UTC()
	res, err = jobs[0].Handler(t.Context(), HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result["reason"] != "outside_stage_hour" {
		t.Fatalf("handler result = %#v, want outside_stage_hour", res.Result)
	}
}

func TestMemoryCurationStageNormalization(t *testing.T) {
	cases := map[string]memorycuration.Stage{
		"l1_daily":   memorycuration.StageL1,
		"review":     memorycuration.StageL2,
		"promote":    memorycuration.StageL3,
		"l4_curator": memorycuration.StageL4,
		"all":        memorycuration.StageAll,
	}
	for input, want := range cases {
		got, err := memorycuration.NormalizeStage(input)
		if err != nil {
			t.Fatalf("NormalizeStage(%q) error: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeStage(%q) = %q, want %q", input, got, want)
		}
	}
}
