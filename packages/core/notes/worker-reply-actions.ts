import type { ChannelMessage, ChatMessage, MessagePart } from "../types";

const NOTE_PAGE_IN_PATH =
  /(?:^|\/)notes\/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\b/gi;

/** Timeline confirmation for an agent reply that may write a product note. */
export type NoteWriteConfirmation =
  | { mode: "existing"; pageId: string }
  | { mode: "create" };

/** Max chars kept for a derived note / section title from an agent reply. */
export const NOTE_WORKER_REPLY_TITLE_MAX = 80;

/**
 * Derive a short title from agent reply text (first non-empty line, markdown
 * heading markers stripped). Falls back to `fallback` when empty.
 */
export function deriveNoteWorkerReplyTitle(replyText: string, fallback: string): string {
  const fallbackTitle = fallback.trim() || "Untitled";
  const firstLine = replyText
    .split(/\r?\n/)
    .map((line) => line.trim())
    .find((line) => line.length > 0);
  if (!firstLine) return fallbackTitle;
  const cleaned = firstLine.replace(/^#{1,6}\s+/, "").replace(/^[*_`~\s]+|[*_`~\s]+$/g, "").trim();
  if (!cleaned) return fallbackTitle;
  if (cleaned.length <= NOTE_WORKER_REPLY_TITLE_MAX) return cleaned;
  return `${cleaned.slice(0, NOTE_WORKER_REPLY_TITLE_MAX - 1)}…`;
}

/**
 * Append a titled Worker reply below existing note markdown, with one blank
 * line between the original body and the new section ("空一个格").
 */
export function appendWorkerReplyBelowNote(
  existingContent: string,
  title: string,
  replyBody: string,
): string {
  const heading = title.trim() || "Untitled";
  const body = replyBody.replace(/^\s+|\s+$/g, "");
  const section = body ? `## ${heading}\n\n${body}` : `## ${heading}`;
  const base = existingContent.replace(/\s+$/g, "");
  if (!base) return section;
  return `${base}\n\n${section}`;
}

/** Resolve plain reply text used for note writebacks. */
export function noteWorkerReplyPlainText(message: Pick<ChannelMessage, "content" | "parts">): string {
  const fromParts = message.parts
    ?.filter((part): part is Extract<MessagePart, { type: "text" }> => part.type === "text")
    .map((part) => part.text.trim())
    .filter(Boolean)
    .join("\n\n");
  if (fromParts) return fromParts;
  return (message.content ?? "").trim();
}

/** Pull note page ids from `/notes/<uuid>` links in visible message text. */
export function extractNotePageIdsFromText(text: string): string[] {
  if (!text) return [];
  const ids: string[] = [];
  NOTE_PAGE_IN_PATH.lastIndex = 0;
  for (const match of text.matchAll(NOTE_PAGE_IN_PATH)) {
    const id = match[1]?.trim();
    if (id) ids.push(id.toLowerCase());
  }
  return ids;
}

function noteBriefPageId(message: Pick<ChannelMessage, "parts">): string | null {
  const brief = message.parts?.find(
    (part): part is Extract<MessagePart, { type: "note_brief" }> => part.type === "note_brief",
  );
  const id = brief?.ref_id?.trim();
  return id ? id : null;
}

function noteWritePart(
  message: Pick<ChannelMessage, "parts">,
): Extract<MessagePart, { type: "note_write" }> | null {
  return (
    message.parts?.find(
      (part): part is Extract<MessagePart, { type: "note_write" }> => part.type === "note_write",
    ) ?? null
  );
}

/**
 * True when the human asked to write/insert a product note, or asked for the
 * confirm button. Ordinary chat ("write a poem") does not match.
 */
export function isProductNoteWriteRequest(text: string): boolean {
  const value = text.trim();
  if (!value) return false;
  if (/(写入|插入|写进|写到|存到|记到|新建|创建|保存).{0,12}(笔记|当前页|本页|子页|新页)/.test(value)) {
    return true;
  }
  if (/(给我|弹出|出示).{0,12}按钮/.test(value)) return true;
  if (/按钮.{0,12}(插入|写入|笔记)/.test(value)) return true;
  return /\b(?:write|insert|save|create)\b.{0,32}\b(notes?|page|child)\b/i.test(value);
}

const NOTE_MARKDOWN_FENCE = /```(?:markdown|md)?\r?\n([\s\S]*?)```/gi;

/**
 * Prefer the largest fenced markdown body when the assistant wrapped a
 * copy-box; otherwise use the full reply.
 */
export function extractInsertableNoteMarkdown(text: string): string {
  const trimmed = text.trim();
  if (!trimmed) return "";
  let best = "";
  NOTE_MARKDOWN_FENCE.lastIndex = 0;
  for (const match of trimmed.matchAll(NOTE_MARKDOWN_FENCE)) {
    const body = (match[1] ?? "").replace(/\s+$/g, "");
    if (body.length > best.length) best = body;
  }
  return best || trimmed;
}

/**
 * True when an agent reply looks like note payload rather than a one-line ack.
 * Used with {@link isProductNoteWriteRequest} so "insert this" still shows a
 * confirm button if the agent forgot `--note-write`.
 */
export function looksLikeNoteProposal(text: string): boolean {
  const value = text.trim();
  if (!value) return false;
  if (value.length >= 80) return true;
  if (/^#{1,6}\s+\S/m.test(value)) return true;
  return value.split(/\r?\n/).filter((line) => line.trim().length > 0).length >= 3;
}

type NoteWriteScanMessage = {
  id: string;
  kind: "user" | "agent" | "other";
  content: string;
  parts?: MessagePart[];
  deleted?: boolean;
};

function buildNoteWriteConfirmationFromMessages(
  messages: readonly NoteWriteScanMessage[],
): Map<string, NoteWriteConfirmation> {
  const map = new Map<string, NoteWriteConfirmation>();
  let stickyPageId: string | null = null;
  let precedingUserNotePageId: string | null = null;
  let precedingUserAskedWrite = false;
  for (const message of messages) {
    const briefId = noteBriefPageId(message);
    if (briefId) stickyPageId = briefId;

    const write = noteWritePart(message);
    const writePageId = write?.ref_id?.trim() || "";
    if (writePageId) stickyPageId = writePageId;

    if (message.kind === "user" && !message.deleted) {
      const fromText = extractNotePageIdsFromText(message.content ?? "");
      precedingUserNotePageId = fromText.length > 0 ? fromText[fromText.length - 1]! : null;
      precedingUserAskedWrite = isProductNoteWriteRequest(message.content ?? "");
    }

    if (message.kind !== "agent" || message.deleted) continue;
    const proposing =
      Boolean(write) ||
      (precedingUserAskedWrite && looksLikeNoteProposal(noteWorkerReplyPlainText(message)));
    if (!proposing) continue;
    const pageId = writePageId || stickyPageId || precedingUserNotePageId;
    if (pageId) {
      map.set(message.id, { mode: "existing", pageId });
    } else {
      map.set(message.id, { mode: "create" });
    }
  }
  return map;
}

/**
 * For each agent message, decide the human-confirm note write action.
 * `--note-write` always opts that send in. A human insert/write-note request
 * also opts in the next agent replies that look like a proposal, so a forgotten
 * flag still shows Create note. Ordinary chat stays button-free.
 */
export function buildNoteWriteConfirmationByMessageId(
  messages: readonly ChannelMessage[],
): Map<string, NoteWriteConfirmation> {
  return buildNoteWriteConfirmationFromMessages(
    messages.map((message) => ({
      id: message.id,
      kind:
        message.type === "user" ? "user" : message.type === "agent" ? "agent" : "other",
      content: message.content,
      parts: message.parts,
      deleted: Boolean(message.deleted_at),
    })),
  );
}

/** Same confirm gate as channel `--note-write`, for Notes FAB chat_session. */
export function buildChatNoteWriteConfirmationByMessageId(
  messages: readonly ChatMessage[],
): Map<string, NoteWriteConfirmation> {
  return buildNoteWriteConfirmationFromMessages(
    messages.map((message) => ({
      id: message.id,
      kind: message.role === "user" ? "user" : "agent",
      content: message.content,
      parts: message.parts,
    })),
  );
}

/**
 * For each agent message, map to the most recent preceding `note_brief` page id
 * in timeline order (Worker / Period Brief trigger).
 */
export function buildNoteWorkerPageIdByMessageId(
  messages: readonly ChannelMessage[],
): Map<string, string> {
  const map = new Map<string, string>();
  for (const [id, confirmation] of buildNoteWriteConfirmationByMessageId(messages)) {
    if (confirmation.mode === "existing") map.set(id, confirmation.pageId);
  }
  return map;
}
