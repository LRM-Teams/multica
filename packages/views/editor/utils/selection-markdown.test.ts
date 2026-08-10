// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";
import { Editor } from "@tiptap/core";
import { createEditorExtensions } from "../extensions";
import {
  serializeRangeToMarkdown,
  serializeSelectionToMarkdown,
} from "./selection-markdown";

let editor: Editor | null = null;

function makeEditor(markdown: string) {
  const element = document.createElement("div");
  document.body.appendChild(element);
  editor = new Editor({
    element,
    extensions: createEditorExtensions({}),
  });
  editor.commands.setContent(markdown, { contentType: "markdown" });
  return editor;
}

type Range = { from: number; to: number };

function findTextRange(ed: Editor, text: string): Range {
  let range: Range | null = null;
  ed.state.doc.descendants((node, pos) => {
    if (range) return false;
    if (!node.isText) return true;
    const index = node.text?.indexOf(text) ?? -1;
    if (index < 0) return true;
    range = { from: pos + index, to: pos + index + text.length };
    return false;
  });
  if (!range) throw new Error(`Text not found: ${text}`);
  return range;
}

function findNodeRange(ed: Editor, typeName: string): Range {
  let range: Range | null = null;
  ed.state.doc.descendants((node, pos) => {
    if (range || node.type.name !== typeName) return !range;
    range = { from: pos, to: pos + node.nodeSize };
    return false;
  });
  if (!range) throw new Error(`Node not found: ${typeName}`);
  return range;
}

afterEach(() => {
  editor?.destroy();
  editor = null;
});

describe("selection Markdown serialization", () => {
  it("keeps link marks in selected AI input", () => {
    const ed = makeEditor("See [docs](https://example.com) now");
    const range = findTextRange(ed, "docs");
    ed.commands.setTextSelection(range);

    expect(serializeSelectionToMarkdown(ed)).toBe("[docs](https://example.com)");
  });

  it("keeps block structure when serializing selected ranges", () => {
    const ed = makeEditor("Before\n\n```ts\nconst value = 1;\n```\n\nAfter");
    const range = findNodeRange(ed, "codeBlock");

    expect(serializeRangeToMarkdown(ed, range.from, range.to)).toBe(
      "```ts\nconst value = 1;\n```",
    );
  });
});
