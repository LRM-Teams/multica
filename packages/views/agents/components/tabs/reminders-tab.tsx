"use client";

import { useMemo } from "react";
import { useQuery, useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, Repeat } from "lucide-react";
import type { Agent } from "@multica/core/types";
import { ApiError } from "@multica/core/api";
import {
  agentRemindersKeys,
  agentRemindersUpcomingOptions,
  agentRemindersHistoryOptions,
} from "@multica/core/agents/queries";
import {
  adaptUpcomingRow,
  adaptFiredRow,
  type UpcomingReminderRow,
  type FiredReminderRow,
} from "@multica/core/agents/reminder-view-model";
import { useT } from "../../../i18n";
import { useAgentRemindersRealtime } from "./use-agent-reminders-realtime";

interface RemindersTabProps {
  agent: Agent;
}

/**
 * Agent Card Reminders tab — human READ-ONLY per the V2 product contract
 * (docs/superpowers/specs/2026-07-22-raft-reminder-parity.md). Zero mutation
 * affordances: no schedule/snooze/update/cancel/dismiss button, menu, or
 * inline form anywhere in this file. A human who wants a change asks the
 * Agent; only the owning Agent may act, via its own CLI.
 *
 * Two sections in one scroll pane: Upcoming (scheduled definitions, ordered
 * by next fire) then History (fired occurrences, cursor-paginated
 * newest-first). These are genuinely different data — a History row
 * describes one past OCCURRENCE, not the parent definition's current state;
 * a still-recurring reminder's past fire must not read as "this reminder is
 * done."
 */
