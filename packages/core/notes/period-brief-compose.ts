/**
 * Merge chip selections with free-text Period Brief requests.
 * When the text names a window or computer, the text wins.
 */

/** Same intent as Go `looksLikePeriodBriefRequest` — keep the two in lockstep. */
const PERIOD_BRIEF_INTENT_RE =
  /((写|整理|做|生成|帮我).{0,12}(汇报|周报)|period\s*work\s*brief|period\s*brief|weekly\s*report)/i;

export function looksLikePeriodBriefRequest(text: string): boolean {
  return PERIOD_BRIEF_INTENT_RE.test(text.trim());
}

import type { NotePeriodBriefWindow } from "../types";
import { isValidPeriodBriefCustomRange, shiftPeriodBriefCalendarDay } from "./period-brief-window";

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

const YMD = /(\d{4}-\d{2}-\d{2})/g;
const CN_RANGE =
  /(\d{1,2})\s*月\s*(\d{1,2})\s*日?\s*(?:到|至|-|~|—|–)\s*(\d{1,2})\s*月\s*(\d{1,2})\s*日?/;

function yearFromAnchor(anchorYYYYMMDD: string): number {
  const year = Number(anchorYYYYMMDD.slice(0, 4));
  return Number.isFinite(year) ? year : new Date().getUTCFullYear();
}

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

function extractCustomRange(
  text: string,
  anchorYYYYMMDD: string,
): { start_date: string; end_date: string } | null {
  const ymds = [...text.matchAll(YMD)].map((match) => match[1]!).filter(Boolean);
  if (ymds.length >= 2) {
    const start = ymds[0]!;
    const end = ymds[1]!;
    if (isValidPeriodBriefCustomRange(start, end)) return { start_date: start, end_date: end };
    if (isValidPeriodBriefCustomRange(end, start)) return { start_date: end, end_date: start };
  }
  const cn = CN_RANGE.exec(text);
  if (!cn) return null;
  const year = yearFromAnchor(anchorYYYYMMDD);
  const start = `${year}-${pad2(Number(cn[1]))}-${pad2(Number(cn[2]))}`;
  const end = `${year}-${pad2(Number(cn[3]))}-${pad2(Number(cn[4]))}`;
  if (isValidPeriodBriefCustomRange(start, end)) return { start_date: start, end_date: end };
  if (isValidPeriodBriefCustomRange(end, start)) return { start_date: end, end_date: start };
  return null;
}

function extractWindowOverride(
  text: string,
  anchorYYYYMMDD: string,
): { window: NotePeriodBriefWindow; date?: string; start_date?: string; end_date?: string } | null {
  const range = extractCustomRange(text, anchorYYYYMMDD);
  if (range) return { window: "custom", ...range };

  const lower = text.toLowerCase();
  if (/(上个月|上月|last\s+month)/i.test(text)) {
    return { window: "month", date: shiftPeriodBriefCalendarDay(anchorYYYYMMDD, -31) };
  }
  if (/(本月|这个月|这个月份|this\s+month|monthly)/i.test(text) || /\bmonth\b/.test(lower)) {
    return { window: "month", date: anchorYYYYMMDD };
  }
  if (/(上周|上週|last\s+week)/i.test(text)) {
    return { window: "week", date: shiftPeriodBriefCalendarDay(anchorYYYYMMDD, -7) };
  }
  if (/(本周|本週|这周|這周|这一周|這一周|this\s+week|weekly)/i.test(text) || /\bweek\b/.test(lower)) {
    return { window: "week", date: anchorYYYYMMDD };
  }
  if (/(昨天|昨日|yesterday)/i.test(text)) {
    return { window: "day", date: shiftPeriodBriefCalendarDay(anchorYYYYMMDD, -1) };
  }
  if (/(今天|今日|本日|这一天|this\s+day|today|daily)/i.test(text)) {
    return { window: "day", date: anchorYYYYMMDD };
  }
  return null;
}

function extractCollectorOverride(
  text: string,
  collectors: readonly PeriodBriefComposeCollector[],
  selectedIds: readonly string[],
): string[] | null {
  if (collectors.length === 0) return null;
  const lower = text.toLowerCase();
  const named = collectors.filter((collector) => {
    const label = collector.label.trim();
    if (!label) return false;
    if (text.includes(label)) return true;
    const stripped = label.replace(/^采集\s*·\s*(云端\s*·\s*)?/, "").trim();
    if (stripped.length >= 2 && text.includes(stripped)) return true;
    return lower.includes(stripped.toLowerCase());
  });
  if (named.length > 0) return named.map((collector) => collector.id);

  const wantsCloud = /(云端|云上|cloud)/i.test(text);
  const wantsLocal = /(本地|这台电脑|這台電腦|local(\s+computer)?)/i.test(text);
  if (wantsCloud === wantsLocal) return null;

  const matched = collectors.filter((collector) =>
    wantsCloud ? collector.runtime_mode === "cloud" : collector.runtime_mode !== "cloud",
  );
  if (matched.length === 0) return null;
  const ids = matched.map((collector) => collector.id);
  const sameAsChips =
    ids.length === selectedIds.length && ids.every((id) => selectedIds.includes(id));
  if (sameAsChips) return null;
  return ids;
}

export function resolvePeriodBriefComposeRequest(
  selection: PeriodBriefComposeSelection,
  collectors: readonly PeriodBriefComposeCollector[],
  text: string,
): PeriodBriefComposeRequest {
  const trimmed = text.trim();
  const windowOverride = trimmed ? extractWindowOverride(trimmed, selection.date) : null;
  const collectorOverride = trimmed
    ? extractCollectorOverride(trimmed, collectors, selection.collector_ids)
    : null;

  const window = windowOverride?.window ?? selection.window;
  const request: PeriodBriefComposeRequest = {
    window,
    collector_ids: collectorOverride ?? [...selection.collector_ids],
  };
  if (window === "custom") {
    request.start_date = windowOverride?.start_date ?? selection.start_date;
    request.end_date = windowOverride?.end_date ?? selection.end_date;
  } else {
    request.date = windowOverride?.date ?? selection.date;
  }
  if (trimmed) request.focus = trimmed;
  return request;
}

/** One user-visible bubble turn from the chip selection + optional request. */
export function formatPeriodBriefUserTurn(input: {
  windowLabel: string;
  collectorLabels: readonly string[];
  focus?: string;
}): string {
  const lines = ["写汇报", "", `时间：${input.windowLabel.trim() || "本周"}`];
  const computers = input.collectorLabels.map((label) => label.trim()).filter(Boolean);
  if (computers.length > 0) {
    lines.push(`电脑：${computers.join("、")}`);
  }
  const focus = input.focus?.trim();
  if (focus) {
    lines.push("", focus);
  }
  return lines.join("\n");
}

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
