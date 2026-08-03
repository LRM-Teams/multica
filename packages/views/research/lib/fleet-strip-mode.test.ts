// @vitest-environment node
import { describe, expect, it } from "vitest";
import { resolveFleetStripMode } from "./fleet-strip-mode";

describe("resolveFleetStripMode (LRM-980)", () => {
  it("resolves empty vs loading vs running vs done", () => {
    expect(resolveFleetStripMode(0, "drafting")).toBe("empty");
    expect(resolveFleetStripMode(0, "running")).toBe("loading");
    expect(resolveFleetStripMode(0, "paused")).toBe("loading");
    expect(resolveFleetStripMode(0, "drafting", true)).toBe("loading");
    expect(resolveFleetStripMode(2, "running")).toBe("running");
    expect(resolveFleetStripMode(2, "awaiting_user_confirm")).toBe("done");
    expect(resolveFleetStripMode(2, "completed")).toBe("done");
  });
});
