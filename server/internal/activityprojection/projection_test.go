package activityprojection

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProjectSummaryOwnsAllKnownSemanticsAndUnknownFallback(t *testing.T) {
	cases := []struct {
		kind, detail, want string
	}{
		{"online", "idle", "Online"},
		{"thinking", "", "Thinking..."},
		{"working", "starting", "Starting"},
		{"working", "message_received", "Message received"},
		{"working", "running_command", "Running command..."},
		{"working", "checking_messages", "Checking messages..."},
		{"working", "compacting_context", "Compacting context..."},
		{"working", "compaction_finished", "Compaction finished"},
		{"working", "compaction_stale", "Compaction still running"},
		{"working", "reading_file", "Reading file..."},
		{"working", "editing_file", "Editing file..."},
		{"working", "searching_code", "Searching code..."},
		{"working", "fetching_url", "Fetching web..."},
		{"working", "sending_message", "Sending message..."},
		{"working", "waiting_for_message", "Waiting for messages..."},
		{"working", "claiming_task", "Claiming tasks..."},
		{"working", "commenting_issue", "Commenting on issue..."},
		{"working", "scheduling_reminder", "Scheduling reminder..."},
		{"working", "collaborating", "Collaborating..."},
		{"working", "future_detail", "Working..."},
		{"error", "", "Error"},
		{"offline", "machine_disconnected", "Offline"},
		{"offline", "stopped", "Offline"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.detail, func(t *testing.T) {
			got := ProjectSummary(protocol.AgentActivitySnapshot{ActivityKind: tc.kind, DetailKind: tc.detail})
			if got.Label != tc.want {
				t.Fatalf("label = %q, want %q", got.Label, tc.want)
			}
		})
	}
}

func TestProjectSummaryUsesLifecycleToneVocabulary(t *testing.T) {
	cases := []struct {
		kind, detail, want string
	}{
		{protocol.ActivityKindOnline, "idle", "success"},
		{protocol.ActivityKindWorking, "message_received", "warning"},
		{protocol.ActivityKindError, "runtime_error", "error"},
		{protocol.ActivityKindOffline, "machine_disconnected", "neutral"},
	}
	for _, tc := range cases {
		got := ProjectSummary(protocol.AgentActivitySnapshot{ActivityKind: tc.kind, DetailKind: tc.detail})
		if got.Tone != tc.want {
			t.Fatalf("%s/%s tone = %q, want %q", tc.kind, tc.detail, got.Tone, tc.want)
		}
	}
}

func TestProjectSummaryCannotLeakSensitiveProviderDetail(t *testing.T) {
	summary := ProjectSummary(protocol.AgentActivitySnapshot{ActivityKind: protocol.ActivityKindWorking, DetailKind: "running_command"})
	for _, forbidden := range []string{"rm -rf /secret", "/private/path", "prompt", "stderr"} {
		if strings.Contains(summary.Label, forbidden) {
			t.Fatalf("summary leaked %q: %+v", forbidden, summary)
		}
	}
}

func TestTimelineRowFromSnapshotHasNoProviderBody(t *testing.T) {
	row := TimelineRowFromSnapshot(protocol.AgentActivitySnapshot{
		ActivityKind: protocol.ActivityKindWorking,
		DetailKind:   "running_command",
	})
	if row.Title != "Running command..." || row.BodyKind != "none" || row.Body != "" || row.Subtext != "" {
		t.Fatalf("snapshot row = %+v, want bounded presentation without provider detail", row)
	}
}

func TestProjectTimelineEntryUsesGenericFallbackAndBoundsText(t *testing.T) {
	unknown := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "future_kind", Body: []byte(`{"secret":"must not render"}`)}, Summary{Label: "Working...", Tone: "info"})
	if unknown.Body != "" || unknown.BodyKind != "generic" {
		t.Fatalf("unknown entry = %+v", unknown)
	}
	long := strings.Repeat("x", maxTimelineBodyBytes+1)
	row := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(`{"text":"` + long + `"}`)}, Summary{Label: "Working...", Tone: "info"})
	if row.BodyKind != "none" || len(row.Subtext) != maxTimelineBodyBytes || row.Body != "" {
		t.Fatalf("bounded row = %+v", row)
	}
}

func TestProjectTimelineEntryUsesEventLocalLifecycleInsteadOfLatestSnapshot(t *testing.T) {
	latest := Summary{Label: "Online", Tone: "success", Visibility: "visible"}
	message := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(`{"text":"Message received","activity_kind":"working","detail_kind":"message_received"}`)}, latest)
	if message.Title != "Working" || message.Subtext != "Message received" || message.Tone != "warning" || message.BodyKind != "none" {
		t.Fatalf("message row = %+v", message)
	}
	errorRow := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(`{"text":"Runtime error","activity_kind":"error","detail_kind":"runtime_error"}`)}, latest)
	if errorRow.Title != "Error" || errorRow.Subtext != "Runtime error" || errorRow.Tone != "error" {
		t.Fatalf("error row = %+v", errorRow)
	}
	idle := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(`{"text":"Idle","activity_kind":"online","detail_kind":"idle"}`)}, Summary{Label: "Error", Tone: "error"})
	if idle.Title != "Idle" || idle.Subtext != "Idle" || idle.Tone != "success" {
		t.Fatalf("idle row = %+v", idle)
	}
}

