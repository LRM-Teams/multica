import { describe, expect, it } from "vitest";
import {
  CHANNEL_OUTPUT_ACTIONS_CAPABILITY,
  deriveChannelOutputRuntimeStatus,
  isActionableChannelOutputRuntimeStatus,
} from "./channel-output-actions";

describe("deriveChannelOutputRuntimeStatus", () => {
  it("requires a runtime binding", () => {
    expect(deriveChannelOutputRuntimeStatus(null, null)).toBe("missing");
    expect(deriveChannelOutputRuntimeStatus({ runtime_id: "" }, null)).toBe("missing");
    expect(deriveChannelOutputRuntimeStatus({ runtime_id: "rt-1" }, null)).toBe("missing");
  });

  it("treats offline runtimes as disconnected", () => {
    expect(
      deriveChannelOutputRuntimeStatus(
        { runtime_id: "rt-1" },
        { status: "offline", capabilities: [CHANNEL_OUTPUT_ACTIONS_CAPABILITY] },
      ),
    ).toBe("disconnected");
  });

  it("requires channel output actions on online runtimes", () => {
    expect(
      deriveChannelOutputRuntimeStatus(
        { runtime_id: "rt-1" },
        { status: "online", capabilities: [] },
      ),
    ).toBe("outdated");
    expect(
      deriveChannelOutputRuntimeStatus(
        { runtime_id: "rt-1" },
        { status: "online", capabilities: [CHANNEL_OUTPUT_ACTIONS_CAPABILITY] },
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
