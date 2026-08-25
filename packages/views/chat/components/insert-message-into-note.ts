import { api } from "@multica/core/api";
import {
  appendWorkerReplyBelowNote,
  deriveNoteWorkerReplyTitle,
} from "@multica/core/notes/worker-reply-actions";

export type NoteMessageInsertMode = "append" | "child";

/** Write copied chat text onto the current Notes page or a new child. */
export async function insertMessageIntoNote(options: {
  pageId: string;
  text: string;
  mode: NoteMessageInsertMode;
  titleFallback: string;
}): Promise<{ title: string; pageId: string }> {
  const title = deriveNoteWorkerReplyTitle(options.text, options.titleFallback);
  if (options.mode === "append") {
    const parent = await api.getNotePage(options.pageId);
    const nextContent = appendWorkerReplyBelowNote(parent.content ?? "", title, options.text);
    await api.updateNotePage(options.pageId, { content: nextContent });
    return { title, pageId: options.pageId };
  }
  const child = await api.createNotePage({ parent_id: options.pageId, title });
  const updated = await api.updateNotePage(child.id, { content: options.text });
  return { title, pageId: updated.id };
}
