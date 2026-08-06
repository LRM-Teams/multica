import type {
  ResearchV6Delta,
  ResearchV6ProjectionEdge,
  ResearchV6ProjectionNode,
  ResearchV6Snapshot,
  ResearchV6TransitionKind,
} from "../types/research-v6";

/**
 * Deterministic contract fixtures for the Research V6 Graph Projection.
 *
 * The backend projection HTTP/WS surface is still landing; these fixtures
 * strictly follow design doc 7.1 / 7.2 so FE slices can build the cache and
 * its tests without faking production behavior. They are explicitly named and
 * exported — never silently injected into prod query functions.
 */

const RUN_ID = "run-fixture";

function node(
  seq: number,
  kind: string,
  entityId: string,
  overrides: Partial<ResearchV6ProjectionNode> = {},
): ResearchV6ProjectionNode {
  const id = `${RUN_ID}:${kind}:${entityId}`;
  return {
    id,
    run_id: RUN_ID,
    entity_kind: kind,
    entity_id: entityId,
    node_kind: kind,
    node_subtype: "",
    schema_version: 1,
    title: `${kind} ${entityId}`,
    summary: `summary of ${kind}:${entityId}`,
    status: "running",
    importance: 1,
    freshness: null,
    contract_version: "1",
    plan_version: "1",
    strategy_version: "1",
    actor_agent_id: null,
    task_id: null,
    attempt_id: null,
    created_at: null,
    updated_at: null,
    cost: null,
    detail: { entityId },
    created_sequence: seq,
    updated_sequence: seq,
    terminal_sequence: null,
    ...overrides,
  };
}

function edge(
  seq: number,
  from: string,
  to: string,
  edgeType: string,
): ResearchV6ProjectionEdge {
  return {
    id: `${RUN_ID}:edge:${seq}:${from}->${to}`,
    run_id: RUN_ID,
    from_node_id: from,
    to_node_id: to,
    edge_type: edgeType,
    created_sequence: seq,
    tombstoned_at_sequence: null,
  };
}

function hashOf(nodes: ResearchV6ProjectionNode[], edges: ResearchV6ProjectionEdge[]) {
  return {
    nodes: nodes.map((n) => n.id).join("|"),
    edges: edges.map((e) => e.id).join("|"),
  };
}

/** A small, stable snapshot: goal + one branch + one task, through seq 2. */
export function researchV6FixtureSnapshot(): ResearchV6Snapshot {
  const goal = node(0, "question", "goal-1");
  const branch = node(1, "branch", "branch-1");
  const task = node(2, "task", "task-1");
  const nodes = [goal, branch, task];
  const edges = [
    edge(1, goal.id, branch.id, "decomposes"),
    edge(2, branch.id, task.id, "triggered"),
  ];
  return {
    snapshot_id: "snap-fixture-2",
    run_id: RUN_ID,
    through_event_sequence: 2,
    graph_content_hash: hashOf(nodes, edges),
    nodes,
    edges,
    next_cursor: null,
  };
}

/** A contiguous delta [2, 4): accepts a result, adds an insight + claim. */
export function researchV6FixtureDelta(): ResearchV6Delta {
  const branch = `${RUN_ID}:branch:branch-1`;
  const task = `${RUN_ID}:task:task-1`;
  const insight = node(4, "insight", "insight-1");
  const claim = node(3, "claim", "claim-1");
  return {
    from_sequence_exclusive: 2,
    through_sequence: 4,
    node_upserts: [claim, insight],
    edge_upserts: [
      edge(3, task, claim.id, "produced"),
      edge(4, claim.id, insight.id, "derived_from"),
    ],
    node_tombstones: [],
    edge_tombstones: [],
    affected_root_node_ids: [branch],
    transition_kind: "result_accepted" as ResearchV6TransitionKind,
  };
}

export const researchV6Fixtures = {
  runId: RUN_ID,
  snapshot: researchV6FixtureSnapshot,
  delta: researchV6FixtureDelta,
  node,
  edge,
};
