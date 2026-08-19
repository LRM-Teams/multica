package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/activityprojection"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProjectRunnerActivityTimelineEntryUsesResolvedIssueTitle(t *testing.T) {
	const issueID = "c3128dbf-4ea9-4da2-8b39-8e69df3ced47"
	entry := protocol.AgentActivityEntry{
		Kind: "tool_start",
		Body: []byte(`{"toolName":"get_issue","toolInput":"` + issueID + `"}`),
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
				Kind: "tool_start",
				Body: []byte(`{"toolName":"list_issue_comments","toolInput":"` + ref + `"}`),
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
		Kind: "tool_start",
		Body: []byte(`{"toolName":"bash","toolInput":"go test ./internal/handler"}`),
	}
	row := projectRunnerActivityTimelineEntry(entry, activityprojection.Summary{Label: "Online", Tone: "success"}, nil)
	if row.Title != "Running command" || row.Subtext != "go test ./internal/handler" {
		t.Fatalf("row = %+v, want command text preserved", row)
	}
}

func TestOverlayInFlightInboxOnIdleRunnerSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"Online", "Thinking..."},
		{"Idle", "Thinking..."},
		{"Working...", "Thinking..."},
		{"Running command...", "Running command..."},
		{"Thinking...", "Thinking..."},
	}
	for _, tc := range cases {
		got := overlayInFlightInboxOnIdleRunnerSummary(activityprojection.Summary{
			Label: tc.in, Tone: "success", Visibility: "visible",
		})
		if got.Label != tc.want {
			t.Fatalf("%q → %q, want %q", tc.in, got.Label, tc.want)
		}
	}
}
