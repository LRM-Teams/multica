package daemon

import (
	"encoding/json"
	"strings"

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type structuredMessageOutput struct {
	Type     string                        `json:"type"`
	Output   string                        `json:"output"`
	Content  string                        `json:"content"`
	Parts    []protocol.MessagePart        `json:"parts"`
	Reaction *protocol.ChatReactionPayload `json:"reaction"`
}

func parseStructuredMessageOutput(raw string) (string, []protocol.MessagePart, string, *protocol.ChatReactionPayload, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw, nil, "", nil, false, nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		if reaction, ok := protocol.ParseLegacyChatReactionCommand(trimmed); ok {
			return "", nil, protocol.ChatOutputKindReaction, reaction, true, nil
		}
		return raw, nil, "", nil, false, nil
	}
	var payload structuredMessageOutput
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return raw, nil, "", nil, false, nil
	}
	outputTypeText := strings.TrimSpace(strings.ToLower(payload.Type))
	if outputTypeText == "" && len(payload.Parts) == 0 && payload.Reaction == nil {
		return raw, nil, "", nil, false, nil
	}
	output := payload.Output
	if output == "" {
		output = payload.Content
	}
	outputType, err := protocol.NormalizeChatOutputType(outputTypeText, strings.TrimSpace(output) != "" || len(payload.Parts) > 0, payload.Reaction != nil)
	if err != nil {
		return raw, nil, "", nil, true, err
	}
	switch outputType {
	case protocol.ChatOutputKindNoReply:
		return "", nil, outputType, nil, true, nil
	case protocol.ChatOutputKindReaction:
		if payload.Reaction == nil || strings.TrimSpace(payload.Reaction.Emoji) == "" {
			return raw, nil, "", nil, true, protocol.ErrInvalidChatOutputType(outputType)
		}
		return "", nil, protocol.ChatOutputKindReaction, payload.Reaction, true, nil
	}
	content, parts, err := messageparts.Normalize(output, payload.Parts)
	if err != nil {
		return raw, nil, "", nil, true, err
	}
	return content, parts, protocol.ChatOutputKindMessage, nil, true, nil
}
