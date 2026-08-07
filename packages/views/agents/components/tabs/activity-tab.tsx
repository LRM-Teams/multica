"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";
import { AlertCircle, ArrowDown, ChevronDown, ChevronUp, Clock, Copy } from "lucide-react";
import { useRunnerActivity } from "@multica/core/agents";
import type { Agent, RunnerActivityTimelineRow } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { useViewingTimezone } from "../../../common/use-viewing-timezone";
import { useT } from "../../../i18n";

const TONE_DOT: Record<string, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-brand",
  info: "bg-blue-500",
  warning: "bg-amber-500",
  error: "bg-destructive",
  success: "bg-emerald-500",
};

const TIMESTAMP_CLASS =
  "ml-auto shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground/55";
const LOADING_SKELETON_WIDTHS = ["w-[72%]", "w-[58%]", "w-[81%]", "w-[45%]", "w-[66%]"] as const;

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

function TimelineRow({ row, exactTimeFormatter }: { row: RunnerActivityTimelineRow; exactTimeFormatter: Intl.DateTimeFormat }) {
  const { t } = useT("agents");
  const detailId = useId();
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const body = row.body?.trim();
  const exactTime = formatExactTime(row.occurred_at, exactTimeFormatter);

  const copy = async () => {
    if (!body || !(await copyText(body))) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  return (
    <article className="relative flex items-start gap-2 py-1" data-testid="runner-activity-row">
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
        {body ? (
          <button
            type="button"
            className="group flex min-h-11 w-full items-start gap-2 rounded-md text-left transition-colors hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:min-h-0"
            aria-expanded={expanded}
            aria-controls={detailId}
            onClick={() => setExpanded((value) => !value)}
          >
            <span className="min-w-0 flex-1">
              <span className="block text-[13.5px] font-semibold leading-snug text-foreground">
                {row.title}
              </span>
              {!expanded && row.subtext ? (
                <span className="mt-0.5 block line-clamp-2 whitespace-pre-wrap break-words text-[13px] text-muted-foreground">
                  {row.subtext}
                </span>
              ) : null}
            </span>
            <time className={cn(TIMESTAMP_CLASS, "pt-0.5")} dateTime={row.occurred_at} title={exactTime}>
              {formatRelativeTime(row.occurred_at)}
            </time>
            <span className="mt-0.5 shrink-0 text-muted-foreground/70 transition-colors group-hover:text-foreground" aria-hidden>
              {expanded ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
            </span>
          </button>
        ) : (
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="shrink-0 text-[13.5px] font-semibold text-foreground">{row.title}</span>
            {row.subtext ? (
              <span className="min-w-0 whitespace-pre-wrap break-words text-[13px] text-muted-foreground">
                {row.subtext}
              </span>
            ) : null}
            <time className={TIMESTAMP_CLASS} dateTime={row.occurred_at} title={exactTime}>
              {formatRelativeTime(row.occurred_at)}
            </time>
          </div>
        )}
        {body && expanded ? (
          <div
            id={detailId}
            className="mt-1 rounded-r-md border-l-2 border-brand bg-muted px-2.5 py-2 text-xs text-foreground"
          >
            <p className="whitespace-pre-wrap break-words leading-5">{body}</p>
            <button
              type="button"
              className="mt-1 inline-flex items-center gap-1 font-medium text-muted-foreground hover:text-foreground"
              onClick={(event) => {
                event.stopPropagation();
                void copy();
              }}
            >
              <Copy className="size-3" aria-hidden />
              {copied
                ? t(($) => $.tab_body.activity.command_copied)
                : t(($) => $.tab_body.activity.copy_command)}
            </button>
          </div>
        ) : null}
      </div>
    </article>
  );
}

function TimelineLoading() {
  return (
    <div className="flex flex-col gap-3 py-1" data-testid="activity-timeline-loading" aria-busy="true">
      {LOADING_SKELETON_WIDTHS.map((width) => (
        <Skeleton key={width} className={cn("h-3.5 rounded-md", width)} />
      ))}
    </div>
  );
}

function StreamBottomAnchor({
  anchorRef,
  onReachedChange,
}: {
  anchorRef: React.RefObject<HTMLDivElement | null>;
  onReachedChange: (reached: boolean) => void;
}) {
  const onReachedChangeRef = useRef(onReachedChange);
  onReachedChangeRef.current = onReachedChange;

  useEffect(() => {
    const bottom = anchorRef.current;
    if (!bottom || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry) onReachedChangeRef.current(entry.isIntersecting);
      },
      { rootMargin: "0px 0px 40px 0px" },
    );
    observer.observe(bottom);
    return () => observer.disconnect();
  }, [anchorRef]);

  return <div ref={anchorRef} aria-hidden className="h-px w-full" />;
}

