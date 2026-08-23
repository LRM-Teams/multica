/**
 * Deterministic contract fixtures for the adapters. These are REAL contracts
 * for the active V5 adapter and the shared canvas model. No production path
 * imports these.
 */
import type { ResearchGraphEdge, ResearchGraphNode } from "../types/research";
import type {
  CanvasDelta,
  CanvasEdge,
  CanvasNode,
  CanvasSnapshot,
} from "./canvas-types";

export const V5_SESSION_ID = "session-research-a";

function v5Node(
  partial: Partial<ResearchGraphNode> &
    Pick<ResearchGraphNode, "id" | "node_type" | "title">,
): ResearchGraphNode {
  return {
    session_id: V5_SESSION_ID,
    summary: "",
    status: "active",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...partial,
  };
}

function v5Edge(
  id: string,
  from: string,
  to: string,
  edge_type: ResearchGraphEdge["edge_type"] = "leads_to",
  created_at = "2026-08-01T00:00:00Z",
): ResearchGraphEdge {
  return { id, session_id: V5_SESSION_ID, from_node_id: from, to_node_id: to, edge_type, created_at };
}

/** Goal → fork → two probes → finding, with a supports edge. */
export function v5FixtureGraph(): {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
} {
  return {
    nodes: [
      v5Node({ id: "goal", node_type: "goal", title: "Pick a payment provider" }),
      v5Node({
        id: "fork",
        node_type: "stage_gate",
        title: "Cost & model comparison",
      }),
      v5Node({
        id: "probe-a",
        node_type: "probe",
        title: "Stripe pricing",
        payload: { logic_lane: "source" },
      }),
      v5Node({
        id: "probe-b",
        node_type: "probe",
        title: "PayPal fees",
        payload: { logic_lane: "deep_read" },
      }),
      v5Node({
        id: "finding",
        node_type: "finding",
        title: "Stripe is cheaper at volume",
        status: "done",
      }),
    ],
    edges: [
      v5Edge("e-goal-fork", "goal", "fork"),
      v5Edge("e-fork-a", "fork", "probe-a"),
      v5Edge("e-fork-b", "fork", "probe-b"),
      v5Edge("e-a-finding", "probe-a", "finding"),
      v5Edge("e-b-finding", "probe-b", "finding"),
      v5Edge("e-a-supports", "probe-a", "finding", "supports"),
    ],
  };
}

const CANVAS_RUN_ID = "run-canvas-fixture";

function fixtureNodeId(kind: string, id: string): string {
  return `${CANVAS_RUN_ID}:${kind}:${id}`;
}

/** Canvas fixture used to verify the shared reducer and layout model directly. */
export function canvasFixtureSnapshot(): CanvasSnapshot {
  const nodes: CanvasNode[] = [
    cNode(fixtureNodeId("question", "q1"), "question", "Which provider?"),
    cNode(fixtureNodeId("claim", "c1"), "claim", "A is cheaper"),
    cNode(fixtureNodeId("claim", "c2"), "claim", "B has lower fees"),
    cNode(fixtureNodeId("hypothesis", "h1"), "hypothesis", "Volume drives A"),
    {
      ...cNode(
        fixtureNodeId("insight", "i1"),
        "insight",
        "Cost trade-off is volume-dependent",
      ),
      importance: 0.9,
      level: "xxl",
      clusterId: "cluster-cost",
      round: 2,
      confidence: 0.84,
      documentCount: 46,
      conclusionCount: 4,
      derivedFrom: fixtureNodeId("claim", "c1"),
      mergedFrom: [fixtureNodeId("claim", "c1"), fixtureNodeId("claim", "c2")],
    },
    cNode(fixtureNodeId("future", "u1"), "generic", "Future node"),
  ];
  const edges: CanvasEdge[] = [
    cEdge("e1", fixtureNodeId("question", "q1"), fixtureNodeId("claim", "c1"), "decomposes"),
    cEdge("e2", fixtureNodeId("question", "q1"), fixtureNodeId("claim", "c2"), "decomposes"),
    cEdge("e3", fixtureNodeId("hypothesis", "h1"), fixtureNodeId("claim", "c1"), "tests"),
    cEdge("e4", fixtureNodeId("claim", "c1"), fixtureNodeId("insight", "i1"), "integrates"),
    cEdge("e5", fixtureNodeId("claim", "c2"), fixtureNodeId("insight", "i1"), "integrates"),
    cEdge("e6", fixtureNodeId("claim", "c1"), fixtureNodeId("claim", "c2"), "contradicts"),
  ];
  return {
    snapshotId: "canvas-snapshot-1",
    throughEventSequence: 6,
    graphContentHash: "canvas-fixture",
    nodes,
    edges,
    clusters: [
      {
        id: "cluster-cost",
        label: "Cost evidence",
        clusterType: "stable_result",
        memberNodeIds: [fixtureNodeId("insight", "i1")],
        confidence: 0.84,
        documentCount: 46,
        conclusionCount: 4,
      },
    ],
  };
}

