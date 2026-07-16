import { describe, it, expect, afterEach } from "vitest";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import Link from "@tiptap/extension-link";
import { Markdown } from "@tiptap/markdown";
import Mention from "@tiptap/extension-mention";
import {
  createWordBoundaryAutolink,
  wordHref,
  autolinkFinalWord,
} from "./autolink-word-boundary";

// LinkExtension with the built-in autolink DISABLED (as #531 configures it).
const LinkExtension = Link.extend({ inclusive: false }).configure({
  openOnClick: false,
  autolink: false,
  linkOnPaste: true,
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
      createWordBoundaryAutolink(),
    ],
  });
  return editor;
}

/** Type char-by-char via separate transactions (each triggers appendTransaction). */
function type(ed: Editor, text: string) {
  for (const ch of text) {
    ed.view.dispatch(ed.state.tr.insertText(ch, ed.state.selection.from));
  }
}

function linkMarks(ed: Editor): { text: string; href: string }[] {
  const out: { text: string; href: string }[] = [];
  ed.state.doc.descendants((node) => {
    if (node.isText) {
      const m = node.marks.find((mk) => mk.type.name === "link");
      if (m) out.push({ text: node.text ?? "", href: m.attrs.href });
    }
    return true;
  });
  return out;
}

afterEach(() => {
  editor?.destroy();
  editor = null;
});

describe("wordHref", () => {
  it("preserves explicit https", () => {
    expect(wordHref("https://x.com/a")).toBe("https://x.com/a");
  });
  it("preserves explicit http (does not upgrade)", () => {
    expect(wordHref("http://x.com")).toBe("http://x.com");
  });
  it("adds https to a scheme-less host", () => {
    expect(wordHref("example.com/x")).toBe("https://example.com/x");
  });
  it("returns null for a non-URL word", () => {
    expect(wordHref("hello")).toBeNull();
  });
});

describe("word-boundary autolink (#531)", () => {
  it("anchor 1: typed full URL + space → exactly one complete mark", () => {
    const ed = makeEditor();
    type(ed, "iris https://wire.com/w ");
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]).toEqual({ text: "https://wire.com/w", href: "https://wire.com/w" });
  });

  it("anchor 2: prose typed after the URL stays outside the link", () => {
    const ed = makeEditor();
    type(ed, "https://wire.com/w tail");
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]!.text).toBe("https://wire.com/w");
    // The trailing " tail" must not be linked.
    expect(ed.getMarkdown()).toContain("[https://wire.com/w](https://wire.com/w) tail");
  });

  it("anchor 4: scheme-less host → https", () => {
    const ed = makeEditor();
    type(ed, "see example.com/x ");
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]).toEqual({ text: "example.com/x", href: "https://example.com/x" });
  });

  it("anchor 5: no half-mark appears mid-word (before the boundary)", () => {
    const ed = makeEditor();
    type(ed, "https://wire.com/w"); // no trailing space yet
    expect(linkMarks(ed)).toHaveLength(0); // nothing linked until the boundary
    type(ed, " ");
    expect(linkMarks(ed)).toHaveLength(1);
  });

  it("does not double-link an already-linked word", () => {
    const ed = makeEditor();
    type(ed, "https://wire.com/w "); // linked
    type(ed, "more "); // types after; should not touch the existing link
    expect(linkMarks(ed)).toHaveLength(1);
  });

  it("finalization: links the last word (no trailing space) before submit", () => {
    const ed = makeEditor();
    type(ed, "参见 https://x.com"); // URL is the last thing typed, no boundary
    expect(linkMarks(ed)).toHaveLength(0); // not linked yet
    const tr = autolinkFinalWord(ed.state);
    expect(tr).not.toBeNull();
    ed.view.dispatch(tr!);
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]).toEqual({ text: "https://x.com", href: "https://x.com" });
  });

  it("IME guard: does not fire while composing", () => {
    const ed = makeEditor();
    // Simulate an active composition (ProseMirror sets this between
    // compositionstart and compositionend).
    let composing = true;
    Object.defineProperty(ed.view, "composing", {
      configurable: true,
      get: () => composing,
    });
    type(ed, "https://wire.com/w ");
    expect(linkMarks(ed)).toHaveLength(0); // suppressed mid-composition
    // After composition ends, a normally-typed URL links as usual.
    composing = false;
    type(ed, "https://ok.com/a ");
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]!.href).toBe("https://ok.com/a");
  });

  it("trailing punctuation stays outside the link (https://x.com, )", () => {
    const ed = makeEditor();
    type(ed, "see https://x.com, ");
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]).toEqual({ text: "https://x.com", href: "https://x.com" });
  });

  it("glued CJK particle stays outside the link (https://x.com吗 )", () => {
    const ed = makeEditor();
    type(ed, "见 https://x.com吗 ");
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]).toEqual({ text: "https://x.com", href: "https://x.com" });
  });

  it("parenthesized URL links the URL, parens outside", () => {
    const ed = makeEditor();
    type(ed, "(https://x.com) ");
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]!.text).toBe("https://x.com");
  });

  it("Enter / paragraph boundary links the previous block's URL", () => {
    const ed = makeEditor();
    type(ed, "https://x.com");
    expect(linkMarks(ed)).toHaveLength(0); // nothing until the boundary
    ed.commands.splitBlock(); // Enter
    const marks = linkMarks(ed);
    expect(marks).toHaveLength(1);
    expect(marks[0]!.text).toBe("https://x.com");
  });

  it("undo removes the autolink mark with the typed boundary", () => {
    const ed = makeEditor();
    type(ed, "https://x.com/w ");
    expect(linkMarks(ed)).toHaveLength(1);
    ed.commands.undo();
    expect(linkMarks(ed)).toHaveLength(0);
  });

  it("mention atom before the URL: word range stops at the atom, URL clean", () => {
    const element = document.createElement("div");
    document.body.appendChild(element);
    editor = new Editor({
      element,
      extensions: [
        StarterKit.configure({ link: false }),
        LinkExtension,
        Mention,
        Markdown.configure({ indentation: { style: "space", size: 3 } }),
        createWordBoundaryAutolink(),
      ],
    });
    editor.commands.insertContent({ type: "mention", attrs: { id: "u1", label: "iris" } });
    type(editor, "https://wire.com/w ");
    const marks = linkMarks(editor);
    expect(marks).toHaveLength(1);
    expect(marks[0]).toEqual({ text: "https://wire.com/w", href: "https://wire.com/w" });
  });
});
