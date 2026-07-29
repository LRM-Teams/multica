import { describe, expect, it } from "vitest";
import {
  EMPTY_RESEARCH_SNAPSHOT,
  ListResearchSessionsResponseSchema,
  ResearchSessionSnapshotSchema,
} from "./schemas";
import { parseWithFallback } from "../api/schema";

describe("research schemas", () => {
  it("accepts a minimal valid snapshot", () => {
    const raw = {
      session: { id: "s1", workspace_id: "w1", title: "t", goal: "g" },
      fleet: { id: "f1", workspace_id: "w1", members: [] },
      nodes: [],
      edges: [],
      sources: [],
      report: null,
      evals: [],
      messages: [],
    };
    const parsed = parseWithFallback(raw, ResearchSessionSnapshotSchema, EMPTY_RESEARCH_SNAPSHOT, {
      endpoint: "test",
    });
    expect(parsed.session.id).toBe("s1");
    expect(parsed.fleet.id).toBe("f1");
  });

  it("falls back on malformed list response", () => {
    const parsed = parseWithFallback(
      { sessions: "nope" },
      ListResearchSessionsResponseSchema,
      { sessions: [] },
      { endpoint: "test" },
    );
    expect(parsed.sessions).toEqual([]);
  });
});
