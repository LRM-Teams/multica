package protocol

import "testing"

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
