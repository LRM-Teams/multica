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

func TestPlanAgentASCIIHandleBackfillRepairsTruncatedTrailingSeparator(t *testing.T) {
	updates, err := planAgentASCIIHandleBackfill([]agentHandleBackfillRow{
		{
			ID:          "1",
			WorkspaceID: "workspace",
			Name:        "ai-fa-qi-tuan-dui-chan-pin-jing-",
			DisplayName: "AI发起团队产品经理agent",
		},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got, want := updates["1"], "ai-fa-qi-tuan-dui-chan-pin-jing"; got != want {
		t.Fatalf("update[1] = %q, want %q", got, want)
	}
}

func TestPlanAgentDefaultHandleRepair(t *testing.T) {
	updates, err := planAgentDefaultHandleRepair([]agentHandleBackfillRow{
		{ID: "1", WorkspaceID: "workspace", Name: "hou-duan-gong-cheng-shi", DisplayName: "后端工程师"},
		{ID: "2", WorkspaceID: "workspace", Name: "actor", DisplayName: "后端工程师"},
		{ID: "3", WorkspaceID: "workspace", Name: "agent", DisplayName: "产品策略官"},
		{ID: "4", WorkspaceID: "workspace", Name: "custom-name", DisplayName: "Custom Name"},
		{ID: "5", WorkspaceID: "other", Name: "actor", DisplayName: "QA Bot"},
		{ID: "6", WorkspaceID: "fallbacks", Name: "actor", DisplayName: "actor"},
		{ID: "7", WorkspaceID: "fallbacks", Name: "agent", DisplayName: "Agent"},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	want := map[string]string{
		"2": "hou-duan-gong-cheng-shi-2",
		"3": "chan-pin-ce-lve-guan",
		"5": "qa-bot",
		"6": "actor-2",
		"7": "agent-2",
	}
	if len(updates) != len(want) {
		t.Fatalf("updates = %#v, want %#v", updates, want)
	}
	for id, handle := range want {
		if updates[id] != handle {
			t.Fatalf("update[%s] = %q, want %q", id, updates[id], handle)
		}
		if isHistoricDefaultHandle(updates[id]) {
			t.Fatalf("update[%s] remained a historic default: %#v", id, updates)
		}
	}
	if _, ok := updates["4"]; ok {
		t.Fatalf("valid user-chosen handle was unexpectedly changed: %#v", updates)
	}
}
