// @vitest-environment node
import { describe, expect, it } from "vitest";
import { enforceVisibleBudget, type LODEntry } from "./budget";
import { classifySemanticLOD } from "./classify";
import { selectSemanticNodes } from "./selector";
import { VIEWPORT_BUDGETS, type SemanticContext } from "./model";
import type { LodClassification } from "./classify";

function ctx(overrides: Partial<SemanticContext> = {}): SemanticContext {
  return {
    selectedId: null,
    ancestorIds: [],
    blockingIds: [],
    runningIds: [],
    pinnedIds: [],
    transitionRootIds: [],
    zoomPct: 100,
    depthById: new Map(),
    explicitThirdLevel: false,
    ...overrides,
  };
}

function entry(
  id: string,
  kind: string,
  status: string,
  importance: number,
  context: SemanticContext,
): LODEntry {
  const classification: LodClassification = classifySemanticLOD({
    id,
    kind,
    status,
    importance,
    context,
  });
  return { id, kind, status, importance, classification };
}

describe("enforceVisibleBudget — retention invariants", () => {
  it("never cuts selected / ancestor / blocking nodes", () => {
    // Build a large set of nodes all at depth 1 (non-protected) plus one
    // selected and one blocking node.
    const context = ctx({
      selectedId: "sel",
      blockingIds: ["block"],
      ancestorIds: ["anc"],
      depthById: new Map(
        Array.from({ length: 300 }, (_, i) => [`n${i}`, 1]),
      ),
    });
    const contextWithIds = { ...context, depthById: context.depthById };
    const all: LODEntry[] = [
      entry("sel", "task", "running", 0.9, contextWithIds),
      entry("block", "attempt", "failed", 0.8, contextWithIds),
      entry("anc", "question", "active", 0.7, contextWithIds),
      ...Array.from({ length: 300 }, (_, i) =>
        entry(`n${i}`, "observation", "active", 0.05, contextWithIds),
      ),
    ];
    const res = enforceVisibleBudget({
      entries: all,
      context: contextWithIds,
      tier: "desktop",
    });

    expect(res.byNode.get("sel")).toBe("landmark");
    expect(res.byNode.get("block")).toBe("landmark");
    expect(res.byNode.get("anc")).toBe("landmark");
    expect(res.foldedIntoBundle).not.toContain("sel");
    expect(res.foldedIntoBundle).not.toContain("block");
    expect(res.foldedIntoBundle).not.toContain("anc");
  });
});

describe("enforceVisibleBudget — 25% zoom landmark ≤12 (AC1)", () => {
  it("desktop overview keeps at most 12 landmark cards", () => {
    const depthById = new Map<string, number>([["root", 0]]);
    const context = ctx({
      zoomPct: 25,
      selectedId: "root",
      depthById,
    });
    // 40 narrative landmarks at depth 0..2 + many low dots.
    const entries: LODEntry[] = [
      entry("root", "task", "running", 1, context),
    ];
    for (let i = 0; i < 40; i++) {
      const id = `insight${i}`;
      depthById.set(id, 1);
      entries.push(entry(id, "insight", "accepted", 0.9 - i / 100, context));
    }
    // Re-declare context with full depth map (mutated above).
    const res = enforceVisibleBudget({
      entries,
      context: ctx({ zoomPct: 25, selectedId: "root", depthById }),
      tier: "desktop",
    });
    expect(res.counts.landmark).toBeLessThanOrEqual(12);
  });
});

describe("enforceVisibleBudget — desktop semantic ≤180 / graphic DOM ≤220 (AC1)", () => {
  it("350-node fixture folds to within hard semantic-node and DOM caps", () => {
    const depthById = new Map<string, number>();
    const seed = new Set<string>(["root"]);
    const entries: LODEntry[] = [];
    const context0 = ctx({
      zoomPct: 100,
      selectedId: "root",
      depthById,
    });
    entries.push(entry("root", "task", "running", 1, context0));
    depthById.set("root", 0);
    for (let i = 0; i < 349; i++) {
      const id = `n${i}`;
      // spread depth 1..6 to exercise folding
      depthById.set(id, 1 + (i % 6));
      seed.add(id);
      entries.push(
        entry(id, i % 3 === 0 ? "insight" : "observation", "active", 0.1, {
          ...context0,
          selectedId: "root",
          depthById,
          ancestorIds: [],
          blockingIds: [],
        }),
      );
    }

    const res = enforceVisibleBudget({
      entries,
      context: ctx({
        zoomPct: 100,
        selectedId: "root",
        depthById,
        explicitThirdLevel: false,
      }),
      tier: "desktop",
    });

    // Hard semantic-node cap can never be exceeded.
    expect(res.counts.totalSemanticNodes).toBeLessThanOrEqual(
      VIEWPORT_BUDGETS.desktop.semanticNodeHard,
    );
    // Graphic-DOM estimate (canonical cards + bundles) stays ≤220.
    expect(res.counts.graphicDomEstimate).toBeLessThanOrEqual(
      VIEWPORT_BUDGETS.desktop.graphicDomHard,
    );
  });
});

describe("selectSemanticNodes — happy path integration", () => {
  it("classifies a small route graph and folds the 4th layer into a bundle", () => {
    const nodes = [
      { id: "root", kind: "task", status: "running", importance: 1 },
      { id: "a", kind: "question", status: "active", importance: 0.6 },
      { id: "b", kind: "attempt", status: "running", importance: 0.7 },
      { id: "c", kind: "claim", status: "accepted", importance: 0.8 },
      { id: "deep1", kind: "observation", status: "active", importance: 0.05 },
      { id: "deep2", kind: "observation", status: "active", importance: 0.05 },
      { id: "deep3", kind: "observation", status: "active", importance: 0.05 },
      { id: "deep4", kind: "query_execution", status: "active", importance: 0.05 },
      { id: "deep5", kind: "query_execution", status: "failed", importance: 0.05 },
    ];
    const edges = [
      { from: "root", to: "a", relation: "decomposes" },
      { from: "a", to: "b", relation: "produced" },
      { from: "b", to: "c", relation: "produced" },
      { from: "c", to: "deep1", relation: "produced" },
      { from: "c", to: "deep2", relation: "produced" },
      { from: "c", to: "deep3", relation: "produced" },
      { from: "c", to: "deep4", relation: "produced" },
      { from: "c", to: "deep5", relation: "produced" },
    ];
    const context = {
      rootId: "root",
      selectedId: "root",
      ancestorIds: [],
      blockingIds: [],
      runningIds: ["root", "b"],
      pinnedIds: [],
      transitionRootIds: [],
      zoomPct: 100,
      depthById: new Map<string, number>(),
      explicitThirdLevel: false,
    };

    const res = selectSemanticNodes({ nodes, edges, context, tier: "desktop" });

    // root is landmark.
    expect(res.byNode.get("root")).toBe("landmark");
    // deep nodes are at depth 4+ → folded into bundle.
    for (const d of ["deep1", "deep2", "deep3", "deep4", "deep5"]) {
      expect(res.foldedIntoBundle).toContain(d);
      expect(res.byNode.has(d)).toBe(false);
    }
    expect(res.counts.bundle).toBe(1);
  });
});
