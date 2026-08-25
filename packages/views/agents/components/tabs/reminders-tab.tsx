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
  toUpcomingReminderRow,
  type ReminderDefinitionRow,
  type ReminderCadence,
} from "@multica/core/agents/reminder-view-model";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../../i18n";
import { AppLink } from "../../../navigation/app-link";
import { useOptionalNavigation } from "../../../navigation/context";
import { useAgentRemindersRealtime } from "./use-agent-reminders-realtime";

interface RemindersTabProps {
  agent: Agent;
}

export function RemindersTab({ agent }: RemindersTabProps) {
  const { t } = useT("agents");
  const queryClient = useQueryClient();
  useAgentRemindersRealtime(agent.id);

  const { data, isLoading, isError, error } = useQuery(
    agentRemindersUpcomingOptions(agent.id),
  );
  const rows = useMemo<ReminderDefinitionRow[]>(
    () =>
      (data?.definitions ?? [])
        .map(toUpcomingReminderRow)
        .filter((row): row is ReminderDefinitionRow => row !== null),
    [data],
  );

  if (error instanceof ApiError && error.status === 403) {
    return (
      <output className="block p-4 text-xs text-muted-foreground">
        {t(($) => $.reminders.inaccessible)}
      </output>
    );
  }

  const retry = () => {
    void queryClient.invalidateQueries({ queryKey: agentRemindersKeys.all(agent.id) });
  };

  return (
    <div className="flex min-w-0 flex-col" aria-label={t(($) => $.tabs.reminders)}>
      {isLoading ? (
        <LoadingState label={t(($) => $.reminders.loading)} />
      ) : isError ? (
        <ErrorState
          onRetry={retry}
          label={t(($) => $.reminders.error_title)}
          retryLabel={t(($) => $.reminders.retry)}
        />
      ) : rows.length === 0 ? (
        <EmptyState label={t(($) => $.reminders.empty_list)} />
      ) : (
        <ul className="divide-y">
          {rows.map((row) => (
            <UpcomingRowView key={row.id} row={row} />
          ))}
        </ul>
      )}
    </div>
  );
}

function LoadingState({ label }: { label: string }) {
  return <p className="px-3 py-3 text-xs text-muted-foreground md:px-4">{label}</p>;
}

function EmptyState({ label }: { label: string }) {
  return <p className="px-3 py-4 text-xs text-muted-foreground md:px-4">{label}</p>;
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

function formatCadenceChipLabel(
  cadence: Extract<ReminderCadence, { kind: "recurring" }>,
): string {
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
  const { t } = useT("agents");
  if (cadence.kind === "one_shot") {
    return (
      <span className="inline-flex rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
        {t(($) => $.reminders.one_shot)}
      </span>
    );
  }
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
  const className =
    "inline-flex max-w-full items-center gap-1 truncate rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-primary hover:underline";
  const body = (
    <>
      <Link2 className="size-3 shrink-0" aria-hidden />
      <span className="truncate">{anchor.label}</span>
    </>
  );
  return navigation ? (
    <Tooltip>
      <TooltipTrigger render={<AppLink href={anchor.href} className={className}>{body}</AppLink>} />
      <TooltipContent side="top">{anchor.label}</TooltipContent>
    </Tooltip>
  ) : (
    <Tooltip>
      <TooltipTrigger render={<a href={anchor.href} className={className}>{body}</a>} />
      <TooltipContent side="top">{anchor.label}</TooltipContent>
    </Tooltip>
  );
}

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
  if (Math.abs(diffSec) < 60) return relativeTimeFormatter.format(diffSec, "second");
  const diffMin = Math.round(diffSec / 60);
  if (Math.abs(diffMin) < 60) return relativeTimeFormatter.format(diffMin, "minute");
  const diffHour = Math.round(diffSec / 3600);
  if (Math.abs(diffHour) < 48) return relativeTimeFormatter.format(diffHour, "hour");
  return relativeTimeFormatter.format(Math.round(diffSec / 86400), "day");
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

function resolveUpcomingStatus(row: ReminderDefinitionRow): DisplayStatus {
  if (row.status === "firing") return "firing";
  const fireAt = new Date(row.nextFireAt).getTime();
  return !Number.isNaN(fireAt) && fireAt < Date.now() ? "overdue" : "scheduled";
}

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
    <span className={`inline-flex shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 text-[10px] uppercase tracking-wide ${toneClass}`}>
      {status === "overdue" ? <AlertTriangle className="h-3 w-3" aria-hidden /> : null}
      {label}
    </span>
  );
}

function UpcomingRowView({ row }: { row: ReminderDefinitionRow }) {
  const { t } = useT("agents");
  return (
    <li className="flex flex-col gap-1.5 px-3 py-3 text-xs md:px-4">
      <div className="flex items-start justify-between gap-2">
        <Tooltip>
          <TooltipTrigger render={<p className="min-w-0 whitespace-pre-wrap font-medium text-foreground" />}>
            {row.title}
          </TooltipTrigger>
          <TooltipContent side="top">{row.title}</TooltipContent>
        </Tooltip>
        <StatusBadge status={resolveUpcomingStatus(row)} />
      </div>
      <TimeRow iso={row.nextFireAt} label={t(($) => $.reminders.next_fire_label)} />
      <div className="flex flex-wrap items-center gap-1.5">
        <RecurrenceChip cadence={row.cadence} />
        <AnchorLink anchor={row.anchor} />
      </div>
    </li>
  );
}
