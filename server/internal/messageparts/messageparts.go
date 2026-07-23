package messageparts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/stickers"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const BuiltinStickerPackID = "builtin"

var emptyHTMLPlaceholderRE = regexp.MustCompile(`(?i)^(?:\s|&nbsp;|&#160;|\x{00a0}|\x{200b}|<br\s*/?>)+$`)

type structuredVisibleMessage struct {
	Action   string                 `json:"action"`
	Type     string                 `json:"type"`
	Output   string                 `json:"output"`
	Content  string                 `json:"content"`
	Parts    []protocol.MessagePart `json:"parts"`
	Reaction any                    `json:"reaction"`
}

func Normalize(content string, parts []protocol.MessagePart) (string, []protocol.MessagePart, error) {
	normalizedContent := strings.TrimSpace(content)
	if emptyHTMLPlaceholderRE.MatchString(normalizedContent) {
		normalizedContent = ""
	}
	if len(parts) == 0 {
		return normalizedContent, nil, nil
	}
	out := make([]protocol.MessagePart, 0, len(parts))
	for i, part := range parts {
		normalized, err := normalizePart(part)
		if err != nil {
			return "", nil, fmt.Errorf("part %d: %w", i, err)
		}
		out = append(out, normalized)
	}
	if normalizedContent == "" {
		normalizedContent = FallbackContent(out)
	}
	if hasVoicePart(out) {
		if !hasTextPart(out) {
			return "", nil, fmt.Errorf("voice transcript text part is required")
		}
		if utf8.RuneCountInString(normalizedContent) > protocol.VoiceTranscriptMaxRunes {
			return "", nil, fmt.Errorf("voice transcript exceeds %d characters", protocol.VoiceTranscriptMaxRunes)
		}
	}
	return normalizedContent, out, nil
}

func hasVoicePart(parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeVoice {
			return true
		}
	}
	return false
}

func hasTextPart(parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeText && strings.TrimSpace(part.Text) != "" {
			return true
		}
	}
	return false
}

// UnwrapStructuredMessageSend recovers visible text/parts from an agent action
// JSON payload that accidentally reached a message display or persistence path.
func UnwrapStructuredMessageSend(content string, parts []protocol.MessagePart) (string, []protocol.MessagePart, bool, error) {
	if len(parts) > 0 {
		return content, parts, false, nil
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return content, parts, false, nil
	}
	var payload structuredVisibleMessage
	embedded := false
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return content, parts, false, nil
		}
	} else {
		var ok bool
		payload, ok = findEmbeddedStructuredMessageSend(trimmed)
		if !ok {
			return content, parts, false, nil
		}
		embedded = true
	}
	if strings.TrimSpace(payload.Action) == "" &&
		strings.TrimSpace(payload.Type) == "" &&
		len(payload.Parts) == 0 &&
		payload.Reaction == nil {
		return content, parts, false, nil
	}
	if strings.TrimSpace(payload.Action) != "" {
		action, err := protocol.NormalizeChatOutputAction(payload.Action)
		if err != nil {
			return content, parts, true, err
		}
		if action != protocol.ChatOutputActionMessageSend {
			return content, parts, false, nil
		}
		if embedded && len(payload.Parts) == 0 {
			return content, parts, false, nil
		}
	} else {
		outputType, err := protocol.NormalizeChatOutputType(payload.Type, strings.TrimSpace(payload.Output) != "" || strings.TrimSpace(payload.Content) != "" || len(payload.Parts) > 0, payload.Reaction != nil)
		if err != nil {
			return content, parts, true, err
		}
		if outputType != protocol.ChatOutputKindMessage {
			return content, parts, false, nil
		}
		if embedded {
			return content, parts, false, nil
		}
	}
	output := payload.Output
	if output == "" {
		output = payload.Content
	}
	normalizedContent, normalizedParts, err := Normalize(output, payload.Parts)
	if err != nil {
		return content, parts, true, err
	}
	return normalizedContent, normalizedParts, true, nil
}

