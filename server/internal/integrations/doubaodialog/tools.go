package doubaodialog

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// MulticaDelegateToolName matches the existing RTC VoiceChat tool so product
// copy and agent prompts stay aligned across transports.
const MulticaDelegateToolName = "delegate_work_to_multica_agent"
const MulticaChannelContextToolName = "multica_channel_context"

const (
	multicaDelegateToolDescription = "" +
		"当用户要求创建或修改 issue、执行开发工作、安排或推进任务、检查项目状态，" +
		"或做任何需要 Multica 真实工具和权限的操作时调用。" +
		"普通闲聊、解释和无需改变项目状态的回答不要调用。"
	multicaDelegateRequestDescription = "完整保留用户要执行的任务、对象、约束和验收要求，使用中文。"

	webSearchToolDescription = "" +
		"仅当用户本轮明确要求查询天气、新闻、实时事实、百科或联网信息时调用。" +
		"问候、闲聊、寒暄、确认、闲扯不要调用；不要在通话刚开始时主动搜索。" +
		"不要凭记忆编造时效性答案。"
	webSearchQueryDescription = "搜索关键词，尽量具体（例如「北京 今天天气」）。"

	webFetchToolDescription = "" +
		"仅当用户给出具体 http(s) URL，或搜索后需要打开某一页正文时调用。" +
		"不要抓取内网或非 http(s) 地址；不要在闲聊时主动抓取。"
	webFetchURLDescription = "要抓取的完整 http 或 https URL。"
)

// MulticaDelegateTool returns the session.tools entry for Multica work.
func MulticaDelegateTool() Tool {
	parameters := json.RawMessage(`{` +
		`"type":"object",` +
		`"properties":{` +
		`"request":{"type":"string","description":` + jsonString(multicaDelegateRequestDescription) + `}` +
		`},` +
		`"required":["request"],` +
		`"additionalProperties":false` +
		`}`)
	return Tool{
		Type:        "function",
		Name:        MulticaDelegateToolName,
		Description: multicaDelegateToolDescription,
		Parameters:  parameters,
	}
}

// WebSearchTool returns the Duplex session.tools entry for factual search.
func WebSearchTool() Tool {
	parameters := json.RawMessage(`{` +
		`"type":"object",` +
		`"properties":{` +
		`"query":{"type":"string","description":` + jsonString(webSearchQueryDescription) + `}` +
		`},` +
		`"required":["query"],` +
		`"additionalProperties":false` +
		`}`)
	return Tool{
		Type:        "function",
		Name:        WebSearchToolName,
		Description: webSearchToolDescription,
		Parameters:  parameters,
	}
}

// WebFetchTool returns the Duplex session.tools entry for URL fetch.
func WebFetchTool() Tool {
	parameters := json.RawMessage(`{` +
		`"type":"object",` +
		`"properties":{` +
		`"url":{"type":"string","description":` + jsonString(webFetchURLDescription) + `}` +
		`},` +
		`"required":["url"],` +
		`"additionalProperties":false` +
		`}`)
	return Tool{
		Type:        "function",
		Name:        WebFetchToolName,
		Description: webFetchToolDescription,
		Parameters:  parameters,
	}
}

// DefaultDialogTools is the production Duplex tool set: Multica delegate + web lookup.
func DefaultDialogTools() []Tool {
	return []Tool{MulticaDelegateTool(), MulticaChannelContextTool(), WebSearchTool(), WebFetchTool()}
}

func MulticaChannelContextTool() Tool {
	return Tool{
		Type:        "function",
		Name:        MulticaChannelContextToolName,
		Description: "查询当前被叫 Agent 有权访问的群聊。用户询问相关群、群内讨论或历史消息时调用；服务端会重新校验成员权限。",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","read","search"]},"channel_id":{"type":"string"},"query":{"type":"string"}},"required":["action"],"additionalProperties":false}`),
	}
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// DefaultDialogInstructions is the spoken-system prompt for Duplex product calls.
func DefaultDialogInstructions() string {
	return "" +
		"你是 Multica 语音助手。用自然口语简短回答。" +
		"接通后先正常闲聊寒暄，不要一开口就调用任何工具。" +
		"只有用户本轮明确要求查询天气、新闻、实时信息，或给出网址要你打开时，才调用 " +
		WebSearchToolName + " 或 " + WebFetchToolName +
		"；问候、闲聊、确认不要搜索。调用搜索前先口头说一句「我帮你查一下」。" +
		"调用 " + WebSearchToolName + " / " + WebFetchToolName + " 时继续保持通话可听可说，不要假装静音卡住。" +
		"只有用户明确要求创建 issue、开发任务、派活或需要真实 Multica 工具时，才调用 " +
		MulticaDelegateToolName +
		"。派活后立刻告诉用户已安排后台执行、可以继续聊天，不要假装卡住等待。" +
		"当用户询问 Agent 参加的群聊、群内讨论或历史消息时，调用 " + MulticaChannelContextToolName +
		"；不能仅凭启动上下文中的群名猜测群消息内容。" +
		"不要使用 Markdown。工具结果返回后，用一两句口语向用户播报。"
}

const (
	dialogContextPreamble = "" +
		"以下是当前被叫 Agent 的身份与近期对话/任务上下文。" +
		"接通后必须据此回答，不要以空白会话开场装作什么都不知道。"

	// Keep composed instructions within a conservative Duplex prompt budget.
	maxDialogInstructionsRunes = 24000
)

// ComposeDialogInstructions joins product spoken rules with the Agent-scoped
// Multica context built for the voice call (identity, recent DM, issues, …).
func ComposeDialogInstructions(base string, systemMessages []string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = DefaultDialogInstructions()
	}
	parts := make([]string, 0, 2+len(systemMessages))
	parts = append(parts, base)
	contextParts := make([]string, 0, len(systemMessages))
	for _, message := range systemMessages {
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		contextParts = append(contextParts, message)
	}
	if len(contextParts) > 0 {
		parts = append(parts, dialogContextPreamble)
		parts = append(parts, contextParts...)
	}
	joined := strings.Join(parts, "\n\n")
	if utf8.RuneCountInString(joined) <= maxDialogInstructionsRunes {
		return joined
	}
	runes := []rune(joined)
	trimmed := string(runes[:maxDialogInstructionsRunes])
	return trimmed + "\n...[duplex context truncated by Multica]..."
}
