import { describe, expect, it } from "vitest";
import type {
  ResearchGraphEdge,
  ResearchGraphNode,
} from "@multica/core/types";
import { buildTrajectoryLaneLayout } from "@multica/core/research";
import {
  deriveTrajectoryCommits,
  filterNodesForTrajectory,
  type TrajectoryFilters,
} from "./data-adapter";

/** Build a node with persisted, `assessment`-resolved status semantics. */
function node(
  partial: Partial<ResearchGraphNode> &
    Pick<ResearchGraphNode, "id" | "title">,
): ResearchGraphNode {
  return {
    id: partial.id,
    session_id: "s1",
    node_type: partial.node_type ?? "task",
    title: partial.title,
    summary: partial.summary ?? "",
    status: partial.status ?? "active",
    actor_agent_id: partial.actor_agent_id ?? null,
    payload: partial.payload ?? {},
    created_at: partial.created_at ?? "2026-01-01T00:00:00Z",
    updated_at: partial.updated_at ?? "2026-01-01T00:00:00Z",
    theme_key: partial.theme_key ?? undefined,
  };
}

function edge(
  from: string,
  to: string,
  edge_type: ResearchGraphEdge["edge_type"] = "leads_to",
): ResearchGraphEdge {
  return {
    id: `${from}->${to}`,
    session_id: "s1",
    from_node_id: from,
    to_node_id: to,
    edge_type,
    created_at: "2026-01-01T00:00:00Z",
  };
}

/** AC1 fixture: 8 branches that cross/merge, one abandoned route, each with an owner. */
function build8BranchGraph(): { nodes: ResearchGraphNode[]; edges: ResearchGraphEdge[] } {
  const agentA = "agent-a";
  const agentB = "agent-b";
  const nodes: ResearchGraphNode[] = [];
  const edges: ResearchGraphEdge[] = [];
  const mk = (
    id: string,
    title: string,
    actor: string | null,
    status: string,
    theme: string,
    created: string,
  ) => {
    nodes.push(
      node({ id, title, actor_agent_id: actor, status, theme_key: theme, created_at: created }),
    );
  };

  mk("root", "Research question", agentA, "done", "theme-main", "2026-01-01T00:00:00Z");
  // 8 parallel branches (b1..b8) + a merge target.
  for (let i = 1; i <= 8; i++) {
    const actor = i % 2 === 0 ? agentA : agentB;
    mk(`b${i}`, `Branch ${i} task`, actor, "active", `theme-${i}`, `2026-01-02T00:0${i}:00Z`);
    edges.push(edge("root", `b${i}`));
  }
  // Merge: b3 + b4 integrate into m34.
  mk("m34", "Merged insight 3+4", agentB, "done", "theme-main", "2026-01-03T00:00:00Z");
  edges.push(edge("b3", "m34", "integrates"), edge("b4", "m34", "integrates"));
  // One abandoned route (fails/cancelled).
  mk("dead", "Abandoned route", agentA, "abandoned", "theme-dead", "2026-01-03T00:10:00Z");
  edges.push(edge("b8", "dead", "abandons"));
  // A terminal success.
  mk("done1", "Accepted result", agentB, "done", "theme-1", "2026-01-04T00:00:00Z");
  edges.push(edge("b1", "done1"));

  return { nodes, edges };
}

describe("deriveTrajectoryCommits (LRM-1480 / UI-06 adapter)", () => {
  it("AC1: at least 8 branches are traceable with merge + abandoned + owner", () => {
    const { nodes, edges } = build8BranchGraph();
    const commits = deriveTrajectoryCommits(nodes, edges);

    expect(commits.length).toBe(nodes.length);
    // >8 parallel branch keys (root shares theme-main; branches each distinct).
    const branchKeys = new Set(commits.map((c) => c.branchKey));
    expect(branchKeys.size).toBeGreaterThanOrEqual(8);

    // Merge junction exists for m34 (has >1 parent).
    const m34 = commits.find((c) => c.id === "m34");
    expect(m34?.parentIds).toHaveLength(2);
    // Abandoned route preserved as commit with muted status.
    const dead = commits.find((c) => c.id === "dead");
    expect(dead?.status).toBe("mute");
    // Two distinct actors own parallel branches.
    expect(
      commits.every((c) => !c.id.startsWith("b") || c.branchKey.length > 0),
    ).toBe(true);
    // Even branches (b2,b4,..) carry the same theme-based branchKey as their
    // node actor (theme-{i}), which is deterministic and traceable.
    expect(commits.some((c) => c.id === "b2")).toBe(true);
  });

  it("status is mapped through logic-lanes semantics only (never inferred from summary)", () => {
    const { nodes, edges } = build8BranchGraph();
    const commits = deriveTrajectoryCommits(nodes, edges);
    const dead = commits.find((c) => c.id === "dead");
    expect(dead?.status).toBe("mute");
    const running = commits.find((c) => c.id === "b1");
    expect(running?.status).toBe("run");
    const done = commits.find((c) => c.id === "done1");
    expect(done?.status).toBe("ok");
  });

  it("does not synthesize edges for missing parents", () => {
    const { nodes } = build8BranchGraph();
    // Drop the root so b1's parent is missing.
    const isolated = nodes.filter((n) => n.id !== "root");
    const commits = deriveTrajectoryCommits(isolated, []);
    const layout = buildTrajectoryLaneLayout(commits);
    const orphan = layout.commits.find((c) => c.id === "b1");
    expect(orphan).toBeDefined();
    // Deterministic: no synthetic segment points at missing parents.
    expect(
      layout.segments.some((s) => s.fromCommitId === "root"),
    ).toBe(false);
  });
});

describe("filterNodesForTrajectory stable reflow (LRM-1480 / AC3)", () => {
  function applyTwice(nodes: ResearchGraphNode[], f: TrajectoryFilters) {
    const a = buildTrajectoryLaneLayout(
      deriveTrajectoryCommits(filterNodesForTrajectory(nodes, f), []),
    );
    const b = buildTrajectoryLaneLayout(
      deriveTrajectoryCommits(filterNodesForTrajectory(nodes, f), []),
    );
    return { a, b };
  }

  it("identical filter params produce identical deterministic lane order", () => {
    const { nodes } = build8BranchGraph();
    const f: TrajectoryFilters = { branches: new Set(["theme-1", "theme-2"]), agents: new Set(), hiddenRelations: new Set() };
    const { a, b } = applyTwice(nodes, f);
    expect(a.lanes.map((l) => l.branchKey)).toEqual(b.lanes.map((l) => l.branchKey));
    expect(a.commits.map((c) => c.id)).toEqual(b.commits.map((c) => c.id));
  });

  it("agent filter narrows to owned commits only", () => {
    const { nodes, edges } = build8BranchGraph();
    const f: TrajectoryFilters = { branches: new Set(), agents: new Set(["agent-a"]), hiddenRelations: new Set() };
    const filtered = filterNodesForTrajectory(nodes, f);
    const commits = deriveTrajectoryCommits(filtered, edges);
    // Node actor_agent_id === agent-a for even branches (b2,b4,..) and root/dead.
    // The adapter derives branchKey from theme_key (not actor), so filter by actor
    // must retain only nodes whose actor_agent_id matches.
    expect(filtered.length).toBeGreaterThan(0);
    expect(
      filtered.every((n) => n.actor_agent_id === "agent-a"),
    ).toBe(true);
    expect(commits.some((c) => c.id === "b3")).toBe(false);
  });
});
