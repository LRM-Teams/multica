"use client";

import { useMemo } from "react";
import type { Agent, AgentHealthEvent, AgentHealthSummary } from "@multica/core/types";
import { useAgentHealth } from "@multica/core/agents";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT, useTimeAgo } from "../../../i18n";
import {
  formatClockTime,
  formatHealthDuration,
  healthStateConfig,
} from "../../health";

/**
 * Agent Profile → Activity tab Health block (#178 / #266, Iris §3). Read-only
 * surface — NO react/quote/message-row affordances — showing an agent's
 * server⇄daemon connectivity:
 *
 *   head     — big state chip (color + human copy) + "在线 3h" relative
 *              duration + "最后活跃 09:41" last-seen.
 *   timeline — reverse-chron health_events, each a state chip + human copy +
 *              relative time. `recovered` rows are kept even after the summary
 *              settles back to online.
 *
 * Copy is derived ONLY from the HealthState (healthStateConfig + locale). The
 * internal BE event type codes (server_ping_received / daemon_liveness_probe_
 * sent / probe_timeout_reconnect / transport_reconnected) are NEVER rendered
 * (E5). Empty and loading states are explicit, never silent.
 *
 * Transitional: when the health API isn't live yet the summary/events queries
 * settle into an error with `undefined` data — the block shows a neutral
 * "unavailable" head and an empty timeline rather than crashing.
 */
export function HealthBlock({ agent }: { agent: Agent }) {
  // One query feeds both head and timeline (A1 same-source). The View keeps
  // separate loading props for isolated testability; both are driven off the
  // single query's loading state here.
  const { summary, events, isLoading } = useAgentHealth(agent.id);
  return (
    <HealthBlockView
      summary={summary}
      events={events}
      summaryLoading={isLoading}
      eventsLoading={isLoading}
    />
  );
}

export interface HealthBlockViewProps {
  summary: AgentHealthSummary | undefined;
  events: AgentHealthEvent[] | undefined;
  summaryLoading: boolean;
  eventsLoading: boolean;
  // Injectable clock for deterministic tests; defaults to Date.now().
  now?: number;
}

export function HealthBlockView({
  summary,
  events,
  summaryLoading,
  eventsLoading,
  now,
}: HealthBlockViewProps) {
  const { t } = useT("agents");

  return (
    <section
      // w-full + min-w-0 so the block is a narrow-screen card with no
      // horizontal overflow; content wraps rather than pushing the page wide.
      className="flex w-full min-w-0 flex-col gap-4 rounded-lg border bg-background p-5"
      aria-label={t(($) => $.tab_body.activity.health.section_title)}
    >
      <div className="flex items-baseline gap-2">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.tab_body.activity.health.section_title)}
        </h3>
      </div>

      <HealthHead summary={summary} loading={summaryLoading} now={now} />

      <div className="flex flex-col gap-2">
        <h4 className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground/70">
          {t(($) => $.tab_body.activity.health.timeline_title)}
        </h4>
        <HealthTimeline events={events} loading={eventsLoading} />
      </div>
    </section>
  );
}

function HealthHead({
  summary,
  loading,
  now,
}: {
  summary: AgentHealthSummary | undefined;
  loading: boolean;
  now?: number;
}) {
  const { t } = useT("agents");

  if (loading) {
    return (
      <div className="flex flex-col gap-2">
        <Skeleton className="h-7 w-40 rounded-full" />
        <Skeleton className="h-4 w-32" />
      </div>
    );
  }

  // Transitional: no summary and not loading = API not live / errored. Keep
  // the head non-silent with a neutral note rather than blanking.
  if (!summary) {
    return (
      <p className="text-xs italic text-muted-foreground/60">
        {t(($) => $.tab_body.activity.health.head_unavailable)}
      </p>
    );
  }

  // Both timestamps are nullable (Barry's contract): runtime missing / no
  // heartbeat / no lifecycle event yet. Never fake a time — hide the line.
  const duration = formatHealthDuration(summary.state_since, now ?? Date.now());
  const lastSeen = formatClockTime(summary.last_seen_at);

  // LRM-248: live Health head is Online / Offline only — fold
  // recovered / suspected_disconnect / reconnecting → Online. Timeline
  // history rows below keep their event-state chips as diagnostic records.
  const liveState =
    summary.state === "offline" ? ("offline" as const) : ("online" as const);

  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <StateChip state={liveState} size="lg" />
        {duration && (
          <span className="text-sm text-muted-foreground tabular-nums">
            {duration}
          </span>
        )}
      </div>
      {lastSeen && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.tab_body.activity.health.last_seen, { time: lastSeen })}
        </p>
      )}
    </div>
  );
}

function HealthTimeline({
  events,
  loading,
}: {
  events: AgentHealthEvent[] | undefined;
  loading: boolean;
}) {
  const { t } = useT("agents");
  const timeAgo = useTimeAgo();

  // §3-v2 density: the timeline shows only state TRANSITIONS, never every ping
  // and never a big blank card per row.
  //   1. Drop the synthetic current-state event — the head (§3.1) already shows
  //      the current state, so keeping it here would repeat the badge.
  //   2. Fold consecutive same-state runs into a single row (a new row only at a
  //      change point), so a run of identical states can't wall the panel.
  // `recovered` transitions are kept as history (§3c).
  const rows = useMemo(() => {
    if (!events) return [];
    const sorted = events
      .filter((event) => !event.synthetic)
      .toSorted(
        (a, b) =>
          new Date(b.occurred_at).getTime() - new Date(a.occurred_at).getTime(),
      );
    const folded: AgentHealthEvent[] = [];
    for (const event of sorted) {
      const prev = folded[folded.length - 1];
      if (prev && prev.state_after === event.state_after) continue;
      folded.push(event);
    }
    return folded;
  }, [events]);

  if (loading) {
    return (
      <div className="space-y-1" aria-hidden="true">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-5 w-40 rounded" />
        ))}
      </div>
    );
  }

  if (rows.length === 0) {
    return (
      <p className="text-xs italic text-muted-foreground/60">
        {t(($) => $.tab_body.activity.health.empty_events)}
      </p>
    );
  }

  // Compact rows (chip + relative time) — no bordered box per event.
  return (
    <ul className="flex flex-col gap-1.5">
      {rows.map((event) => (
        <li
          key={event.id}
          className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs"
        >
          <StateChip state={event.state_after} size="sm" />
          <span className="text-muted-foreground">{timeAgo(event.occurred_at)}</span>
        </li>
      ))}
    </ul>
  );
}

// The state chip — the single visual atom shared by the head and every
// timeline row. Copy comes ONLY from the HealthState via the locale; the
// internal BE event `type` is never passed in here (E5).
function StateChip({
  state,
  size,
}: {
  state: AgentHealthSummary["state"];
  size: "lg" | "sm";
}) {
  const { t } = useT("agents");
  const cfg = healthStateConfig[state];
  const Icon = cfg.icon;
  const label = t(($) => $.tab_body.activity.health[`state_${cfg.labelKey}`]);
  const sizing =
    size === "lg"
      ? "gap-1.5 px-2.5 py-1 text-sm font-medium"
      : "gap-1 px-2 py-0.5 text-xs font-medium";
  const iconSize = size === "lg" ? "h-4 w-4" : "h-3 w-3";
  return (
    <span
      className={`inline-flex shrink-0 items-center rounded-full ${cfg.chipClass} ${sizing}`}
    >
      <Icon className={`${iconSize} shrink-0`} aria-hidden="true" />
      {label}
    </span>
  );
}
