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

interface StructuredActionEnvelope {
  parts: MessagePart[];
  output?: unknown;
}

/**
 * Parse a raw structured-action envelope out of message content. Historical
 * agent messages whose denormalized `parts` were never backfilled carry the
 * envelope JSON in `content` (e.g.
 * `{"action":"message_send","output":"…","parts":[…]}`).
 *
 * The discriminator REQUIRES a top-level `action` key: the structured-action
 * envelope always has one, whereas legit user-pasted JSON that merely happens
 * to have a `parts` array (e.g. `{"parts":["a","b"]}`) must NOT be intercepted.
 * A cheap substring pre-parse guard avoids JSON.parse on ordinary text.
 *
 * Returns null for anything that is not a recognizable envelope, so normal
 * content flows through unchanged.
 */
function parseStructuredActionEnvelope(content: string): StructuredActionEnvelope | null {
  const trimmed = content.trim();
  if (
    !trimmed.startsWith("{") ||
    !trimmed.includes('"parts"') ||
    !trimmed.includes('"action"')
  ) {
    return null;
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }

  if (typeof parsed !== "object" || parsed === null) return null;
  const envelope = parsed as { action?: unknown; parts?: unknown; output?: unknown };
  // The structured-action envelope always carries a top-level `action`; require
  // it so legit JSON with only a `parts` array is left as normal text.
  if (!("action" in envelope)) return null;
  if (!Array.isArray(envelope.parts)) return null;

  return { parts: envelope.parts as MessagePart[], output: envelope.output };
}

/**
 * Return the `parts` array carried by a raw structured-action envelope in
 * `content`, or null when `content` is not such an envelope. Lets a historical
 * message with empty denormalized `parts` render its REAL parts through
 * {@link MessagePartsRenderer} (stickers etc.) instead of leaking raw JSON.
 *
 * NOTE: an envelope with an empty `parts` array still returns `[]` (not null),
 * so callers can distinguish "envelope with nothing renderable" (render a safe
 * neutral) from "not an envelope" (render the content as-is).
 */
export function extractEnvelopeParts(content: string): MessagePart[] | null {
  return parseStructuredActionEnvelope(content)?.parts ?? null;
}

/**
 * Defense-in-depth guard for historical agent messages whose denormalized
 * `parts` were never backfilled: their raw content is the structured-action
 * envelope JSON.
 *
 * When `content` looks like such an envelope, unwrap it to human text so the
 * raw JSON is NEVER rendered as a preview. Returns null for anything that is
 * not a recognizable envelope, so normal text content renders unchanged.
 */
export function unwrapStructuredPreviewContent(content: string): string | null {
  const envelope = parseStructuredActionEnvelope(content);
  if (!envelope) return null;

  const fromParts = formatMessagePartsPreview(envelope.parts);
  if (fromParts) return fromParts;

  if (typeof envelope.output === "string") {
    const output = normalizeText(envelope.output);
    if (output) return output;
  }

  return STRUCTURED_ENVELOPE_PLACEHOLDER;
}
