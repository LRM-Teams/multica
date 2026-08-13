import { beforeEach, describe, expect, it } from "vitest";
import {
  countHiddenByFilter,
  emptyCanvasFilter,
  isBlankFilter,
  matchesResearchCanvasFilter,
  useResearchCanvasStore,
} from "./canvas-store";

const NODES = [
  { id: "a", node_type: "discover", status: "completed", round: "R1", cluster: "alpha", title: "Voice trend", agent: "oren", conclusion: "adoption rising" },
  { id: "b", node_type: "verify", status: "in_progress", round: "R2", cluster: "beta", title: "Synthesis", agent: "luca", evidence: "【来源】summary.md" },
  { id: "c", node_type: "aggregate", status: "completed", round: "R2", cluster: "alpha", title: "Conclusion", agent: "nel", conclusion: "ship v2" },
];

describe("useResearchCanvasStore (LRM-1497 shared client state)", () => {
  beforeEach(() => {
    useResearchCanvasStore.setState({
      viewport: null,
      viewportBySession: {},
      selectedNodeId: null,
      selectedNodeBySession: {},
      filter: emptyCanvasFilter(),
    });
  });

  it("defaults to no viewport / selection / filter", () => {
    const s = useResearchCanvasStore.getState();
    expect(s.viewport).toBeNull();
    expect(s.selectedNodeId).toBeNull();
    expect(isBlankFilter(s.filter)).toBe(true);
  });

  it("stores the world-space viewport (zoom preserved on resize path)", () => {
    useResearchCanvasStore.getState().setViewport({ x: 120, y: 40, zoom: 1.5 });
    expect(useResearchCanvasStore.getState().viewport).toEqual({ x: 120, y: 40, zoom: 1.5 });
  });

  it("isolates persisted viewports by research session", () => {
    const store = useResearchCanvasStore.getState();
    store.setSessionViewport("session-a", { x: 120, y: 40, zoom: 1.5 });
    store.setSessionViewport("session-b", { x: -20, y: 80, zoom: 0.8 });

    expect(useResearchCanvasStore.getState().viewportBySession).toEqual({
      "session-a": { x: 120, y: 40, zoom: 1.5 },
      "session-b": { x: -20, y: 80, zoom: 0.8 },
    });
  });

  it("bounds retained session viewports", () => {
    for (let index = 0; index < 24; index += 1) {
      useResearchCanvasStore
        .getState()
        .setSessionViewport(`session-${index}`, { x: index, y: 0, zoom: 1 });
    }

    const saved = useResearchCanvasStore.getState().viewportBySession;
    expect(Object.keys(saved)).toHaveLength(20);
    expect(saved["session-0"]).toBeUndefined();
    expect(saved["session-23"]).toEqual({ x: 23, y: 0, zoom: 1 });
  });

  it("keeps selection across a filter change (AC: select then fit preserves selection)", () => {
    useResearchCanvasStore.getState().selectNode("b");
    useResearchCanvasStore.getState().setFilter({ status: "completed" });
    expect(useResearchCanvasStore.getState().selectedNodeId).toBe("b");
    useResearchCanvasStore.getState().clearSelection();
    expect(useResearchCanvasStore.getState().selectedNodeId).toBeNull();
  });

  it("isolates and clears persisted selections by research session", () => {
    const store = useResearchCanvasStore.getState();
    store.selectSessionNode("session-a", "node-a");
    store.selectSessionNode("session-b", "node-b");
    expect(useResearchCanvasStore.getState().selectedNodeBySession).toEqual({
      "session-a": "node-a",
      "session-b": "node-b",
    });

    store.selectSessionNode("session-a", null);
    expect(useResearchCanvasStore.getState().selectedNodeBySession).toEqual({
      "session-b": "node-b",
    });
  });

  it("bounds retained session selections", () => {
    for (let index = 0; index < 24; index += 1) {
      useResearchCanvasStore
        .getState()
        .selectSessionNode(`session-${index}`, `node-${index}`);
    }

    const saved = useResearchCanvasStore.getState().selectedNodeBySession;
    expect(Object.keys(saved)).toHaveLength(20);
    expect(saved["session-0"]).toBeUndefined();
    expect(saved["session-23"]).toBe("node-23");
  });

  it("merges partial filter updates and clears with clearFilter", () => {
    useResearchCanvasStore.getState().setFilter({ status: "completed" });
    useResearchCanvasStore.getState().setFilter({ cluster: "alpha" });
    const f = useResearchCanvasStore.getState().filter;
    expect(f.status).toBe("completed");
    expect(f.cluster).toBe("alpha");
    useResearchCanvasStore.getState().clearFilter();
    expect(isBlankFilter(useResearchCanvasStore.getState().filter)).toBe(true);
  });
});

describe("countHiddenByFilter (LRM-1497 hidden-count helper)", () => {
  it("blank filter hides nothing", () => {
    const r = countHiddenByFilter(NODES, emptyCanvasFilter());
    expect(r).toEqual({ visible: 3, hidden: 0 });
  });

  it("counts hidden by status and reports visible count", () => {
    const r = countHiddenByFilter(NODES, { status: "completed" });
    expect(r.visible).toBe(2);
    expect(r.hidden).toBe(1);
  });

  it("filters by free-text query across title/agent/conclusion/evidence", () => {
    const exact = countHiddenByFilter(NODES, { query: "synthesis" });
    expect(exact.visible).toBe(1);
    const byEvidence = countHiddenByFilter(NODES, { query: "summary" });
    expect(byEvidence.visible).toBe(1);
    const none = countHiddenByFilter(NODES, { query: "zzz-nope" });
    expect(none.visible).toBe(0);
    expect(none.hidden).toBe(3);
  });

  it("never mutates the canonical input array", () => {
    const snapshot = JSON.stringify(NODES);
    countHiddenByFilter(NODES, { cluster: "alpha", status: "completed" });
    expect(JSON.stringify(NODES)).toBe(snapshot);
  });

  it("matches typed graph level as tier filter", () => {
    expect(
      matchesResearchCanvasFilter(
        { id: "x", level: "l", status: "completed" },
        { tier: "l" },
      ),
    ).toBe(true);
    expect(
      matchesResearchCanvasFilter(
        { id: "x", level: "m", status: "completed" },
        { tier: "l" },
      ),
    ).toBe(false);
  });
});
