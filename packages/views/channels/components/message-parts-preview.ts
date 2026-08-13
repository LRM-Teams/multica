import type { MessagePart } from "@multica/core/types";

const STICKER_LABEL = "[Sticker]";
const STICKER_UNAVAILABLE_LABEL = "[Sticker unavailable]";

export function hasStructuredMessageParts(parts?: MessagePart[] | null): parts is MessagePart[] {
  return Array.isArray(parts) && parts.length > 0;
}

/**
 * Resolve the structured parts to render for a message body. Prefers the
 * message's own denormalized `parts`; for historical agent messages whose
 * `parts` were never backfilled, unwraps the structured-action envelope carried
 * in `content` so stickers etc. still render through `MessagePartsRenderer`.
 * Returns null only for ordinary, non-envelope content, which callers render as
 * plain markdown. This is the single source of truth shared by every message
 * body surface (channel bubble, thread root, DM parent) so they stay consistent.
 */
export function resolveMessageParts(
  content: string,
  parts?: MessagePart[] | null,
): MessagePart[] | null {
  return hasStructuredMessageParts(parts) ? parts : extractEnvelopeParts(content);
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
    if (part.type === "choice") {
      const prompt = normalizeText(part.prompt);
      return prompt ? [prompt] : ["[Choice]"];
    }
    if (part.type === "choice_reply") {
      const label = normalizeText(part.label);
      return [`选择：${label || "?"}`];
    }
    if (part.type === "note_brief") {
      const title = normalizeText(part.label ?? "");
      return title ? [`笔记「${title}」`] : ["笔记"];
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

const MESSAGE_SEND_ACTION = "message_send";

/**
 * Validate that a parsed JSON value is a `message_send` structured-action
 * envelope: an object with a top-level `action === "message_send"` AND an array
 * `parts`. Requiring the SPECIFIC `message_send` action (not merely any string
 * `action`) is what keeps this narrow:
 *   - GAP 2 — prose that merely embeds a NON-message_send action object (e.g.
 *     `{"action":"navigate","parts":["home"]}`, a tool-call) is rejected, so the
 *     surrounding prose is never intercepted and blanked.
 *   - GAP 3 — legit JSON that merely has a `parts` array (e.g.
 *     `{"parts":["a","b"]}`) has no `action` and is likewise rejected.
 */
function validateStructuredActionEnvelope(parsed: unknown): StructuredActionEnvelope | null {
  if (typeof parsed !== "object" || parsed === null) return null;
  const envelope = parsed as { action?: unknown; parts?: unknown; output?: unknown };
  // Only a `message_send` envelope represents a real message to unwrap; any
  // other action (or a missing/non-string action) is left as normal text.
  if (envelope.action !== MESSAGE_SEND_ACTION) return null;
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
 * balanced candidate, and JSON-parses it. Conservative by construction: a
 * candidate must parse to a `message_send` envelope (top-level
 * `action === "message_send"` + array `parts`), so ordinary prose, a stray
 * `{...}`, or a non-message_send action object (e.g. a tool-call) is never
 * intercepted.
 *
 * Because leaking historical content can carry MULTIPLE envelopes (e.g. an
 * earlier tool-call or an empty-`parts` envelope BEFORE the real message), the
 * scan does not stop at the first match. It collects every `message_send`
 * candidate in scan order and PREFERS the first whose `parts` yield renderable
 * text; if none has text it falls back to the first candidate (so its `output`
 * / placeholder path still runs). This is what surfaces the real message
 * instead of an earlier empty envelope.
 */
function findEmbeddedStructuredActionEnvelope(content: string): StructuredActionEnvelope | null {
  let firstCandidate: StructuredActionEnvelope | null = null;
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
    if (!envelope) continue;
    if (firstCandidate === null) firstCandidate = envelope;
    // Prefer the first message_send envelope with renderable text; an earlier
    // empty-`parts` envelope must not shadow a later real message.
    if (formatMessagePartsPreview(envelope.parts) !== null) return envelope;
  }
  return firstCandidate;
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
 * The discriminator REQUIRES `action === "message_send"`, so legit user-pasted
 * JSON with only a `parts` array (`{"parts":["a","b"]}`), prose that mentions a
 * stray `{"foo":1}`, or a non-message_send action object embedded in prose
 * (`{"action":"navigate","parts":["home"]}`) is NEVER intercepted. A cheap
 * substring pre-parse guard avoids scanning ordinary text.
 *
 * Returns null for anything that is not a recognizable `message_send` envelope,
 * so normal content flows through unchanged.
 */
function parseStructuredActionEnvelope(content: string): StructuredActionEnvelope | null {
  // Cheap pre-parse guard: a message_send envelope always carries both the
  // literal `"message_send"` action value and a `"parts"` key. Ordinary prose
  // (and GAP-2/GAP-3 JSON like `here is {"foo":1}` or a `navigate` action)
  // lacks these, so bail before any parse/scan work.
  if (!content.includes('"message_send"') || !content.includes('"parts"')) {
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
