/**
 * Note page references in the Notes assistant composer — chips while drafting,
 * page ids attached on send so the agent can `notes get` them. Drag payload
 * uses a dedicated MIME so tree reorder (text/plain = id) stays separate.
 */

export const NOTE_PAGE_DRAG_MIME = "application/x-multica-note-page";
export const NOTE_PAGE_REF_MAX = 8;

export type NotePageRef = {
  pageId: string;
  title: string;
};

/** Refs attached to one Notes FAB bubble (keyed by the open context page). */
export type NotePageRefBundle = {
  bubblePageId: string;
  refs: NotePageRef[];
};

export function encodeNotePageDragPayload(ref: NotePageRef): string {
  return JSON.stringify({
    pageId: ref.pageId,
    title: ref.title,
  });
}

export function parseNotePageDragPayload(raw: string): NotePageRef | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  try {
    const parsed = JSON.parse(trimmed) as { pageId?: unknown; title?: unknown };
    const pageId = typeof parsed.pageId === "string" ? parsed.pageId.trim() : "";
    if (!pageId) return null;
    const title =
      typeof parsed.title === "string" && parsed.title.trim()
        ? parsed.title.trim()
        : "Untitled";
    return { pageId, title };
  } catch {
    return null;
  }
}

export function appendNotePageRef(
  current: NotePageRefBundle | null,
  bubblePageId: string,
  ref: NotePageRef,
): NotePageRefBundle | null {
  const pageId = ref.pageId.trim();
  if (!pageId || !bubblePageId.trim()) return current;
  const title = ref.title.trim() || "Untitled";
  if (!current || current.bubblePageId !== bubblePageId) {
    return { bubblePageId, refs: [{ pageId, title }] };
  }
  if (current.refs.some((item) => item.pageId === pageId)) {
    return {
      bubblePageId,
      refs: current.refs.map((item) =>
        item.pageId === pageId ? { pageId, title } : item,
      ),
    };
  }
  if (current.refs.length >= NOTE_PAGE_REF_MAX) {
    return current;
  }
  return {
    bubblePageId,
    refs: [...current.refs, { pageId, title }],
  };
}

export function removeNotePageRef(
  current: NotePageRefBundle | null,
  pageId: string,
): NotePageRefBundle | null {
  if (!current) return null;
  const refs = current.refs.filter((item) => item.pageId !== pageId);
  if (refs.length === 0) return null;
  return { ...current, refs };
}

/**
 * Prepend referenced pages as markdown lines the Notes assistant can open
 * with `multica notes get` / `/notes/<id>` (same path shape as Worker replies).
 */
export function attachNotePageRefs(
  question: string,
  refs: readonly NotePageRef[],
): string {
  const lines: string[] = [];
  for (const ref of refs) {
    const pageId = ref.pageId.trim();
    if (!pageId) continue;
    const title = ref.title.trim() || "Untitled";
    lines.push(`> 笔记引用：${title}（/notes/${pageId}）`);
  }
  const q = question.trim();
  if (lines.length === 0) return q;
  return q ? `${lines.join("\n")}\n\n${q}` : lines.join("\n");
}
