import { describe, expect, it } from "vitest";
import {
  CHANNEL_OUTPUT_ACTIONS_CAPABILITY,
  deriveChannelOutputRuntimeStatus,
  isActionableChannelOutputRuntimeStatus,
} from "./channel-output-actions";

const NOW = Date.parse("2026-07-31T12:00:00Z");

describe("deriveChannelOutputRuntimeStatus", () => {
  it("requires a runtime binding", () => {
    expect(deriveChannelOutputRuntimeStatus(null, null, NOW)).toBe("missing");
    expect(deriveChannelOutputRuntimeStatus({ runtime_id: "" }, null, NOW)).toBe("missing");
    expect(deriveChannelOutputRuntimeStatus({ runtime_id: "rt-1" }, null, NOW)).toBe("missing");
  });

  it("treats offline runtimes as disconnected", () => {
    expect(
      deriveChannelOutputRuntimeStatus(
        { runtime_id: "rt-1" },
        {
          status: "offline",
          last_seen_at: new Date(NOW - 10 * 60 * 1000).toISOString(),
          capabilities: [CHANNEL_OUTPUT_ACTIONS_CAPABILITY],
        },
        NOW,
      ),
    ).toBe("disconnected");
  });

  it("treats a stale heartbeat as disconnected even when status still says online", () => {
    // Same bug class as #10: the server hasn't flipped `status` to offline
    // yet, but the heartbeat has been silent for hours.
    expect(
      deriveChannelOutputRuntimeStatus(
        { runtime_id: "rt-1" },
        {
          status: "online",
          last_seen_at: new Date(NOW - 8 * 60 * 60 * 1000).toISOString(),
          capabilities: [CHANNEL_OUTPUT_ACTIONS_CAPABILITY],
        },
        NOW,
      ),
    ).toBe("disconnected");
  });

  it("requires channel output actions on online runtimes", () => {
    expect(
      deriveChannelOutputRuntimeStatus(
        { runtime_id: "rt-1" },
        { status: "online", last_seen_at: new Date(NOW).toISOString(), capabilities: [] },
        NOW,
      ),
    ).toBe("outdated");
    expect(
      deriveChannelOutputRuntimeStatus(
        { runtime_id: "rt-1" },
        {
          status: "online",
          last_seen_at: new Date(NOW).toISOString(),
          capabilities: [CHANNEL_OUTPUT_ACTIONS_CAPABILITY],
        },
        NOW,
      ),
    ).toBe("ok");
  });

  it("separates actionable statuses from ok", () => {
    expect(isActionableChannelOutputRuntimeStatus("ok")).toBe(false);
    expect(isActionableChannelOutputRuntimeStatus("outdated")).toBe(true);
    expect(isActionableChannelOutputRuntimeStatus("disconnected")).toBe(true);
    expect(isActionableChannelOutputRuntimeStatus("missing")).toBe(true);
  });
});
