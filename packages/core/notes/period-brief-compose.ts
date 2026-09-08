/**
 * Period Brief plan-card helpers. The plan card is session plan state;
 * speech and chips share it. Dispatch is the start tool, not chat XML.
 */

import type { NotePeriodBriefWindow } from "../types";

/** Same intent as Go `looksLikePeriodBriefRequest` — keep the two in lockstep. */
const PERIOD_BRIEF_INTENT_RE =
  /((写|整理|做|生成|帮我).{0,12}(汇报|周报)|period\s*work\s*brief|period\s*brief|weekly\s*report|write\s+(a\s+)?(period\s+work\s+)?reports?|^(report|reports)$)/i;

const PERIOD_BRIEF_UNCONSTRAINED_RE =
  /^(全部|不限范围|不限範圍|unconstrained|full\s+scope|everything)$/i;

export function looksLikePeriodBriefRequest(text: string): boolean {
  return PERIOD_BRIEF_INTENT_RE.test(text.trim());
}

/** Typed 「全部」 is the same as leaving focus empty. */
export function looksLikeUnconstrainedScope(text: string): boolean {
  return text.trim() === "" || PERIOD_BRIEF_UNCONSTRAINED_RE.test(text.trim());
}

export function isPeriodBriefPlanComplete(input: {
  collectorIds: readonly string[];
  customRangeValid?: boolean;
}): boolean {
  return listPeriodBriefPlanGaps(input).length === 0;
}

export type PeriodBriefPlanGap = "computers" | "custom_range";

export function listPeriodBriefPlanGaps(input: {
  collectorIds: readonly string[];
  customRangeValid?: boolean;
}): PeriodBriefPlanGap[] {
  const gaps: PeriodBriefPlanGap[] = [];
  if (input.collectorIds.length === 0) gaps.push("computers");
  if (input.customRangeValid === false) gaps.push("custom_range");
  return gaps;
}

/** In-window 写汇报 button: exact phrase opens the collect card (no soft-confirm). */
export const PERIOD_BRIEF_SATELLITE_ASK = "写汇报";

/** Soft-confirm answers — same strings the platform speech path accepts. */
export const PERIOD_BRIEF_INTENT_YES = "是";
export const PERIOD_BRIEF_INTENT_NO = "否";

export function isPeriodBriefAwaitingIntent(
  plan: { status?: string | null } | null | undefined,
): boolean {
  return (plan?.status ?? "").trim() === "awaiting_intent";
}

export function isPeriodBriefClarifyingPlan(
  plan: {
    status?: string | null;
    window?: string | null;
    collector_agent_ids?: readonly string[] | null;
  } | null | undefined,
): boolean {
  if (!plan || isPeriodBriefAwaitingIntent(plan)) return false;
  const status = (plan.status ?? "").trim();
  if (status === "clarifying") return true;
  // Legacy payloads without status: only a real collect draft, not soft-confirm.
  if (status === "") {
    const window = (plan.window ?? "").trim();
    const collectors = plan.collector_agent_ids ?? [];
    return window.length > 0 || collectors.length > 0;
  }
  return false;
}

export type PeriodBriefComposeSelection = {
  window: NotePeriodBriefWindow;
  date: string;
  start_date: string;
  end_date: string;
  collector_ids: string[];
};

export type PeriodBriefComposeCollector = {
  id: string;
  label: string;
  runtime_mode?: "local" | "cloud" | string | null;
};

export type PeriodBriefComposeRequest = {
  window: NotePeriodBriefWindow;
  date?: string;
  start_date?: string;
  end_date?: string;
  collector_ids: string[];
  focus?: string;
};

export function periodBriefRunLocksComposer(status: string | null | undefined): boolean {
  switch (status) {
    case "planning":
    case "collecting":
    case "synthesizing":
      return true;
    default:
      return false;
  }
}
