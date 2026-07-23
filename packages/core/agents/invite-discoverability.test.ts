import { describe, expect, it } from "vitest";
import { isAgentInviteDiscoverableInChannel } from "./invite-discoverability";

describe("isAgentInviteDiscoverableInChannel (LRM-399)", () => {
  const base = {
    visibility: "workspace" as const,
    home_channel_id: null as string | null,
    managed_role: undefined as "group_manager" | undefined,
    archived_at: null as string | null,
  };

  it("hides group managers in every channel (hire/create only)", () => {
    expect(
      isAgentInviteDiscoverableInChannel(
        { ...base, managed_role: "group_manager", visibility: "channel", home_channel_id: "ch-a" },
        "ch-a",
      ),
    ).toBe(false);
    expect(
      isAgentInviteDiscoverableInChannel(
        { ...base, managed_role: "group_manager", visibility: "channel", home_channel_id: "ch-a" },
        "ch-c",
      ),
    ).toBe(false);
  });

  it("hides other groups' channel-visibility agents in group C", () => {
    expect(
      isAgentInviteDiscoverableInChannel(
        { ...base, visibility: "channel", home_channel_id: "ch-a" },
        "ch-c",
      ),
    ).toBe(false);
  });

  it("allows channel-visibility agents only in their home channel", () => {
    expect(
      isAgentInviteDiscoverableInChannel(
        { ...base, visibility: "channel", home_channel_id: "ch-c" },
        "ch-c",
      ),
    ).toBe(true);
  });

  it("rejects channel agents missing home_channel_id (no silent fallback)", () => {
    expect(
      isAgentInviteDiscoverableInChannel(
        { ...base, visibility: "channel", home_channel_id: null },
        "ch-c",
      ),
    ).toBe(false);
  });

  it("hides channel agents when there is no channel context", () => {
    expect(
      isAgentInviteDiscoverableInChannel(
        { ...base, visibility: "channel", home_channel_id: "ch-c" },
        null,
      ),
    ).toBe(false);
  });

  it("allows workspace agents in any channel", () => {
    expect(isAgentInviteDiscoverableInChannel(base, "ch-c")).toBe(true);
  });

  it("hides archived agents", () => {
    expect(
      isAgentInviteDiscoverableInChannel(
        { ...base, archived_at: "2026-07-01T00:00:00Z" },
        "ch-c",
      ),
    ).toBe(false);
  });
});
