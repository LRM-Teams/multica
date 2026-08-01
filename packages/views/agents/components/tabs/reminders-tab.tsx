"use client";

import { useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Clock, Link2, Repeat } from "lucide-react";
import type { Agent } from "@multica/core/types";
import { ApiError } from "@multica/core/api";
import {
  agentRemindersKeys,
  agentRemindersUpcomingOptions,
} from "@multica/core/agents/queries";
import {
  adaptUpcomingRow,
  type ReminderDefinitionRow,
  type ReminderCadence,
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
 * One flat list of active reminder definitions ordered by next fire. There is
 * no Upcoming/History partition: once a one-shot fires it disappears and the
 * agent's resulting action is the user-visible record.
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
  const upcomingRows = useMemo<ReminderDefinitionRow[]>(
    () =>
      (upcomingData?.definitions ?? [])
        .map(adaptUpcomingRow)
        .filter((row): row is ReminderDefinitionRow => row !== null),
    [upcomingData],
  );

  const inaccessible = upcomingError instanceof ApiError && upcomingError.status === 403;

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

  if (!upcomingLoading && !upcomingIsError && upcomingRows.length === 0) {
    return (
      <div className="p-4 text-xs text-muted-foreground">{t(($) => $.reminders.empty_all)}</div>
    );
  }

  return (
    <div className="flex min-w-0 flex-col" aria-label={t(($) => $.tabs.reminders)}>
      {upcomingLoading ? (
        <p className="px-3 py-3 text-xs text-muted-foreground md:px-4">
          {t(($) => $.reminders.loading)}
        </p>
      ) : upcomingIsError ? (
        <ErrorState onRetry={retry} label={t(($) => $.reminders.error_title)} retryLabel={t(($) => $.reminders.retry)} />
      ) : (
        <ul className="divide-y">
          {upcomingRows.map((row) => (
            <UpcomingRowView key={row.id} row={row} />
          ))}
        </ul>
      )}
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
function formatCadenceChipLabel(cadence: Extract<ReminderCadence, { kind: "recurring" }>): string {
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

function AnchorLink({ anchor }: { anchor: ReminderDefinitionRow["anchor"] }) {
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

// Hoisted once — rebuilding Intl.* on every row render fails react-doctor
// (js-hoist-intl). English-first per LRM-504; full locale wiring follows site i18n.
const absoluteDateFormatter = new Intl.DateTimeFormat("en", { month: "short", day: "numeric" });
const absoluteTimeFormatter = new Intl.DateTimeFormat("en", {
  hour: "numeric",
  minute: "2-digit",
});
const relativeTimeFormatter = new Intl.RelativeTimeFormat("en", { numeric: "auto" });

function formatAbsoluteInstant(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return `${absoluteDateFormatter.format(date)} at ${absoluteTimeFormatter.format(date)}`;
}

function formatRelativeInstant(iso: string, nowMs = Date.now()): string {
  const target = new Date(iso).getTime();
  if (Number.isNaN(target)) return iso;
  const diffSec = Math.round((target - nowMs) / 1000);
  const abs = Math.abs(diffSec);
  if (abs < 60) return relativeTimeFormatter.format(diffSec, "second");
  const diffMin = Math.round(diffSec / 60);
  if (Math.abs(diffMin) < 60) return relativeTimeFormatter.format(diffMin, "minute");
  const diffHour = Math.round(diffSec / 3600);
  if (Math.abs(diffHour) < 48) return relativeTimeFormatter.format(diffHour, "hour");
  const diffDay = Math.round(diffSec / 86400);
  return relativeTimeFormatter.format(diffDay, "day");
}

function TimeRow({ iso, label }: { iso: string; label?: string }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-muted-foreground">
      <Clock className="size-3.5 shrink-0" aria-hidden />
      {label ? <span>{label}</span> : null}
      <span className="font-medium text-foreground">{formatRelativeInstant(iso)}</span>
      <span aria-hidden>·</span>
      <span>{formatAbsoluteInstant(iso)}</span>
    </div>
  );
}

type DisplayStatus = "scheduled" | "firing" | "overdue";

function resolveUpcomingStatus(row: ReminderDefinitionRow, nowMs = Date.now()): DisplayStatus {
  if (row.status === "firing") return "firing";
  const fireAt = new Date(row.nextFireAt).getTime();
  if (!Number.isNaN(fireAt) && fireAt < nowMs) return "overdue";
  return "scheduled";
}

// Overdue is the only "something is broken" state of the three — it must
// read as distinct at a glance, not just by the label text (found during
// the 08-01 reminder-outage incident: the three states were visually
// identical grey badges, so an overdue reminder didn't stand out even to
// someone actively looking at this tab).
function StatusBadge({ status }: { status: DisplayStatus }) {
  const { t } = useT("agents");
  const label =
    status === "scheduled"
      ? t(($) => $.reminders.status_scheduled)
      : status === "firing"
        ? t(($) => $.reminders.status_firing)
        : t(($) => $.reminders.status_overdue);
  const toneClass =
    status === "firing"
      ? "border-brand/30 bg-brand/10 text-brand"
      : status === "overdue"
        ? "border-destructive/30 bg-destructive/10 text-destructive"
        : "border-border text-muted-foreground";
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 text-[10px] uppercase tracking-wide ${toneClass}`}
    >
      {status === "overdue" ? <AlertTriangle className="h-3 w-3" aria-hidden /> : null}
      {label}
    </span>
  );
}

function UpcomingRowView({ row }: { row: ReminderDefinitionRow }) {
  const { t } = useT("agents");
  const displayStatus = resolveUpcomingStatus(row);
  return (
    <li className="flex flex-col gap-1.5 px-3 py-3 text-xs md:px-4">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <p className="min-w-0 whitespace-pre-wrap font-medium text-foreground" title={row.title}>
            {row.title}
          </p>
        </div>
        <StatusBadge status={displayStatus} />
      </div>
      <TimeRow iso={row.nextFireAt} label={t(($) => $.reminders.next_fire_label)} />
      <div className="flex flex-wrap items-center gap-1.5">
        <RecurrenceChip cadence={row.cadence} />
        <AnchorLink anchor={row.anchor} />
      </div>
    </li>
  );
}
