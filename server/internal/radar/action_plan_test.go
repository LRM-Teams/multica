package radar

import "testing"

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

func TestParseActionPlanRejectsUnknownAction(t *testing.T) {
	_, err := ParseActionPlan(`{"actions":[{"type":"delete_workspace"}]}`)
	if err == nil {
		t.Fatal("expected unknown action error")
	}
}
