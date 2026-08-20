// @vitest-environment node
import { describe, it, expect } from "vitest";
import { pickStageKeys } from "./task-status-pill";

describe("pickStageKeys", () => {
  it("returns offline when the agent is offline while a turn is outstanding", () => {
    expect(pickStageKeys("running", "offline")).toEqual({
      stageKey: "offline",
      static: true,
    });
    expect(pickStageKeys("queued", "offline")).toEqual({
      stageKey: "offline",
      static: true,
    });
  });

  it("returns thinking for outstanding standalone turns", () => {
    // Standalone Raft deliver has no live transcript stages — Thinking
    // covers the wait until chat:done.
    expect(pickStageKeys("running", "online")).toEqual({ stageKey: "thinking" });
    expect(pickStageKeys("queued", "online")).toEqual({ stageKey: "thinking" });
    expect(pickStageKeys(undefined, "online")).toEqual({ stageKey: "thinking" });
  });
});
