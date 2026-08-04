import type { ResearchPresenceMap } from "@multica/core/research";
import type { ResearchFleetMember, ResearchGraphNode } from "@multica/core/types";
import type { ResearchExecutionAgent } from "./research-execution-panel-fixture";

const STATUS_ACTION: Record<ResearchExecutionAgent["status"], string> = {
  queued: "等待开始当前任务",
  running: "正在执行当前任务",
  done: "最近任务已完成",
  failed: "最近任务执行失败",
  stale: "执行状态已过期",
  idle: "当前没有可领取的小任务",
};

const STATUS_TIME: Record<ResearchExecutionAgent["status"], string> = {
  queued: "排队中",
  running: "执行中",
  done: "最近更新",
  failed: "执行失败",
  stale: "长时间未更新",
  idle: "空闲",
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
        action: signal?.activity || STATUS_ACTION[status],
        actionDetail: signal?.activity || undefined,
        failureReason:
          status === "failed" ? "任务未完成，可查看最近活动后重试。" : undefined,
        timeLabel: STATUS_TIME[status],
        currentNodeId: node?.id,
        locationLabel: node?.title || undefined,
      };
    });
}
