package messageparts

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/stickers"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const BuiltinStickerPackID = "builtin"

func Normalize(content string, parts []protocol.MessagePart) (string, []protocol.MessagePart, error) {
	normalizedContent := strings.TrimSpace(content)
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
	return normalizedContent, out, nil
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
		part.PackID = ""
		part.StickerID = ""
		part.Alt = ""
		return part, nil
	case protocol.MessagePartTypeSticker:
		part.Text = ""
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
