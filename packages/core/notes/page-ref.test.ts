import { describe, expect, it } from "vitest";
import {
  NOTE_PAGE_REF_MAX,
  appendNotePageRef,
  attachNotePageRefs,
  encodeNotePageDragPayload,
  isNotePageId,
  parseNotePageDragPayload,
  removeNotePageRef,
} from "./page-ref";

describe("note page drag payload", () => {
  it("round-trips id and title", () => {
    const encoded = encodeNotePageDragPayload({ pageId: "p1", title: "工作介绍" });
    expect(parseNotePageDragPayload(encoded)).toEqual({
      pageId: "p1",
      title: "工作介绍",
    });
  });

  it("rejects empty or invalid payloads", () => {
    expect(parseNotePageDragPayload("")).toBeNull();
    expect(parseNotePageDragPayload("{")).toBeNull();
    expect(parseNotePageDragPayload(JSON.stringify({ title: "x" }))).toBeNull();
  });

  it("detects UUID page ids used as text/plain fallback", () => {
    expect(isNotePageId("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")).toBe(true);
    expect(isNotePageId("not-a-uuid")).toBe(false);
    expect(isNotePageId("12345")).toBe(false);
  });
});

describe("appendNotePageRef", () => {
  it("starts a bundle for the bubble page", () => {
    expect(
      appendNotePageRef(null, "bubble", { pageId: "p1", title: " A " }),
    ).toEqual({
      bubblePageId: "bubble",
      refs: [{ pageId: "p1", title: "A" }],
    });
  });

  it("dedupes by page id and refreshes the title", () => {
    const first = appendNotePageRef(null, "bubble", { pageId: "p1", title: "Old" });
    expect(appendNotePageRef(first, "bubble", { pageId: "p1", title: "New" })).toEqual({
      bubblePageId: "bubble",
      refs: [{ pageId: "p1", title: "New" }],
    });
  });

  it("caps the number of refs", () => {
    let bundle = appendNotePageRef(null, "bubble", { pageId: "p0", title: "0" });
    for (let i = 1; i < NOTE_PAGE_REF_MAX + 3; i++) {
      bundle = appendNotePageRef(bundle, "bubble", {
        pageId: `p${i}`,
        title: String(i),
      });
    }
    expect(bundle?.refs).toHaveLength(NOTE_PAGE_REF_MAX);
  });
});

describe("removeNotePageRef", () => {
  it("clears the bundle when the last ref is removed", () => {
    const bundle = appendNotePageRef(null, "bubble", { pageId: "p1", title: "A" });
    expect(removeNotePageRef(bundle, "p1")).toBeNull();
  });
});

describe("attachNotePageRefs", () => {
  it("prepends /notes/ paths then the question", () => {
    expect(
      attachNotePageRefs("总结一下", [
        { pageId: "aaa", title: "本周" },
        { pageId: "bbb", title: "计划" },
      ]),
    ).toBe(
      "> 笔记引用：本周（/notes/aaa）\n> 笔记引用：计划（/notes/bbb）\n\n总结一下",
    );
  });

  it("returns the question alone when there are no refs", () => {
    expect(attachNotePageRefs("  hi  ", [])).toBe("hi");
  });
});
