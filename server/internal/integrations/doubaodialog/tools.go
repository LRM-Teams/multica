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

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// DefaultDialogInstructions is the spoken-system prompt for the Spike demo.
func DefaultDialogInstructions() string {
	return "" +
		"你是 Multica 语音助手。用自然口语简短回答。" +
		"只要用户提到创建 issue、开发任务、派活或需要真实工具，必须先调用工具 " +
		MulticaDelegateToolName + "，不要直接口头答应而不调用。" +
		"不要使用 Markdown。工具结果返回后，用一句话向用户播报结果。"
}
