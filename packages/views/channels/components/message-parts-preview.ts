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
 * Validate that a parsed JSON value is a structured-action envelope: an object
 * with BOTH a top-level string `action` AND an array `parts`. This is the GAP 3
 * discriminator — legit JSON that merely has a `parts` array (e.g.
 * `{"parts":["a","b"]}`) has no `action` and is rejected, so it stays as text.
 */
function validateStructuredActionEnvelope(parsed: unknown): StructuredActionEnvelope | null {
  if (typeof parsed !== "object" || parsed === null) return null;
  const envelope = parsed as { action?: unknown; parts?: unknown; output?: unknown };
  // The structured-action envelope always carries a top-level string `action`;
  // require it so legit JSON with only a `parts` array is left as normal text.
  if (typeof envelope.action !== "string") return null;
  if (!Array.isArray(envelope.parts)) return null;

  return { parts: envelope.parts as MessagePart[], output: envelope.output };
}

/**
 * Given `content[start] === "{"`, return the index of the matching closing
 * `}`, tracking JSON string literals so that braces INSIDE string values do not
 * affect nesting depth. Escaped characters inside strings are skipped so an
 * escaped quote (`\"`) never ends a string early. Returns -1 when no balanced
 * closing brace is found (e.g. a stray `{` in a reasoning prefix).
 */
function matchBraceEnd(content: string, start: number): number {
  let depth = 0;
  let inString = false;
  for (let i = start; i < content.length; i++) {
    const ch = content[i];
    if (inString) {
      if (ch === "\\") {
        i++; // skip the escaped character (incl. an escaped quote)
        continue;
      }
      if (ch === '"') inString = false;
      continue;
    }
    if (ch === '"') {
      inString = true;
    } else if (ch === "{") {
      depth++;
    } else if (ch === "}") {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
}

/**
 * Locate a structured-action envelope object embedded WITHIN `content`,
 * tolerating a reasoning-text prefix and/or suffix. Historical agent messages
 * sometimes carry `content` shaped as `reasoning text … {"action":…,"parts":…}`
 * (agent prose concatenated with the envelope JSON), so a whole-string
 * JSON.parse throws and the raw string would otherwise leak.
 *
 * Scans each `{` position with a string-literal-aware brace matcher, slices the
 * balanced candidate, JSON-parses it, and returns the FIRST candidate that
 * validates as an action+parts envelope. Conservative by construction: a
 * candidate must genuinely parse to an object with a top-level `action` key and
 * an array `parts`, so ordinary prose or a stray `{...}` is never intercepted.
 */
function findEmbeddedStructuredActionEnvelope(content: string): StructuredActionEnvelope | null {
  for (let i = 0; i < content.length; i++) {
    if (content[i] !== "{") continue;
    const end = matchBraceEnd(content, i);
    if (end === -1) continue;
    const candidate = content.slice(i, end + 1);
    let parsed: unknown;
    try {
      parsed = JSON.parse(candidate);
    } catch {
      continue;
    }
    const envelope = validateStructuredActionEnvelope(parsed);
    if (envelope) return envelope;
  }
  return null;
}

/**
 * Parse a raw structured-action envelope out of message content. Historical
 * agent messages whose denormalized `parts` were never backfilled carry the
 * envelope JSON in `content` — either as a PURE envelope
 * (`{"action":"message_send","output":"…","parts":[…]}`) or, when the agent's
 * reasoning text was concatenated ahead of it, as a text-prefixed EMBEDDED
 * envelope (`Repo isn't checked out … {"action":…,"parts":[…]}`).
 *
 * Two-stage detection:
 *   1. Fast path — a whole-string parse of the trimmed content that validates
 *      as an envelope (the pure-envelope case).
 *   2. Embedded scan — a brace-matched sweep locating an envelope object within
 *      content, tolerating a reasoning prefix and/or suffix.
 *
 * The discriminator REQUIRES a top-level string `action`, so legit user-pasted
 * JSON that merely happens to have a `parts` array (e.g. `{"parts":["a","b"]}`)
 * or prose that mentions a stray `{"foo":1}` is NEVER intercepted. A cheap
 * substring pre-parse guard avoids scanning ordinary text.
 *
 * Returns null for anything that is not a recognizable envelope, so normal
 * content flows through unchanged.
 */
function parseStructuredActionEnvelope(content: string): StructuredActionEnvelope | null {
  // Cheap pre-parse guard: a structured-action envelope always carries both an
  // `"action"` and a `"parts"` key. Ordinary prose (and GAP-3 JSON like
  // `here is {"foo":1}`) lacks these, so bail before any parse/scan work.
  if (!content.includes('"parts"') || !content.includes('"action"')) {
    return null;
  }

  // Fast path: whole-string pure envelope.
  const trimmed = content.trim();
  if (trimmed.startsWith("{")) {
    try {
      const envelope = validateStructuredActionEnvelope(JSON.parse(trimmed));
      if (envelope) return envelope;
    } catch {
      // Not a pure envelope — fall through to the embedded scan.
    }
  }

  // Embedded envelope: reasoning-text prefix and/or suffix around the JSON.
  return findEmbeddedStructuredActionEnvelope(content);
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
