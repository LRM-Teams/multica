package radar

import (
	"strings"
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

func TestBuildAmbientChannelPromptCoversCoordinationActions(t *testing.T) {
	prompt := BuildAmbientChannelPrompt("## Channel\n\n- channel_id=abc")
	for _, want := range []string{
		"manager of ONE group channel",
		"no_action",
		"mention_agent",
		"create_issue",
		`"project_id":"<project uuid from Project; omit only when the channel is unbound>"`,
		"post_channel_message",
		"untrusted evidence",
		"Return at most 5 actions",
		// Naming in prose does not wake an agent — must use mention_agent.
		"mention_agent action targeting that agent",
		"server adds the one target mention",
		"must not repeat that target by @handle or display name",
		// Spec-driven review (审): production standard, evidence-based, playable≠done.
		"spec-driven review",
		"not a playable demo",
		"acceptance criteria",
		"based on evidence",
		// Visual/UI审 leverages image reading: review the screenshot vs reference.
		"review the actual screenshot",
		// 对话→issue→开发: requirements become issues; execution builds from issues.
		"Requirements go through issues, not chat",
		// On shortfall: diagnose spec-wrong vs impl-wrong; owner owns criteria.
		"diagnose spec-wrong vs implementation-wrong",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("ambient prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildIdleNudgeChannelPromptDrivesWork(t *testing.T) {
	prompt := BuildIdleNudgeChannelPrompt("## Channel\n\n- channel_id=abc")
	for _, want := range []string{
		"NO agent in this group is working",
		"mention_agent", // must use mention_agent to wake
		"产品经理",          // fall back to the product manager
		"break the final goal into concrete issues",
		// silence only when the whole goal is genuinely complete
		"no_action ONLY if the entire goal is genuinely complete",
		"nudged_without_progress",
		"ask the specific blocker",
		"reassign the work",
		"escalate to a human",
		"full identifier from its current Open Issues row",
		"dynamically supplied for this review",
		"bare `#number`",
		"Keep mention_agent payload content to the instruction only",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("idle-nudge prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Reference the issue by #number") {
		t.Fatalf("idle-nudge prompt still teaches bare issue numbers:\n%s", prompt)
	}
}
