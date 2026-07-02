import type { MessagePart } from "@multica/core/types";

const STICKER_LABEL = "[Sticker]";
const STICKER_UNAVAILABLE_LABEL = "[Sticker unavailable]";

export function hasStructuredMessageParts(parts?: MessagePart[] | null): parts is MessagePart[] {
  return Array.isArray(parts) && parts.length > 0;
}

function normalizeText(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}

function safeStickerLabel(part: Extract<MessagePart, { type: "sticker" }>): string {
  const alt = normalizeText(part.alt ?? "");
  if (!alt || alt === part.sticker_id || alt === `:sticker:${part.sticker_id}:`) {
    return STICKER_LABEL;
  }
  return `${STICKER_LABEL} ${alt}`;
}

export function formatMessagePartsPreview(parts?: MessagePart[] | null): string | null {
  if (!parts?.length) return null;
  const chunks = parts.flatMap((part) => {
    if (part.type === "text") {
      const text = normalizeText(part.text);
      return text ? [text] : [];
    }
    if (part.type === "sticker") {
      return part.sticker_id ? [safeStickerLabel(part)] : [STICKER_UNAVAILABLE_LABEL];
    }
    return [];
  });
  return chunks.length > 0 ? chunks.join(" ") : null;
}

export function formatMessagePartsCopyText(parts?: MessagePart[] | null): string | null {
  return formatMessagePartsPreview(parts);
}
