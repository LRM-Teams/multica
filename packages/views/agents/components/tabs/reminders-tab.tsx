"use client";

import { useMemo } from "react";
import { useQuery, useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { Clock, Link2, Repeat } from "lucide-react";
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
  type ReminderCadence,
  type ReminderDefinitionStatus,
} from "@multica/core/agents/reminder-view-model";
import { useT } from "../../../i18n";
import { AppLink } from "../../../navigation/app-link";
import { useOptionalNavigation } from "../../../navigation/context";
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
 *
 * LRM-505 field/IA alignment (not neo-brutal skin): title; clock + relative
 * + absolute time; recurring cadence chip (one-shot: no chip); readable
 * anchor (bare `#workspace:shortId` suppressed upstream); status badge.
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
                  aria-busy={isFetchingNextPage}
                  className="text-xs font-medium text-muted-foreground hover:text-foreground disabled:opacity-50"
                >
                  {isFetchingNextPage ? t(($) => $.reminders.loading) : t(($) => $.reminders.load_more)}
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

/** Wire `daily@09:00` / `weekly:mon,fri@10:30` / `every:30m` → readable chip text. */
export function formatCadenceChipLabel(cadence: Extract<ReminderCadence, { kind: "recurring" }>): string {
  const raw = cadence.description;
  let body = raw;
  const daily = /^daily@(\d{2}:\d{2})$/i.exec(raw);
  const weekly = /^weekly:([^@]+)@(\d{2}:\d{2})$/i.exec(raw);
  const every = /^every:(.+)$/i.exec(raw);
  if (daily) body = `daily at ${daily[1]}`;
  else if (weekly) body = `weekly ${weekly[1]} at ${weekly[2]}`;
  else if (every) body = `every ${every[1]}`;
  if (cadence.family === "calendar" && cadence.timezone) {
    return `${body} ${cadence.timezone}`;
  }
  return body;
}

function RecurrenceChip({ cadence }: { cadence: ReminderCadence }) {
  // AC: recurring shows one cadence+timezone chip; one-shot does not show it.
  if (cadence.kind !== "recurring") return null;
  return (
    <span className="inline-flex max-w-full items-center gap-1 rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
      <Repeat className="size-3 shrink-0" aria-hidden />
      <span className="truncate">{formatCadenceChipLabel(cadence)}</span>
    </span>
  );
}

function AnchorLink({ anchor }: { anchor: UpcomingReminderRow["anchor"] }) {
  const { t } = useT("agents");
  const navigation = useOptionalNavigation();
  if (!anchor.available) {
    return (
      <span className="inline-flex items-center gap-1 text-muted-foreground">
        <Link2 className="size-3 shrink-0" aria-hidden />
        {t(($) => $.reminders.anchor_unavailable)}
      </span>
    );
  }
  // `href` is a server-computed, already-authorized internal path (never
  // built from raw ids client-side) — `?message=` (kind: "channel") or
  // `?thread=<root>&message=<reply>` (kind: "thread"). Both group channels
  // (channels-page.tsx: threadDeepLinkId -> ThreadPanel) and DMs
  // (dm-conversation.tsx: same-shaped threadDeepLinkId/deepLinkMessageId
  // props -> its own inline thread reply list) resolve either shape into the
  // right surface — main-timeline highlight, or opening the thread and
  // highlighting the reply inside it. Must go through AppLink's client-side
  // push, not a plain `<a>`: a full page navigation would defeat "open the
  // EXISTING conversation surface" (it would remount everything from
  // scratch) and is visibly slower than the SPA route change. Falls back to
  // a plain `<a>` only if rendered outside a NavigationProvider (matches
  // channel-system-event-content.tsx's established pattern).
  const className =
    "inline-flex max-w-full items-center gap-1 truncate rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-primary hover:underline";
  const body = (
    <>
      <Link2 className="size-3 shrink-0" aria-hidden />
      <span className="truncate">{anchor.label}</span>
    </>
  );
  return navigation ? (
    <AppLink href={anchor.href} className={className} title={anchor.label}>
      {body}
    </AppLink>
  ) : (
    <a href={anchor.href} className={className} title={anchor.label}>
      {body}
    </a>
  );
}

