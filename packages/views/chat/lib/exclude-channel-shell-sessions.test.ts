// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  excludeChannelShellSessions,
  isChannelShellSessionTitle,
} from "./exclude-channel-shell-sessions";

describe("isChannelShellSessionTitle", () => {
  it("matches #channelName shells without needing a live channel list", () => {
    expect(isChannelShellSessionTitle("#multica_jhp研发群")).toBe(true);
    expect(isChannelShellSessionTitle(" #multica_jhp研发群 ")).toBe(true);
  });

  it("keeps genuine bubble titles", () => {
    expect(isChannelShellSessionTitle("hi")).toBe(false);
    expect(isChannelShellSessionTitle("PR #1217 follow-up")).toBe(false);
    expect(isChannelShellSessionTitle("jianghp3, 已补完并开了 follow-up PR")).toBe(false);
    expect(isChannelShellSessionTitle("#")).toBe(false);
  });
});

describe("excludeChannelShellSessions", () => {
  it("drops channel shells and keeps bubble rows", () => {
    const sessions = [
      { id: "1", title: "hi" },
      { id: "2", title: "#multica_jhp研发群" },
      { id: "3", title: "PR follow-up" },
    ];
    expect(excludeChannelShellSessions(sessions).map((s) => s.id)).toEqual(["1", "3"]);
  });
});
