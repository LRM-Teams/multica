import { describe, expect, it } from "vitest";
import { isAgentDiscoverableInChannelContext } from "./channel-discoverability";

describe("isAgentDiscoverableInChannelContext (LRM-399)", () => {
  it("passes workspace and private agents regardless of channel context", () => {
    expect(
      isAgentDiscoverableInChannelContext({ visibility: "workspace" }, null),
    ).toBe(true);
    expect(
      isAgentDiscoverableInChannelContext(
        { visibility: "private" },
        "ch-other",
      ),
    ).toBe(true);
  });

  it("hides channel agents from workspace directory (no channel context)", () => {
    expect(
      isAgentDiscoverableInChannelContext(
        { visibility: "channel", home_channel_id: "ch-home" },
        null,
      ),
    ).toBe(false);
    expect(
      isAgentDiscoverableInChannelContext(
        { visibility: "channel", home_channel_id: "ch-home" },
        "",
      ),
    ).toBe(false);
  });

  it("shows channel agents only in their home group", () => {
    expect(
      isAgentDiscoverableInChannelContext(
        { visibility: "channel", home_channel_id: "ch-home" },
        "ch-home",
      ),
    ).toBe(true);
    expect(
      isAgentDiscoverableInChannelContext(
        { visibility: "channel", home_channel_id: "ch-home" },
        "ch-other",
      ),
    ).toBe(false);
  });

  it("hides channel agents missing home_channel_id (no silent promote)", () => {
    expect(
      isAgentDiscoverableInChannelContext(
        { visibility: "channel", home_channel_id: null },
        "ch-home",
      ),
    ).toBe(false);
  });
});
