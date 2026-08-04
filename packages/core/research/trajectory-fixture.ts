import type { TrajectoryInput } from "./trajectory-graph";

/** Typed coverage fixture shared by trajectory layout, panel and card work. */
export const trajectoryGraphFixture = {
  nodes: [
    { id: "merge", taskId: "merge", attempt: 1, sequence: 7, parentIds: ["finding", "gate"], nodeType: "finding", status: "merged", timestamp: "2026-08-04T10:07:00Z", title: "Merge", agentId: "lead" },
    { id: "gate", taskId: "gate", attempt: 1, sequence: 6, parentIds: ["dead-end"], nodeType: "stage_gate", timestamp: "2026-08-04T10:06:00Z", title: "Stage gate", agentId: "lead" },
    { id: "finding-shadow", taskId: "finding-task", attempt: 1, sequence: 5, parentIds: ["probe-a2", "missing-upstream"], nodeType: "finding", timestamp: "2026-08-04T10:05:00Z", evidenceRefs: ["source-1"], agentId: "scout" },
    { id: "finding", taskId: "finding-task", attempt: 1, sequence: 5, parentIds: ["probe-a2", "missing-upstream"], nodeType: "finding", timestamp: "2026-08-04T10:05:00Z", title: "Finding", evidenceRefs: ["claim-1"], agentId: "scout" },
    { id: "dead-end", taskId: "dead", attempt: 1, sequence: 4, parentIds: ["probe-a1"], nodeType: "dead_end", timestamp: "2026-08-04T10:04:00Z", title: "Dead end", agentId: "critic" },
    { id: "probe-a2", taskId: "probe", attempt: 2, sequence: 3, parentIds: ["probe-a1"], nodeType: "probe", status: "success", timestamp: "2026-08-04T10:03:00Z", title: "Retry", agentId: "scout" },
    { id: "probe-a1", taskId: "probe", attempt: 1, sequence: 2, parentIds: ["plan"], nodeType: "probe", status: "failed", timestamp: "2026-08-04T10:02:00Z", title: "First attempt", agentId: "scout" },
    { id: "plan", taskId: "plan", attempt: 1, sequence: 1, nodeType: "stage_gate", status: "running", timestamp: "2026-08-04T10:01:00Z", title: "Plan", agentId: "lead" },
  ],
} satisfies TrajectoryInput;
