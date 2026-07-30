import { useMemo } from "react";
import { useT } from "./use-t";
import { useViewingTimezone } from "../common/use-viewing-timezone";

export interface MessageDayLabels {
  today: string;
  yesterday: string;
}

// YYYY-MM-DD in the given timezone — a comparable local-day key. en-CA yields
// ISO-ordered parts regardless of the display locale.
function localDateKey(ms: number, tz: string): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: tz,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(ms);
}

// HH:MM, 24-hour (h23), in the given timezone. No AM/PM — aligns with raft.
export function localTime(ms: number, tz: string): string {
  return new Intl.DateTimeFormat("en-GB", {
    timeZone: tz,
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(ms);
}

// Inline message timestamp, relative-date bucketed (pure — inject now/tz for
// tests):
//   today           -> HH:MM
//   yesterday       -> {yesterday} HH:MM
//   earlier this yr -> MM/DD HH:MM   (zero-padded, raft-aligned)
//   previous years  -> YYYY/MM/DD HH:MM
export function formatMessageTime(
  valueMs: number,
  nowMs: number,
  tz: string,
  labels: MessageDayLabels,
): string {
  const time = localTime(valueMs, tz);
  const msg = localDateKey(valueMs, tz);
  const today = localDateKey(nowMs, tz);
  if (msg === today) return time;
  if (msg === localDateKey(nowMs - 86_400_000, tz)) {
    return `${labels.yesterday} ${time}`;
  }
  const [y, m, d] = msg.split("-");
  if (y === today.slice(0, 4)) return `${m}/${d} ${time}`;
  return `${y}/${m}/${d} ${time}`;
}

// How many local days before `nowMs`'s day `valueMs` falls (0 = today,
// 1 = yesterday, …), counted by local-day keys rather than raw 24h math so
// DST transitions can't skew the bucket.
function localDaysAgo(valueMs: number, nowMs: number, tz: string): number {
  const msg = localDateKey(valueMs, tz);
  for (let n = 0; n <= 7; n += 1) {
    if (localDateKey(nowMs - n * 86_400_000, tz) === msg) return n;
  }
  return 8;
}

// Sidebar conversation-list timestamp (LRM-763 contract):
//   today           -> HH:MM
//   yesterday       -> {yesterday}           (no clock — row stays compact)
//   2..6 days ago   -> localized weekday     (zh 星期三 / en Wednesday)
//   earlier this yr -> MM/DD
//   previous years  -> YYYY/MM/DD
// Same bucketing as the message timestamp but day-granular: a list row never
// reads "42 分钟前" — recency lives in the row order, not the label.
export function formatListTime(
  valueMs: number,
  nowMs: number,
  tz: string,
  locale: string,
  labels: MessageDayLabels,
): string {
  const daysAgo = localDaysAgo(valueMs, nowMs, tz);
  if (daysAgo === 0) return localTime(valueMs, tz);
  if (daysAgo === 1) return labels.yesterday;
  if (daysAgo <= 6) {
    return new Intl.DateTimeFormat(locale, { timeZone: tz, weekday: "long" }).format(valueMs);
  }
  const [y, m, d] = localDateKey(valueMs, tz).split("-");
  if (y === localDateKey(nowMs, tz).slice(0, 4)) return `${m}/${d}`;
  return `${y}/${m}/${d}`;
}

// Full absolute timestamp for the hover tooltip (locale-aware, 24-hour).
export function fullTimestamp(valueMs: number, tz: string, locale: string): string {
  return new Intl.DateTimeFormat(locale, {
    timeZone: tz,
    dateStyle: "full",
    timeStyle: "short",
    hourCycle: "h23",
  }).format(valueMs);
}

// Date-divider label: today / yesterday / localized weekday + date.
export function messageDayLabel(
  valueMs: number,
  nowMs: number,
  tz: string,
  locale: string,
  labels: MessageDayLabels,
): string {
  const msg = localDateKey(valueMs, tz);
  const today = localDateKey(nowMs, tz);
  if (msg === today) return labels.today;
  if (msg === localDateKey(nowMs - 86_400_000, tz)) return labels.yesterday;
  return new Intl.DateTimeFormat(locale, {
    timeZone: tz,
    weekday: "long",
    month: "long",
    day: "numeric",
    // Only spell the year on cross-year dividers.
    ...(msg.slice(0, 4) === today.slice(0, 4) ? {} : { year: "numeric" }),
  }).format(valueMs);
}

// True when `valueMs` falls on a different local day than `prevMs` (or there is
// no previous message) — i.e. a date divider should precede this message.
export function startsNewLocalDay(
  valueMs: number,
  prevMs: number | null,
  tz: string,
): boolean {
  if (prevMs === null) return true;
  return localDateKey(valueMs, tz) !== localDateKey(prevMs, tz);
}

// Shared message-time formatter — one source for the inline timestamp, the
// hover tooltip, and the date-divider label. Respects the viewing timezone and
// the active locale. Companion to `useTimeAgo` (relative) — this one is the
// absolute clock/date reading.
export function useMessageTime() {
  const { t, i18n } = useT("common");
  const tz = useViewingTimezone();
  const locale = i18n?.language || "en";
  const labels: MessageDayLabels = {
    today: t(($) => $.time.today),
    yesterday: t(($) => $.time.yesterday),
  };
  const parse = (value: string): number | null => {
    const ms = Date.parse(value);
    return Number.isNaN(ms) ? null : ms;
  };
  return {
    /** Inline bucketed timestamp (today HH:MM / yesterday / MM/DD / YYYY/MM/DD). */
    format: (value: string): string => {
      const ms = parse(value);
      return ms === null ? "" : formatMessageTime(ms, Date.now(), tz, labels);
    },
    /** Full absolute timestamp for a hover title. */
    full: (value: string): string => {
      const ms = parse(value);
      return ms === null ? "" : fullTimestamp(ms, tz, locale);
    },
    /** Date-divider label for `value`'s local day. */
    dayLabel: (value: string): string => {
      const ms = parse(value);
      return ms === null ? "" : messageDayLabel(ms, Date.now(), tz, locale, labels);
    },
    /** Whether a date divider should precede `value` given the previous row. */
    startsNewDay: (value: string, prev: string | null): boolean => {
      const ms = parse(value);
      if (ms === null) return false;
      const prevMs = prev === null ? null : parse(prev);
      return startsNewLocalDay(ms, prevMs, tz);
    },
    /** HH:mm clock for compact group gutter hover (same-day continuations). */
    clock: (value: string): string => {
      const ms = parse(value);
      return ms === null ? "" : localTime(ms, tz);
    },
  };
}

// Precomputes date-divider labels for a message list: maps each message id that
// opens a new local day (including the first) to its divider label. Computed
// once per list (not per row) so a virtualized renderer can look up by id on
// both the Virtuoso and fallback paths without threading indices.
export function useMessageDayDividers(
  messages: readonly { id: string; created_at: string }[],
): Map<string, string> {
  const { t, i18n } = useT("common");
  const tz = useViewingTimezone();
  const locale = i18n?.language || "en";
  const today = t(($) => $.time.today);
  const yesterday = t(($) => $.time.yesterday);
  return useMemo(() => {
    const labels: MessageDayLabels = { today, yesterday };
    const now = Date.now();
    const map = new Map<string, string>();
    for (let i = 0; i < messages.length; i += 1) {
      const msg = messages[i];
      if (!msg) continue;
      const ms = Date.parse(msg.created_at);
      if (Number.isNaN(ms)) continue;
      const prev = messages[i - 1];
      const prevRaw = prev ? Date.parse(prev.created_at) : NaN;
      const prevMs = Number.isNaN(prevRaw) ? null : prevRaw;
      if (startsNewLocalDay(ms, prevMs, tz)) {
        map.set(msg.id, messageDayLabel(ms, now, tz, locale, labels));
      }
    }
    return map;
  }, [messages, tz, locale, today, yesterday]);
}
