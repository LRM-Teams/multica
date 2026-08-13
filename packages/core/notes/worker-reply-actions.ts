import type { ChannelMessage, MessagePart } from "../types";

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

/**
 * For each agent message, map to the most recent preceding `note_brief` page id
 * in timeline order (Worker 「按这篇做」 trigger).
 */
export function buildNoteWorkerPageIdByMessageId(
  messages: readonly ChannelMessage[],
): Map<string, string> {
  const map = new Map<string, string>();
  let activePageId: string | null = null;
  for (const message of messages) {
    const brief = message.parts?.find(
      (part): part is Extract<MessagePart, { type: "note_brief" }> => part.type === "note_brief",
    );
    if (brief?.ref_id?.trim()) {
      activePageId = brief.ref_id.trim();
    }
    if (message.type === "agent" && !message.deleted_at && activePageId) {
      map.set(message.id, activePageId);
    }
  }
  return map;
}
