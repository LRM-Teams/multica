package researchrun

import (
	"os"
	"strings"
	"testing"
)

func TestV6WorkActivityReadsAttemptScopedPersistentTimeline(t *testing.T) {
	raw, err := os.ReadFile("work_activity_v6.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"research_work_item_activity_entry",
		"agent_inbox_event inbox",
		"inbox.started_at",
		"inbox.completed_at",
		"entry.work_item_attempt_id=$3::uuid",
		"entry.inbox_task_id=$4::uuid",
		"v6WorkActivityTimelineLimit+1",
		"progress.updated_at",
		"v6_work_progress_reported",
		"event.payload->>'work_item_id'=work.id::text",
		"event.payload->>'attempt_id'=attempt.id::text",
		"NULLIF(work.payload->>'mission_prompt','')",
		"membership.mission_prompt",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("V6 Work activity read model missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"FROM agent_activity_entry",
		"entry.observed_at >=",
		"entry.observed_at <=",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("V6 Work activity must not use generic Agent/time-window source %q", forbidden)
		}
	}
}
