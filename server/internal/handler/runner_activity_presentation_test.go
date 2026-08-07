package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/activityprojection"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProjectRunnerActivityTimelineEntryUsesResolvedIssueTitle(t *testing.T) {
	const issueID = "c3128dbf-4ea9-4da2-8b39-8e69df3ced47"
	entry := protocol.AgentActivityEntry{
		Kind: "narrative",
		Body: []byte(`{"text":"` + issueID + `","activity_kind":"working","detail_kind":"getting_issue"}`),
	}
	row := projectRunnerActivityTimelineEntry(entry, activityprojection.Summary{Label: "Online", Tone: "success"}, map[string]string{
		issueID: "Fix agent mention delivery",
	})
	if row.Title != "Checking issue" || row.Subtext != "Fix agent mention delivery" {
		t.Fatalf("row = %+v, want resolved issue title", row)
	}
}

func TestProjectRunnerActivityTimelineEntryHidesUnresolvedIssueReference(t *testing.T) {
	for _, ref := range []string{"$id", "c3128dbf-4ea9-4da2-8b39-8e69df3ced47", "guessed-key"} {
		t.Run(ref, func(t *testing.T) {
			entry := protocol.AgentActivityEntry{
				Kind: "narrative",
				Body: []byte(`{"text":"` + ref + `","activity_kind":"working","detail_kind":"listing_issue_comments"}`),
			}
			row := projectRunnerActivityTimelineEntry(entry, activityprojection.Summary{Label: "Online", Tone: "success"}, nil)
			if row.Title != "Checking issue comments" || row.Subtext != "" {
				t.Fatalf("row = %+v, want unresolved reference hidden", row)
			}
		})
	}
}

func TestProjectRunnerActivityTimelineEntryKeepsCommandText(t *testing.T) {
	entry := protocol.AgentActivityEntry{
		Kind: "narrative",
		Body: []byte(`{"text":"go test ./internal/handler","activity_kind":"working","detail_kind":"running_command"}`),
	}
	row := projectRunnerActivityTimelineEntry(entry, activityprojection.Summary{Label: "Online", Tone: "success"}, nil)
	if row.Title != "Running command" || row.Subtext != "go test ./internal/handler" {
		t.Fatalf("row = %+v, want command text preserved", row)
	}
}

func TestTruncateRunnerActivitySummary(t *testing.T) {
	if got := truncateRunnerActivitySummary("payment required", 240); got != "payment required" {
		t.Fatalf("short reason = %q", got)
	}
	if got := truncateRunnerActivitySummary("abcdef", 4); got != "abcd…" {
		t.Fatalf("truncated reason = %q", got)
	}
}