func findEmbeddedStructuredMessageSend(content string) (structuredVisibleMessage, bool) {
	for start := 0; start < len(content); {
		idx := strings.Index(content[start:], "{")
		if idx < 0 {
			return structuredVisibleMessage{}, false
		}
		idx += start
		end := matchingJSONObjectEnd(content, idx)
		if end < 0 {
			start = idx + 1
			continue
		}
		var payload structuredVisibleMessage
		if err := json.Unmarshal([]byte(content[idx:end]), &payload); err == nil {
			if strings.TrimSpace(payload.Action) != "" && len(payload.Parts) > 0 {
				return payload, true
			}
		}
		start = idx + 1
	}
	return structuredVisibleMessage{}, false
}

func matchingJSONObjectEnd(content string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(content); i++ {
		ch := content[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

func MustJSON(parts []protocol.MessagePart) []byte {
	if len(parts) == 0 {
		return []byte("[]")
	}
	body, err := json.Marshal(parts)
	if err != nil {
		return []byte("[]")
	}
	return body
}

func Decode(raw []byte) []protocol.MessagePart {
	if len(raw) == 0 {
		return nil
	}
	var parts []protocol.MessagePart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	return parts
}

func FallbackContent(parts []protocol.MessagePart) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case protocol.MessagePartTypeText:
			text := strings.TrimSpace(part.Text)
			if text != "" {
				values = append(values, text)
			}
		case protocol.MessagePartTypeSticker:
			label := strings.TrimSpace(part.Alt)
			if label == "" {
				label = "[Sticker]"
			}
			values = append(values, label)
		case protocol.MessagePartTypeAttachment:
			// Attachment-only messages may have empty content. Do not invent
			// markdown URLs or synthetic labels from attachment metadata.
		case protocol.MessagePartTypeReference:
			// Reference parts are routing/link metadata. Visible text remains in
			// content/text so clients that do not render refs yet still show what
			// the sender wrote.
		}
	}
	return strings.TrimSpace(strings.Join(values, " "))
}

