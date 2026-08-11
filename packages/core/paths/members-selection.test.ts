import { describe, it, expect } from "vitest";
import {
  appendQueryParams,
  encodeMembersSelection,
  membersPathWithSelection,
  membersSelectionQueryKey,
  parseMembersSelectionFromSearch,
  parseMembersSelectionParam,
} from "./members-selection";

describe("members selection query", () => {
  it("uses a stable query key", () => {
    expect(membersSelectionQueryKey()).toBe("member");
  });

  it("encodes agent and user selections", () => {
    expect(encodeMembersSelection("agent", "a-1")).toBe(
      "member=agent%3Aa-1",
    );
    expect(encodeMembersSelection("user", "u-1")).toBe("member=user%3Au-1");
    expect(encodeMembersSelection("agent", "  ")).toBe("");
  });

  it("parses valid member params and rejects garbage", () => {
    expect(parseMembersSelectionParam("agent:a-1")).toEqual({
      kind: "agent",
      id: "a-1",
    });
    expect(parseMembersSelectionParam("user:u-9")).toEqual({
      kind: "user",
      id: "u-9",
    });
    expect(parseMembersSelectionParam("agent:")).toBeNull();
    expect(parseMembersSelectionParam("bot:x")).toBeNull();
    expect(parseMembersSelectionParam("")).toBeNull();
    expect(parseMembersSelectionParam(null)).toBeNull();
  });

  it("parses from search string and URLSearchParams", () => {
    expect(
      parseMembersSelectionFromSearch("member=agent%3Aa-1&x=1"),
    ).toEqual({ kind: "agent", id: "a-1" });
    expect(
      parseMembersSelectionFromSearch("?member=user:u-2"),
    ).toEqual({ kind: "user", id: "u-2" });
    const params = new URLSearchParams("member=agent:z");
    expect(parseMembersSelectionFromSearch(params)).toEqual({
      kind: "agent",
      id: "z",
    });
  });

  it("builds members path with selection", () => {
    expect(membersPathWithSelection("/acme/members", "agent", "a1")).toBe(
      "/acme/members?member=agent%3Aa1",
    );
    expect(
      membersPathWithSelection("/acme/members?member=user:old", "user", "u2"),
    ).toBe("/acme/members?member=user%3Au2");
  });

  it("appends extra query params without a second ?", () => {
    const base = membersPathWithSelection("/acme/members", "agent", "a1");
    expect(appendQueryParams(base, { run: "run-9" })).toBe(
      "/acme/members?member=agent%3Aa1&run=run-9",
    );
    expect(appendQueryParams("/acme/members", { tab: "honor" })).toBe(
      "/acme/members?tab=honor",
    );
  });
});
