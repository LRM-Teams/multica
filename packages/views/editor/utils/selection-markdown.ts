import type { Editor } from "@tiptap/core";
import type { Slice } from "@tiptap/pm/model";

// Blob URLs are process-local; never send them to AI prompts or clipboards.
const BLOB_IMAGE_RE = /!\[[^\]]*\]\(blob:[^)]*\)\n?/g;

function cleanSerializedMarkdown(markdown: string) {
  return markdown.replace(BLOB_IMAGE_RE, "").replace(/\n+$/, "");
}

function fallbackSliceText(slice: Slice) {
  return slice.content.textBetween(0, slice.content.size, "\n\n");
}

export function serializeSliceToMarkdown(editor: Editor, slice: Slice) {
  const fallback = () => fallbackSliceText(slice);
  if (!editor.markdown) return fallback();
  try {
    // Wrap slice content in a temp doc so the Markdown serializer preserves
    // block nodes and marks instead of flattening them like textBetween().
    const doc = editor.schema.topNodeType.create(null, slice.content);
    return cleanSerializedMarkdown(editor.markdown.serialize(doc.toJSON()));
  } catch {
    return fallback();
  }
}

export function serializeRangeToMarkdown(editor: Editor, from: number, to: number) {
  const docSize = editor.state.doc.content.size;
  const safeFrom = Math.max(0, Math.min(from, docSize));
  const safeTo = Math.max(safeFrom, Math.min(to, docSize));
  if (safeFrom === safeTo) return "";
  return serializeSliceToMarkdown(editor, editor.state.doc.slice(safeFrom, safeTo));
}

export function serializeSelectionToMarkdown(editor: Editor) {
  const { selection } = editor.state;
  if (selection.empty) return "";
  return serializeSliceToMarkdown(editor, selection.content());
}
