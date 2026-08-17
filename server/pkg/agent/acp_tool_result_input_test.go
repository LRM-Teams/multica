package agent

import (
	"encoding/json"
	"testing"
)

// #103: Cursor ACP uses acpClient; completed tool_call_update must
// carry Input on MessageToolResult so Activity backfill sees path.
func TestACPToolResultCarriesStartedInput(t *testing.T) {
	t.Parallel()
	var got []Message
	c := &acpClient{
		pendingTools: make(map[string]*pendingToolCall),
		onMessage: func(m Message) {
			got = append(got, m)
		},
	}
	// start with rawInput path (write-like)
	start := json.RawMessage(`{"toolCallId":"tc-w1","title":"write_file","kind":"edit","rawInput":{"path":"/ws/a.go","contents":"x"}}`)
	c.handleToolCallStart(start)
	// completed with no rawInput (prod write shape)
	done := json.RawMessage(`{"toolCallId":"tc-w1","status":"completed","title":"write_file","content":[{"type":"content","content":{"type":"text","text":"ok"}}]}`)
	c.handleToolCallUpdate(done)

	var result *Message
	for i := range got {
		if got[i].Type == MessageToolResult {
			result = &got[i]
			break
		}
	}
	if result == nil {
		t.Fatalf("no MessageToolResult in %#v", got)
	}
	if result.Tool != "write_file" && result.Tool == "" {
		// acpToolNameFromTitle may map edit kind differently; require non-empty Input.path
	}
	if path, _ := result.Input["path"].(string); path != "/ws/a.go" {
		t.Fatalf("ToolResult.Input.path=%v full=%v tool=%q", result.Input["path"], result.Input, result.Tool)
	}
}
