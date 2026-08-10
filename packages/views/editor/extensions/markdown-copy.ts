/**
 * Markdown copy extension — make the clipboard's text/plain channel carry
 * Markdown source instead of plain textContent.
 *
 * Symmetric to markdown-paste.ts:
 *   paste:  text/plain  →  editor.markdown.parse  →  doc
 *   copy:   slice       →  editor.markdown.serialize  →  text/plain
 *
 * Why: ProseMirror's default clipboardTextSerializer calls Slice.textBetween,
 * which flattens every node to its inner text. Headings, lists, code blocks,
 * mentions, file cards — all lose their Markdown markers. Pasting into VS
 * Code, terminals, or messaging apps then sees only naked text.
 *
 * The text/html channel is left at ProseMirror's default so pasting back
 * into another ProseMirror editor still preserves exact node structure via
 * data-pm-slice.
 */
import { Extension } from "@tiptap/core";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import type { Slice } from "@tiptap/pm/model";
import { serializeSliceToMarkdown } from "../utils/selection-markdown";

export function createMarkdownCopyExtension() {
  return Extension.create({
    name: "markdownCopy",
    addProseMirrorPlugins() {
      const { editor } = this;

      return [
        new Plugin({
          key: new PluginKey("markdownCopy"),
          props: {
            clipboardTextSerializer(slice: Slice) {
              return serializeSliceToMarkdown(editor, slice);
            },
          },
        }),
      ];
    },
  });
}
