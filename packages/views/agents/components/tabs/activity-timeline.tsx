"use client";

import { useMemo, useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useViewingTimezone } from "../../../common/use-viewing-timezone";
import { useT } from "../../../i18n";
import {
  type ActivityEvent,
  type ActivityTone,
  formatActivityTime,
} from "./activity-event";

const TONE_DOT: Record<ActivityTone, string> = {
  wake: "bg-brand",
  action: "bg-brand",
  progress: "bg-warning",
  success: "bg-success",
  failure: "bg-destructive",
  muted: "bg-muted-foreground/40",
};

function ActivityRow({ event, time }: { event: ActivityEvent; time: string }) {
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
        className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", TONE_DOT[event.tone])}
        aria-hidden
      />
      <div className="min-w-0">
        <span className="text-sm text-foreground">{event.label}</span>
        {event.subtext && (
          <span className="ml-2 text-xs text-muted-foreground">{event.subtext}</span>
        )}
      </div>
    </div>
  );
}

/**
 * Read-only agent-activity narrative timeline (#267). One time-ordered stream —
 * each row = `time · source dot · human label · optional subtext`. Default shows
 * only BE-tagged `user_facing` events; `diagnostic_only` events (raw
 * command/error/freshness plumbing) stay behind an explicit "view diagnostics"
 * toggle in the same coherent surface. Never renders raw command/output — the
 * label/subtext come from the BE read model. Shared by the Activity tab (full)
 * and the profile/hover card (compact subset, no diagnostics toggle).
 */
export function ActivityTimeline({
  events,
  compact = false,
}: {
  events: ActivityEvent[];
  /** Profile-card mode: user-facing rows only, no diagnostics toggle. */
  compact?: boolean;
}) {
  const { t } = useT("agents");
  const tz = useViewingTimezone();
  const [showDiagnostics, setShowDiagnostics] = useState(false);

  const userFacing = useMemo(
    () => events.filter((e) => e.visibility === "user_facing"),
    [events],
  );
  const hasDiagnostics = !compact && events.length > userFacing.length;
  const shown = showDiagnostics ? events : userFacing;

  if (userFacing.length === 0 && !showDiagnostics) {
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
      {hasDiagnostics && (
        <button
          type="button"
          onClick={() => setShowDiagnostics((s) => !s)}
          className="mt-1.5 self-start text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          {showDiagnostics
            ? t(($) => $.tab_body.activity.hide_diagnostics)
            : t(($) => $.tab_body.activity.view_diagnostics)}
        </button>
      )}
    </div>
  );
}
