package doubaodialog

import "encoding/json"

// MulticaDelegateToolName matches the existing RTC VoiceChat tool so product
// copy and agent prompts stay aligned across transports.
const MulticaDelegateToolName = "delegate_work_to_multica_agent"

const (
	multicaDelegateToolDescription = "" +
		"当用户要求创建或修改 issue、执行开发工作、安排或推进任务、检查项目状态，" +
		"或做任何需要 Multica 真实工具和权限的操作时调用。" +
		"普通闲聊、解释和无需改变项目状态的回答不要调用。"
	multicaDelegateRequestDescription = "完整保留用户要执行的任务、对象、约束和验收要求，使用中文。"

	webSearchToolDescription = "" +
		"当用户询问天气、新闻、事实、百科、实时信息或需要联网检索时调用。" +
		"不要凭记忆编造时效性答案。"
	webSearchQueryDescription = "搜索关键词，尽量具体（例如「北京 今天天气」）。"

	webFetchToolDescription = "" +
		"当已有具体 http(s) URL，需要读取页面正文以回答用户时调用。" +
		"不要抓取内网或非 http(s) 地址。"
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
	return []Tool{MulticaDelegateTool(), WebSearchTool(), WebFetchTool()}
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
		"只要用户提到创建 issue、开发任务、派活或需要真实 Multica 工具，必须先调用工具 " +
		MulticaDelegateToolName + "，不要直接口头答应而不调用。" +
		"只要用户问天气、新闻、事实、百科或任何可能过时的信息，必须先调用 " +
		WebSearchToolName + " 或 " + WebFetchToolName + "，不要凭记忆编造。" +
		"不要使用 Markdown。工具结果返回后，用一两句口语向用户播报结果。"
}
