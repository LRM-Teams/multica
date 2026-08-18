package protocol

import (
	"encoding/json"
	"testing"
)

func TestAgentWorkspaceFileProtocolMatchesRaft(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{got: EventAgentWorkspaceList, want: "agent:workspace:list"},
		{got: EventAgentWorkspaceFileTree, want: "agent:workspace:file_tree"},
		{got: EventAgentWorkspaceRead, want: "agent:workspace:read"},
		{got: EventAgentWorkspaceFileContent, want: "agent:workspace:file_content"},
	}
	for _, test := range tests {
		if test.got != test.want {
			t.Errorf("event = %q, want %q", test.got, test.want)
		}
	}
}

func TestAgentSkillsProtocolMatchesRaft(t *testing.T) {
	if EventAgentSkillsList != "agent:skills:list" || EventAgentSkillsListResult != "agent:skills:list_result" {
		t.Fatalf("skills events do not match Raft")
	}
	raw, err := json.Marshal(AgentSkillsListPayload{AgentID: "agent-1", Runtime: "runtime-1", RequestID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"agentId":"agent-1","runtime":"runtime-1","requestId":"req-1"}` {
		t.Fatalf("request fields = %s", raw)
	}
	raw, err = json.Marshal(AgentSkillsListResultPayload{
		AgentID: "agent-1", RequestID: "req-1",
		Global:    []AgentSkillSummary{{Name: "global", Path: "/global", Source: "global"}},
		Workspace: []AgentSkillSummary{{Name: "workspace", Path: "/workspace", Source: "workspace"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"agentId":"agent-1","requestId":"req-1","global":[{"name":"global","description":"","path":"/global","source":"global"}],"workspace":[{"name":"workspace","description":"","path":"/workspace","source":"workspace"}]}` {
		t.Fatalf("result fields = %s", raw)
	}
}
