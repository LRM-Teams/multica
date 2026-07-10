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

function ActivityRow({ event, time }: { event: ActivityEvent; time: string }) {
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
  // (tool target, reasons, "Message received") stay inline.
  const expandable =
    !!subtext && !presentation.subtextKey && (event.kind === "thinking" || event.kind === "text");
  return (
    <div
      className="flex items-baseline gap-3 py-1"
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
    </div>
  );
}

/**
 * Read-only agent-activity narrative timeline (#267). One time-ordered stream —
 * each row = `time · source dot · human label · optional subtext`. Default shows
 * only BE `user_facing` narrative events; `diagnostic_only` and internal
 * boundary events stay out of the ordinary surface. Never renders raw
 * command/output for tool rows. Rendered by the Activity tab (agent overview
 * page and channel side panel). NOTE: the profile "Recent activity" surface is a
 * separate server task-summary projection today; converging it onto this
 * `activityPresentation` timeline is a tracked #382 follow-up.
 */
export function ActivityTimeline({
  events,
}: {
  events: ActivityEvent[];
  /**
   * Reserved for the compact profile "Recent activity" surface — the #382
   * follow-up that converges it onto this timeline. Not yet wired to a consumer.
   */
  compact?: boolean;
}) {
  const { t } = useT("agents");
  const tz = useViewingTimezone();

  const shown = useMemo(
    () => events.filter(isNarrativeActivityEvent),
    [events],
  );

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
        />
      ))}
    </div>
  );
}
