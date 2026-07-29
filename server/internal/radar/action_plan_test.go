package radar

import (
	"testing"
)

func TestParseActionPlanFromJSONFence(t *testing.T) {
	plan, err := ParseActionPlan("```json\n{\"summary\":\"found risk\",\"actions\":[{\"type\":\"post_channel_message\",\"reason\":\"CI failed\",\"evidence\":[\"task:abc\"],\"confidence\":\"high\",\"risk_level\":\"low\",\"dedupe_key\":\"ci-failed\"}]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary != "found risk" {
		t.Fatalf("summary = %q", plan.Summary)
	}
	if len(plan.Actions) != 1 {
		t.Fatalf("actions len = %d", len(plan.Actions))
	}
	action := plan.Actions[0]
	if action.Type != ActionPostChannelMessage || action.Confidence != "high" || action.RiskLevel != "low" {
		t.Fatalf("unexpected action: %+v", action)
	}
}

func TestParseActionPlanFromChinesePreamble(t *testing.T) {
	raw := "近窗是打招呼闲聊；主线已收口，无需再派活。{\"summary\":\"chatter only\",\"actions\":[{\"type\":\"no_action\",\"reason\":\"no coordination needed\",\"evidence\":[\"message:abc\"],\"confidence\":\"high\",\"risk_level\":\"low\",\"dedupe_key\":\"radar-no-action\",\"target_kind\":\"none\",\"target_id\":\"\",\"payload\":{}}]}"
	plan, err := ParseActionPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary != "chatter only" {
		t.Fatalf("summary = %q", plan.Summary)
	}
	if len(plan.Actions) != 1 || plan.Actions[0].Type != ActionNoAction {
		t.Fatalf("unexpected actions: %+v", plan.Actions)
	}
}

func TestParseActionPlanFromPreambleAndFence(t *testing.T) {
	raw := "正在收集证据。\n```json\n{\"summary\":\"ok\",\"actions\":[{\"type\":\"no_action\",\"reason\":\"quiet\",\"evidence\":[],\"confidence\":\"medium\",\"risk_level\":\"low\",\"dedupe_key\":\"k\"}]}\n```"
	plan, err := ParseActionPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary != "ok" || len(plan.Actions) != 1 || plan.Actions[0].Type != ActionNoAction {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestParseActionPlanRejectsUnknownAction(t *testing.T) {
	_, err := ParseActionPlan(`{"actions":[{"type":"delete_workspace"}]}`)
	if err == nil {
		t.Fatal("expected unknown action error")
	}
}

func TestParseActionPlanRejectsDeclaredButUnexecutableActions(t *testing.T) {
	for _, actionType := range []string{ActionAssignIssue, ActionScheduleReminder} {
		_, err := ParseActionPlan(`{"actions":[{"type":"` + actionType + `"}]}`)
		if err == nil {
			t.Fatalf("expected unexecutable action %q to be rejected", actionType)
		}
	}
}

func TestParseActionPlanAcceptsRequestRework(t *testing.T) {
	plan, err := ParseActionPlan(`{"actions":[{"type":"request_rework","payload":{"issue_id":"00000000-0000-0000-0000-000000000001","target_agent_id":"00000000-0000-0000-0000-000000000002","content":"match the approved visual reference"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 1 || plan.Actions[0].Type != ActionRequestRework {
		t.Fatalf("unexpected actions: %+v", plan.Actions)
	}
}
