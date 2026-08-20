package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

const (
	testWSID   = "11111111-1111-1111-1111-111111111111"
	testProjID = "22222222-2222-2222-2222-222222222222"
	testChanID = "33333333-3333-3333-3333-333333333333"
	testMember = "44444444-4444-4444-4444-444444444444"
)

func writeAgentFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func memoryNames(memories []execenv.MemoryContextForEnv) map[string]bool {
	out := map[string]bool{}
	for _, m := range memories {
		out[m.Name] = true
	}
	return out
}

// P0 evidence (spec §13 P0-1, §8): graph mode keeps legacy user/agent memory,
// appends the graph recall blob, and never injects legacy
// project/channel/daily/workspace/team sources.
func TestGraphModeExecutionMemoryMergesLegacyUserAgent(t *testing.T) {
	agentRoot := t.TempDir()
	writeAgentFile(t, agentRoot, "users/"+testMember+"/USER.md", "user prefers Chinese")
	writeAgentFile(t, agentRoot, "users/"+testMember+"/RELATIONSHIP.md", "user is the owner")
	writeAgentFile(t, agentRoot, "memory/MEMORY.md", "agent long-term memory")
	writeAgentFile(t, agentRoot, "memory/STATE.md", "agent active state")
	writeAgentFile(t, agentRoot, "projects/"+testProjID+"/MEMORY.md", "legacy project memory")
	writeAgentFile(t, agentRoot, "channels/"+testChanID+"/CONTEXT.md", "legacy channel context")

	task := Task{
		WorkspaceID: testWSID, AgentID: "agent-1",
		ProjectID: testProjID, ChannelID: testChanID,
		InitiatorType: "member", InitiatorID: testMember,
	}
	serverMemories := []execenv.MemoryContextForEnv{
		{Name: "Server user memory", Content: "db user memory", Scope: "user"},
		{Name: "Server project memory", Content: "db project memory", Scope: "project"},
		{Name: "Team knowledge", Content: "wiki page", Scope: "team"},
	}
	graph := []execenv.MemoryContextForEnv{{
		Name: "Graph memory recall", Content: "## Graph Memory Recall\ngraph body", Scope: "workspace",
	}}

	merged := mergeGraphModeExecutionMemory(agentRoot, task, serverMemories, graph)
	names := memoryNames(merged)

	for _, want := range []string{
		"Current user preferences", "Current user relationship context",
		"Agent global memory", "Agent active state",
		"Server user memory", "Graph memory recall",
	} {
		if !names[want] {
			t.Errorf("merged memory missing %q (got %v)", want, names)
		}
	}
	for _, forbidden := range []string{
		"Current project memory", "Current channel context",
		"Server project memory", "Team knowledge", "Today activity summary",
	} {
		if names[forbidden] {
			t.Errorf("merged memory must not contain %q in graph mode", forbidden)
		}
	}
}

// The scope whitelist is explicit (spec §8): keep user/member scopes and
// agent scope except the legacy daily summary; drop everything else.
func TestFilterGraphModeLegacyMemories(t *testing.T) {
	in := []execenv.MemoryContextForEnv{
		{Name: "Current user preferences", Scope: "user", Content: "u"},
		{Name: "Server member memory", Scope: "member", Content: "m"},
		{Name: "Agent global memory", Scope: "agent", Content: "a"},
		{Name: "Today activity summary", Scope: "agent", Content: "daily"},
		{Name: "Current project memory", Scope: "project", Content: "p"},
		{Name: "Current channel context", Scope: "channel", Content: "c"},
		{Name: "Team knowledge", Scope: "team", Content: "t"},
		{Name: "Workspace memory", Scope: "workspace", Content: "w"},
	}
	out := filterGraphModeLegacyMemories(in)
	names := memoryNames(out)
	for _, want := range []string{"Current user preferences", "Server member memory", "Agent global memory"} {
		if !names[want] {
			t.Errorf("filter dropped %q", want)
		}
	}
	for _, forbidden := range []string{
		"Today activity summary", "Current project memory",
		"Current channel context", "Team knowledge", "Workspace memory",
	} {
		if names[forbidden] {
			t.Errorf("filter kept forbidden %q", forbidden)
		}
	}
}

// Spec §8/§13 P0-7: a graph miss or failure injects no graph data AND no
// legacy project/channel/daily data — the task proceeds with legacy
// user/agent memory only.
func TestGraphFailureInjectsNoLegacyProjectChannelDaily(t *testing.T) {
	agentRoot := t.TempDir()
	writeAgentFile(t, agentRoot, "users/"+testMember+"/USER.md", "user prefs")
	writeAgentFile(t, agentRoot, "memory/MEMORY.md", "agent memory")
	writeAgentFile(t, agentRoot, "projects/"+testProjID+"/MEMORY.md", "legacy project memory")
	writeAgentFile(t, agentRoot, "channels/"+testChanID+"/CONTEXT.md", "legacy channel context")

	task := Task{
		WorkspaceID: testWSID, AgentID: "agent-1",
		ProjectID: testProjID, ChannelID: testChanID,
		InitiatorType: "member", InitiatorID: testMember,
	}
	// nil graphMemories == miss/error result of graphExecutionMemories.
	merged := mergeGraphModeExecutionMemory(agentRoot, task, nil, nil)
	names := memoryNames(merged)
	if !names["Current user preferences"] || !names["Agent global memory"] {
		t.Fatalf("legacy user/agent memory must survive graph failure: %v", names)
	}
	for _, forbidden := range []string{
		"Current project memory", "Current channel context", "Today activity summary", "Graph memory recall",
	} {
		if names[forbidden] {
			t.Fatalf("graph failure must not surface %q", forbidden)
		}
	}
}
