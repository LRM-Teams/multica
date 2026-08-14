import { describe, expect, it } from "vitest";
import {
  RESEARCH_V6_EDGE_TYPES,
  RESEARCH_V6_NODE_KINDS,
  RESEARCH_V6_TRANSITION_KINDS,
  ResearchV6EdgeTypeSchema,
  ResearchV6NodeKindSchema,
  ResearchV6TransitionKindSchema,
  RESEARCH_V6_NODE_REGISTRY,
  RESEARCH_V6_EDGE_REGISTRY,
  RESEARCH_V6_TRANSITION_REGISTRY,
  classifyNodeKind,
  classifyEdgeType,
  classifyTransitionKind,
} from "./registry";
import type { ResearchV6UnknownKindDiagnostic } from "../types/research-v6";

const nodeKinds = [...RESEARCH_V6_NODE_KINDS];
const edgeTypes = [...RESEARCH_V6_EDGE_TYPES];
const transitionKinds = [...RESEARCH_V6_TRANSITION_KINDS];

describe("Research V6 registry — AC #1: every documented kind has a type and parse", () => {
  it("documents 30 entity kinds plus the explicit goal origin", () => {
    expect(nodeKinds).toHaveLength(31);
    expect(new Set(nodeKinds).size).toBe(31);
  });

  it("documents the exact §7.1 edge type set plus restart lineage", () => {
    expect(edgeTypes).toEqual([
      // structure
      "decomposes", "tests", "depends_on", "triggered",
      // production
      "produced", "consumed", "derived_from", "integrates",
      // evidence
      "supports", "contradicts", "refines", "supersedes", "invalidates",
      // discussion / escalation
      "discussed_by", "challenged_by", "escalated_to", "resolved_by",
      // reporting / review
      "reported_in", "reviewed_by", "revised_by",
      // staffing / lifecycle
      "staffed_by", "created_for", "retired_after", "restart_of",
    ]);
  });

  it("documents exactly 10 transition kinds (§7.2)", () => {
    expect(transitionKinds).toHaveLength(10);
    expect(new Set(transitionKinds).size).toBe(10);
  });

  it("every node kind has a registry entry + display metadata", () => {
    for (const kind of nodeKinds) {
      const meta = RESEARCH_V6_NODE_REGISTRY.get(kind);
      expect(meta, `missing node meta for ${kind}`).toBeDefined();
      expect(meta!.label.length).toBeGreaterThan(0);
      expect(meta!.group).toBeDefined();
    }
  });

  it("every edge type has a registry entry + family + label", () => {
    for (const type of edgeTypes) {
      const meta = RESEARCH_V6_EDGE_REGISTRY.get(type);
      expect(meta, `missing edge meta for ${type}`).toBeDefined();
      expect(meta!.family).toBeDefined();
      expect(meta!.label.length).toBeGreaterThan(0);
    }
  });

  it("every transition kind has a registry entry + label", () => {
    for (const kind of transitionKinds) {
      const meta = RESEARCH_V6_TRANSITION_REGISTRY.get(kind);
      expect(meta, `missing transition meta for ${kind}`).toBeDefined();
      expect(meta!.label.length).toBeGreaterThan(0);
    }
  });

  it("parses every node kind as a known kind (zod parse)", () => {
    for (const kind of nodeKinds) {
      const result = ResearchV6NodeKindSchema.safeParse(kind);
      expect(result.success, `node kind ${kind} must parse`).toBe(true);
    }
  });

  it("parses every edge type as a known type (zod parse)", () => {
    for (const type of edgeTypes) {
      const result = ResearchV6EdgeTypeSchema.safeParse(type);
      expect(result.success, `edge type ${type} must parse`).toBe(true);
    }
  });

  it("parses every transition kind as a known kind (zod parse)", () => {
    for (const kind of transitionKinds) {
      const result = ResearchV6TransitionKindSchema.safeParse(kind);
      expect(result.success, `transition ${kind} must parse`).toBe(true);
    }
  });

  it("classifies every node kind as known (runtime classification)", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    for (const kind of nodeKinds) {
      const surface = classifyNodeKind(kind, `node-${kind}`, "run-1", diags);
      expect(surface.isGeneric, `node kind ${kind} must be known`).toBe(false);
      expect(surface.kind).toBe(kind);
    }
    expect(diags).toHaveLength(0);
  });

  it("classifies every edge type as known", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    for (const type of edgeTypes) {
      const edge = classifyEdgeType(type, `edge-${type}`, "run-1", diags);
      expect(edge.isGeneric, `edge type ${type} must be known`).toBe(false);
    }
    expect(diags).toHaveLength(0);
  });

  it("classifies every transition kind as known", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    for (const kind of transitionKinds) {
      const t = classifyTransitionKind(kind, "run-1", diags);
      expect(t.isGeneric, `transition ${kind} must be known`).toBe(false);
      expect(t.label).toBeTruthy();
    }
    expect(diags).toHaveLength(0);
  });
});

