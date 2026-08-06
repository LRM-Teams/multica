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

  it("defaults message card_kind and meta when missing", () => {
    const raw = {
      session: { id: "s1", workspace_id: "w1" },
      fleet: { id: "f1", workspace_id: "w1", members: [] },
      messages: [{ id: "m1", body: "kickoff", sender_type: "system" }],
    };
    const parsed = parseWithFallback(raw, ResearchSessionSnapshotSchema, EMPTY_RESEARCH_SNAPSHOT, {
      endpoint: "test",
    });
    expect(parsed.messages[0]?.card_kind).toBe("chat");
    expect(parsed.messages[0]?.meta).toEqual({});
  });

  it("keeps process card fields", () => {
    const raw = {
      session: { id: "s1", workspace_id: "w1" },
      fleet: { id: "f1", workspace_id: "w1", members: [] },
      messages: [
        {
          id: "m1",
          body: "调研团已就位",
          sender_type: "system",
          card_kind: "process",
          meta: { op: "session_kickoff" },
        },
      ],
    };
    const parsed = parseWithFallback(raw, ResearchSessionSnapshotSchema, EMPTY_RESEARCH_SNAPSHOT, {
      endpoint: "test",
    });
    expect(parsed.messages[0]?.card_kind).toBe("process");
    expect((parsed.messages[0]?.meta as { op?: string })?.op).toBe("session_kickoff");
  });
});
