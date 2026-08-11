import type { Editor } from "@tiptap/core";
import { Slice } from "@tiptap/pm/model";
import type { NoteAIEditResult } from "@multica/core/types";

export function replaceRangeWithMarkdown(editor: Editor, from: number, to: number, markdown: string) {
  const docSize = editor.state.doc.content.size;
  const safeFrom = Math.max(0, Math.min(from, docSize));
  const safeTo = Math.max(safeFrom, Math.min(to, docSize));
  if (!editor.markdown) {
    editor.chain().focus().command(({ tr }) => {
      tr.insertText(markdown, safeFrom, safeTo);
      return true;
    }).run();
    return;
  }
  const json = editor.markdown.parse(markdown);
  const node = editor.schema.nodeFromJSON(json);
  const slice = Slice.maxOpen(node.content);
  editor.chain().focus().command(({ tr }) => {
    tr.replaceRange(safeFrom, safeTo, slice);
    return true;
  }).run();
}

export function patchedDocumentMarkdown(current: string, result: NoteAIEditResult) {
  const target = result.target?.trim();
  if (!target) return null;
  const source = current.trimEnd();
  if (source.includes(target)) return source.replace(target, result.markdown);
  const looseTarget = target.trim();
  if (looseTarget && source.includes(looseTarget)) return source.replace(looseTarget, result.markdown);
  return null;
}
