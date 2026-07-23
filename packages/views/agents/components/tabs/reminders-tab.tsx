"use client";

import { useMemo } from "react";
import { useQuery, useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, Clock, Link2, Repeat } from "lucide-react";
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
import { useViewingTimezone } from "../../../common/use-viewing-timezone";
import { AppLink } from "../../../navigation/app-link";
import { useOptionalNavigation } from "../../../navigation/context";
import { useAgentRemindersRealtime } from "./use-agent-reminders-realtime";
import {
  deriveReminderStatusChip,
  formatReminderAbsolute,
  formatReminderCadence,
  formatReminderRelative,
  isBareShortIdAnchorLabel,
  type ReminderCadenceLabels,
  type ReminderStatusChip,
} from "./reminder-display";

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

function useReminderCadenceLabels(): ReminderCadenceLabels {
  const { t } = useT("agents");
  const withTz = (timezone: string | undefined) =>
    timezone ? t(($) => $.reminders.cadence_timezone_suffix, { timezone }) : "";
  return {
    oneShot: t(($) => $.reminders.one_shot),
    daily: (time, timezone) =>
      t(($) => $.reminders.cadence_daily, { time, timezone: withTz(timezone) }),
    weekly: (days, time, timezone) =>
      t(($) => $.reminders.cadence_weekly, { days, time, timezone: withTz(timezone) }),
    everyMinutes: (count) => t(($) => $.reminders.cadence_every_minutes, { count }),
    everyHours: (count) => t(($) => $.reminders.cadence_every_hours, { count }),
    everyDays: (count) => t(($) => $.reminders.cadence_every_days, { count }),
    raw: (description, timezone) =>
      t(($) => $.reminders.cadence_raw, {
        description,
        timezone: withTz(timezone),
      }),
  };
}

function CadenceLabel({ row }: { row: { cadence: UpcomingReminderRow["cadence"] } }) {
  const labels = useReminderCadenceLabels();
  // AC: recurring shows cadence+tz as one chip; one-shot does NOT show this chip.
  if (row.cadence.kind === "one_shot") return null;
  const text = formatReminderCadence(row.cadence, labels);
  return (
    <span className="inline-flex items-center gap-1 text-muted-foreground">
      <Repeat className="size-3 shrink-0" aria-hidden />
      {text}
    </span>
  );
}

function StatusChip({ chip }: { chip: ReminderStatusChip }) {
  const { t } = useT("agents");
  const label =
    chip === "scheduled"
      ? t(($) => $.reminders.status_scheduled)
      : chip === "overdue"
        ? t(($) => $.reminders.status_overdue)
        : chip === "fired"
          ? t(($) => $.reminders.status_fired)
          : t(($) => $.reminders.status_cancelled);
  return (
    <span className="inline-flex w-fit items-center rounded-sm bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
      {label}
    </span>
  );
}

function AnchorLink({ anchor }: { anchor: UpcomingReminderRow["anchor"] }) {
  const { t } = useT("agents");
  const navigation = useOptionalNavigation();
  // AC: never show bare `#multica:<shortid>` / `#workspace:<shortid>` — humans
  // need a channel/session readable name. Server `display` projects `#channel`
  // / DM labels (LRM-507 completes name quality); residual short ids → unavailable.
  if (!anchor.available) {
    return <span className="text-muted-foreground">{t(($) => $.reminders.anchor_unavailable)}</span>;
  }
  if (isBareShortIdAnchorLabel(anchor.label)) {
    return <span className="text-muted-foreground">{t(($) => $.reminders.anchor_unavailable)}</span>;
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
  const className = "inline-flex min-w-0 items-center gap-1 truncate text-primary hover:underline";
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

/** Relative + absolute on one meta line — Frank field IA (Clock icon, not neo-brutal). */
function ReminderTimeLine({ iso }: { iso: string }) {
  const { t, i18n } = useT("agents");
  const timeZone = useViewingTimezone();
  const locale = i18n?.language || "en";
  const relative = formatReminderRelative(iso, locale);
  const absolute = formatReminderAbsolute(
    iso,
    locale,
    timeZone,
    t(($) => $.reminders.at_word),
  );
  return (
    <span className="inline-flex min-w-0 items-center gap-1 text-muted-foreground">
      <Clock className="size-3 shrink-0" aria-hidden />
      <span className="min-w-0">
        <span>{relative}</span>
        <span aria-hidden className="mx-1.5 text-muted-foreground/50">
          ·
        </span>
        <span>{absolute}</span>
      </span>
    </span>
  );
}

function UpcomingRowView({ row }: { row: UpcomingReminderRow }) {
  const chip = deriveReminderStatusChip({
    section: "upcoming",
    definitionStatus: row.status,
    fireAtIso: row.nextFireAt,
  });
  return (
    <li className="flex flex-col gap-1 px-3 py-3 text-xs md:px-4">
      <div className="flex items-start gap-1.5">
        <Bell className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        {/* Full title — no line-clamp; Frank hard requirement: 标题正文完整可读. */}
        <p className="min-w-0 whitespace-pre-wrap break-words font-medium text-foreground">
          {row.title}
        </p>
      </div>
      <div className="flex flex-col gap-0.5 pl-5">
        <StatusChip chip={chip} />
        <ReminderTimeLine iso={row.nextFireAt} />
        <CadenceLabel row={row} />
        <AnchorLink anchor={row.anchor} />
      </div>
    </li>
  );
}

function FiredRowView({ row }: { row: FiredReminderRow }) {
  const chip = deriveReminderStatusChip({
    section: "history",
    definitionStatus: row.definitionStatus,
    fireAtIso: row.firedAt,
  });
  return (
    <li className="flex flex-col gap-1 px-3 py-3 text-xs md:px-4">
      <div className="flex items-start gap-1.5">
        <Bell className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <p className="min-w-0 whitespace-pre-wrap break-words font-medium text-foreground">
          {row.title}
        </p>
      </div>
      <div className="flex flex-col gap-0.5 pl-5">
        {/* Relative+absolute describe THIS occurrence's fire time. Cadence
            still describes the DEFINITION (recurring stays labeled recurring
            even after a past fire — must not read as "this reminder is done").
            Status chip surfaces scheduled/过期/取消 (History → fired|cancelled). */}
        <StatusChip chip={chip} />
        <ReminderTimeLine iso={row.firedAt} />
        <CadenceLabel row={row} />
        <AnchorLink anchor={row.anchor} />
      </div>
    </li>
  );
}
