/**
 * Notes selection quotes — abbreviated chips in the assistant composer,
 * full excerpts attached when the user sends a question. A second
 * selection appends; it does not replace the first.
 */

export const NOTE_SELECTION_QUOTE_PREVIEW_CHARS = 80;
export const NOTE_SELECTION_QUOTE_SEND_CHARS = 4000;
export const NOTE_SELECTION_QUOTE_SEND_TOTAL_CHARS = 8000;
export const NOTE_SELECTION_QUOTE_MAX_EXCERPTS = 12;

export type NoteSelectionExcerpt = {
  id: string;
  text: string;
};

export type NoteSelectionQuote = {
  pageId: string;
  excerpts: NoteSelectionExcerpt[];
  askedAt: number;
};

export function abbreviateNoteSelection(
  text: string,
  maxChars = NOTE_SELECTION_QUOTE_PREVIEW_CHARS,
): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  if (!collapsed) return "";
  if (collapsed.length <= maxChars) return collapsed;
  const keep = Math.max(1, maxChars - 1);
  return `${collapsed.slice(0, keep).trimEnd()}…`;
}

function clipExcerpt(text: string, maxChars: number): string {
  if (text.length <= maxChars) return text;
  return `${text.slice(0, Math.max(1, maxChars - 1)).trimEnd()}…`;
}

function toBlockquote(text: string): string {
  return text
    .split("\n")
    .map((line) => `> ${line}`)
    .join("\n");
}

export function attachNoteSelectionQuote(
  question: string,
  excerpts: readonly string[],
): string {
  const parts: string[] = [];
  let used = 0;
  for (const raw of excerpts) {
    const excerpt = raw.trim();
    if (!excerpt) continue;
    const remaining = NOTE_SELECTION_QUOTE_SEND_TOTAL_CHARS - used;
    if (remaining <= 0) break;
    const clipped = clipExcerpt(excerpt, Math.min(NOTE_SELECTION_QUOTE_SEND_CHARS, remaining));
    parts.push(toBlockquote(clipped));
    used += clipped.length;
  }
  const q = question.trim();
  if (parts.length === 0) return q;
  return q ? `${parts.join("\n\n")}\n\n${q}` : parts.join("\n\n");
}

export function appendNoteSelectionExcerpt(
  current: NoteSelectionQuote | null,
  pageId: string,
  text: string,
  options?: { now?: number; id?: string },
): NoteSelectionQuote | null {
  const trimmed = text.trim();
  if (!trimmed) return current?.pageId === pageId ? current : null;
  const now = options?.now ?? Date.now();
  const id = options?.id ?? `excerpt-${now}`;
  if (!current || current.pageId !== pageId) {
    return { pageId, askedAt: now, excerpts: [{ id, text: trimmed }] };
  }
  if (current.excerpts.some((excerpt) => excerpt.text === trimmed)) {
    return { ...current, askedAt: now };
  }
  if (current.excerpts.length >= NOTE_SELECTION_QUOTE_MAX_EXCERPTS) {
    return { ...current, askedAt: now };
  }
  return {
    pageId,
    askedAt: now,
    excerpts: [...current.excerpts, { id, text: trimmed }],
  };
}

export function removeNoteSelectionExcerpt(
  current: NoteSelectionQuote | null,
  excerptId: string,
): NoteSelectionQuote | null {
  if (!current) return null;
  const excerpts = current.excerpts.filter((excerpt) => excerpt.id !== excerptId);
  if (excerpts.length === 0) return null;
  return { ...current, excerpts };
}
