package radar

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	ActionNoAction           = "no_action"
	ActionPostChannelMessage = "post_channel_message"
	ActionReplyThread        = "reply_thread"
	ActionMentionAgent       = "mention_agent"
	ActionCreateIssue        = "create_issue"
	ActionCommentIssue       = "comment_issue"
	ActionAssignIssue        = "assign_issue"
	ActionScheduleReminder   = "schedule_reminder"
	ActionUpdateAgentPlan    = "update_agent_plan"
)

var actionPlanFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

type ActionPlan struct {
	Summary string        `json:"summary"`
	Actions []RadarAction `json:"actions"`
}

type RadarAction struct {
	Type       string          `json:"type"`
	Reason     string          `json:"reason"`
	Evidence   []string        `json:"evidence"`
	Confidence string          `json:"confidence"`
	RiskLevel  string          `json:"risk_level"`
	DedupeKey  string          `json:"dedupe_key"`
	TargetKind string          `json:"target_kind"`
	TargetID   string          `json:"target_id"`
	Payload    json.RawMessage `json:"payload"`
}

func BuildPrompt(ctx Context) string {
	var b strings.Builder
	b.WriteString("You are running an Agent Radar check for Multica.\n")
	b.WriteString("Read your Agent Plan, memory, project context, code signals, issues, tasks, and channel discussion below.\n")
	b.WriteString("Decide whether there is a meaningful initiative to take now. If there is no new evidence or useful next step, return no_action.\n")
	b.WriteString("Return ONLY JSON with this shape:\n")
	b.WriteString(`{"summary":"...","actions":[{"type":"no_action|post_channel_message|reply_thread|mention_agent|create_issue|comment_issue|assign_issue|schedule_reminder|update_agent_plan","reason":"...","evidence":["kind:id"],"confidence":"low|medium|high","risk_level":"low|medium|high","dedupe_key":"stable-key","target_kind":"none|channel|thread|issue|agent|reminder|plan","target_id":"","payload":{}}]}`)
	b.WriteString("\n\n")
	b.WriteString(ctx.Markdown)
	return b.String()
}

func ParseActionPlan(raw string) (ActionPlan, error) {
	body := strings.TrimSpace(raw)
	if match := actionPlanFenceRe.FindStringSubmatch(body); len(match) == 2 {
		body = strings.TrimSpace(match[1])
	}
	var plan ActionPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		return ActionPlan{}, err
	}
	for i := range plan.Actions {
		action := &plan.Actions[i]
		if !validActionType(action.Type) {
			return ActionPlan{}, fmt.Errorf("unknown radar action type %q", action.Type)
		}
		if action.Confidence == "" {
			action.Confidence = "medium"
		}
		if action.RiskLevel == "" {
			action.RiskLevel = "low"
		}
		if action.TargetKind == "" {
			action.TargetKind = "none"
		}
		if action.Payload == nil {
			action.Payload = json.RawMessage(`{}`)
		}
	}
	return plan, nil
}

func validActionType(t string) bool {
	switch t {
	case ActionNoAction, ActionPostChannelMessage, ActionReplyThread, ActionMentionAgent, ActionCreateIssue, ActionCommentIssue, ActionAssignIssue, ActionScheduleReminder, ActionUpdateAgentPlan:
		return true
	default:
		return false
	}
}
