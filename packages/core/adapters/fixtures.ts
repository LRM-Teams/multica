/**
 * Deterministic contract fixtures for the adapters. These are REAL contracts
 * (following the V5 types and the V6 §7.1/§7.2 plan contract), not fake data:
 * they are the seed sets used to prove each adapter's field mapping and the
 * shared render layer. No production path imports these.
 */
import type { ResearchGraphEdge, ResearchGraphNode } from "../types/research";
import type {
  ResearchV6Delta,
  ResearchV6ProjectionEdge,
  ResearchV6ProjectionNode,
  ResearchV6Snapshot,
} from "../types/research-v6";
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

const V6_RUN_ID = "run-v6-contract-fixture";

/** Canonical stable node id — `${runId}:${entityKind}:${entityId}` (§7.1). */
function v6NodeId(kind: string, id: string): string {
  return `${V6_RUN_ID}:${kind}:${id}`;
}

function v6Node(
  partial: Partial<ResearchV6ProjectionNode> &
    Pick<ResearchV6ProjectionNode, "entity_kind" | "entity_id" | "title">,
): ResearchV6ProjectionNode {
  const id = v6NodeId(partial.entity_kind, partial.entity_id);
  return {
    id,
    run_id: V6_RUN_ID,
    node_kind: partial.entity_kind as ResearchV6ProjectionNode["node_kind"],
    node_subtype: "",
    schema_version: 1,
    summary: "",
    status: "active",
    importance: 0.5,
    freshness: "fresh:1",
    contract_version: null,
    plan_version: null,
    strategy_version: null,
    actor_agent_id: null,
    task_id: null,
    attempt_id: null,
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    detail: {},
    created_sequence: null,
    updated_sequence: null,
    terminal_sequence: null,
    ...partial,
  };
}

function v6Edge(
  id: string,
  fromKind: string,
  fromId: string,
  toKind: string,
  toId: string,
  relation: ResearchV6ProjectionEdge["edge_type"],
): ResearchV6ProjectionEdge {
  return {
    id,
    run_id: V6_RUN_ID,
    from_node_id: v6NodeId(fromKind, fromId),
    to_node_id: v6NodeId(toKind, toId),
    edge_type: relation,
    created_sequence: null,
    tombstoned_at_sequence: null,
  };
}

/** A small §7.1 snapshot covering several node_kinds + typed edges. */
export function v6FixtureSnapshot(): ResearchV6Snapshot {
  const nodes: ResearchV6ProjectionNode[] = [
    v6Node({ entity_kind: "question", entity_id: "q1", title: "Which provider?" }),
    v6Node({ entity_kind: "claim", entity_id: "c1", title: "A is cheaper" }),
    v6Node({ entity_kind: "claim", entity_id: "c2", title: "B has lower fees" }),
    v6Node({ entity_kind: "hypothesis", entity_id: "h1", title: "Volume drives A" }),
    v6Node({
      entity_kind: "insight",
      entity_id: "i1",
      title: "Cost trade-off is volume-dependent",
      importance: 0.9,
    }),
    v6Node({
      entity_kind: "unknown_future_kind",
      entity_id: "u1",
      title: "Future node",
      importance: 0.3,
    }),
  ];
  const edges: ResearchV6ProjectionEdge[] = [
    v6Edge("e1", "question", "q1", "claim", "c1", "decomposes"),
    v6Edge("e2", "question", "q1", "claim", "c2", "decomposes"),
    v6Edge("e3", "hypothesis", "h1", "claim", "c1", "tests"),
    v6Edge("e4", "claim", "c1", "insight", "i1", "integrates"),
    v6Edge("e5", "claim", "c2", "insight", "i1", "integrates"),
    v6Edge("e6", "claim", "c1", "claim", "c2", "contradicts"),
  ];
  return {
    run_id: V6_RUN_ID,
    snapshot_id: `v6-snap-1`,
    through_event_sequence: 6,
    graph_content_hash: { nodes: "n", edges: "e" },
    nodes,
    edges,
    next_cursor: null,
  };
}

/** Delta that tombstones claim c1 (and its edges) + adds a resolution claim. */
export function v6FixtureDelta(): ResearchV6Delta {
  return {
    from_sequence_exclusive: 6,
    through_sequence: 8,
    node_upserts: [
      v6Node({
        entity_kind: "claim",
        entity_id: "c3",
        title: "A cheaper under volume, B under fixed fee",
        status: "resolved",
        importance: 0.85,
      }),
    ],
    edge_upserts: [
      v6Edge("e7", "question", "q1", "claim", "c3", "resolved_by"),
      v6Edge("e8", "claim", "c3", "insight", "i1", "refines"),
    ],
    // §7.2 tombstones reference full projection node ids (same id space as
    // the snapshot nodes), not bare entity refs.
    node_tombstones: [v6NodeId("claim", "c1")],
    edge_tombstones: ["e1", "e3", "e4", "e6"],
    affected_root_node_ids: [v6NodeId("question", "q1"), v6NodeId("insight", "i1")],
    transition_kind: "insight_staled",
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
