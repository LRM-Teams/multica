"use client";

import { useMemo, useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useViewingTimezone } from "../../../common/use-viewing-timezone";
import { useT } from "../../../i18n";
import {
  type ActivityEvent,
  type ActivityTone,
  activityPresentation,
  formatActivityTime,
  isNarrativeActivityEvent,
} from "./activity-event";

const TONE_DOT: Record<ActivityTone, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-brand animate-pulse",
  waiting: "bg-warning",
  failure: "bg-destructive",
};

function ActivityRow({
  event,
  time,
  compact = false,
}: {
  event: ActivityEvent;
  time: string;
  compact?: boolean;
}) {
  const { t } = useT("agents");
  const [expanded, setExpanded] = useState(false);
  const presentation = activityPresentation(event);
  const rawLabel = t(($) => $.tab_body.activity.labels[presentation.labelKey]);
  // Locale values are base form (no ellipsis). The trailing "…" is raft's
  // in-progress signal, appended at render for an active tool action only —
  // never on settled rows or non-tool states (wake / compaction / reply).
  const label =
    event.kind === "tool_call" && presentation.tone === "active" ? `${rawLabel}…` : rawLabel;
  const subtext = presentation.subtextKey
    ? t(($) => $.tab_body.activity.subtexts[presentation.subtextKey!])
    : presentation.subtext;
  // Thinking and reply Output carry the model's full text (§2.1: collapse to the
  // first line, click to expand the full content block). Fixed / short subtexts
  // (tool target, reasons, "Message received") stay inline. The compact profile
  // surface never expands — it single-line truncates instead (§2.1: full expand
  // is the Activity tab's job).
  const expandable =
    !compact &&
    !!subtext &&
    !presentation.subtextKey &&
    (event.kind === "thinking" || event.kind === "text");
  return (
    <div
      className={cn("flex items-baseline gap-3", compact ? "py-0.5" : "py-1")}
      data-testid="activity-row"
      data-visibility={event.visibility}
    >
      <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground/70">
        {time}
      </span>
      <span
        className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", TONE_DOT[presentation.tone])}
        aria-hidden
      />
      {compact ? (
        <div className="flex min-w-0 items-baseline gap-2">
          <span className="shrink-0 text-sm text-foreground">{label}</span>
          {subtext && (
            <span className="truncate text-xs text-muted-foreground">{subtext}</span>
          )}
        </div>
      ) : (
        <div className="min-w-0">
          <span className="text-sm text-foreground">{label}</span>
          {subtext && !expandable && (
            <span className="ml-2 text-xs text-muted-foreground">{subtext}</span>
          )}
          {subtext && expandable && (
            <button
              type="button"
              onClick={() => setExpanded((prev) => !prev)}
              aria-expanded={expanded}
              className={cn(
                "mt-0.5 block w-full whitespace-pre-wrap text-left text-xs text-muted-foreground transition-colors hover:text-foreground",
                !expanded && "line-clamp-1",
              )}
            >
              {subtext}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// The compact profile "Recent activity" surface shows only the most recent
// handful of narrative rows (§2.1 / #383). Layout-only delta — same projection.
const COMPACT_RECENT_LIMIT = 5;

/**
 * Read-only agent-activity narrative timeline (#267). One time-ordered stream —
 * each row = `time · source dot · human label · optional subtext`. Shows only BE
 * `user_facing` narrative events; `diagnostic_only` and internal boundary events
 * stay out. Never renders raw command/output for tool rows.
 *
 * Rendered by the Activity tab (agent overview page + channel side panel) and,
 * in `compact` mode, the profile "Recent activity" hover surface (#383) — the
 * SAME `activityPresentation` (labels/tone/folding/active-…), the only delta is
 * layout: last N rows, dense, single-line truncated subtext, no click-to-expand.
 */
export function ActivityTimeline({
  events,
  compact = false,
}: {
  events: ActivityEvent[];
  /** Profile "Recent activity" compact mode: last N narrative rows, no expand. */
  compact?: boolean;
}) {
  const { t } = useT("agents");
  const tz = useViewingTimezone();

  const shown = useMemo(() => {
    const narrative = events.filter(isNarrativeActivityEvent);
    return compact ? narrative.slice(-COMPACT_RECENT_LIMIT) : narrative;
  }, [events, compact]);

  if (shown.length === 0) {
    return (
      <p className="text-xs italic text-muted-foreground/60">
        {t(($) => $.tab_body.activity.timeline_empty)}
      </p>
    );
  }

  return (
    <div className="flex flex-col">
      {shown.map((event) => (
        <ActivityRow
          key={event.id}
          event={event}
          time={formatActivityTime(event.occurred_at, tz)}
          compact={compact}
        />
      ))}
    </div>
  );
}
