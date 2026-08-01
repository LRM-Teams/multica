// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphNodeType } from "@multica/core/types";
import {
  nodeIsVisuallyBusy,
  normalizeNodeStatusKey,
  shouldPulseNode,
  visualForEdgeType,
  visualForNodeType,
} from "./node-visuals";

const NODE_TYPES: ResearchGraphNodeType[] = [
  "goal",
  "subquestion",
  "probe",
  "finding",
  "conflict",
  "dead_end",
  "refuted",
  "pivot",
  "roster_change",
  "stage_gate",
  "product_round_gate",
  "agent_activity",
];

const FORBIDDEN =
  /#[0-9a-fA-F]{3,8}\b|-(?:sky|emerald|amber|orange|teal|rose|red|green|blue|yellow|indigo|violet|fuchsia|pink|lime|cyan)-[0-9]{2,3}\b/;

describe("node-visuals (LRM-798)", () => {
  it("maps dead_end to danger tone", () => {
    expect(visualForNodeType("dead_end").labelTone).toBe("danger");
  });

  it("uses semantic token classes for every node_type (no hex / palette-500)", () => {
    for (const type of NODE_TYPES) {
      const v = visualForNodeType(type);
      expect(v.ringClass, type).not.toMatch(FORBIDDEN);
      expect(v.accentBarClass, type).not.toMatch(FORBIDDEN);
    }
  });

  it("edge strokes are CSS variables / color-mix, never hex", () => {
    for (const edge of ["supports", "contradicts", "supersedes", "abandons", "leads_to"] as const) {
      const v = visualForEdgeType(edge);
      expect(v.stroke, edge).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
      expect(v.stroke, edge).toMatch(/var\(--/);
    }
    expect(visualForEdgeType("contradicts").strokeDasharray).toBeTruthy();
  });

  it("pulses active probes but not goals", () => {
    expect(shouldPulseNode("active", "probe")).toBe(true);
    expect(shouldPulseNode("active", "goal")).toBe(false);
    expect(shouldPulseNode("done", "probe")).toBe(false);
  });

  it("pulses when actor has live activity even for quiet types / running", () => {
    expect(nodeIsVisuallyBusy("active", "goal", true)).toBe(true);
    expect(nodeIsVisuallyBusy("running", "probe", true)).toBe(true);
    expect(nodeIsVisuallyBusy("waiting", "finding", true)).toBe(true);
    expect(nodeIsVisuallyBusy("active", "goal", false)).toBe(false);
    expect(nodeIsVisuallyBusy("done", "probe", true)).toBe(false);
  });

  it("normalizes status keys for i18n", () => {
    expect(normalizeNodeStatusKey("done")).toBe("done");
    expect(normalizeNodeStatusKey("ACTIVE")).toBe("active");
    expect(normalizeNodeStatusKey("weird")).toBe("unknown");
  });
});
