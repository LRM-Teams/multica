import { describe, expect, it } from "vitest";
import { previewNoteWritebackContent, writebackHasOpenableEvidence } from "./writeback-preview";

describe("previewNoteWritebackContent", () => {
  it("appends markdown with a blank line", () => {
    expect(
      previewNoteWritebackContent("Hello", { action: "append", content: "World" }),
    ).toBe("Hello\n\nWorld");
  });

  it("patches the first matching target", () => {
    expect(
      previewNoteWritebackContent("keep old text", {
        action: "patch",
        content: "new",
        target: "old",
      }),
    ).toBe("keep new text");
  });

  it("replaces the whole page", () => {
    expect(
      previewNoteWritebackContent("old", { action: "replace_page", content: "fresh" }),
    ).toBe("fresh");
  });

  it("returns null when patch target is missing", () => {
    expect(
      previewNoteWritebackContent("nothing here", {
        action: "patch",
        content: "x",
        target: "missing",
      }),
    ).toBeNull();
  });
});

describe("writebackHasOpenableEvidence", () => {
  it("requires at least one issue or run-like evidence id", () => {
    expect(writebackHasOpenableEvidence([])).toBe(false);
    expect(writebackHasOpenableEvidence([{ type: "issue", id: "i1" }])).toBe(true);
    expect(writebackHasOpenableEvidence([{ type: "run", id: "r1" }])).toBe(true);
    expect(writebackHasOpenableEvidence([{ type: "note", id: "n1" }])).toBe(false);
  });
});