describe("Research V6 unknown-kind degradation — AC #2: GenericNode, no crash", () => {
  it("degrades an unknown node kind to generic, records a diagnostic, does not throw", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    const surface = classifyNodeKind("future_quantum_insight", "node-404", "run-9", diags);
    expect(surface.isGeneric).toBe(true);
    if (surface.isGeneric) {
      expect(surface.label).toBe("未知节点");
      expect(surface.group).toBe("generic");
      expect(surface.kind).toBe("future_quantum_insight");
      expect(surface.diagnostic).toBeDefined();
      expect(surface.diagnostic.raw).toBe("future_quantum_insight");
      expect(surface.diagnostic.field).toBe("node_kind");
      expect(surface.diagnostic.owner_id).toBe("node-404");
      expect(surface.diagnostic.run_id).toBe("run-9");
    }
    expect(diags).toHaveLength(1);
    expect(diags[0]!.sequence).toBe(1);
  });

  it("multiples unknown kinds each get a monotonic diagnostic and distinct owner ids", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    classifyNodeKind("k1", "a", "run-1", diags);
    classifyNodeKind("k2", "b", "run-1", diags);
    expect(diags.map((d) => d.sequence)).toEqual([1, 2]);
    expect(diags.map((d) => d.owner_id)).toEqual(["a", "b"]);
  });

  it("degrades an unknown edge type to a generic relation with a diagnostic", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    const edge = classifyEdgeType("teleports", "edge-404", "run-9", diags);
    expect(edge.isGeneric).toBe(true);
    expect(edge.label).toBe("未知关系");
    expect(edge.diagnostic?.field).toBe("edge_type");
    expect(edge.diagnostic?.raw).toBe("teleports");
    expect(diags).toHaveLength(1);
  });

  it("degrades an unknown transition to null display label + diagnostic, no crash", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    const t = classifyTransitionKind("universe_restarted", "run-9", diags);
    expect(t.isGeneric).toBe(true);
    expect(t.label).toBeNull();
    expect(t.diagnostic?.field).toBe("transition_kind");
    expect(diags).toHaveLength(1);
  });

  it("empty string is not a valid known kind and degrades gracefully", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    const s = classifyNodeKind("", "node-x", "run-1", diags);
    expect(s.isGeneric).toBe(true);
    expect(diags).toHaveLength(1);
  });

  it("classification of many unknown kinds never throws (page does not crash)", () => {
    const diags: ResearchV6UnknownKindDiagnostic[] = [];
    const inputs = ["a_1", "b_2", "c_3", "d_4", "e_5", "9", "!!", "node:kind"];
    const surfaces = inputs.map((raw, i) =>
      classifyNodeKind(raw, `n${i}`, "run-1", diags),
    );
    expect(surfaces.every((s) => s.isGeneric)).toBe(true);
    expect(diags).toHaveLength(inputs.length);
  });
});
