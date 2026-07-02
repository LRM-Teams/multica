package daemon

import (
	"encoding/json"
	"strings"

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type structuredMessageOutput struct {
	Action   string                        `json:"action"`
	Type     string                        `json:"type"`
	Output   string                        `json:"output"`
	Content  string                        `json:"content"`
	Parts    []protocol.MessagePart        `json:"parts"`
	Reaction *protocol.ChatReactionPayload `json:"reaction"`
}

func parseStructuredMessageOutput(raw string) (string, []protocol.MessagePart, string, string, *protocol.ChatReactionPayload, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw, nil, "", "", nil, false, nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		if message, ok := protocol.ParseChannelSendCommand(trimmed); ok {
			content, parts, err := messageparts.Normalize(message, nil)
			if err != nil {
				return raw, nil, "", "", nil, true, err
			}
			return content, parts, protocol.ChatOutputKindMessage, protocol.ChatOutputActionSendChannelMessage, nil, true, nil
		}
		if reaction, ok := protocol.ParseMessageReactCommand(trimmed); ok {
			return "", nil, protocol.ChatOutputKindReaction, protocol.ChatOutputActionMessageReact, reaction, true, nil
		}
		return raw, nil, "", "", nil, false, nil
	}
	var payload structuredMessageOutput
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return raw, nil, "", "", nil, false, nil
	}
	action, err := protocol.NormalizeChatOutputAction(payload.Action)
	if err != nil {
		return raw, nil, "", "", nil, true, err
	}
	outputTypeText := strings.TrimSpace(strings.ToLower(payload.Type))
	if action == "" && outputTypeText == "" && len(payload.Parts) == 0 && payload.Reaction == nil {
		return raw, nil, "", "", nil, false, nil
	}
	output := payload.Output
	if output == "" {
		output = payload.Content
	}
	outputType := ""
	if action != "" {
		outputType, err = protocol.ChatOutputTypeForAction(action)
	} else {
		outputType, err = protocol.NormalizeChatOutputType(outputTypeText, strings.TrimSpace(output) != "" || len(payload.Parts) > 0, payload.Reaction != nil)
		if err == nil && outputType == protocol.ChatOutputKindReaction {
			action = protocol.ChatOutputActionMessageReact
		} else if err == nil && outputType == protocol.ChatOutputKindMessage {
			action = protocol.ChatOutputActionSendChannelMessage
		} else if err == nil && outputType == protocol.ChatOutputKindNoReply {
			action = protocol.ChatOutputActionNoReply
		}
	}
	if err != nil {
		return raw, nil, "", "", nil, true, err
	}
	switch outputType {
	case protocol.ChatOutputKindNoReply:
		return "", nil, outputType, action, nil, true, nil
	case protocol.ChatOutputKindReaction:
		if payload.Reaction == nil || strings.TrimSpace(payload.Reaction.Emoji) == "" {
			return raw, nil, "", "", nil, true, protocol.ErrInvalidChatOutputType(outputType)
		}
		return "", nil, protocol.ChatOutputKindReaction, action, payload.Reaction, true, nil
	}
	content, parts, err := messageparts.Normalize(output, payload.Parts)
	if err != nil {
		return raw, nil, "", "", nil, true, err
	}
	return content, parts, protocol.ChatOutputKindMessage, action, nil, true, nil
}
