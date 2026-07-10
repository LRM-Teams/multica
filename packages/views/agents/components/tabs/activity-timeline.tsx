"use client";

import { useMemo } from "react";
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
  const presentation = activityPresentation(event);
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
        <span className="text-sm text-foreground">{presentation.label}</span>
        {presentation.subtext && (
          <span className="ml-2 text-xs text-muted-foreground">{presentation.subtext}</span>
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
 * command/output for tool rows. Shared by the Activity tab (full) and the
 * profile/hover card (compact subset).
 */
export function ActivityTimeline({
  events,
}: {
  events: ActivityEvent[];
  /** Profile-card mode: user-facing rows only, no diagnostics toggle. */
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
