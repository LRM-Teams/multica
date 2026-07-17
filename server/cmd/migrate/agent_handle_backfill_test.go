package main

import "testing"

func TestPlanAgentASCIIHandleBackfill(t *testing.T) {
	updates, err := planAgentASCIIHandleBackfill([]agentHandleBackfillRow{
		{ID: "1", WorkspaceID: "workspace", Name: "actor_14", DisplayName: "后端工程师"},
		{ID: "2", WorkspaceID: "workspace", Name: "agent_xxx", DisplayName: "后端工程师"},
		{ID: "3", WorkspaceID: "workspace", Name: "qa-bot", DisplayName: "QA Bot"},
		{ID: "4", WorkspaceID: "workspace", Name: "agent_qa", DisplayName: "QA Bot"},
		{ID: "5", WorkspaceID: "other", Name: "actor_1", DisplayName: "QA Bot"},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	want := map[string]string{
		"1": "hou-duan-gong-cheng-shi",
		"2": "hou-duan-gong-cheng-shi-2",
		"4": "qa-bot-2",
		"5": "qa-bot",
	}
	if len(updates) != len(want) {
		t.Fatalf("updates = %#v, want %#v", updates, want)
	}
	for id, handle := range want {
		if updates[id] != handle {
			t.Fatalf("update[%s] = %q, want %q", id, updates[id], handle)
		}
	}
	if _, ok := updates["3"]; ok {
		t.Fatalf("valid current handle was unexpectedly changed: %#v", updates)
	}
}
