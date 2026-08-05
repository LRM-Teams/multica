import type { ResearchPresenceMap } from "@multica/core/research";
import type { ResearchFleetMember, ResearchGraphNode } from "@multica/core/types";
import type {
  ResearchExecutionActionKey,
  ResearchExecutionAgent,
  ResearchExecutionTimeKey,
} from "./research-execution-panel-fixture";

// The panel chrome (status badge, fallback action, last-update label, failure
// reason) is locale-independent: this view-model only carries semantic codes
// and the UI component translates them against the active locale. Hardcoding
// Chinese here is what leaked 混文 into en/ja/ko. Live `signal.activity` text
// (server-provided, already locale-appropriate) remains the primary action.
const STATUS_ACTION_KEY: Record<ResearchExecutionAgent["status"], ResearchExecutionActionKey> = {
  queued: "waiting",
  running: "working",
  done: "recent_done",
  failed: "recent_failed",
  stale: "stale",
  idle: "idle",
};

const STATUS_TIME_KEY: Record<ResearchExecutionAgent["status"], ResearchExecutionTimeKey> = {
  queued: "queued",
  running: "running",
  done: "recent",
  failed: "failed",
  stale: "stale",
  idle: "idle",
};

function initials(name: string): string {
  const compact = name.trim();
  if (!compact) return "AI";
  return compact
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

export function buildResearchExecutionAgents(
  members: readonly ResearchFleetMember[],
  presence: ResearchPresenceMap,
  nodes: readonly ResearchGraphNode[],
): ResearchExecutionAgent[] {
  const nodesById = new Map(nodes.map((node) => [node.id, node]));
  return members
    .filter((member) => member.status !== "archived")
    .map((member) => {
      const signal = presence[member.agent_id];
      const status = signal?.phase ?? "idle";
      const name = member.display_name || member.name || member.role || "Agent";
      const node = signal?.nodeId ? nodesById.get(signal.nodeId) : undefined;
      return {
        id: member.agent_id,
        name,
        role: member.role || signal?.role || "worker",
        initials: initials(name),
        avatarUrl: member.avatar_url ?? undefined,
        status,
        action: signal?.activity || undefined,
        actionKey: signal?.activity ? undefined : STATUS_ACTION_KEY[status],
        actionDetail: signal?.activity || undefined,
        failureReasonKey: status === "failed" ? "failed" : undefined,
        timeKey: STATUS_TIME_KEY[status],
        currentNodeId: node?.id,
        locationLabel: node?.title || undefined,
      };
    });
}
