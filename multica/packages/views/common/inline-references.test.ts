// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { MessagePart } from "@multica/core/types";
import { projectInlineReferences, type InlineSegment } from "./inline-references";

function mention(start: number, end: number, over: Partial<MessagePart> = {}): MessagePart {
  return {
    type: "reference",
    ref_type: "mention",
    ref_subtype: "agent",
    ref_id: "agent-1",
    label: "@Felix",
    content_start_utf16: start,
    content_end_utf16: end,
    ...over,
  } as MessagePart;
}

function issueRef(start: number, end: number): MessagePart {
  return {
    type: "reference",
    ref_type: "issue-ref",
    ref_subtype: "issue",
    ref_id: "issue-uuid",
    label: "MUL-123",
    content_start_utf16: start,
    content_end_utf16: end,
  } as MessagePart;
}

const kinds = (segs: InlineSegment[]) => segs.map((s) => s.kind);
const texts = (segs: InlineSegment[]) => segs.map((s) => s.text);

describe("projectInlineReferences", () => {
  it("returns nothing for empty content", () => {
    expect(projectInlineReferences("", [mention(0, 6)])).toEqual([]);
    expect(projectInlineReferences(null, undefined)).toEqual([]);
  });

  it("returns one text segment when there are no anchored references", () => {
    expect(projectInlineReferences("hello world", [])).toEqual([
      { kind: "text", text: "hello world" },
    ]);
    // A sticker part is not a reference → still just text.
    expect(
      projectInlineReferences("hi", [{ type: "sticker", sticker_id: "wave" }]),
    ).toEqual([{ kind: "text", text: "hi" }]);
  });

  it("splits text around a single reference span (before + ref + after)", () => {
    // "hey @Felix now" — @Felix at [4,10)
    const segs = projectInlineReferences("hey @Felix now", [mention(4, 10)]);
    expect(kinds(segs)).toEqual(["text", "reference", "text"]);
    expect(texts(segs)).toEqual(["hey ", "@Felix", " now"]);
    expect(segs[1]).toMatchObject({ kind: "reference", ref: { ref_type: "mention" }, text: "@Felix" });
  });

  it("no leading/trailing text segment when the ref sits at the edge", () => {
    const start = projectInlineReferences("@Felix hi", [mention(0, 6)]);
    expect(kinds(start)).toEqual(["reference", "text"]);
    const end = projectInlineReferences("hi @Felix", [mention(3, 9)]);
    expect(kinds(end)).toEqual(["text", "reference"]);
  });

  it("orders multiple references by span and keeps interleaved text", () => {
    // "@Felix see MUL-123 ok" : @Felix [0,6), MUL-123 [11,18)
    const segs = projectInlineReferences("@Felix see MUL-123 ok", [
      issueRef(11, 18),
      mention(0, 6), // deliberately out of order
    ]);
    expect(kinds(segs)).toEqual(["reference", "text", "reference", "text"]);
    expect(texts(segs)).toEqual(["@Felix", " see ", "MUL-123", " ok"]);
    expect(segs[0]).toMatchObject({ ref: { ref_type: "mention" } });
    expect(segs[2]).toMatchObject({ ref: { ref_type: "issue-ref" } });
  });

  it("drops overlapping spans (keep earliest) and never double-renders characters", () => {
    // two refs both claiming [4,10) and [7,12) — keep the first, drop the overlap
    const segs = projectInlineReferences("hey @Felix now", [mention(4, 10), mention(7, 12)]);
    expect(kinds(segs)).toEqual(["text", "reference", "text"]);
    expect(texts(segs)).toEqual(["hey ", "@Felix", " now"]);
  });

  it("drops out-of-range or inverted spans → plain text (bad anchor degrades safely)", () => {
    expect(projectInlineReferences("hello", [mention(2, 99)])).toEqual([
      { kind: "text", text: "hello" },
    ]);
    expect(projectInlineReferences("hello", [mention(4, 2)])).toEqual([
      { kind: "text", text: "hello" },
    ]);
    expect(projectInlineReferences("hello", [mention(-1, 3)])).toEqual([
      { kind: "text", text: "hello" },
    ]);
  });

  it("ignores references without a span (un-migrated historical rows stay plain text)", () => {
    const noSpan = { type: "reference", ref_type: "mention", ref_subtype: "agent", ref_id: "a", label: "@x" } as MessagePart;
    expect(projectInlineReferences("hi @x", [noSpan])).toEqual([{ kind: "text", text: "hi @x" }]);
  });

  describe("emphasis straddling a reference span (#635)", () => {
    it("folds a **bold**-wrapped issue ref into the reference segment's emphasis", () => {
      // "Fixed **MUL-123** today" — MUL-123 at [8,15)
      const segs = projectInlineReferences("Fixed **MUL-123** today", [issueRef(8, 15)]);
      expect(segs).toEqual([
        { kind: "text", text: "Fixed " },
        { kind: "reference", ref: expect.objectContaining({ ref_type: "issue-ref" }), text: "MUL-123", emphasis: "strong" },
        { kind: "text", text: " today" },
      ]);
    });

    it("folds a *em*-wrapped mention, consuming the markers (not left as literal text)", () => {
      // "see *@Felix* now" — @Felix at [5,11)
      const segs = projectInlineReferences("see *@Felix* now", [mention(5, 11)]);
      expect(segs[1]).toMatchObject({ kind: "reference", text: "@Felix", emphasis: "em" });
      expect(texts(segs)).toEqual(["see ", "@Felix", " now"]);
      expect(segs[0]).toEqual({ kind: "text", text: "see " });
      expect(segs[2]).toEqual({ kind: "text", text: " now" });
    });

    it("folds __bold__ (underscore form) the same way as **bold**", () => {
      const segs = projectInlineReferences("__MUL-123__ done", [issueRef(2, 9)]);
      expect(segs[0]).toMatchObject({ emphasis: "strong" });
      expect(texts(segs)).toEqual(["MUL-123", " done"]);
    });

    it("does NOT fold when the marker only appears on one side", () => {
      // "Fixed **MUL-123 today" — closing ** is missing entirely.
      const segs = projectInlineReferences("Fixed **MUL-123 today", [issueRef(8, 15)]);
      expect(segs.every((s) => s.kind !== "reference" || !s.emphasis)).toBe(true);
      expect(texts(segs)).toEqual(["Fixed **", "MUL-123", " today"]);
    });

    it("does NOT fold across whitespace (matches CommonMark's flanking rule)", () => {
      // "** MUL-123 **" has spaces immediately inside the markers — not valid
      // emphasis in CommonMark either, so this must stay untouched/literal.
      const segs = projectInlineReferences("** MUL-123 ** now", [issueRef(3, 10)]);
      expect(segs.every((s) => s.kind !== "reference" || !s.emphasis)).toBe(true);
      expect(texts(segs)).toEqual(["** ", "MUL-123", " ** now"]);
    });

    it("leaves an ordinary reference with no adjacent markers untouched", () => {
      const segs = projectInlineReferences("hey @Felix now", [mention(4, 10)]);
      expect(segs[1]).toEqual({ kind: "reference", ref: expect.objectContaining({ ref_type: "mention" }), text: "@Felix" });
    });
  });
});
