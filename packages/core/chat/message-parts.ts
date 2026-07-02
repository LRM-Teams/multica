/**
 * Structured message-parts protocol shared with the agent runtime.
 *
 * Instead of markdown text, an agent may reply with a structured JSON body —
 * today a sticker-only reply, e.g.:
 *
 *   {"parts":[{"type":"sticker","sticker_id":"hi"}]}
 *
 * The chat renderer must recognise such a body and render the parts (stickers)
 * rather than dumping the raw JSON into the message bubble (LRM-84). Any body
 * that is not a well-formed parts payload is left untouched, so ordinary text —
 * including text that merely looks JSON-ish, or invalid/partial JSON — keeps
 * rendering as markdown.
 */

export interface StickerPart {
  type: "sticker";
  sticker_id: string;
}

// Extensible union: today only stickers are rendered structurally. A payload
// that mixes in an unrecognised part type is treated as plain text (safe
// fallback) rather than being partially rendered — see parseMessageParts.
export type MessagePart = StickerPart;

/**
 * Sticker ids are interpolated into an asset URL (`/api/stickers/{id}`) and
 * used as alt text / React keys, so restrict them to a conservative, URL-safe
 * charset. Anything outside this set is rejected (and the body renders as text)
 * to avoid broken URLs or path traversal.
 */
const STICKER_ID_PATTERN = /^[a-zA-Z0-9_-]{1,64}$/;

interface RawPartsPayload {
  parts?: unknown;
}

/**
 * Parse a chat message body as a structured `{"parts":[...]}` payload.
 *
 * Returns the validated parts array only when the ENTIRE body is a single JSON
 * object with a non-empty `parts` array whose every element is a part type we
 * render structurally. Returns `null` for everything else — plain text,
 * invalid/partial JSON, JSON that isn't a parts object, an empty `parts`
 * array, or a payload containing an unrecognised part — so the caller falls
 * back to rendering the body as markdown text.
 */
export function parseMessageParts(
  content: string | null | undefined,
): MessagePart[] | null {
  if (!content) return null;
  const trimmed = content.trim();

  // Cheap guards before attempting JSON.parse on every message body: a parts
  // payload is a single JSON object (starts with `{`, ends with `}`) that
  // mentions "parts". This keeps the hot path (ordinary text) allocation-free.
  if (
    trimmed.length < 2 ||
    trimmed[0] !== "{" ||
    trimmed[trimmed.length - 1] !== "}" ||
    !trimmed.includes('"parts"')
  ) {
    return null;
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }

  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return null;
  }

  const rawParts = (parsed as RawPartsPayload).parts;
  if (!Array.isArray(rawParts) || rawParts.length === 0) return null;

  const parts: MessagePart[] = [];
  for (const raw of rawParts) {
    if (typeof raw !== "object" || raw === null) return null;
    const part = raw as Record<string, unknown>;
    if (part.type === "sticker") {
      const id = part.sticker_id;
      if (typeof id !== "string" || !STICKER_ID_PATTERN.test(id)) return null;
      parts.push({ type: "sticker", sticker_id: id });
    } else {
      // Unknown / mixed part type: don't half-render and don't silently drop
      // content — treat the whole body as plain text instead.
      return null;
    }
  }

  return parts;
}

/**
 * Convenience over {@link parseMessageParts} for the current sticker protocol:
 * if the body is composed solely of sticker parts, return their ids in order;
 * otherwise `null`. The chat renderer uses this to swap a raw JSON body for
 * rendered stickers.
 */
export function parseStickerMessage(
  content: string | null | undefined,
): string[] | null {
  const parts = parseMessageParts(content);
  if (!parts || parts.length === 0) return null;
  return parts.map((p) => p.sticker_id);
}