// ActivityTab keeps the previous timeline UI while consuming only the
// server-projected Runner presentation. It does not restore the retired legacy
// Activity event stream or infer provider/runtime semantics in the browser.
export function ActivityTab({ agent }: { agent: Agent }) {
  const { t } = useT("agents");
  const timeZone = useViewingTimezone();
  const exactTimeFormatter = useMemo(
    () => new Intl.DateTimeFormat("en-US", {
      timeZone,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
    }),
    [timeZone],
  );
  const { data, isLoading, isError, refetch } = useRunnerActivity(agent.workspace_id, agent.id);
  const rootRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const atBottomRef = useRef(true);
  const landedRef = useRef(false);
  const [showJump, setShowJump] = useState(false);
  const timeline = [...(data?.timeline ?? [])].reverse();

  const previousAgentIdRef = useRef(agent.id);
  if (previousAgentIdRef.current !== agent.id) {
    previousAgentIdRef.current = agent.id;
    landedRef.current = false;
    atBottomRef.current = true;
    setShowJump(false);
  }

  useEffect(() => {
    if (timeline.length === 0) return;
    if (!landedRef.current || atBottomRef.current) {
      landedRef.current = true;
      bottomRef.current?.scrollIntoView({ block: "end" });
    }
  }, [timeline.length]);

  if (isLoading) {
    return <div className="p-6"><TimelineLoading /></div>;
  }
  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-2 px-4 py-16 text-center" data-testid="activity-timeline-error">
        <AlertCircle className="size-8 text-destructive" aria-hidden />
        <p className="text-sm font-medium text-foreground">{t(($) => $.tab_body.activity.timeline_load_failed)}</p>
        <p className="max-w-xs text-xs text-muted-foreground">{t(($) => $.tab_body.activity.timeline_load_failed_hint)}</p>
        <Button type="button" variant="outline" size="sm" className="mt-2" onClick={() => void refetch()}>
          {t(($) => $.tab_body.activity.retry)}
        </Button>
      </div>
    );
  }

  return (
    <div ref={rootRef} className="p-6" data-testid="activity-tab">
      {timeline.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-2 px-4 py-16 text-center" data-testid="activity-timeline-empty">
          <Clock className="size-8 text-muted-foreground/50" aria-hidden />
          <p className="text-sm font-medium text-foreground">{t(($) => $.tab_body.activity.timeline_empty)}</p>
          <p className="max-w-xs text-xs text-muted-foreground">{t(($) => $.tab_body.activity.timeline_empty_hint)}</p>
        </div>
      ) : (
        <div className="relative flex flex-col" data-testid="activity-timeline">
          <div className="pointer-events-none absolute bottom-2 left-[5px] top-2 w-[1.5px] bg-border" aria-hidden data-testid="activity-timeline-spine" />
          {timeline.map((row) => <TimelineRow key={row.id} row={row} exactTimeFormatter={exactTimeFormatter} />)}
        </div>
      )}
      <StreamBottomAnchor
        anchorRef={bottomRef}
        onReachedChange={(reached) => {
          atBottomRef.current = reached;
          setShowJump(!reached);
        }}
      />
      {showJump && timeline.length > 0 ? (
        <div className="pointer-events-none sticky bottom-4 flex justify-center">
          <button
            type="button"
            className="pointer-events-auto inline-flex items-center gap-1.5 rounded-full border bg-background/95 px-3 py-1.5 text-xs font-medium text-foreground shadow-sm backdrop-blur transition-colors hover:bg-accent"
            onClick={() => bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" })}
          >
            <ArrowDown className="size-3.5" aria-hidden />
            {t(($) => $.tab_body.activity.jump_to_latest)}
          </button>
        </div>
      ) : null}
    </div>
  );
}
