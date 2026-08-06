import { describe, it, expect, afterEach } from "vitest";
import { Editor } from "@tiptap/core";
import { createEditorExtensions } from "./index";

// #531 (Frank's call): the composer never auto-links typed or pasted URLs —
// they stay plain text in the editor. Bare URLs become clickable on the READ
// side (preprocessLinks), not in the input.
//
// This builds the REAL composer via `createEditorExtensions({})` (the full
// factory, 20+ extensions) rather than a hand-picked subset — so it also
// guards against a *future* extension re-introducing autolinking into the
// factory, not just someone flipping LinkExtension's `autolink` back to true.
// (A subset harness missed exactly that class of factory-level regression.)
let editor: Editor | null = null;
function makeEditor(): Editor {
  const element = document.createElement("div");
  document.body.appendChild(element);
  editor = new Editor({
    element,
    extensions: createEditorExtensions({}),
  });
  return editor;
}
function type(ed: Editor, text: string) {
  for (const ch of text) {
    ed.view.dispatch(ed.state.tr.insertText(ch, ed.state.selection.from));
  }
}
function linkMarkCount(ed: Editor): number {
  let n = 0;
  ed.state.doc.descendants((node) => {
    if (node.isText && node.marks.some((m) => m.type.name === "link")) n++;
    return true;
  });
  return n;
}

afterEach(() => {
  editor?.destroy();
  editor = null;
});

describe("composer does not autolink typed URLs (#531)", () => {
  it("a typed URL followed by a space stays plain text (no link mark)", () => {
    const ed = makeEditor();
    type(ed, "see https://wire.com/w tail");
    expect(linkMarkCount(ed)).toBe(0);
    expect(ed.getMarkdown()).toBe("see https://wire.com/w tail");
  });

  it("a URL typed then Enter stays plain text", () => {
    const ed = makeEditor();
    type(ed, "https://x.com");
    ed.commands.splitBlock();
    expect(linkMarkCount(ed)).toBe(0);
  });

  it("still preserves an existing markdown link loaded into the editor", () => {
    // Historical messages with real links must still render/edit — the link
    // mark stays in the schema; only auto-detection is off.
    const ed = makeEditor();
    ed.commands.setContent("[docs](https://x.com/docs)", { contentType: "markdown" });
    expect(linkMarkCount(ed)).toBe(1);
    expect(ed.getMarkdown()).toContain("[docs](https://x.com/docs)");
  });
});