func TestProjectTimelineEntryShowsCommandTextAsSubtext(t *testing.T) {
	row := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(`{"text":"pnpm test","activity_kind":"working","detail_kind":"running_command"}`)}, Summary{Label: "Online", Tone: "success"})
	if row.Title != "Running command" || row.Subtext != "pnpm test" || row.Tone != "warning" || row.BodyKind != "none" {
		t.Fatalf("command row = %+v, want title Running command with the command as subtext", row)
	}
	// The generic label narrative (no command in the tool input) must not
	// echo the title as its own subtext.
	plain := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(`{"text":"Running command","activity_kind":"working","detail_kind":"running_command"}`)}, Summary{Label: "Online", Tone: "success"})
	if plain.Title != "Running command" || plain.Subtext != "" {
		t.Fatalf("generic command row = %+v, want no subtext", plain)
	}
}

func TestProjectTimelineEntryShowsCompactionLifecycleUnderWorking(t *testing.T) {
	latest := Summary{Label: "Online", Tone: "success"}
	cases := []struct {
		body        string
		wantTitle   string
		wantSubtext string
	}{
		{
			body:        `{"text":"Compacting context","activity_kind":"working","detail_kind":"compacting_context"}`,
			wantTitle:   "Working",
			wantSubtext: "Compacting context",
		},
		{
			body:        `{"text":"Context compaction finished","activity_kind":"working","detail_kind":"compaction_finished"}`,
			wantTitle:   "Working",
			wantSubtext: "Context compaction finished",
		},
		{
			body:        `{"text":"Context compaction still running; no finish event observed","activity_kind":"working","detail_kind":"compaction_stale"}`,
			wantTitle:   "Compaction still running",
			wantSubtext: "Context compaction still running; no finish event observed",
		},
	}
	for _, tc := range cases {
		row := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(tc.body)}, latest)
		if row.Title != tc.wantTitle || row.Subtext != tc.wantSubtext || row.Tone != "warning" {
			t.Fatalf("compaction row = %+v, want title=%q subtext=%q", row, tc.wantTitle, tc.wantSubtext)
		}
	}
}

func TestProjectTimelineEntryLabelsFineGrainedToolKinds(t *testing.T) {
	latest := Summary{Label: "Online", Tone: "success"}
	cases := []struct {
		name        string
		body        string
		wantTitle   string
		wantSubtext string
	}{
		{
			name:        "sending message shows the target",
			body:        `{"text":"#general","activity_kind":"working","detail_kind":"sending_message"}`,
			wantTitle:   "Sending message",
			wantSubtext: "#general",
		},
		{
			name:        "reading file shows the path",
			body:        `{"text":"/repo/main.go","activity_kind":"working","detail_kind":"reading_file"}`,
			wantTitle:   "Reading file",
			wantSubtext: "/repo/main.go",
		},
		{
			name:        "tool without a summary renders the label alone",
			body:        `{"text":"","activity_kind":"working","detail_kind":"checking_messages"}`,
			wantTitle:   "Checking messages",
			wantSubtext: "",
		},
		{
			name:        "issue lookup uses checking vocabulary",
			body:        `{"text":"Fix message delivery","activity_kind":"working","detail_kind":"getting_issue"}`,
			wantTitle:   "Checking issue",
			wantSubtext: "Fix message delivery",
		},
		{
			name:        "issue comments use checking vocabulary",
			body:        `{"text":"Fix message delivery","activity_kind":"working","detail_kind":"listing_issue_comments"}`,
			wantTitle:   "Checking issue comments",
			wantSubtext: "Fix message delivery",
		},
		{
			name:        "unknown detail kind falls back to Working",
			body:        `{"text":"Using cursor-agent","activity_kind":"working","detail_kind":"other"}`,
			wantTitle:   "Working",
			wantSubtext: "Using cursor-agent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(tc.body)}, latest)
			if row.Title != tc.wantTitle || row.Subtext != tc.wantSubtext || row.Tone != "warning" {
				t.Fatalf("row = %+v, want title %q subtext %q", row, tc.wantTitle, tc.wantSubtext)
			}
		})
	}
}

func TestProjectTimelineEntryKeepsComputerDisconnectOutOfAgentVocabulary(t *testing.T) {
	row := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "narrative", Body: []byte(`{"text":"","activity_kind":"offline","detail_kind":"machine_disconnected"}`)}, Summary{Label: "Online", Tone: "success"})
	if row.Title != "Offline" || row.Tone != "neutral" {
		t.Fatalf("computer disconnect Agent row = %+v, want Offline", row)
	}
}

func TestProjectTimelineEntryProjectsRuntimeDiagnosticWithoutChangingLifecycle(t *testing.T) {
	row := ProjectTimelineEntry(protocol.AgentActivityEntry{Kind: "system", Body: []byte(`{"title":"Codex config warning","text":"User namespaces are unavailable"}`)}, Summary{Label: "Online", Tone: "success"})
	if row.Title != "Codex config warning" || row.Subtext != "User namespaces are unavailable" || row.Tone != "warning" || row.BodyKind != "none" {
		t.Fatalf("diagnostic row = %+v", row)
	}
}
