// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  adaptV5Graph,
  adaptV6Delta,
  adaptV6Snapshot,
} from "@multica/core/adapters";
import {
  tombstoneA2Delta,
  twoComponentSnapshot,
  v5FixtureGraph,
  v6FixtureDelta,
  v6FixtureSnapshot,
} from "@multica/core/adapters/fixtures";
import {
  canvasModelReducer,
  renderCanvas,
  resetWithSnapshot,
  type CanvasModelState,
} from "./model";

function freshModel(state: CanvasModelState): CanvasModelState {
  // clone the reducer's snapshot so tests don't share references
  return {
    snapshot: { ...state.snapshot, nodes: [...state.snapshot.nodes], edges: [...state.snapshot.edges] },
    hiddenNodeIds: new Set(state.hiddenNodeIds),
    positions: new Map(state.positions),
  };
}

describe("unified canvas ViewModel — (AC3) one render layer for V5 and V6", () => {
  it("renders a V5 fixture through the same render layer", () => {
    const { nodes, edges } = v5FixtureGraph();
    const snap = adaptV5Graph("session-research-a", nodes, edges);
    const model = resetWithSnapshot(snap);
    const render = renderCanvas(model);
    expect(render.nodes.map((n) => n.id)).toContain("goal");
    expect(render.nodes.map((n) => n.id)).toContain("probe-a");
    expect(render.edges.some((e) => e.relation === "supports")).toBe(true);
    for (const e of render.edges) {
      expect(render.nodes.some((n) => n.id === e.from)).toBe(true);
      expect(render.nodes.some((n) => n.id === e.to)).toBe(true);
    }
  });

  it("renders a V6 fixture through the same render layer", () => {
    const snap = adaptV6Snapshot(v6FixtureSnapshot());
    const model = resetWithSnapshot(snap);
    const render = renderCanvas(model);
    const ids = render.nodes.map((n) => n.id);
    expect(ids.some((id) => id.includes(":claim:c1"))).toBe(true);
    expect(ids.some((id) => id.includes(":insight:i1"))).toBe(true);
    // Unknown future kind degraded to generic but still rendered with geometry.
    const generic = render.nodes.find((n) => n.id.includes(":unknown_future_kind:u1"))!;
    expect(generic.kind).toBe("generic");
    expect(generic.position).toBeDefined();
    expect(render.edges.some((e) => e.relation === "integrates")).toBe(true);
  });

  it("V5 and V6 both enter the identical reducer/render pipeline (no separate path)", () => {
    const v5 = resetWithSnapshot(
      adaptV5Graph("s1", v5FixtureGraph().nodes, v5FixtureGraph().edges),
    );
    const v6 = resetWithSnapshot(adaptV6Snapshot(v6FixtureSnapshot()));
    // Both models are the same type and are driven by the same reducer/render fns.
    for (const model of [v5, v6]) {
      const state = freshModel(model);
      expect(typeof canvasModelReducer).toBe("function");
      expect(renderCanvas(state).nodes.length).toBeGreaterThan(0);
    }
  });
});

describe("unified canvas ViewModel — (AC1) stable identity & position on recompute", () => {
  it("recomputing the same V5 snapshot yields identical positions and ids", () => {
    const snap = adaptV5Graph("s1", v5FixtureGraph().nodes, v5FixtureGraph().edges);
    const a = resetWithSnapshot(snap);
    const b = resetWithSnapshot(snap);
    expect(renderCanvas(a).nodes.map((n) => n.id)).toEqual(
      renderCanvas(b).nodes.map((n) => n.id),
    );
    expect(renderCanvas(a).nodes.map((n) => n.position)).toEqual(
      renderCanvas(b).nodes.map((n) => n.position),
    );
  });

  it("recomputing the same V6 snapshot yields identical positions and ids", () => {
    const snap = adaptV6Snapshot(v6FixtureSnapshot());
    const a = resetWithSnapshot(snap);
    const b = resetWithSnapshot(snap);
    expect(renderCanvas(a).nodes.map((n) => n.id)).toEqual(
      renderCanvas(b).nodes.map((n) => n.id),
    );
    expect(renderCanvas(a).nodes.map((n) => n.position)).toEqual(
      renderCanvas(b).nodes.map((n) => n.position),
    );
  });
});

describe("unified canvas ViewModel — (AC2) visibility tombstone local recompute", () => {
  const v6Model = () =>
    freshModel(resetWithSnapshot(adaptV6Snapshot(v6FixtureSnapshot())));

  it("removes the tombstoned view node and dangling edges from the render", () => {
    let model = v6Model();
    model = canvasModelReducer(model, {
      type: "delta",
      delta: adaptV6Delta(v6FixtureDelta()),
    });
    const render = renderCanvas(model);
    expect(render.nodes.some((n) => n.id.includes(":claim:c1"))).toBe(false);
    // No dangling edges — every rendered edge touches two rendered nodes.
    for (const e of render.edges) {
      expect(render.nodes.some((n) => n.id === e.from)).toBe(true);
      expect(render.nodes.some((n) => n.id === e.to)).toBe(true);
    }
    // The resolution claim from the delta arrived.
    expect(render.nodes.some((n) => n.id.includes(":claim:c3"))).toBe(true);
  });

  it("keeps an unrelated component's positions stable while recomputing the affected region", () => {
    // Dedicated two-component scene: tombstoning a2 must leave component B
    // (b1->b2) exactly where it was — proving local-only recompute.
    const snap = twoComponentSnapshot();
    let model = freshModel(resetWithSnapshot(snap));
    const before = renderCanvas(model);
    model = canvasModelReducer(model, { type: "delta", delta: tombstoneA2Delta() });
    const after = renderCanvas(model);

    expect(after.nodes.some((n) => n.id === "a2")).toBe(false);
    expect(after.nodes.some((n) => n.id === "a4")).toBe(true);
    const b1Before = before.nodes.find((n) => n.id === "b1")!.position;
    const b2Before = before.nodes.find((n) => n.id === "b2")!.position;
    expect(after.nodes.find((n) => n.id === "b1")!.position).toEqual(b1Before);
    expect(after.nodes.find((n) => n.id === "b2")!.position).toEqual(b2Before);
  });

  it("is idempotent — the same delta twice never duplicates nodes", () => {
    let model = v6Model();
    const delta = adaptV6Delta(v6FixtureDelta());
    model = canvasModelReducer(model, { type: "delta", delta });
    const once = renderCanvas(model).nodes.length;
    model = canvasModelReducer(model, { type: "delta", delta });
    const twice = renderCanvas(model).nodes.length;
    expect(twice).toBe(once);
    // claim:c3 appears exactly once.
    expect(
      renderCanvas(model).nodes.filter((n) => n.id.includes(":claim:c3")).length,
    ).toBe(1);
  });

  it("discards hidden (folded) nodes as display-only view without mutating canonical graph", () => {
    let model = v6Model();
    const canonicalCount = model.snapshot.nodes.length;
    model = canvasModelReducer(model, {
      type: "setHidden",
      nodeIds: ["run-v6-contract-fixture:claim:c2"],
    });
    const render = renderCanvas(model);
    expect(render.nodes.some((n) => n.id.includes(":claim:c2"))).toBe(false);
    // Canonical snapshot is untouched — folding is pure display state.
    expect(model.snapshot.nodes.length).toBe(canonicalCount);
  });
});
