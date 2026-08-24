import { describe, expect, it } from "vitest";
import {
  NOTE_SELECTION_QUOTE_MAX_EXCERPTS,
  NOTE_SELECTION_QUOTE_PREVIEW_CHARS,
  NOTE_SELECTION_QUOTE_SEND_CHARS,
  abbreviateNoteSelection,
  appendNoteSelectionExcerpt,
  attachNoteSelectionQuote,
  removeNoteSelectionExcerpt,
  type NoteSelectionQuote,
} from "./selection-quote";

describe("abbreviateNoteSelection", () => {
  it("collapses whitespace and keeps short excerpts intact", () => {
    expect(abbreviateNoteSelection("  hello\n\nworld  ")).toBe("hello world");
  });

  it("truncates long excerpts with an ellipsis at the preview budget", () => {
    const text = "字".repeat(NOTE_SELECTION_QUOTE_PREVIEW_CHARS + 12);
    const abbreviated = abbreviateNoteSelection(text);
    expect(abbreviated.endsWith("…")).toBe(true);
    expect(abbreviated.length).toBe(NOTE_SELECTION_QUOTE_PREVIEW_CHARS);
    expect(abbreviated.startsWith("字".repeat(NOTE_SELECTION_QUOTE_PREVIEW_CHARS - 1))).toBe(true);
  });

  it("returns empty for blank input", () => {
    expect(abbreviateNoteSelection(" \n\t ")).toBe("");
  });
});

describe("appendNoteSelectionExcerpt", () => {
  it("starts a quote from the first selection", () => {
    expect(appendNoteSelectionExcerpt(null, "page-1", "  第一段  ", { now: 10, id: "e1" })).toEqual({
      pageId: "page-1",
      askedAt: 10,
      excerpts: [{ id: "e1", text: "第一段" }],
    });
  });

  it("appends a second selection on the same page", () => {
    const first = appendNoteSelectionExcerpt(null, "page-1", "第一段", { now: 10, id: "e1" });
    expect(appendNoteSelectionExcerpt(first, "page-1", "第二段", { now: 20, id: "e2" })).toEqual({
      pageId: "page-1",
      askedAt: 20,
      excerpts: [
        { id: "e1", text: "第一段" },
        { id: "e2", text: "第二段" },
      ],
    });
  });

  it("does not add the same excerpt twice", () => {
    const first = appendNoteSelectionExcerpt(null, "page-1", "第一段", { now: 10, id: "e1" });
    const again = appendNoteSelectionExcerpt(first, "page-1", "第一段", { now: 20, id: "e2" });
    expect(again?.excerpts).toEqual([{ id: "e1", text: "第一段" }]);
    expect(again?.askedAt).toBe(20);
  });

  it("replaces quotes when the selection belongs to another page", () => {
    const first = appendNoteSelectionExcerpt(null, "page-1", "第一段", { now: 10, id: "e1" });
    expect(appendNoteSelectionExcerpt(first, "page-2", "另一页", { now: 20, id: "e2" })).toEqual({
      pageId: "page-2",
      askedAt: 20,
      excerpts: [{ id: "e2", text: "另一页" }],
    });
  });

  it("stops appending after the excerpt cap", () => {
    let current: NoteSelectionQuote | null = null;
    for (let i = 0; i < NOTE_SELECTION_QUOTE_MAX_EXCERPTS + 3; i += 1) {
      current = appendNoteSelectionExcerpt(current, "page-1", `段${i}`, {
        now: i,
        id: `e${i}`,
      });
    }
    expect(current?.excerpts).toHaveLength(NOTE_SELECTION_QUOTE_MAX_EXCERPTS);
    expect(current?.excerpts[0]?.text).toBe("段0");
  });
});

describe("removeNoteSelectionExcerpt", () => {
  it("drops one excerpt and clears the quote when the last one is removed", () => {
    const quote: NoteSelectionQuote = {
      pageId: "page-1",
      askedAt: 1,
      excerpts: [
        { id: "e1", text: "第一段" },
        { id: "e2", text: "第二段" },
      ],
    };
    const afterFirst = removeNoteSelectionExcerpt(quote, "e1");
    expect(afterFirst?.excerpts).toEqual([{ id: "e2", text: "第二段" }]);
    expect(removeNoteSelectionExcerpt(afterFirst, "e2")).toBeNull();
  });
});

describe("attachNoteSelectionQuote", () => {
  it("prefixes the question with a markdown blockquote of the excerpt", () => {
    expect(attachNoteSelectionQuote("这句话想表达什么？", ["第一行\n第二行"])).toBe(
      "> 第一行\n> 第二行\n\n这句话想表达什么？",
    );
  });

  it("joins multiple excerpts as separate blockquotes", () => {
    expect(attachNoteSelectionQuote("对比这两段", ["第一段", "第二段"])).toBe(
      "> 第一段\n\n> 第二段\n\n对比这两段",
    );
  });

  it("returns only the question when every excerpt is blank", () => {
    expect(attachNoteSelectionQuote("你好", ["   ", ""])).toBe("你好");
  });

  it("caps a huge excerpt so the sent message stays bounded", () => {
    const excerpt = "a".repeat(NOTE_SELECTION_QUOTE_SEND_CHARS + 40);
    expect(attachNoteSelectionQuote("summarize", [excerpt])).toBe(
      `> ${"a".repeat(NOTE_SELECTION_QUOTE_SEND_CHARS - 1)}…\n\nsummarize`,
    );
  });
});
