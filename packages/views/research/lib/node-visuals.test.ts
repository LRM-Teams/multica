import { describe, expect, it } from "vitest";
import { shouldPulseNode, visualForEdgeType, visualForNodeType } from "./node-visuals";

describe("node-visuals", () => {
  it("maps dead_end to danger tone", () => {
    expect(visualForNodeType("dead_end").labelTone).toBe("danger");
  });

  it("dashes contradicting edges", () => {
    expect(visualForEdgeType("contradicts").strokeDasharray).toBeTruthy();
  });

  it("pulses active probes but not goals", () => {
    expect(shouldPulseNode("active", "probe")).toBe(true);
    expect(shouldPulseNode("active", "goal")).toBe(false);
    expect(shouldPulseNode("done", "probe")).toBe(false);
  });
});
