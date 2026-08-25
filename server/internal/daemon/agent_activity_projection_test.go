package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProjectActivitySummaryReturnsFactsAndBoundedLabel(t *testing.T) {
	got := projectActivitySummary(protocol.AgentActivitySnapshot{ActivityKind: protocol.ActivityKindWorking, DetailKind: "running_command"})
	if got.ActivityKind != protocol.ActivityKindWorking || got.DetailKind != "running_command" || got.Label != "Running command..." {
		t.Fatalf("summary = %+v", got)
	}
}

func TestProjectActivityTimelineEntryReturnsFactsAndSafeText(t *testing.T) {
	row := projectActivityTimelineEntry(protocol.AgentActivityEntry{Kind: "tool_start", Body: []byte(`{"toolName":"bash","toolInput":"pnpm test"}`)}, protocol.AgentActivitySummary{ActivityKind: protocol.ActivityKindOnline, DetailKind: "idle", Label: "Online"})
	if row.ActivityKind != protocol.ActivityKindWorking || row.DetailKind != "running_command" || row.Title != "Running command" || row.Subtext != "pnpm test" || row.BodyKind != "command" {
		t.Fatalf("timeline row = %+v", row)
	}
}

func TestProjectActivitySystemTimelineEntryUsesDiagnosticFacts(t *testing.T) {
	row := projectActivityTimelineEntry(protocol.AgentActivityEntry{Kind: "system", Body: []byte(`{"title":"Runtime warning","text":"Provider unavailable"}`)}, protocol.AgentActivitySummary{ActivityKind: protocol.ActivityKindOnline, DetailKind: "idle", Label: "Online"})
	if row.ActivityKind != protocol.ActivityKindError || row.DetailKind != "runtime_diagnostic" || row.Title != "Runtime warning" || row.Subtext != "Provider unavailable" {
		t.Fatalf("timeline row = %+v", row)
	}
}
