package execenv

import "testing"

func TestStartupStaticDigestStableAcrossPerTurnFields(t *testing.T) {
	base := TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent A",
		AgentInstructions: "be careful",
		ManagedRole:       "group_manager",
		ProjectID:         "proj-1",
		AgentSkills:       []SkillContextForEnv{{Name: "s1", Content: "body"}},
		AgentMemories:     []MemoryContextForEnv{{Name: "m1", Content: "note"}},
	}
	a := base
	a.ChatSessionID = "chat-1"
	a.InitiatorName = "Alice"
	a.InitiatorID = "member-a"
	a.IssueID = "issue-A"
	b := base
	b.ChatSessionID = "chat-1"
	b.InitiatorName = "Bob"
	b.InitiatorID = "member-b"
	b.IssueID = "issue-B"
	if StartupStaticDigest(a) != StartupStaticDigest(b) {
		t.Fatal("per-turn initiator/issue must not change startup static digest")
	}
	c := base
	c.AgentMemories = []MemoryContextForEnv{{Name: "m1", Content: "note-updated"}}
	if StartupStaticDigest(a) == StartupStaticDigest(c) {
		t.Fatal("memory content change must change startup static digest")
	}
}