/** Canvas delta that replaces one claim and its incident edges. */
export function canvasFixtureDelta(): CanvasDelta {
  return {
    fromSequenceExclusive: 6,
    throughSequence: 8,
    upsertNodes: [
      {
        ...cNode(
          fixtureNodeId("claim", "c3"),
          "claim",
          "A cheaper under volume, B under fixed fee",
        ),
        status: "resolved",
        importance: 0.85,
      },
    ],
    upsertEdges: [
      cEdge("e7", fixtureNodeId("question", "q1"), fixtureNodeId("claim", "c3"), "resolved_by"),
      cEdge("e8", fixtureNodeId("claim", "c3"), fixtureNodeId("insight", "i1"), "refines"),
    ],
    tombstoneNodeIds: [fixtureNodeId("claim", "c1")],
    tombstoneEdgeIds: ["e1", "e3", "e4", "e6"],
    affectedRootIds: [fixtureNodeId("question", "q1"), fixtureNodeId("insight", "i1")],
    transitionKind: "insight_staled",
  };
}

function cNode(id: string, kind: string, title: string): CanvasNode {
  return {
    id,
    kind,
    title,
    summary: "",
    status: "active",
    importance: 0.5,
    freshness: 0.5,
    detailRef: id,
    payload: {},
    createdAt: "2026-08-01T00:00:00Z",
    updatedAt: "2026-08-01T00:00:00Z",
  };
}

function cEdge(id: string, from: string, to: string, relation: string): CanvasEdge {
  return { id, from, to, relation, createdAt: "2026-08-01T00:00:00Z" };
}

/**
 * Two disconnected canvas components. Tombstoning a2 (component A) must NOT
 * move component B — proving a visibility tombstone only triggers local
 * recompute (AC2).
 *   A: a1 -> a2 -> a3
 *   B: b1 -> b2
 */
export function twoComponentSnapshot(): CanvasSnapshot {
  const nodes = [
    cNode("a1", "task", "A1"),
    cNode("a2", "claim", "A2"),
    cNode("a3", "insight", "A3"),
    cNode("b1", "question", "B1"),
    cNode("b2", "claim", "B2"),
  ];
  const edges = [
    cEdge("ea1", "a1", "a2", "produces"),
    cEdge("ea2", "a2", "a3", "derived_from"),
    cEdge("eb1", "b1", "b2", "decomposes"),
  ];
  return {
    snapshotId: "two-component-1",
    throughEventSequence: 1,
    graphContentHash: "recomputed-in-tests",
    nodes,
    edges,
  };
}

/** Tombstone a2 + its two dangling edges (+ a replacement a4 in component A). */
export function tombstoneA2Delta(): CanvasDelta {
  return {
    fromSequenceExclusive: 1,
    throughSequence: 3,
    upsertNodes: [cNode("a4", "claim", "A4 replacement")],
    upsertEdges: [cEdge("ea3", "a1", "a4", "produces"), cEdge("ea4", "a4", "a3", "derived_from")],
    tombstoneNodeIds: ["a2"],
    tombstoneEdgeIds: ["ea1", "ea2"],
    affectedRootIds: [],
    transitionKind: "insight_staled",
  };
}
