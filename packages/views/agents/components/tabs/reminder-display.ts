/**
 * Display helpers for the Agent Card Reminders tab — field IA aligned with
 * Frank's Raft reference (relative + absolute time, human cadence, readable
 * anchors). Pure functions so unit tests can pin the strings without mounting
 * the tab.
 */

export type ReminderCadenceDisplay =
  | { kind: "one_shot" }
  | {
      kind: "recurring";
      family: "calendar" | "interval";
      description: string;
      timezone?: string;
    };

/** Match Raft-style absolute clock: `Jul 23 at 22:29` (viewer locale + tz). */
export function formatReminderAbsolute(
  iso: string,
  locale: string,
  timeZone: string,
  atWord: string,
): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const datePart = new Intl.DateTimeFormat(locale, {
    timeZone,
    month: "short",
    day: "numeric",
  }).format(date);
  const timePart = new Intl.DateTimeFormat(locale, {
    timeZone,
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(date);
  const connector = atWord.trim();
  return connector ? `${datePart} ${connector} ${timePart}` : `${datePart} ${timePart}`;
}

/**
 * Relative instant for both upcoming ("in 3 minutes") and history
 * ("3 hours ago") — `Intl.RelativeTimeFormat` keeps locale grammar correct.
 */
export function formatReminderRelative(
  iso: string,
  locale: string,
  nowMs: number = Date.now(),
): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const diffSec = Math.round((date.getTime() - nowMs) / 1000);
  const abs = Math.abs(diffSec);
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (abs < 60) return rtf.format(diffSec, "second");
  if (abs < 3600) return rtf.format(Math.round(diffSec / 60), "minute");
  if (abs < 86_400) return rtf.format(Math.round(diffSec / 3600), "hour");
  if (abs < 86_400 * 30) return rtf.format(Math.round(diffSec / 86_400), "day");
  return rtf.format(Math.round(diffSec / (86_400 * 30)), "month");
}

export interface ReminderCadenceLabels {
  oneShot: string;
  daily: (time: string, timezone: string | undefined) => string;
  weekly: (days: string, time: string, timezone: string | undefined) => string;
  everyMinutes: (count: number) => string;
  everyHours: (count: number) => string;
  everyDays: (count: number) => string;
  /** Fallback when the cadence grammar is unrecognized — still append tz. */
  raw: (description: string, timezone: string | undefined) => string;
}

/**
 * Turn server cadence grammar (`daily@10:00`, `weekly:mon,fri@09:00`,
 * `every:30m`) into the reference-style human line
 * (`daily at 10:00 Asia/Shanghai`). One-shot stays a dedicated label.
 */
export function formatReminderCadence(
  cadence: ReminderCadenceDisplay,
  labels: ReminderCadenceLabels,
): string {
  if (cadence.kind === "one_shot") return labels.oneShot;

  const raw = cadence.description.trim();
  const tz = cadence.timezone;

  const daily = /^daily@(\d{2}:\d{2})$/i.exec(raw);
  if (daily) return labels.daily(daily[1]!, tz);

  const weekly = /^weekly:([a-z,]+)@(\d{2}:\d{2})$/i.exec(raw);
  if (weekly) {
    const days = weekly[1]!.split(",").map((d) => d.trim()).filter(Boolean).join(", ");
    return labels.weekly(days, weekly[2]!, tz);
  }

  const every = /^every:(\d+)([mhd])$/i.exec(raw);
  if (every) {
    const count = Number(every[1]);
    const unit = every[2]!.toLowerCase();
    if (unit === "m") return labels.everyMinutes(count);
    if (unit === "h") return labels.everyHours(count);
    return labels.everyDays(count);
  }

  return labels.raw(raw, tz);
}

/**
 * Bare Raft/workspace-style short ids must never surface as the anchor
 * label — humans need a channel/session name. Server already projects
 * readable `display`; this is a last-line FE guard for both
 * `#multica:<hex>` and `#workspace:<hex>` (AC + Morgan).
 */
export function isBareShortIdAnchorLabel(label: string): boolean {
  return /^#(multica|workspace):[0-9a-f]+$/i.test(label.trim());
}

/** @deprecated Prefer `isBareShortIdAnchorLabel` — kept as a thin alias. */
export function isBareMulticaAnchorLabel(label: string): boolean {
  return isBareShortIdAnchorLabel(label);
}

/** Card status chip — AC: scheduled / 过期 / 取消 (+ fired for History). */
export type ReminderStatusChip = "scheduled" | "overdue" | "fired" | "cancelled";

/**
 * Derive the visible status chip from definition lifecycle + fire instant.
 * Upcoming past-due (`next_fire_at` < now while still scheduled/firing) → overdue.
 * History occurrences → fired, unless the parent definition is cancelled.
 */
export function deriveReminderStatusChip(input: {
  section: "upcoming" | "history";
  definitionStatus: "scheduled" | "firing" | "fired" | "cancelled";
  fireAtIso: string;
  nowMs?: number;
}): ReminderStatusChip {
  if (input.definitionStatus === "cancelled") return "cancelled";
  if (input.section === "history") return "fired";
  const fireMs = Date.parse(input.fireAtIso);
  const now = input.nowMs ?? Date.now();
  if (!Number.isNaN(fireMs) && fireMs < now) return "overdue";
  return "scheduled";
}
