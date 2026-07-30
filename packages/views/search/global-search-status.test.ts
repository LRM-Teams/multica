// @vitest-environment node
import { describe, expect, it } from "vitest";
import { deriveGlobalSearchStatus, scopeCount } from "./global-search-status";
import type { WorkspaceSearchResponse } from "@multica/core/types";

const empty: WorkspaceSearchResponse = {
  query: "x",
  scope: "all",
  counts: { messages: 0, channels: 0, dms: 0, people: 0 },
  messages: [],
  channels: [],
  dms: [],
  people: [],
};

function resp(over: Partial<WorkspaceSearchResponse>): WorkspaceSearchResponse {
  return { ...empty, ...over };
}

describe("deriveGlobalSearchStatus", () => {
  it("idle when query is blank", () => {
    expect(
      deriveGlobalSearchStatus({ query: "   ", isFetching: false, isLoading: false, isError: false, data: undefined }),
    ).toBe("idle");
  });

  it("loading on the first fetch with no data yet", () => {
    expect(
      deriveGlobalSearchStatus({ query: "foo", isFetching: true, isLoading: true, isError: false, data: undefined }),
    ).toBe("loading");
  });

  it("success when results present", () => {
    const data = resp({ messages: [{ result_type: "message", message_id: "m1", channel_id: "c", channel_name: "x", channel_kind: "group", hit_count: 1, author_name: "a", content: "hi", snippet: "hi", created_at: "" }] });
    expect(
      deriveGlobalSearchStatus({ query: "foo", isFetching: false, isLoading: false, isError: false, data }),
    ).toBe("success");
  });

  it("empty when query returned no visible matches", () => {
    expect(
      deriveGlobalSearchStatus({ query: "foo", isFetching: false, isLoading: false, isError: false, data: empty }),
    ).toBe("empty");
  });

  it("error wins over stale data (no silent fallback)", () => {
    const data = resp({ messages: [{ result_type: "message", message_id: "m1", channel_id: "c", channel_name: "x", channel_kind: "group", hit_count: 1, author_name: "a", content: "hi", snippet: "hi", created_at: "" }] });
    expect(
      deriveGlobalSearchStatus({ query: "foo", isFetching: true, isLoading: false, isError: true, data }),
    ).toBe("error");
  });

});

describe("scopeCount", () => {
  const data = resp({ counts: { messages: 5, channels: 2, dms: 3, people: 1 } });

  it("returns the per-scope count", () => {
    expect(scopeCount(data, "messages")).toBe(5);
    expect(scopeCount(data, "channels")).toBe(2);
    expect(scopeCount(data, "dms")).toBe(3);
    expect(scopeCount(data, "people")).toBe(1);
  });

  it("all sums every scope", () => {
    expect(scopeCount(data, "all")).toBe(11);
  });

  it("returns 0 for undefined data", () => {
    expect(scopeCount(undefined, "all")).toBe(0);
  });
});
