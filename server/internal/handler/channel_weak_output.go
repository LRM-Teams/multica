package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type channelAgentTriggerContext struct {
	Reason      string
	AuthorType  string
	Content     string
	Parts       []protocol.MessagePart
	ChannelID   pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (h *Handler) shouldSuppressWeakAgentThreadPlainText(ctx context.Context, task db.AgentTaskQueue, output string) bool {
	trigger, ok := h.channelAgentTriggerContextForTask(ctx, task.ID)
	if !ok || trigger.Reason != "thread_reply" || trigger.AuthorType != "agent" {
		return false
	}
	agent, err := h.Queries.GetAgent(ctx, task.AgentID)
	if err != nil {
		return false
	}
	if h.channelAgentThreadReplyIsDirected(ctx, trigger, agent) {
		return false
	}
	return isWeakAgentThreadNonActionOutput(output)
}

func isWeakAgentThreadNonActionOutput(output string) bool {
	if isNoReplyRationaleFinalText(output, nil) {
		return true
	}
	normalized := normalizeNoReplyRationaleCandidate(output)
	if normalized == "" {
		return true
	}
	for _, phrase := range []string{
		"收到",
		"收到,无需回复",
		"收到,无需处理",
		"ack",
		"acknowledged",
		"no action",
		"no action needed",
		"nothing to do",
	} {
		if normalized == phrase {
			return true
		}
	}
	for _, marker := range []string{
		"无需回复",
		"无需进行频道回复",
		"不需要回复",
		"无需处理",
		"不需要处理",
		"无需行动",
		"无进一步操作",
		"没有新的可执行请求",
		"没有新可执行请求",
		"无事可催",
		"不刷屏",
		"不再重复",
		"话题已收敛",
		"话题中给出了",
		"结论已经",
		"当前全员正确等待",
		"no action needed",
		"no further action",
		"nothing to do",
		"nothing else to do",
		"no new actionable request",
		"no actionable request",
		"would be redundant",
		"already concluded",
		"topic is settled",
		"thread is settled",
		"avoid spam",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func (h *Handler) channelAgentTriggerContextForTask(ctx context.Context, eventID pgtype.UUID) (channelAgentTriggerContext, bool) {
	var out channelAgentTriggerContext
	var partsBytes []byte
	err := h.DB.QueryRow(ctx, `
		SELECT e.reason, m.author_type, m.content, m.parts, m.channel_id, m.workspace_id
		FROM agent_inbox_event e
		JOIN channel_message m ON m.id = e.source_message_id
		WHERE e.id = $1`, eventID).Scan(&out.Reason, &out.AuthorType, &out.Content, &partsBytes, &out.ChannelID, &out.WorkspaceID)
	if err != nil {
		// Best-effort guard: if metadata is unavailable, preserve existing behavior.
		return channelAgentTriggerContext{}, false
	}
	out.Reason = strings.TrimSpace(out.Reason)
	out.AuthorType = strings.TrimSpace(out.AuthorType)
	out.Parts = messageparts.Decode(partsBytes)
	return out, true
}

func (h *Handler) channelAgentThreadReplyIsDirected(ctx context.Context, trigger channelAgentTriggerContext, agent db.Agent) bool {
	mentions := utilParseChannelMentions(trigger.Content, trigger.Parts)
	for _, mention := range mentions {
		if mention.Type == "agent" && mention.ID == uuidToString(agent.ID) {
			return true
		}
	}
	content := normalizeDirectedCandidate(trigger.Content)
	if content == "" {
		return false
	}
	if channelMessageTargetsAllAgents(content) && channelMessageHasTaskRequest(content) {
		return true
	}
	profile := normalizeDirectedCandidate(agentDisplayName(agent) + " " + agent.Description + " " + agent.Instructions)
	if profile == "" {
		return false
	}
	if channelMessageNamesAgent(content, agent) && channelMessageHasTaskRequest(content) {
		return true
	}
	if channelMessageTargetsAgentRole(content, profile) && channelMessageHasTaskRequest(content) {
		return true
	}
	return false
}

func utilParseChannelMentions(content string, parts []protocol.MessagePart) []struct{ Type, ID string } {
	parsed := util.ParseMentionsFromContentAndParts(content, parts)
	out := make([]struct{ Type, ID string }, 0, len(parsed))
	for _, mention := range parsed {
		out = append(out, struct{ Type, ID string }{Type: mention.Type, ID: mention.ID})
	}
	return out
}

func normalizeDirectedCandidate(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "，", ",")
	s = strings.ReplaceAll(s, "。", ".")
	s = strings.ReplaceAll(s, "：", ":")
	s = strings.ReplaceAll(s, "、", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func channelMessageNamesAgent(content string, agent db.Agent) bool {
	for _, name := range []string{agent.Name, agent.DisplayName} {
		name = normalizeDirectedCandidate(name)
		if len([]rune(name)) >= 2 && strings.Contains(content, name) {
			return true
		}
	}
	return false
}

func channelMessageTargetsAllAgents(content string) bool {
	for _, marker := range []string{"所有agent", "所有 agent", "全部agent", "全部 agent", "all agents", "大家", "各位"} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func channelMessageHasTaskRequest(content string) bool {
	for _, verb := range []string{
		"看一下", "看下", "看一眼", "复核", "review", "检查", "确认", "处理", "推进", "修", "改", "做", "设计", "测试", "跑一下", "帮忙", "负责", "决策", "拍板", "给个方案", "issue", "pr",
	} {
		if strings.Contains(content, verb) {
			return true
		}
	}
	return false
}

func channelMessageTargetsAgentRole(content, profile string) bool {
	for _, role := range []string{
		"产品经理", "产品", "pm", "后端", "backend", "服务端", "server", "前端", "frontend", "client", "客户端", "设计", "designer", "ui", "ux", "测试", "qa", "运营", "文案", "增长", "数据", "架构", "reviewer", "复核", "工程师",
	} {
		if strings.Contains(content, role) && strings.Contains(profile, role) {
			return true
		}
	}
	return false
}
