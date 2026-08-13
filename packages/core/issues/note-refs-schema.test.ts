import { describe, expect, it } from "vitest";
import { IssueNoteRefListResponseSchema, EMPTY_ISSUE_NOTE_REF_LIST } from "../api/schemas";
import { parseWithFallback } from "../api/schema";

describe("IssueNoteRefListResponseSchema (S3-R5b)", () => {
  it("parses linked notes", () => {
    const parsed = IssueNoteRefListResponseSchema.parse({
      notes: [{ id: "n1", title: "Brief", created_at: "2026-08-13T00:00:00Z" }],
    });
    expect(parsed.notes).toHaveLength(1);
    expect(parsed.notes[0]?.id).toBe("n1");
  });

  it("falls back on malformed payload", () => {
    const parsed = parseWithFallback(
      { notes: null },
      IssueNoteRefListResponseSchema,
      EMPTY_ISSUE_NOTE_REF_LIST,
      { endpoint: "test" },
    );
    expect(parsed).toEqual(EMPTY_ISSUE_NOTE_REF_LIST);
  });
});
