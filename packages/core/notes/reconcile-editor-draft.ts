export type NoteEditorDraft = {
  title: string;
  content: string;
  serverTitle: string;
  serverContent: string;
};

/**
 * Merge a React Query note snapshot into the open editor draft.
 *
 * Clean fields take the server value. A dirty body still accepts a remote
 * *append* (period-brief insert-below, worker insert) so the open note shows
 * what the user just confirmed; unrelated remote rewrites stay ignored so
 * in-progress keystrokes are not replaced.
 */
export function reconcileNoteEditorDraft(
  draft: NoteEditorDraft,
  selected: { title: string; content: string },
): NoteEditorDraft | null {
  if (draft.serverTitle === selected.title && draft.serverContent === selected.content) {
    return null;
  }
  const titleClean = draft.title === draft.serverTitle;
  const contentClean = draft.content === draft.serverContent;
  const title = titleClean ? selected.title : draft.title;
  const serverTitle = titleClean ? selected.title : draft.serverTitle;
  let content = contentClean ? selected.content : draft.content;
  let serverContent = contentClean ? selected.content : draft.serverContent;
  if (!contentClean) {
    const appended = applyRemoteNoteAppend(draft.content, draft.serverContent, selected.content);
    if (appended !== null) {
      content = appended;
      serverContent = selected.content;
    }
  }
  if (
    title === draft.title &&
    content === draft.content &&
    serverTitle === draft.serverTitle &&
    serverContent === draft.serverContent
  ) {
    return null;
  }
  return { title, content, serverTitle, serverContent };
}

function trimEndWhitespace(value: string): string {
  return value.replace(/[ \t\r\n]+$/g, "");
}

function applyRemoteNoteAppend(localContent: string, previousServer: string, nextServer: string): string | null {
  const previous = trimEndWhitespace(previousServer);
  const next = trimEndWhitespace(nextServer);
  if (next === previous) return null;
  if (previous !== "" && !next.startsWith(previous)) return null;
  const suffix = previous === "" ? next : next.slice(previous.length);
  if (!suffix) return null;
  const local = trimEndWhitespace(localContent);
  if (!local) return next;
  // Autosave onMutate echoes the open draft into React Query. That snapshot
  // looks like an insert-below (it starts with the last saved body). Concat
  // would duplicate the user's Enter — a new list item or paragraph — on
  // every render.
  if (local === next || local.startsWith(next)) return null;
  // IME Space-commit (and other inline replacements): local is 你好 while the
  // cache still holds pinyin. Both extend the last saved body, but the cache
  // suffix is not a block insert. Concat would produce 你好nihao. Period-brief
  // / worker insert-below still start their suffix with a newline (or a
  // heading, when the page was empty).
  const localExtendsPrevious = previous === "" || local.startsWith(previous);
  const suffixIsBlockAppend = /^\r?\n/.test(suffix) || /^\s*#{1,6}\s/.test(suffix);
  if (localExtendsPrevious && local !== previous && !suffixIsBlockAppend) return null;
  return local + suffix;
}