function formatAbsoluteInstant(iso: string, locale = "en"): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const datePart = date.toLocaleDateString(locale, { month: "short", day: "numeric" });
  const timePart = date.toLocaleTimeString(locale, {
    hour: "numeric",
    minute: "2-digit",
  });
  return `${datePart} at ${timePart}`;
}

function formatRelativeInstant(iso: string, nowMs = Date.now(), locale = "en"): string {
  const target = new Date(iso).getTime();
  if (Number.isNaN(target)) return iso;
  const diffSec = Math.round((target - nowMs) / 1000);
  const abs = Math.abs(diffSec);
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (abs < 60) return rtf.format(diffSec, "second");
  const diffMin = Math.round(diffSec / 60);
  if (Math.abs(diffMin) < 60) return rtf.format(diffMin, "minute");
  const diffHour = Math.round(diffSec / 3600);
  if (Math.abs(diffHour) < 48) return rtf.format(diffHour, "hour");
  const diffDay = Math.round(diffSec / 86400);
  return rtf.format(diffDay, "day");
}

function TimeRow({ iso }: { iso: string }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-muted-foreground">
      <Clock className="size-3.5 shrink-0" aria-hidden />
      <span className="font-medium text-foreground">{formatRelativeInstant(iso)}</span>
      <span aria-hidden>·</span>
      <span>{formatAbsoluteInstant(iso)}</span>
    </div>
  );
}

type DisplayStatus = "scheduled" | "firing" | "overdue" | "cancelled" | "fired";

function resolveUpcomingStatus(row: UpcomingReminderRow, nowMs = Date.now()): DisplayStatus {
  if (row.status === "firing") return "firing";
  const fireAt = new Date(row.nextFireAt).getTime();
  if (!Number.isNaN(fireAt) && fireAt < nowMs) return "overdue";
  return "scheduled";
}

function StatusBadge({ status }: { status: DisplayStatus }) {
  const { t } = useT("agents");
  const label =
    status === "scheduled"
      ? t(($) => $.reminders.status_scheduled)
      : status === "firing"
        ? t(($) => $.reminders.status_firing)
        : status === "overdue"
          ? t(($) => $.reminders.status_overdue)
          : status === "cancelled"
            ? t(($) => $.reminders.status_cancelled)
            : t(($) => $.reminders.status_fired);
  return (
    <span className="inline-flex shrink-0 rounded-md border border-border px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
      {label}
    </span>
  );
}

function historyStatus(definitionStatus: ReminderDefinitionStatus): DisplayStatus | null {
  if (definitionStatus === "cancelled") return "cancelled";
  if (definitionStatus === "fired") return "fired";
  // Recurring definitions stay `scheduled` after a fire — don't stamp every
  // History row as "scheduled" (noise). Terminal states only.
  return null;
}

function UpcomingRowView({ row }: { row: UpcomingReminderRow }) {
  const displayStatus = resolveUpcomingStatus(row);
  return (
    <li className="flex flex-col gap-1.5 px-3 py-3 text-xs md:px-4">
      <div className="flex items-start justify-between gap-2">
        <p className="min-w-0 whitespace-pre-wrap font-medium text-foreground" title={row.title}>
          {row.title}
        </p>
        <StatusBadge status={displayStatus} />
      </div>
      <TimeRow iso={row.nextFireAt} />
      <div className="flex flex-wrap items-center gap-1.5">
        <RecurrenceChip cadence={row.cadence} />
        <AnchorLink anchor={row.anchor} />
      </div>
    </li>
  );
}

function FiredRowView({ row }: { row: FiredReminderRow }) {
  const status = historyStatus(row.definitionStatus);
  return (
    <li className="flex flex-col gap-1.5 px-3 py-3 text-xs md:px-4">
      <div className="flex items-start justify-between gap-2">
        <p className="min-w-0 whitespace-pre-wrap font-medium text-foreground" title={row.title}>
          {row.title}
        </p>
        {status ? <StatusBadge status={status} /> : null}
      </div>
      <TimeRow iso={row.firedAt} />
      <div className="flex flex-wrap items-center gap-1.5">
        {/* This describes the DEFINITION's own cadence, distinct from "this row
            fired" — a recurring definition that's still `scheduled` must not
            read as if this past occurrence terminated it. */}
        <RecurrenceChip cadence={row.cadence} />
        <AnchorLink anchor={row.anchor} />
      </div>
    </li>
  );
}
