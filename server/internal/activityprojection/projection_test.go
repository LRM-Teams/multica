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
		{"working", "message_received", "Message received"},
		{"working", "runtime_reconnecting", "Reconnecting..."},
		{"working", "running_command", "Running command..."},
		{"working", "checking_messages", "Checking messages..."},
		{"working", "compacting_context", "Compacting context..."},
		{"working", "future_detail", "Working..."},
		{"error", "", "Runtime error"},
		{"offline", "machine_disconnected", "Disconnected"},
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
	if row.BodyKind != "text" || len(row.Body) != maxTimelineBodyBytes {
		t.Fatalf("bounded row = %+v", row)
	}
}
