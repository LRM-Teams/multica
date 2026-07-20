import { describe, it, expect } from "vitest";
import type { AgentActivityEventsPage } from "../types";
import { agentActivityEventsOptions } from "./queries";

// #620: the Activity event stream is a cursor-paginated infinite query so the
// timeline can load OLDER pages as the reader scrolls up (a high-frequency
// agent's newer rows had pushed old command rows past the fixed first ~50). The
// contract that drives "is there an older page, and what cursor fetches it" is
// `getNextPageParam`, so pin it.
describe("agentActivityEventsOptions (cursor pagination)", () => {
  const opts = agentActivityEventsOptions("agent-1") as unknown as {
    initialPageParam: string | null;
    getNextPageParam: (page: AgentActivityEventsPage) => string | undefined;
    queryKey: readonly unknown[];
  };

  const page = (over: Partial<AgentActivityEventsPage>): AgentActivityEventsPage => ({
    events: [],
    limit: 50,
    has_more: false,
    next_cursor: null,
    ...over,
  });

  it("starts from no cursor (newest first page)", () => {
    expect(opts.initialPageParam).toBeNull();
  });

  it("is keyed per agent", () => {
    expect(opts.queryKey).toEqual(["agent-activity-events", "agent-1"]);
  });

  it("returns the next cursor when more (older) pages remain", () => {
    expect(opts.getNextPageParam(page({ has_more: true, next_cursor: "cur-42" }))).toBe(
      "cur-42",
    );
  });

  it("stops (undefined) when no more pages", () => {
    expect(
      opts.getNextPageParam(page({ has_more: false, next_cursor: "cur-42" })),
    ).toBeUndefined();
  });

  it("stops (undefined) when has_more is true but the cursor is missing", () => {
    expect(
      opts.getNextPageParam(page({ has_more: true, next_cursor: null })),
    ).toBeUndefined();
  });
});