export function RemindersTab({ agent }: RemindersTabProps) {
  const { t } = useT("agents");
  const queryClient = useQueryClient();
  useAgentRemindersRealtime(agent.id);

  const {
    data: upcomingData,
    isLoading: upcomingLoading,
    isError: upcomingIsError,
    error: upcomingError,
  } = useQuery(agentRemindersUpcomingOptions(agent.id));
  const {
    data: historyData,
    isLoading: historyLoading,
    isError: historyIsError,
    error: historyError,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = useInfiniteQuery(agentRemindersHistoryOptions(agent.id));

  const upcomingRows = useMemo<UpcomingReminderRow[]>(
    () =>
      (upcomingData?.definitions ?? [])
        .map(adaptUpcomingRow)
        .filter((row): row is UpcomingReminderRow => row !== null),
    [upcomingData],
  );
  const historyRows = useMemo<FiredReminderRow[]>(
    () =>
      (historyData?.pages ?? []).reduce<FiredReminderRow[]>((rows, page) => {
        for (const raw of page.occurrences) {
          const row = adaptFiredRow(raw);
          if (row) rows.push(row);
        }
        return rows;
      }, []),
    [historyData],
  );

  const inaccessible =
    (upcomingError instanceof ApiError && upcomingError.status === 403) ||
    (historyError instanceof ApiError && historyError.status === 403);

  if (inaccessible) {
    return (
      <output className="block p-4 text-xs text-muted-foreground">
        {t(($) => $.reminders.inaccessible)}
      </output>
    );
  }

  const retry = () => {
    void queryClient.invalidateQueries({ queryKey: agentRemindersKeys.all(agent.id) });
  };

  // Empty state is decided at THIS aggregate level, not independently per
  // section — a genuinely empty Agent (no reminders anywhere, ever) reads
  // as one honest "No reminders yet", not two stacked "No upcoming.../No
  // fired..." messages that redundantly say the same thing twice. Only
  // once one section has real content does the other's specific empty
  // copy become meaningful (it's now telling you something the other
  // section's content doesn't already imply).
  const upcomingSettled = !upcomingLoading && !upcomingIsError;
  const historySettled = !historyLoading && !historyIsError;
  const bothGenuinelyEmpty =
    upcomingSettled && historySettled && upcomingRows.length === 0 && historyRows.length === 0;

  if (bothGenuinelyEmpty) {
    return (
      <div className="p-4 text-xs text-muted-foreground">{t(($) => $.reminders.empty_all)}</div>
    );
  }

  return (
    <div className="flex min-w-0 flex-col">
      <section aria-label={t(($) => $.reminders.upcoming_heading)}>
        <SectionHeading label={t(($) => $.reminders.upcoming_heading)} />
        {upcomingLoading ? (
          <p className="px-3 py-3 text-xs text-muted-foreground md:px-4">
            {t(($) => $.reminders.loading)}
          </p>
        ) : upcomingIsError ? (
          <ErrorState onRetry={retry} label={t(($) => $.reminders.error_title)} retryLabel={t(($) => $.reminders.retry)} />
        ) : upcomingRows.length === 0 ? (
          <p className="px-3 py-3 text-xs text-muted-foreground md:px-4">
            {t(($) => $.reminders.empty_upcoming)}
          </p>
        ) : (
          <ul className="divide-y">
            {upcomingRows.map((row) => (
              <UpcomingRowView key={row.id} row={row} />
            ))}
          </ul>
        )}
      </section>

      <section aria-label={t(($) => $.reminders.history_heading)}>
        <SectionHeading label={t(($) => $.reminders.history_heading)} />
        {historyLoading ? (
          <p className="px-3 py-3 text-xs text-muted-foreground md:px-4">
            {t(($) => $.reminders.loading)}
          </p>
        ) : historyIsError ? (
          <ErrorState onRetry={retry} label={t(($) => $.reminders.error_title)} retryLabel={t(($) => $.reminders.retry)} />
        ) : historyRows.length === 0 ? (
          <p className="px-3 py-3 text-xs text-muted-foreground md:px-4">
            {t(($) => $.reminders.empty_history)}
          </p>
        ) : (
          <>
            <ul className="divide-y">
              {historyRows.map((row) => (
                <FiredRowView key={`${row.id}:${row.firedAt}`} row={row} />
              ))}
            </ul>
            {hasNextPage && (
              <div className="px-3 py-3 md:px-4">
                <button
                  type="button"
                  onClick={() => void fetchNextPage()}
                  disabled={isFetchingNextPage}
                  className="text-xs font-medium text-muted-foreground hover:text-foreground disabled:opacity-50"
                >
                  {t(($) => $.reminders.load_more)}
                </button>
              </div>
            )}
          </>
        )}
      </section>
    </div>
  );
}

function SectionHeading({ label }: { label: string }) {
  return (
    <div className="border-b bg-muted/30 px-3 py-1.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground md:px-4">
      {label}
    </div>
  );
}

function ErrorState({
  label,
  retryLabel,
  onRetry,
}: {
  label: string;
  retryLabel: string;
  onRetry: () => void;
}) {
  return (
    <div className="flex flex-col items-start gap-1.5 px-3 py-3 md:px-4" role="alert">
      <p className="text-xs text-muted-foreground">{label}</p>
      {/* Retry re-fetches the reminders query — it doesn't mutate any
          reminder, so it's not a Schedule/Snooze/Update/Cancel affordance. */}
      <button
        type="button"
        onClick={onRetry}
        className="text-xs font-medium text-primary hover:underline"
      >
        {retryLabel}
      </button>
    </div>
  );
}

function CadenceLabel({ row }: { row: { cadence: UpcomingReminderRow["cadence"] } }) {
  const { t } = useT("agents");
  if (row.cadence.kind === "one_shot") {
    return <span className="text-muted-foreground">{t(($) => $.reminders.one_shot)}</span>;
  }
  return (
    <span className="inline-flex items-center gap-1 text-muted-foreground">
      <Repeat className="size-3" aria-hidden />
      {row.cadence.description}
    </span>
  );
}

function AnchorLink({ anchor }: { anchor: UpcomingReminderRow["anchor"] }) {
  const { t } = useT("agents");
  if (!anchor.available) {
    return <span className="text-muted-foreground">{t(($) => $.reminders.anchor_unavailable)}</span>;
  }
  // `href` is a server-computed, already-authorized internal path (never
  // built from raw ids client-side) — `?message=` (kind: "channel") or
  // `?thread=<root>&message=<reply>` (kind: "thread"). Channels' page-mount
  // deep-link handling resolves either shape into the right surface
  // (main-timeline highlight vs opening ThreadPanel + highlighting the
  // reply inside it) — this link needs no special click handling itself.
  return (
    <a href={anchor.href} className="truncate text-primary hover:underline" title={anchor.label}>
      {anchor.label}
    </a>
  );
}

function TimezoneTag({ cadence }: { cadence: UpcomingReminderRow["cadence"] }) {
  const { t } = useT("agents");
  // Only a calendar cadence (daily/weekly) resolves against a schedule-time-
  // locked timezone in a way that's meaningfully different from "just a
  // moment" — an interval cadence or a one-shot fire-at instant has none.
  if (cadence.kind !== "recurring" || cadence.family !== "calendar" || !cadence.timezone) return null;
  return (
    <span className="text-[10px] text-muted-foreground" title={t(($) => $.reminders.timezone_label)}>
      {cadence.timezone}
    </span>
  );
}

function UpcomingRowView({ row }: { row: UpcomingReminderRow }) {
  const { t } = useT("agents");
  return (
    <li className="flex flex-col gap-1 px-3 py-3 text-xs md:px-4">
      <div className="flex items-center gap-1.5">
        <Bell className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <p className="min-w-0 line-clamp-2 font-medium text-foreground" title={row.title}>
          {row.title}
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-5 text-muted-foreground">
        <span>
          {t(($) => $.reminders.next_fire_label)}: {formatInstant(row.nextFireAt)}
        </span>
        <CadenceLabel row={row} />
        <TimezoneTag cadence={row.cadence} />
      </div>
      <div className="pl-5">
        <AnchorLink anchor={row.anchor} />
      </div>
    </li>
  );
}

function FiredRowView({ row }: { row: FiredReminderRow }) {
  const { t } = useT("agents");
  return (
    <li className="flex flex-col gap-1 px-3 py-3 text-xs md:px-4">
      <div className="flex items-center gap-1.5">
        <Bell className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <p className="min-w-0 line-clamp-2 font-medium text-foreground" title={row.title}>
          {row.title}
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-5 text-muted-foreground">
        <span>
          {t(($) => $.reminders.fired_label)}: {formatInstant(row.firedAt)}
        </span>
        {/* This describes the DEFINITION's own state, distinct from "this row
            fired" — a recurring definition that's still `scheduled` must not
            read as if this past occurrence terminated it. */}
        <CadenceLabel row={row} />
        <TimezoneTag cadence={row.cadence} />
      </div>
      <div className="pl-5">
        <AnchorLink anchor={row.anchor} />
      </div>
    </li>
  );
}

function formatInstant(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  // Viewer's own locale/zone for the human-readable instant — the LOCKED
  // schedule timezone is shown separately via `TimezoneTag`, never implied
  // by this formatted string.
  return date.toLocaleString();
}
