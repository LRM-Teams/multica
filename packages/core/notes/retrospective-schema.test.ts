import { describe, expect, it } from "vitest";
import {
  CreateNoteRetrospectiveResponseSchema,
  EMPTY_CREATE_NOTE_RETROSPECTIVE_RESPONSE,
} from "../api/schemas";
import { parseWithFallback } from "../api/schema";

describe("CreateNoteRetrospectiveResponseSchema (S4-S1/S4-S3)", () => {
  it("falls back on malformed payload without throwing", () => {
    const parsed = parseWithFallback(
      { page: null, fact_count: "nope" },
      CreateNoteRetrospectiveResponseSchema,
      EMPTY_CREATE_NOTE_RETROSPECTIVE_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed.page.id).toBe("");
    expect(parsed.fact_count).toBe(0);
    expect(parsed.sources_used).toEqual([]);
    expect(parsed.composition).toBe("");
    expect(parsed.layers_used).toEqual([]);
  });

  it("keeps page id and source lists from a valid payload", () => {
    const parsed = parseWithFallback(
      {
        page: {
          id: "page-1",
          workspace_id: "ws",
          parent_id: null,
          owner_user_id: "u",
          title: "回顾 2026-08-13",
          content: "body",
          sort_key: "1",
          share_user_ids: [],
          can_manage_shares: true,
          created_at: "t",
          updated_at: "t",
          deleted_at: null,
        },
        window: {
          kind: "month",
          timezone: "UTC",
          start: "2026-08-01T00:00:00Z",
          end: "2026-09-01T00:00:00Z",
          label: "2026-08",
        },
        sources_used: ["issue_activity"],
        sources_empty: ["touched_notes"],
        sources_skipped: ["agent_runs"],
        fact_count: 3,
        composition: "layered_summaries",
        layers_used: ["week", "day"],
        child_pages_used: ["page-day"],
      },
      CreateNoteRetrospectiveResponseSchema,
      EMPTY_CREATE_NOTE_RETROSPECTIVE_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed.page.id).toBe("page-1");
    expect(parsed.page.title).toContain("回顾");
    expect(parsed.sources_used).toEqual(["issue_activity"]);
    expect(parsed.sources_skipped).toEqual(["agent_runs"]);
    expect(parsed.fact_count).toBe(3);
    expect(parsed.composition).toBe("layered_summaries");
    expect(parsed.layers_used).toEqual(["week", "day"]);
    expect(parsed.child_pages_used).toEqual(["page-day"]);
  });

  it("accepts null source arrays without falling back to empty page", () => {
    const parsed = parseWithFallback(
      {
        page: {
          id: "page-empty",
          workspace_id: "ws",
          parent_id: null,
          owner_user_id: "u",
          title: "回顾 2099-01-01",
          content: "empty",
          sort_key: "1",
          share_user_ids: [],
          can_manage_shares: true,
          created_at: "t",
          updated_at: "t",
          deleted_at: null,
        },
        window: {
          kind: "day",
          timezone: "UTC",
          start: "2099-01-01T00:00:00Z",
          end: "2099-01-02T00:00:00Z",
          label: "2099-01-01",
        },
        sources_used: null,
        sources_empty: null,
        sources_skipped: null,
        fact_count: 0,
        composition: "day_raw",
        layers_used: null,
        child_pages_used: null,
      },
      CreateNoteRetrospectiveResponseSchema,
      EMPTY_CREATE_NOTE_RETROSPECTIVE_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed.page.id).toBe("page-empty");
    expect(parsed.sources_used).toEqual([]);
    expect(parsed.sources_empty).toEqual([]);
    expect(parsed.layers_used).toEqual([]);
  });
});
