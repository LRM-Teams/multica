import { describe, expect, it, vi, beforeEach } from "vitest";
import { extractIssueIdsFromNoteMarkdown, syncNotePageIssueRefsFromContent } from "./issue-refs";

vi.mock("../api", () => ({
  api: {
    listNotePageIssueRefs: vi.fn(),
    createNotePageIssueRef: vi.fn(),
    deleteNotePageIssueRef: vi.fn(),
  },
}));

import { api } from "../api";

describe("extractIssueIdsFromNoteMarkdown", () => {
  it("extracts unique issue UUIDs from mention links", () => {
    const a = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
    const b = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";
    const md = `See [MUL-1](mention://issue/${a}) and [MUL-2](mention://issue/${b}) again [MUL-1](mention://issue/${a}).`;
    expect(extractIssueIdsFromNoteMarkdown(md).sort()).toEqual([a, b].sort());
  });

  it("ignores non-issue mentions and malformed ids", () => {
    expect(
      extractIssueIdsFromNoteMarkdown(
        "[x](mention://member/u1) [#ch](mention://channel/c1) [bad](mention://issue/not-a-uuid)",
      ),
    ).toEqual([]);
  });
});

describe("syncNotePageIssueRefsFromContent", () => {
  beforeEach(() => {
    vi.mocked(api.listNotePageIssueRefs).mockReset();
    vi.mocked(api.createNotePageIssueRef).mockReset();
    vi.mocked(api.deleteNotePageIssueRef).mockReset();
  });

  it("creates missing refs and deletes removed ones", async () => {
    const keep = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
    const add = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";
    const remove = "cccccccc-cccc-cccc-cccc-cccccccccccc";

    vi.mocked(api.listNotePageIssueRefs).mockResolvedValue({
      refs: [
        {
          type: "issue",
          page_id: "p1",
          issue_id: keep,
          workspace_id: "w1",
          identifier: "MUL-1",
          title: "Keep",
          number: 1,
          created_at: "2026-01-01T00:00:00Z",
        },
        {
          type: "issue",
          page_id: "p1",
          issue_id: remove,
          workspace_id: "w1",
          identifier: "MUL-3",
          title: "Remove",
          number: 3,
          created_at: "2026-01-01T00:00:00Z",
        },
      ],
    });
    vi.mocked(api.createNotePageIssueRef).mockResolvedValue({
      type: "issue",
      page_id: "p1",
      issue_id: add,
      workspace_id: "w1",
      identifier: "MUL-2",
      title: "Add",
      number: 2,
      created_at: "2026-01-01T00:00:00Z",
    });
    vi.mocked(api.deleteNotePageIssueRef).mockResolvedValue(undefined);

    const result = await syncNotePageIssueRefsFromContent(
      "p1",
      `[K](mention://issue/${keep}) [A](mention://issue/${add})`,
    );

    expect(api.createNotePageIssueRef).toHaveBeenCalledWith("p1", { issue_id: add });
    expect(api.deleteNotePageIssueRef).toHaveBeenCalledWith("p1", remove);
    expect(result).toEqual({ added: 1, removed: 1, errors: [] });
  });
});
