package daemon

import (
	"encoding/json"
	"strings"

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type structuredMessageOutput struct {
	Output  string                 `json:"output"`
	Content string                 `json:"content"`
	Parts   []protocol.MessagePart `json:"parts"`
}

func parseStructuredMessageOutput(raw string) (string, []protocol.MessagePart, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return raw, nil, false, nil
	}
	var payload structuredMessageOutput
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return raw, nil, false, nil
	}
	if len(payload.Parts) == 0 {
		return raw, nil, false, nil
	}
	output := payload.Output
	if output == "" {
		output = payload.Content
	}
	content, parts, err := messageparts.Normalize(output, payload.Parts)
	if err != nil {
		return raw, nil, true, err
	}
	return content, parts, true, nil
}