func normalizePart(part protocol.MessagePart) (protocol.MessagePart, error) {
	part.Type = strings.TrimSpace(part.Type)
	switch part.Type {
	case protocol.MessagePartTypeText:
		part.Text = strings.TrimSpace(part.Text)
		if part.Text == "" {
			return protocol.MessagePart{}, fmt.Errorf("text is required")
		}
		part.RefType = ""
		part.RefSubType = ""
		part.RefID = ""
		part.Label = ""
		part.Event = ""
		part.EventParams = nil
		part.ContentStartUTF16 = nil
		part.ContentEndUTF16 = nil
		part.PackID = ""
		part.StickerID = ""
		part.Alt = ""
		part.AttachmentID = ""
		part.Filename = ""
		part.ContentType = ""
		part.SizeBytes = 0
		part.DurationMS = 0
		return part, nil
	case protocol.MessagePartTypeReference:
		part.RefType = strings.TrimSpace(part.RefType)
		part.RefSubType = strings.TrimSpace(part.RefSubType)
		part.RefID = strings.TrimSpace(part.RefID)
		part.Label = strings.TrimSpace(part.Label)
		part.Event = ""
		part.EventParams = nil
		// Source ranges are server-authored enrichment facts. Callers can submit
		// reference metadata, but may not choose an arbitrary position in the
		// visible content; channel enrichment attaches verified spans later.
		part.ContentStartUTF16 = nil
		part.ContentEndUTF16 = nil
		if part.RefType == "" {
			return protocol.MessagePart{}, fmt.Errorf("ref_type is required")
		}
		if part.RefID == "" {
			return protocol.MessagePart{}, fmt.Errorf("ref_id is required")
		}
		switch part.RefType {
		case "mention":
			if part.RefSubType == "" {
				return protocol.MessagePart{}, fmt.Errorf("ref_subtype is required for mention reference")
			}
			switch part.RefSubType {
			case "member", "agent", "squad":
			default:
				return protocol.MessagePart{}, fmt.Errorf("unsupported mention ref_subtype %q", part.RefSubType)
			}
		case "issue-ref":
			if part.RefSubType == "" {
				part.RefSubType = "issue"
			}
			if part.RefSubType != "issue" {
				return protocol.MessagePart{}, fmt.Errorf("unsupported issue-ref ref_subtype %q", part.RefSubType)
			}
		default:
			return protocol.MessagePart{}, fmt.Errorf("unsupported ref_type %q", part.RefType)
		}
		part.Text = ""
		part.PackID = ""
		part.StickerID = ""
		part.Alt = ""
		part.AttachmentID = ""
		part.Filename = ""
		part.ContentType = ""
		part.SizeBytes = 0
		part.DurationMS = 0
		return part, nil
	case protocol.MessagePartTypeSticker:
		part.Text = ""
		part.RefType = ""
		part.RefSubType = ""
		part.RefID = ""
		part.Label = ""
		part.Event = ""
		part.EventParams = nil
		part.ContentStartUTF16 = nil
		part.ContentEndUTF16 = nil
		part.AttachmentID = ""
		part.Filename = ""
		part.ContentType = ""
		part.SizeBytes = 0
		part.DurationMS = 0
		part.PackID = strings.TrimSpace(part.PackID)
		if part.PackID == "" {
			part.PackID = BuiltinStickerPackID
		}
		if part.PackID != BuiltinStickerPackID {
			return protocol.MessagePart{}, fmt.Errorf("unsupported sticker pack %q", part.PackID)
		}
		part.StickerID = strings.TrimSpace(part.StickerID)
		sticker, ok := stickers.Get(part.StickerID)
		if !ok {
			return protocol.MessagePart{}, fmt.Errorf("unknown sticker_id %q", part.StickerID)
		}
		if strings.TrimSpace(part.Alt) == "" {
			part.Alt = firstNonEmpty(sticker.Name, sticker.NameEn, sticker.ID)
		} else {
			part.Alt = strings.TrimSpace(part.Alt)
		}
		return part, nil
	case protocol.MessagePartTypeSystemEvent:
		part.Event = strings.TrimSpace(part.Event)
		if part.Event == "" {
			return protocol.MessagePart{}, fmt.Errorf("event is required")
		}
		var params map[string]any
		if len(part.EventParams) == 0 || json.Unmarshal(part.EventParams, &params) != nil {
			return protocol.MessagePart{}, fmt.Errorf("event_params must be an object")
		}
		part.Text = ""
		part.RefType = ""
		part.RefSubType = ""
		part.RefID = ""
		part.Label = ""
		part.ContentStartUTF16 = nil
		part.ContentEndUTF16 = nil
		part.PackID = ""
		part.StickerID = ""
		part.Alt = ""
		part.AttachmentID = ""
		part.Filename = ""
		part.ContentType = ""
		part.SizeBytes = 0
		part.DurationMS = 0
		return part, nil
	case protocol.MessagePartTypeAttachment:
		part.AttachmentID = strings.TrimSpace(part.AttachmentID)
		if part.AttachmentID == "" {
			return protocol.MessagePart{}, fmt.Errorf("attachment_id is required")
		}
		part.Text = ""
		part.RefType = ""
		part.RefSubType = ""
		part.RefID = ""
		part.Label = ""
		part.Event = ""
		part.EventParams = nil
		part.ContentStartUTF16 = nil
		part.ContentEndUTF16 = nil
		part.PackID = ""
		part.StickerID = ""
		part.Alt = ""
		part.DurationMS = 0
		part.Filename = strings.TrimSpace(part.Filename)
		part.ContentType = strings.TrimSpace(part.ContentType)
		// SizeBytes is optional; keep as provided.
		return part, nil
	case protocol.MessagePartTypeVoice:
		if part.DurationMS < 0 || part.DurationMS > 60_000 {
			return protocol.MessagePart{}, fmt.Errorf("duration_ms must be between 0 and 60000")
		}
		part.Text = ""
		part.RefType = ""
		part.RefSubType = ""
		part.RefID = ""
		part.Label = ""
		part.Event = ""
		part.EventParams = nil
		part.ContentStartUTF16 = nil
		part.ContentEndUTF16 = nil
		part.PackID = ""
		part.StickerID = ""
		part.Alt = ""
		part.AttachmentID = strings.TrimSpace(part.AttachmentID)
		if part.AttachmentID == "" {
			part.Filename = ""
			part.ContentType = ""
			part.SizeBytes = 0
		} else {
			part.Filename = strings.TrimSpace(part.Filename)
			part.ContentType = strings.TrimSpace(part.ContentType)
		}
		return part, nil
	default:
		return protocol.MessagePart{}, fmt.Errorf("unsupported type %q", part.Type)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
