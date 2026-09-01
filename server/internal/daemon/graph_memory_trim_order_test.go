package daemon

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// Graph-mode execution memory packs under the shared 16 KiB budget with a
// fixed trim order (unification spec §4.4): historical legacy memory is
// trimmed first, the current graph recall next, and the federated research
// recall last — research knowledge outlives the per-turn channel recall when
// the budget overflows.
func TestGraphModeExecutionMemoryTrimOrder(t *testing.T) {
	const (
		researchBytes = 12 * 1024
		currentBytes  = 8 * 1024
		historyBytes  = 2 * 1024
	)
	merged := mergeGraphModeExecutionMemory("", Task{},
		[]execenv.MemoryContextForEnv{{
			Name: "Agent global memory", Content: strings.Repeat("h", historyBytes), Scope: "agent",
		}},
		[]execenv.MemoryContextForEnv{{
			Name: "Graph memory recall", Content: strings.Repeat("c", currentBytes), Scope: "workspace",
		}},
		[]execenv.MemoryContextForEnv{{
			Name: "Research memory recall", Content: strings.Repeat("r", researchBytes), Scope: "workspace",
		}},
	)

	var research, current, historical int
	for _, memory := range merged {
		switch {
		case strings.HasPrefix(memory.Content, "r"):
			research = len(memory.Content)
		case strings.HasPrefix(memory.Content, "c"):
			current = len(memory.Content)
		case strings.HasPrefix(memory.Content, "h"):
			historical = len(memory.Content)
		}
	}
	if research != researchBytes {
		t.Fatalf("research recall trimmed to %d bytes, want intact %d (research is trimmed last)", research, researchBytes)
	}
	if current == 0 || current >= currentBytes {
		t.Fatalf("current recall = %d bytes, want trimmed below %d before research", current, currentBytes)
	}
	if historical != 0 {
		t.Fatalf("historical legacy memory survived %d bytes, want trimmed first", historical)
	}
}

// Without research memories the merge keeps its existing shape: whitelisted
// legacy memory first, graph recall appended.
func TestGraphModeExecutionMemoryWithoutResearch(t *testing.T) {
	merged := mergeGraphModeExecutionMemory("", Task{}, nil,
		[]execenv.MemoryContextForEnv{{Name: "Graph memory recall", Content: "found node", Scope: "workspace"}},
		nil,
	)
	joined := strings.Join(contentsOf(merged), "\n")
	if !strings.Contains(joined, "found node") {
		t.Fatalf("merge dropped the graph recall: %q", joined)
	}
}

func contentsOf(memories []execenv.MemoryContextForEnv) []string {
	out := make([]string, 0, len(memories))
	for _, m := range memories {
		out = append(out, m.Content)
	}
	return out
}
