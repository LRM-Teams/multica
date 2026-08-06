/**
 * Research V6 — node card detail-field extraction (UI-01 / LRM-1475).
 *
 * The canonical `ResearchV6ProjectionNode` carries an OPACQUE `detail` payload
 * plus the top-level owner (`actor_agent_id`). Per the research data-contract
 * spec §3, the card face shows OWNER / OBJECTIVE / CURRENT ACTION / RESOLVED ·
 * PROGRESS · RISK as separate rows — never merged into prose. This module is
 * the single defensive reader of those fields: it reads the documented field
 * names and falls back to the spec's neutral labels ("未分配", "目标未提供",
 * "暂无执行动作", "暂无新进展") when the canonical fact is absent.
 *
 * It NEVER derives a canonical fact from chat, animation, timers or display
 * grouping — it only mirrors what the backend already stated.
 */

/** The card-face rows extracted from one projection node (opaque detail). */
export interface NodeCardFacts {
  /** 负责人 — canonical top-level actor, else detail, else "未分配". */
  owner: string;
  /** 目标 — objective/goal/question/…, else "目标未提供". */
  objective: string;
  /** 当前动作 — attempt phase/method/status text, else "暂无执行动作". */
  currentAction: string;
  /** 已解决 / 新进展 / 风险 counts (undefined = unknown, render neutral). */
  resolvedCount: number | null;
  progressCount: number | null;
  riskCount: number | null;
}

export interface NodeCardFactsInput {
  actorAgentId: string | null;
  title?: string;
  detail: unknown;
}

const FALLBACKS: Pick<NodeCardFacts, "owner" | "objective" | "currentAction"> = {
  owner: "未分配",
  objective: "目标未提供",
  currentAction: "暂无执行动作",
};

/**
 * Read a string field defensively from an opaque record. Accepts string and
 * number primitives; trims and empty-check so a blank field degrades.
 */
function str(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value === "string") {
    const t = value.trim();
    return t.length > 0 ? t : null;
  }
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return null;
}

/** Read a non-negative integer count field defensively (or null when absent). */
function count(value: unknown): number | null {
  const n = Number(value);
  if (Number.isNaN(n) || !Number.isFinite(n)) return null;
  return Math.max(0, Math.round(n));
}

function detailRecord(detail: unknown): Record<string, unknown> {
  return detail && typeof detail === "object" && !Array.isArray(detail)
    ? (detail as Record<string, unknown>)
    : {};
}

/** First present value across the documented aliases. */
function firstOf(rec: Record<string, unknown>, keys: string[]): unknown {
  for (const key of keys) {
    if (key in rec && rec[key] !== null && rec[key] !== undefined) return rec[key];
  }
  return null;
}

/**
 * Extract the card-face row facts for a projection node. Never throws and
 * never returns undefined text — absent facts become the spec's neutral label.
 */
export function nodeCardFacts(input: NodeCardFactsInput): NodeCardFacts {
  const rec = detailRecord(input.detail);

  // 负责人: canonical `actor_agent_id` first, then detail aliases.
  const owner =
    str(input.actorAgentId) ??
    str(firstOf(rec, ["assigned_agent_id", "agent_id", "actor_agent_id"])) ??
    FALLBACKS.owner;

  // 目标: documented aliases from research-node-detail + spec §3.
  const objective =
    str(firstOf(rec, ["objective", "goal", "question", "small_goal", "purpose"])) ??
    FALLBACKS.objective;

  // 当前动作: attempt phase/method/status text.
  const currentAction =
    str(firstOf(rec, ["current_action", "action", "phase"])) ??
    (str(firstOf(rec, ["method", "approach", "strategy"]))
      ? actionFromMethod(str(firstOf(rec, ["method", "approach", "strategy"]))!)
      : null) ??
    statusActionText(str(firstOf(rec, ["execution_status", "phase_status"]))) ??
    FALLBACKS.currentAction;

  const resolvedCount =
    count(firstOf(rec, ["resolved_count", "accepted_count", "resolved"])) ?? null;
  const progressCount =
    count(firstOf(rec, ["progress_count", "latest_progress_count", "progress_count"])) ?? null;
  const riskCount = count(firstOf(rec, ["risk_count", "open_risks", "risk_count"])) ?? null;

  return { owner, objective, currentAction, resolvedCount, progressCount, riskCount };
}

function actionFromMethod(method: string): string {
  return `正在执行 · ${method}`;
}

function statusActionText(statusText: string | null): string | null {
  if (!statusText) return null;
  const s = statusText.toLowerCase();
  if (s.includes("wait") || s.includes("queued")) return "等待运行时接收";
  if (s.includes("distribut") || s.includes("dispatch")) return "已分派，尚未启动";
  if (s.includes("cancel")) return "停止中 · 等待取消确认";
  return null;
}
