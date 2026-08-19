"use client";

import { useT } from "./use-t";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useTimeAgo } from "./use-time-ago";
import {
  formatListTime,
  formatMessageTime,
  fullTimestamp,
  localTime,
} from "./use-message-time";
import { useViewingTimezone } from "../common/use-viewing-timezone";

// LRM-763: the single entry point for rendering a timestamp. Pages must not
// hand-write toLocale*/self-built relative strings — pick the kind that
// matches the surface:
//   list     sidebar conversation rows  (today HH:MM / 昨天 / 星期X / date)
//   message  inline message timestamp   (the frozen message-bubble contract)
//   relative activity/system surfaces   (刚刚 / N 分钟前 …)
//   clock    bare HH:MM (group gutter)
//   full     absolute locale timestamp
export type TimeKind = "list" | "message" | "relative" | "clock" | "full";

export function Time({
  kind,
  value,
  className,
  title = true,
}: {
  kind: TimeKind;
  /** ISO timestamp string (e.g. message.created_at). */
  value: string;
  className?: string;
  /** Hover tooltip with the full absolute timestamp (default on). */
  title?: boolean;
}) {
  const { t, i18n } = useT("common");
  const tz = useViewingTimezone();
  const timeAgo = useTimeAgo();
  const locale = i18n?.language || "en";
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return null;
  const now = Date.now();
  const text =
    kind === "list"
      ? formatListTime(ms, now, tz, locale, {
          today: t(($) => $.time.today),
          yesterday: t(($) => $.time.yesterday),
        })
      : kind === "message"
        ? formatMessageTime(ms, now, tz, {
            today: t(($) => $.time.today),
            yesterday: t(($) => $.time.yesterday),
          })
        : kind === "relative"
          ? timeAgo(value)
          : kind === "clock"
            ? localTime(ms, tz)
            : fullTimestamp(ms, tz, locale);
  const timestamp = new Date(ms).toISOString();
  const full = fullTimestamp(ms, tz, locale);
  const timeEl = (
    <time dateTime={timestamp} className={className}>
      {text}
    </time>
  );
  if (!title) return timeEl;
  return (
    <Tooltip>
      <TooltipTrigger render={<time dateTime={timestamp} className={className} />}>
        {text}
      </TooltipTrigger>
      <TooltipContent side="top">{full}</TooltipContent>
    </Tooltip>
  );
}
