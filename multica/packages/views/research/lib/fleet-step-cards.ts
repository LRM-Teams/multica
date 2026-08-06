import type { ResearchFleetMember, ResearchMessage } from "@multica/core/types";
import { RESEARCH_STAGE_ORDER, resolveStageStepState } from "./research-stages";

export type FleetStepStatus = "done" | "running" | "waiting" | "failed";

export type FleetStepCardModel = {
  kind: "step";
  id: string;
  status: FleetStepStatus;
  title: string;
  stepLabel: string | null;
  summaryHeadline: string;
  summaryDetail: string;
  bullets: string[];
  evidence: string | null;
  mergeCount: number;
  reason: string | null;
  recoveryHint: string | null;
  actorAgentId: string | null;
  createdAt: string;
  showRetry: boolean;
  showReassign: boolean;
};

export type FleetChatFeedItem =
  | { kind: "chat"; message: ResearchMessage }
  | FleetStepCardModel;

function metaRecord(meta: unknown): Record<string, unknown> | null {
  if (!meta || typeof meta !== "object") return null;
  return meta as Record<string, unknown>;
}

function metaString(meta: unknown, key: string): string | null {
  const value = metaRecord(meta)?.[key];
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function metaNumber(meta: unknown, key: string): number | null {
  const value = metaRecord(meta)?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function formatClock(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

/** Collapse enqueue/snapshot dumps into a short human line. */
export function shortProcessLine(body: string, max = 96): string {
  const cleaned = body
    .replace(/\s+/g, " ")
    .replace(/enqueue research wake:[^.。]*/gi, "")
    .replace(/snapshot[^,.。]*/gi, "")
    .trim();
  const first = cleaned.split(/(?<=[.。!?！？])\s+/)[0] || cleaned;
  if (first.length <= max) return first;
  return `${first.slice(0, max - 1).trimEnd()}…`;
}

function wakeMergeKey(message: ResearchMessage): string {
  const reason = metaString(message.meta, "reason") || "unknown";
  const actor = metaString(message.meta, "actor_agent_id") || "";
  return `${reason}::${actor}`;
}

function opTitleFallback(op: string): string {
  switch (op) {
    case "session_kickoff":
      return "开题确认";
    case "wake_failed":
      return "唤醒";
    case "graph_append":
      return "图更新";
    case "source_upsert":
      return "来源入库";
    case "report_patch":
      return "报告更新";
    case "stage_eval":
      return "阶段评估";
    case "session_stopped":
      return "已停止";
    case "session_resumed":
      return "已恢复";
    case "roster_hire":
      return "编制 · 招聘";
    case "roster_optimize":
      return "编制 · 优化";
    case "roster_archive":
      return "编制 · 归档";
    case "product_round_judgment":
      return "产品轮判定";
    case "clarification_question":
      return "澄清提问";
    default:
      return op || "过程";
  }
}

function bulletsFromMeta(message: ResearchMessage, op: string): string[] {
  const bullets: string[] = [];
  const members = metaNumber(message.meta, "member_count");
  if (members != null) bullets.push(`成员 ${members} 人就位`);
  const domain = metaString(message.meta, "fine_domain");
  if (domain) bullets.push(`领域 · ${domain}`);
  const dims = metaNumber(message.meta, "dimensions");
  if (dims != null) bullets.push(`自适应维度 ${dims}`);
  const stage = metaString(message.meta, "stage");
  if (stage && op !== "session_kickoff") bullets.push(`阶段 · ${stage}`);
  const hint = metaString(message.meta, "recovery_hint");
  if (hint && op === "wake_failed") bullets.push(hint);
  return bullets.slice(0, 4);
}

function stepLabelFor(message: ResearchMessage): string | null {
  const stage = metaString(message.meta, "stage");
  const clock = formatClock(message.created_at);
  if (stage && clock) return `${stage} · ${clock}`;
  if (stage) return stage;
  if (clock) return clock;
  return null;
}

function processToStepCard(
  message: ResearchMessage,
  mergeCount = 1,
): FleetStepCardModel {
  const op = metaString(message.meta, "op") || "process";
  const title =
    metaString(message.meta, "title") || opTitleFallback(op);
  const reason = metaString(message.meta, "reason");
  const recoveryHint = metaString(message.meta, "recovery_hint");
  const headline =
    op === "wake_failed"
      ? shortProcessLine(message.body || title, 72)
      : shortProcessLine(message.body || title, 80);
  const detail =
    op === "wake_failed" && mergeCount > 1
      ? `已合并 ${mergeCount} 次同类失败，不再刷屏。`
      : recoveryHint && op === "wake_failed"
        ? recoveryHint
        : "";
  const failed = op === "wake_failed";
  return {
    kind: "step",
    id: message.id,
    status: failed ? "failed" : "done",
    title,
    stepLabel: stepLabelFor(message),
    summaryHeadline: headline,
    summaryDetail: detail,
    bullets: bulletsFromMeta(message, op),
    evidence: message.body?.trim() ? message.body : null,
    mergeCount,
    reason,
    recoveryHint,
    actorAgentId: metaString(message.meta, "actor_agent_id"),
    createdAt: message.created_at,
    showRetry: failed,
    showReassign: failed,
  };
}

/**
 * Build the fleet chat feed: user/agent chat stays as bubbles;
 * process events become step cards; similar wake_failed collapse by reason(+actor).
 */
export function buildFleetChatFeed(
  messages: ResearchMessage[],
): FleetChatFeedItem[] {
  const out: FleetChatFeedItem[] = [];
  const wakeBuckets = new Map<
    string,
    { index: number; count: number; latest: ResearchMessage }
  >();

  for (const message of messages) {
    const isProcess = message.card_kind === "process";
    if (!isProcess) {
      out.push({ kind: "chat", message });
      continue;
    }

    const op = metaString(message.meta, "op");
    // Interactive product-round judgment stays a chat/process card (LRM-913).
    // Clarification options/form stay interactive in the chat feed (LRM-822).
    if (op === "product_round_judgment" || op === "clarification_question") {
      out.push({ kind: "chat", message });
      continue;
    }
    if (op === "wake_failed") {
      const key = wakeMergeKey(message);
      const existing = wakeBuckets.get(key);
      if (existing) {
        existing.count += 1;
        existing.latest = message;
        out[existing.index] = processToStepCard(message, existing.count);
      } else {
        const index = out.length;
        wakeBuckets.set(key, { index, count: 1, latest: message });
        out.push(processToStepCard(message, 1));
      }
      continue;
    }

    out.push(processToStepCard(message, 1));
  }

  return out;
}

/** Presence → running step cards (appended; one per active agent). */
export function presenceRunningCards(
  presence: Record<string, { activity?: string } | undefined>,
  members: ResearchFleetMember[],
): FleetStepCardModel[] {
  const cards: FleetStepCardModel[] = [];
  for (const member of members) {
    const activity = presence[member.agent_id]?.activity?.trim();
    if (!activity) continue;
    const name = member.display_name || member.name || member.role;
    cards.push({
      kind: "step",
      id: `presence-run-${member.agent_id}`,
      status: "running",
      title: name,
      stepLabel: member.role || null,
      summaryHeadline: activity,
      summaryDetail: "",
      bullets: [],
      evidence: null,
      mergeCount: 1,
      reason: null,
      recoveryHint: null,
      actorAgentId: member.agent_id,
      createdAt: "",
      showRetry: false,
      showReassign: false,
    });
  }
  return cards;
}

/** One waiting card for the next upcoming stage while the session is live. */
export function nextStageWaitingCard(
  currentStage: string,
  sessionStatus: string,
): FleetStepCardModel | null {
  if (
    sessionStatus === "completed" ||
    sessionStatus === "archived" ||
    sessionStatus === "paused"
  ) {
    return null;
  }
  const upcoming = RESEARCH_STAGE_ORDER.find(
    (stage) =>
      resolveStageStepState(stage, currentStage, sessionStatus) === "upcoming",
  );
  if (!upcoming) return null;
  return {
    kind: "step",
    id: `wait-${upcoming}`,
    status: "waiting",
    title: upcoming === "s4_delivery" ? "成稿 / 交付" : upcoming,
    stepLabel: upcoming,
    summaryHeadline: "前置步骤完成后继续",
    summaryDetail: "",
    bullets: [],
    evidence: null,
    mergeCount: 1,
    reason: null,
    recoveryHint: null,
    actorAgentId: null,
    createdAt: "",
    showRetry: false,
    showReassign: false,
  };
}
