import { describe, expect, it } from "vitest";
import {
  actorHandleSearchRank,
  matchesActorIdentitySearch,
  normalizeActorSearchQuery,
} from "./search";

describe("normalizeActorSearchQuery", () => {
  it("normalizes queries for actor search", () => {
    expect(normalizeActorSearchQuery("  @Alice ")).toBe("alice");
    expect(normalizeActorSearchQuery("")).toBe("");
  });
});

describe("matchesActorIdentitySearch", () => {
  it("matches display name and handle", () => {
    expect(matchesActorIdentitySearch("Alice Zhang", "alice", "alice")).toBe(true);
    expect(matchesActorIdentitySearch("Alice Zhang", "alice", "zhang")).toBe(true);
    expect(matchesActorIdentitySearch("Alice Zhang", "alice", "bob")).toBe(false);
  });

  it("matches @-prefixed queries against handles", () => {
    expect(matchesActorIdentitySearch("Aegis", "agent_aegis", "@agent")).toBe(true);
  });

  it("matches extra fields", () => {
    expect(
      matchesActorIdentitySearch("Alice Zhang", "alice", "alice@example.com", {
        extra: ["alice@example.com"],
      }),
    ).toBe(true);
  });

  it("uses extended matchers", () => {
    const extendedMatch = (text: string, query: string) => text === "李云龙" && query === "lyl";
    expect(
      matchesActorIdentitySearch("李云龙", "liyunlong", "lyl", { extendedMatch }),
    ).toBe(true);
    expect(
      matchesActorIdentitySearch("Alice", "alice", "lyl", { extendedMatch }),
    ).toBe(false);
  });

  it("returns true for empty query", () => {
    expect(matchesActorIdentitySearch("Alice", "alice", "")).toBe(true);
    expect(matchesActorIdentitySearch("Alice", "alice", "   ")).toBe(true);
  });
});

describe("actorHandleSearchRank", () => {
  it("ranks handle matches for sorting", () => {
    expect(actorHandleSearchRank("atlas", "atlas")).toBe(0);
    expect(actorHandleSearchRank("atlas", "atl")).toBe(1);
    expect(actorHandleSearchRank("agent_atlas", "atlas")).toBe(2);
    expect(actorHandleSearchRank("zeta", "atlas")).toBe(3);
    expect(actorHandleSearchRank("atlas", "")).toBe(3);
  });
});