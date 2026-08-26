import { describe, expect, it } from "vitest";
import type { ResearchCanvasFilterableNode } from "./canvas-store";
import {
  matchNodeIds,
  searchLocateCursor,
  searchStarGraphNodes,
} from "./star-search";

/** Canonical form: summary + actor_agent_id (as ResearchGraphNode exposes). */
const CANONICAL_NODES: ResearchCanvasFilterableNode[] = [
  {
    id: "a",
    title: "Voice trend adoption",
    summary: "adoption is rising across Asia",
    evidence: "summary.md: adoption cohorts",
    actor_agent_id: "oren",
    node_type: "discover",
    status: "completed",
  },
  {
    id: "b",
    title: "Synthesis of signals",
    summary: "converges on ship-v2",
    evidence: "evidence/synthesis.md",
    actor_agent_id: "luca",
    node_type: "aggregate",
    status: "in_progress",
  },
  {
    id: "c",
    title: "Pricing model",
    summary: "no adoption evidence yet",
    evidence: "sources/pricing.md",
    actor_agent_id: "nel",
    node_type: "verify",
    status: "completed",
  },
];

/** Legacy projection form: conclusion + agent (the filterable node alias). */
const LEGACY_NODES: ResearchCanvasFilterableNode[] = [
  { id: "d", title: "Market scan", conclusion: "region is flat", agent: "mio" },
  { id: "e", title: "Churn probe", conclusion: "churn is spiking", agent: "kai" },
];

describe("searchStarGraphNodes (LRM-1497 keyword quick navigation)", () => {
  it("blank or whitespace query matches nothing", () => {
    expect(searchStarGraphNodes(CANONICAL_NODES, "")).toEqual([]);
    expect(searchStarGraphNodes(CANONICAL_NODES, "   ")).toEqual([]);
  });

  it("matches across title / conclusion(summary) / evidence / agent", () => {
    // title
    const byTitle = searchStarGraphNodes(CANONICAL_NODES, "pricing");
    expect(byTitle).toHaveLength(1);
    expect(byTitle[0]).toMatchObject({ nodeId: "c", field: "title" });

    // conclusion (summary)
    const byConclusion = searchStarGraphNodes(CANONICAL_NODES, "ship-v2");
    expect(byConclusion).toMatchObject([{ nodeId: "b", field: "conclusion" }]);

    // evidence
    const byEvidence = searchStarGraphNodes(CANONICAL_NODES, "sources/pricing");
    expect(byEvidence).toMatchObject([{ nodeId: "c", field: "evidence" }]);

    // agent (actor_agent_id)
    const byAgent = searchStarGraphNodes(CANONICAL_NODES, "luca");
    expect(byAgent).toMatchObject([{ nodeId: "b", field: "agent" }]);
  });

  it("resolves the canonical union fields (summary & actor_agent_id win)", () => {
    // Canonical form uses summary/actor_agent_id; legacy alias conclusion/agent
    // must still be readable so the same cursor works on both.
    const canonical = searchStarGraphNodes(CANONICAL_NODES, "flat"); // none
    expect(canonical).toEqual([]);
    const legacy = searchStarGraphNodes(LEGACY_NODES, "flat");
    expect(legacy).toMatchObject([{ nodeId: "d", field: "conclusion" }]);
    const legacyAgent = searchStarGraphNodes(LEGACY_NODES, "kai");
    expect(legacyAgent).toMatchObject([{ nodeId: "e", field: "agent" }]);
  });

  it("is case-insensitive and trims the query", () => {
    const hits = searchStarGraphNodes(CANONICAL_NODES, "  ADOPTION  ");
    // "adoption" appears in a.title (first field wins → title) and c.summary.
    const ids = matchNodeIds(hits);
    expect(ids).toContain("a");
    expect(ids).toContain("c");
    expect(ids).not.toContain("b");
    expect(hits.every((h) => h.field === "title" || h.field === "conclusion")).toBe(true);
  });

  it("first matching field wins per node (bounded result set)", () => {
    // node 'a' matches in both title and summary; only one match is emitted.
    const hits = searchStarGraphNodes(CANONICAL_NODES, "adoption");
    const aHits = hits.filter((h) => h.nodeId === "a");
    expect(aHits).toHaveLength(1);
    expect(aHits[0]?.field).toBe("title"); // title is scanned before conclusion
  });

  it("returns a bounded local snippet around the hit", () => {
    const hits = searchStarGraphNodes(CANONICAL_NODES, "pricing");
    expect(hits).toHaveLength(1);
    const hit = hits[0];
    expect(hit?.snippet).toContain("Pricing");
    expect(hit?.snippet.length ?? 0).toBeLessThan(120);
  });

  it("keeps input order and never mutates the nodes", () => {
    const before = JSON.stringify(CANONICAL_NODES);
    const hits = searchStarGraphNodes(CANONICAL_NODES, "adoption");
    expect(hits.map((h) => h.nodeId)).toEqual(["a", "c"]);
    expect(JSON.stringify(CANONICAL_NODES)).toBe(before);
  });

  it("handles null/absent fields gracefully (no fabrication)", () => {
    const sparse: ResearchCanvasFilterableNode[] = [
      { id: "x", title: "only title" },
      { id: "y", summary: null, agent: null, evidence: undefined },
    ];
    expect(searchStarGraphNodes(sparse, "title")).toMatchObject([
      { nodeId: "x", field: "title" },
    ]);
    expect(searchStarGraphNodes(sparse, "nope")).toEqual([]);
  });
});

describe("searchLocateCursor (keep-context result stepping)", () => {
  const ids = ["a", "b", "c"];

  it("returns the first from null going next, last going prev", () => {
    expect(searchLocateCursor(ids, null, "next")).toBe("a");
    expect(searchLocateCursor(ids, null, "prev")).toBe("c");
  });

  it("steps forward and wraps", () => {
    expect(searchLocateCursor(ids, "a", "next")).toBe("b");
    expect(searchLocateCursor(ids, "c", "next")).toBe("a");
  });

  it("steps backward and wraps", () => {
    expect(searchLocateCursor(ids, "c", "prev")).toBe("b");
    expect(searchLocateCursor(ids, "a", "prev")).toBe("c");
  });

  it("starts at the first result when current is outside the set", () => {
    expect(searchLocateCursor(ids, "z", "next")).toBe("a");
    expect(searchLocateCursor(ids, "z", "prev")).toBe("a");
  });

  it("returns null on an empty result set", () => {
    expect(searchLocateCursor([], null, "next")).toBeNull();
    expect(searchLocateCursor([], "a", "prev")).toBeNull();
  });

  it("locating only returns an id — never touches viewport/filter (context-safe)", () => {
    const next = searchLocateCursor(ids, "a", "next");
    expect(typeof next).toBe("string");
  });
});

describe("matchNodeIds + end-to-end quick navigation (AC: 10 s locate)", () => {
  it("maps matches to an ordered locate id list", () => {
    const hits = searchStarGraphNodes(CANONICAL_NODES, "adoption");
    expect(matchNodeIds(hits)).toEqual(["a", "c"]);
  });

  it("stepping through matches navigates each located result in turn", () => {
    const hits = searchStarGraphNodes(CANONICAL_NODES, "adoption");
    const ids = matchNodeIds(hits);
    expect(searchLocateCursor(ids, null, "next")).toBe("a");
    expect(searchLocateCursor(ids, "a", "next")).toBe("c");
    expect(searchLocateCursor(ids, "c", "next")).toBe("a"); // wraps
  });
});
