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
