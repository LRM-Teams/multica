// @vitest-environment node
import { describe, expect, it } from "vitest";
import { memberFailureKey } from "./member-failure-key";

const bob = { member_type: "user" as const, member_id: "u-1" };

describe("memberFailureKey (#839)", () => {
  it("scopes by channel — the same member in another channel is a different key", () => {
    // The failure state lives for the whole ChannelsPage. Keyed by member alone,
    // a failed removal in channel A would surface on that member's row in
    // channel B, where nothing was ever attempted (Iris review).
    expect(memberFailureKey("chan-a", bob)).not.toBe(memberFailureKey("chan-b", bob));
  });

  it("separates a user and an agent that share an id", () => {
    expect(memberFailureKey("chan-a", bob)).not.toBe(
      memberFailureKey("chan-a", { member_type: "agent", member_id: "u-1" }),
    );
  });

  it("is stable for the same channel + member", () => {
    expect(memberFailureKey("chan-a", bob)).toBe(memberFailureKey("chan-a", bob));
  });
});
