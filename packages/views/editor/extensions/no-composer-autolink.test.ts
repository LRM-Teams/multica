import { describe, it, expect, afterEach } from "vitest";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import Link from "@tiptap/extension-link";
import { Markdown } from "@tiptap/markdown";

// #531 (Frank's call): the composer never auto-links typed or pasted URLs —
// they stay plain text in the editor. Bare URLs become clickable on the READ
// side (preprocessLinks), not in the input. This test pins the LinkExtension
// config (autolink:false + linkOnPaste:false, no word-boundary plugin) so a URL
// typed into the composer produces no link mark.
const LinkExtension = Link.extend({ inclusive: false }).configure({
  openOnClick: false,
  autolink: false,
  linkOnPaste: false,
  defaultProtocol: "https",
});

let editor: Editor | null = null;
function makeEditor(): Editor {
  const element = document.createElement("div");
  document.body.appendChild(element);
  editor = new Editor({
    element,
    extensions: [
      StarterKit.configure({ link: false }),
      LinkExtension,
      Markdown.configure({ indentation: { style: "space", size: 3 } }),
    ],
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
