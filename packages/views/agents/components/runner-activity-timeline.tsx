"use client";

import { useMemo, useState } from "react";
import { Copy } from "lucide-react";
import type { RunnerActivityTimelineRow as RunnerActivityTimelineRowData } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { useViewingTimezone } from "../../common/use-viewing-timezone";
import { useT } from "../../i18n";
import {
  foldActivityCommandPreview,
  isLongActivityCommand,
} from "./tabs/activity-command-body";
import { ActivitySubtext } from "./tabs/activity-subtext";

const TONE_DOT: Record<string, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-brand",
  info: "bg-blue-500",
  warning: "bg-amber-500",
  running: "bg-running",
  error: "bg-destructive",
  success: "bg-emerald-500",
};

const TIMESTAMP_CLASS =
  "ml-auto shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground/55";
const LOADING_SKELETON_WIDTHS = [
  "w-[72%]",
  "w-[58%]",
  "w-[81%]",
  "w-[45%]",
  "w-[66%]",
] as const;

/** Pathological body dumps only — normal length never hits this scroller. */
const ACTIVITY_BODY_FULL_SCROLL_CLASS = "max-h-[min(60vh,480px)] overflow-y-auto";

function formatRelativeTime(value: string, now = Date.now()): string {
  const occurredAt = Date.parse(value);
  if (Number.isNaN(occurredAt)) return value;
  const minutes = Math.floor(Math.max(0, now - occurredAt) / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function formatExactTime(value: string, formatter: Intl.DateTimeFormat): string {
  const occurredAt = Date.parse(value);
  if (Number.isNaN(occurredAt)) return value;
  return formatter.format(occurredAt);
}

/**
 * Body-bearing rows (commands / expandable detail): default-full body + always
 * visible Copy. Soft fold only for pathological length.
 */
function TimelineBodyBlock({
  body,
  copied,
  copyLabel,
  onCopy,
  scrollable,
}: {
  body: string;
  copied: boolean;
  copyLabel: string;
  onCopy: () => void;
  scrollable: boolean;
}) {
  return (
    <div className="relative mt-1 rounded-md bg-muted" data-testid="activity-command-block">
      <pre
        className={cn(
          "overflow-x-auto px-2.5 py-1.5 pr-9 font-mono text-xs leading-5 break-words whitespace-pre-wrap text-foreground",
          scrollable && ACTIVITY_BODY_FULL_SCROLL_CLASS,
        )}
      >
        <code>{body}</code>
      </pre>
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          onCopy();
        }}
        aria-label={copyLabel}
        className="absolute right-1.5 top-1.5 rounded border border-border bg-background p-1 text-muted-foreground shadow-sm transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Copy className="size-3.5" aria-hidden />
      </button>
      {copied ? <span className="sr-only">{copyLabel}</span> : null}
    </div>
  );
}

function RunnerActivityTimelineItem({
  row,
  workspaceId,
  exactTimeFormatter,
  showDetails,
  testId,
}: {
  row: RunnerActivityTimelineRowData;
  workspaceId: string;
  exactTimeFormatter: Intl.DateTimeFormat;
  showDetails: boolean;
  testId: string;
}) {
  const { t } = useT("agents");
  const [bodyExpanded, setBodyExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const bodyText = showDetails ? (row.body?.trim() ?? "") : "";
  const commandFromSubtext =
    showDetails && !bodyText && row.body_kind === "command"
      ? (row.subtext?.trim() ?? "")
      : "";
  const displayCommand = bodyText || commandFromSubtext;
  const plainSubtext = showDetails && !displayCommand ? row.subtext?.trim() : undefined;
  const bodyIsLong = Boolean(displayCommand && isLongActivityCommand(displayCommand));
  const exactTime = formatExactTime(row.occurred_at, exactTimeFormatter);
  const copyLabel = copied
    ? t(($) => $.tab_body.activity.command_copied)
    : t(($) => $.tab_body.activity.copy_command);

  const copy = async () => {
    if (!displayCommand || !(await copyText(displayCommand))) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  return (
    <article
      className="relative flex items-start gap-2 py-1"
      data-testid={testId}
      data-command-long={bodyIsLong ? "true" : "false"}
    >
      <span className="flex w-3 shrink-0 justify-center">
        <span
          className={cn(
            "relative z-[1] mt-1.5 size-1.5 shrink-0 rounded-full",
            TONE_DOT[row.tone] ?? TONE_DOT.neutral,
          )}
          aria-hidden
        />
      </span>
      <div className="min-w-0 flex-1 pt-0.5">
        <div className="flex min-h-0 w-full items-start gap-2">
          <span className="min-w-0 flex-1 text-[13.5px] font-semibold leading-snug text-foreground">
            {row.title}
          </span>
          <time
            className={cn(TIMESTAMP_CLASS, "pt-0.5")}
            dateTime={row.occurred_at}
            title={exactTime}
          >
            {formatRelativeTime(row.occurred_at)}
          </time>
        </div>
        {displayCommand ? (
          <>
            <TimelineBodyBlock
              body={
                bodyIsLong && !bodyExpanded
                  ? foldActivityCommandPreview(displayCommand)
                  : displayCommand
              }
              copied={copied}
              copyLabel={copyLabel}
              onCopy={() => {
                void copy();
              }}
              scrollable={bodyIsLong && bodyExpanded}
            />
            {bodyIsLong ? (
              <button
                type="button"
                onClick={() => setBodyExpanded((value) => !value)}
                className="mt-1 text-xs font-medium text-brand hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                data-testid="activity-command-fold-toggle"
              >
                {bodyExpanded
                  ? t(($) => $.tab_body.activity.show_less_command)
                  : t(($) => $.tab_body.activity.show_full_command)}
              </button>
            ) : null}
          </>
        ) : plainSubtext ? (
          <ActivitySubtext text={plainSubtext} workspaceId={workspaceId} />
        ) : null}
      </div>
    </article>
  );
}

export function RunnerActivityTimeline({
  rows,
  workspaceId,
  showDetails = true,
  testId = "activity-timeline",
  spineTestId = "activity-timeline-spine",
  rowTestId = "runner-activity-row",
}: {
  rows: readonly RunnerActivityTimelineRowData[];
  workspaceId: string;
  showDetails?: boolean;
  testId?: string;
  spineTestId?: string;
  rowTestId?: string;
}) {
  const timeZone = useViewingTimezone();
  const exactTimeFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat("en-US", {
        timeZone,
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hourCycle: "h23",
      }),
    [timeZone],
  );

  return (
    <div
      className="relative flex flex-col"
      data-testid={testId}
      data-activity-details={showDetails ? "visible" : "hidden"}
    >
      <div
        className="pointer-events-none absolute bottom-2 left-[5px] top-2 w-[1.5px] bg-border"
        aria-hidden
        data-testid={spineTestId}
      />
      {rows.map((row) => (
        <RunnerActivityTimelineItem
          key={row.id}
          row={row}
          workspaceId={workspaceId}
          exactTimeFormatter={exactTimeFormatter}
          showDetails={showDetails}
          testId={rowTestId}
        />
      ))}
    </div>
  );
}

export function RunnerActivityTimelineLoading({
  rows = 5,
  testId = "activity-timeline-loading",
}: {
  rows?: number;
  testId?: string;
}) {
  return (
    <div className="flex flex-col gap-3 py-1" data-testid={testId} aria-busy="true">
      {LOADING_SKELETON_WIDTHS.slice(0, rows).map((width) => (
        <Skeleton key={width} className={cn("h-3.5 rounded-md", width)} />
      ))}
    </div>
  );
}
