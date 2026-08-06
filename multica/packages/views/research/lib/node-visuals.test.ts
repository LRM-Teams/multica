// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphNodeType } from "@multica/core/types";
import {
  edgeVisualForConnection,
  isLowConfidence,
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

describe("node-visuals (LRM-798 / LRM-972)", () => {
  it("maps dead_end to muted dashed sunk shell (not destructive red)", () => {
    const v = visualForNodeType("dead_end");
    expect(v.labelTone).toBe("default");
    expect(v.shellClass).toContain("border-dashed");
    expect(v.shellClass).toContain("opacity-[.72]");
    expect(v.emphasizeType).toBe(true);
    expect(v.ringClass).not.toMatch(/destructive/);
  });

  it("maps refuted to muted opacity + strikethrough title", () => {
    const v = visualForNodeType("refuted");
    expect(v.shellClass).toContain("opacity-55");
    expect(v.titleClass).toContain("line-through");
  });

  it("maps conflict to warning and pivot to info/brand", () => {
    expect(visualForNodeType("conflict").labelTone).toBe("warning");
    expect(visualForNodeType("pivot").labelTone).toBe("info");
    expect(visualForNodeType("pivot").accentBarClass).toContain("brand");
  });

  it("uses semantic token classes for every node_type (no hex / palette-500)", () => {
    for (const type of NODE_TYPES) {
      const v = visualForNodeType(type);
      expect(v.ringClass, type).not.toMatch(FORBIDDEN);
      expect(v.accentBarClass, type).not.toMatch(FORBIDDEN);
      if (v.shellClass) expect(v.shellClass, type).not.toMatch(FORBIDDEN);
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

  it("abandons / dead_end targets use recessed detour edge (40% · dash 2 4)", () => {
    const abandons = visualForEdgeType("abandons");
    expect(abandons.role).toBe("recessed");
    expect(abandons.strokeDasharray).toBe("2 4");
    expect(abandons.strokeOpacity).toBe(0.4);

    const intoDeadEnd = edgeVisualForConnection("leads_to", "dead_end");
    expect(intoDeadEnd.role).toBe("recessed");
    const intoRefuted = edgeVisualForConnection("supports", "refuted");
    expect(intoRefuted.role).toBe("recessed");
    // Conflict edges stay dashed destructive even into dead ends? AC: contradicts stays.
    expect(edgeVisualForConnection("contradicts", "dead_end").role).toBe("dashed");
  });

  it("flags low confidence below 50% (0–1 or 0–100 scales)", () => {
    expect(isLowConfidence(0.35)).toBe(true);
    expect(isLowConfidence(35)).toBe(true);
    expect(isLowConfidence(0.8)).toBe(false);
    expect(isLowConfidence(80)).toBe(false);
    expect(isLowConfidence(null)).toBe(false);
  });

  it("pulses active probes but not goals / dead ends", () => {
    expect(shouldPulseNode("active", "probe")).toBe(true);
    expect(shouldPulseNode("active", "goal")).toBe(false);
    expect(shouldPulseNode("active", "dead_end")).toBe(false);
    expect(shouldPulseNode("done", "probe")).toBe(false);
  });

  it("pulses when actor has live activity even for quiet types / running", () => {
    expect(nodeIsVisuallyBusy("active", "goal", true)).toBe(true);
    expect(nodeIsVisuallyBusy("running", "probe", true)).toBe(true);
    expect(nodeIsVisuallyBusy("waiting", "finding", true)).toBe(true);
    expect(nodeIsVisuallyBusy("active", "goal", false)).toBe(false);
    expect(nodeIsVisuallyBusy("done", "probe", true)).toBe(false);
    expect(nodeIsVisuallyBusy("active", "dead_end", true)).toBe(false);
  });

  it("normalizes status keys for i18n", () => {
    expect(normalizeNodeStatusKey("done")).toBe("done");
    expect(normalizeNodeStatusKey("ACTIVE")).toBe("active");
    expect(normalizeNodeStatusKey("weird")).toBe("unknown");
  });
});
