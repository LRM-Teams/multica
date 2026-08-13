export type ConversationHandleKind = "channel" | "dm";

export type ConversationHandle = {
  kind: ConversationHandleKind;
  name: string;
  messagePrefix: string | null;
};

export type ConversationHandlePart =
  | { kind: "text"; value: string }
  | { kind: "handle"; value: string };

const CHANNEL_NAME = String.raw`[\p{L}\p{N}_-]+`;
const DM_PEER = String.raw`[\w.-]+`;
const THREAD_SHORT = String.raw`[\da-fA-F]{6,8}`;
const SCAN = new RegExp(
  `#(${CHANNEL_NAME})(?::(${THREAD_SHORT}))?|dm:@(${DM_PEER})(?::(${THREAD_SHORT}))?`,
  "giu",
);

export function parseConversationHandle(raw: string): ConversationHandle | null {
  const value = raw.trim();
  if (value.startsWith("dm:@")) return parseNamed("dm", value.slice("dm:@".length), /^[\w.-]+$/u);
  if (value.startsWith("#")) return parseNamed("channel", value.slice(1), /^[\p{L}\p{N}_-]+$/u);
  return null;
}

function parseNamed(
  kind: ConversationHandleKind,
  rest: string,
  nameRe: RegExp,
): ConversationHandle | null {
  const colon = rest.indexOf(":");
  const name = (colon < 0 ? rest : rest.slice(0, colon)).trim();
  if (!name || !nameRe.test(name)) return null;
  if (colon < 0) return { kind, name, messagePrefix: null };
  if (rest.includes(":", colon + 1)) return null;
  const prefix = rest
    .slice(colon + 1)
    .trim()
    .toLowerCase()
    .replace(/-/g, "");
  if (!/^[\da-f]{6,8}$/.test(prefix)) return null;
  return { kind, name, messagePrefix: prefix };
}

export function findConversationHandles(text: string): { raw: string; start: number; end: number }[] {
  const hits: { raw: string; start: number; end: number }[] = [];
  SCAN.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = SCAN.exec(text)) !== null) {
    if (match.index > 0 && isHandleChar(text[match.index - 1] ?? "")) continue;
    if (parseConversationHandle(match[0]) === null) continue;
    hits.push({ raw: match[0], start: match.index, end: match.index + match[0].length });
  }
  return hits;
}

function isHandleChar(ch: string): boolean {
  return /[\p{L}\p{N}_-]/u.test(ch);
}

/** Authorized deep-link shape used by reminder anchors, Activity, and channel-ref chips. */
export function conversationMessageHref(
  channelHref: string,
  opts?: { messageId?: string | null; threadId?: string | null },
): string {
  const params = new URLSearchParams();
  if (opts?.threadId) params.set("thread", opts.threadId);
  if (opts?.messageId) params.set("message", opts.messageId);
  const qs = params.toString();
  return qs ? `${channelHref}?${qs}` : channelHref;
}

export function splitTextWithConversationHandles(text: string): ConversationHandlePart[] {
  const hits = findConversationHandles(text);
  if (hits.length === 0) return [{ kind: "text", value: text }];
  const parts: ConversationHandlePart[] = [];
  let cursor = 0;
  for (const hit of hits) {
    if (hit.start > cursor) parts.push({ kind: "text", value: text.slice(cursor, hit.start) });
    parts.push({ kind: "handle", value: hit.raw });
    cursor = hit.end;
  }
  if (cursor < text.length) parts.push({ kind: "text", value: text.slice(cursor) });
  return parts;
}
