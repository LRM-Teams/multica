import { describe, expect, it } from "vitest";
import { resolveCanvasBodyMode } from "./canvas-body-mode";

describe("resolveCanvasBodyMode (LRM-979)", () => {
  it("is ready when nodes exist", () => {
    expect(resolveCanvasBodyMode(2, "running")).toBe("ready");
    expect(resolveCanvasBodyMode(1, "drafting")).toBe("ready");
  });

  it("uses forming skeleton while in-flight with zero nodes", () => {
    expect(resolveCanvasBodyMode(0, "running")).toBe("forming");
    expect(resolveCanvasBodyMode(0, "paused")).toBe("forming");
  });

  it("uses designed empty when idle / drafting / done with zero nodes", () => {
    expect(resolveCanvasBodyMode(0, "drafting")).toBe("empty");
    expect(resolveCanvasBodyMode(0, "completed")).toBe("empty");
    expect(resolveCanvasBodyMode(0, "awaiting_user_confirm")).toBe("empty");
    expect(resolveCanvasBodyMode(0, null)).toBe("empty");
  });
});
