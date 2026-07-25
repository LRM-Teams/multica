import { describe, expect, it } from "vitest";
import {
  excludeChannelShellSessions,
  isChannelShellSessionTitle,
} from "./exclude-channel-shell-sessions";

describe("isChannelShellSessionTitle", () => {
  it("matches exact #channelName shells", () => {
    expect(isChannelShellSessionTitle("#multica_jhp研发群", ["multica_jhp研发群"])).toBe(true);
  });

  it("keeps genuine bubble titles", () => {
    expect(isChannelShellSessionTitle("hi", ["multica_jhp研发群"])).toBe(false);
    expect(isChannelShellSessionTitle("#1217 fix", ["multica_jhp研发群"])).toBe(false);
    expect(isChannelShellSessionTitle("#multica_jhp研发群", [])).toBe(false);
  });
});

describe("excludeChannelShellSessions", () => {
  it("drops channel shells and keeps bubble rows", () => {
    const sessions = [
      { id: "1", title: "hi" },
      { id: "2", title: "#multica_jhp研发群" },
      { id: "3", title: "PR follow-up" },
    ];
    expect(excludeChannelShellSessions(sessions, ["multica_jhp研发群"]).map((s) => s.id)).toEqual([
      "1",
      "3",
    ]);
  });
});
