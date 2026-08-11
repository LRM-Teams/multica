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
		out = append(out, scrubForeignPartFields(normalized))
	}
	if normalizedContent == "" {
		normalizedContent = FallbackContent(out)
	}
	if hasVoicePart(out) {
		hasText := hasTextPart(out)
		voiceCount := 0
		recordedVoiceCount := 0
		for i := range out {
			if out[i].Type != protocol.MessagePartTypeVoice {
				continue
			}
			voiceCount++
			if out[i].AttachmentID != "" {
				recordedVoiceCount++
				if hasText {
					out[i].TranscriptionStatus = protocol.VoiceTranscriptionCompleted
				} else {
					out[i].TranscriptionStatus = protocol.VoiceTranscriptionPending
				}
				continue
			}
			out[i].TranscriptionStatus = ""
		}
		if !hasText && normalizedContent != "" {
			return "", nil, fmt.Errorf("voice transcript text part is required when content is supplied")
		}
		if !hasText && (voiceCount != 1 || recordedVoiceCount != 1) {
			return "", nil, fmt.Errorf("one recorded voice attachment is required when transcript text is absent")
		}
		if hasText && utf8.RuneCountInString(normalizedContent) > protocol.VoiceTranscriptMaxRunes {
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
			// Unknown actions (e.g. note AI "insert"/"patch"/"replace_page") are
			// not chat message_send envelopes. Leave the payload alone so product
			// surfaces can persist and parse their own structured JSON. Returning
			// unwrapped=true here used to make callers wipe the body and drop the
			// only copy of a successful note AI edit.
			return content, parts, false, nil
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
		case protocol.MessagePartTypeChoice:
			prompt := strings.TrimSpace(part.Prompt)
			if prompt != "" {
				values = append(values, prompt)
			} else {
				values = append(values, "[Choice]")
			}
		case protocol.MessagePartTypeChoiceReply:
			label := strings.TrimSpace(part.Label)
			if label == "" {
				label = "[Choice reply]"
			}
			values = append(values, "选择："+label)
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
	part.SynthesisStatus = ""
	if part.Type != protocol.MessagePartTypeVoice {
		part.TranscriptionStatus = ""
	}
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
		case "channel-ref":
			if part.RefSubType != "" {
				return protocol.MessagePart{}, fmt.Errorf("unsupported channel-ref ref_subtype %q", part.RefSubType)
			}
		case "agent:create":
			// LRM-2343 canonical Proposal. The reference is owned by the
			// channel Message itself: ref_id is the server-provided proposal
			// label/seed, while the committed result is recorded in Params and
			// agent_action. It is deliberately not an opaque card-row UUID.
			if part.RefSubType != "" {
				return protocol.MessagePart{}, fmt.Errorf("agent:create does not support ref_subtype")
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
	case protocol.MessagePartTypeChoice:
		return normalizeChoicePart(part)
	case protocol.MessagePartTypeChoiceReply:
		return normalizeChoiceReplyPart(part)
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
		// The server derives this value from transcript/attachment presence.
		// Ignore client-supplied states so callers cannot skip ASR dispatch.
		part.TranscriptionStatus = ""
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

const (
	choiceIDMaxLen     = 64
	choiceLabelMaxLen  = 80
	choiceDescMaxLen   = 160
	choicePromptMaxLen = 280
	choiceMaxOptions   = 4
	choiceMinOptions   = 2
)

func normalizeChoicePart(part protocol.MessagePart) (protocol.MessagePart, error) {
	clearNonChoiceFields(&part)
	part.ChoiceID = strings.TrimSpace(part.ChoiceID)
	if part.ChoiceID == "" {
		return protocol.MessagePart{}, fmt.Errorf("choice_id is required")
	}
	if utf8.RuneCountInString(part.ChoiceID) > choiceIDMaxLen {
		return protocol.MessagePart{}, fmt.Errorf("choice_id is too long")
	}
	part.Prompt = strings.TrimSpace(part.Prompt)
	if part.Prompt == "" {
		return protocol.MessagePart{}, fmt.Errorf("prompt is required")
	}
	if utf8.RuneCountInString(part.Prompt) > choicePromptMaxLen {
		return protocol.MessagePart{}, fmt.Errorf("prompt is too long")
	}
	part.Layout = strings.TrimSpace(strings.ToLower(part.Layout))
	switch part.Layout {
	case protocol.ChoiceLayoutBinary, protocol.ChoiceLayoutList:
	default:
		return protocol.MessagePart{}, fmt.Errorf("layout must be binary or list")
	}
	if len(part.Options) < choiceMinOptions || len(part.Options) > choiceMaxOptions {
		return protocol.MessagePart{}, fmt.Errorf("options must contain %d–%d items", choiceMinOptions, choiceMaxOptions)
	}
	if part.Layout == protocol.ChoiceLayoutBinary && len(part.Options) != 2 {
		return protocol.MessagePart{}, fmt.Errorf("binary layout requires exactly 2 options")
	}
	seen := make(map[string]struct{}, len(part.Options))
	outOpts := make([]protocol.ChoiceOption, 0, len(part.Options))
	for i, opt := range part.Options {
		id := strings.TrimSpace(opt.ID)
		label := strings.TrimSpace(opt.Label)
		desc := strings.TrimSpace(opt.Description)
		if id == "" {
			return protocol.MessagePart{}, fmt.Errorf("options[%d].id is required", i)
		}
		if utf8.RuneCountInString(id) > choiceIDMaxLen {
			return protocol.MessagePart{}, fmt.Errorf("options[%d].id is too long", i)
		}
		if label == "" {
			return protocol.MessagePart{}, fmt.Errorf("options[%d].label is required", i)
		}
		if utf8.RuneCountInString(label) > choiceLabelMaxLen {
			return protocol.MessagePart{}, fmt.Errorf("options[%d].label is too long", i)
		}
		if utf8.RuneCountInString(desc) > choiceDescMaxLen {
			return protocol.MessagePart{}, fmt.Errorf("options[%d].description is too long", i)
		}
		if _, dup := seen[id]; dup {
			return protocol.MessagePart{}, fmt.Errorf("duplicate option id %q", id)
		}
		seen[id] = struct{}{}
		outOpts = append(outOpts, protocol.ChoiceOption{ID: id, Label: label, Description: desc})
	}
	part.Options = outOpts
	part.ExpiresAt = strings.TrimSpace(part.ExpiresAt)
	part.SelectedOptionID = strings.TrimSpace(part.SelectedOptionID)
	if part.SelectedOptionID != "" {
		if _, ok := seen[part.SelectedOptionID]; !ok {
			return protocol.MessagePart{}, fmt.Errorf("selected_option_id does not match an option")
		}
	}
	// select_count: 0 unset, 1 first pick (one reselect left), 2 locked after reselect.
	if part.SelectCount < 0 || part.SelectCount > 2 {
		return protocol.MessagePart{}, fmt.Errorf("select_count must be 0–2")
	}
	if part.SelectedOptionID == "" {
		part.SelectCount = 0
	} else if part.SelectCount == 0 {
		part.SelectCount = 1
	}
	part.OptionID = ""
	return part, nil
}

func normalizeChoiceReplyPart(part protocol.MessagePart) (protocol.MessagePart, error) {
	clearNonChoiceFields(&part)
	part.ChoiceID = strings.TrimSpace(part.ChoiceID)
	part.OptionID = strings.TrimSpace(part.OptionID)
	part.Label = strings.TrimSpace(part.Label)
	if part.ChoiceID == "" {
		return protocol.MessagePart{}, fmt.Errorf("choice_id is required")
	}
	if part.OptionID == "" {
		return protocol.MessagePart{}, fmt.Errorf("option_id is required")
	}
	if part.Label == "" {
		return protocol.MessagePart{}, fmt.Errorf("label is required")
	}
	part.Options = nil
	part.Prompt = ""
	part.Layout = ""
	part.AllowDismiss = nil
	part.ExpiresAt = ""
	part.SelectedOptionID = ""
	part.SelectCount = 0
	return part, nil
}

func clearNonChoiceFields(part *protocol.MessagePart) {
	part.Text = ""
	part.RefType = ""
	part.RefSubType = ""
	part.RefID = ""
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
	part.TranscriptionStatus = ""
	part.SynthesisStatus = ""
}

func scrubForeignPartFields(part protocol.MessagePart) protocol.MessagePart {
	switch part.Type {
	case protocol.MessagePartTypeChoice:
		part.Label = ""
		part.OptionID = ""
	case protocol.MessagePartTypeChoiceReply:
		part.Prompt = ""
		part.Layout = ""
		part.Options = nil
		part.AllowDismiss = nil
		part.ExpiresAt = ""
		part.SelectedOptionID = ""
		part.SelectCount = 0
	default:
		part.ChoiceID = ""
		part.Prompt = ""
		part.Layout = ""
		part.Options = nil
		part.AllowDismiss = nil
		part.ExpiresAt = ""
		part.SelectedOptionID = ""
		part.SelectCount = 0
		part.OptionID = ""
	}
	return part
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
