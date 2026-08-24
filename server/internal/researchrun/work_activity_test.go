package researchrun

import (
	"os"
	"strings"
	"testing"
)

func TestWorkActivityReadsAttemptScopedPersistentTimeline(t *testing.T) {
	raw, err := os.ReadFile("work_activity.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"agent_activity_entry",
		"agent_inbox_event inbox",
		"inbox.started_at",
		"inbox.completed_at",
		"COALESCE(inbox.started_at, work.updated_at)",
		"COALESCE(inbox.completed_at, work.updated_at)",
		"entry.observed_at >= $3::timestamptz",
		"entry.observed_at <= $4::timestamptz",
		"workActivityTimelineLimit+1",
		"entry.title",
		"entry.body_kind",
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
	if strings.Contains(source, "attempt.started_at,") || strings.Contains(source, "attempt.completed_at,") {
		t.Fatal("dispatch-time Attempt timestamps must not bound Inbox Task activity")
	}
}
