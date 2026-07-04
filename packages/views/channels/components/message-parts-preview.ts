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

// Neutral, non-empty placeholder used when a structured-action envelope carries
// no renderable text and no output. Kept non-empty so downstream truthiness
// checks (e.g. thread-root-preview's compact-body fallback) never fall back to
// rendering the raw content JSON.
const STRUCTURED_ENVELOPE_PLACEHOLDER = "…";

/**
 * Defense-in-depth guard for historical agent messages whose denormalized
 * `parts` were never backfilled: their raw content is the structured-action
 * envelope JSON (e.g. `{"action":"message_send","output":"…","parts":[…]}`).
 *
 * When `content` looks like such an envelope, unwrap it to human text so the
 * raw JSON is NEVER rendered as a preview. Returns null for anything that is
 * not a recognizable envelope, so normal text content renders unchanged.
 */
export function unwrapStructuredPreviewContent(content: string): string | null {
  const trimmed = content.trim();
  // Cheap guard before JSON.parse: must look like a JSON object mentioning parts.
  if (!trimmed.startsWith("{") || !trimmed.includes('"parts"')) return null;

  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }

  if (typeof parsed !== "object" || parsed === null) return null;
  const envelope = parsed as { parts?: unknown; output?: unknown };
  if (!Array.isArray(envelope.parts)) return null;

  const fromParts = formatMessagePartsPreview(envelope.parts as MessagePart[]);
  if (fromParts) return fromParts;

  if (typeof envelope.output === "string") {
    const output = normalizeText(envelope.output);
    if (output) return output;
  }

  return STRUCTURED_ENVELOPE_PLACEHOLDER;
}
