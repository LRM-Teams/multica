"use client";

import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { AlertCircle, ChevronDown, ChevronUp, Clock, Copy } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { MemoizedMarkdown } from "../../../common/markdown";
import { ChannelChip } from "../../../channels/components/channel-chip";
import { useViewingTimezone } from "../../../common/use-viewing-timezone";
import {
  type ActivityEvent,
  type ActivityTimelineItem,
  ACTIVITY_LABEL_EN,
  ACTIVITY_SUBTEXT_EN,
  ACTIVITY_CHROME_EN,
  ACTIVITY_TONE_DOT_CLASS,
  activityExpansionContent,
  activityPresentation,
  collapseConsecutiveIdle,
  formatActivityRelativeTime,
  formatActivityTime,
  isDecayedFailure,
  isNarrativeActivityEvent,
  normalizeActivityExpandedText,
} from "./activity-event";

// All dots are STATIC — no `animate-pulse` (#404 follow-up). Tone → color lives
// in ONE shared table (ACTIVITY_TONE_DOT_CLASS) so the header can't drift from it.
const TONE_DOT = ACTIVITY_TONE_DOT_CLASS;

/** Shared right-aligned tabular timestamp — every Activity row uses this. */
const TIMESTAMP_CLASS =
  "ml-auto shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground/55";

// Keep long Activity details on the exact existing history-message baseline.
// The detail header is already the sole expand/collapse control, so this is a
// scroll boundary only — never a second "Expand" affordance.
const ACTIVITY_DETAIL_SCROLL_HEIGHT_CLASS =
  "max-h-[min(260px,55vh)] md:max-h-[360px]";
const ACTIVITY_DETAIL_SCROLL_EPSILON = 1;

/** Spine column width — dots sit on the 1.5px border line (LRM-560). */
const SPINE_COL = "w-3";

/** Loading skeleton bar widths — bars only, no spine / fake nodes (LRM-563). */
const LOADING_SKELETON_WIDTHS = ["w-[72%]", "w-[58%]", "w-[81%]", "w-[45%]", "w-[66%]"] as const;

/** Command surface: muted + rounded + mono + optional clamp-2 + hover copy (LRM-560). */
function CommandBlock({
  content,
  clamped,
  copied,
  copyLabel,
  onCopy,
}: {
  content: string;
  clamped: boolean;
  copied: boolean;
  copyLabel: string;
  onCopy: () => void;
}) {
  return (
    <div
      className="group/cmd relative mt-1 rounded-md bg-muted"
      data-testid="activity-command-block"
    >
      <pre
        className={cn(
          "overflow-x-auto px-2.5 py-1.5 pr-9 font-mono text-xs leading-5 break-words whitespace-pre-wrap text-foreground",
          clamped && "line-clamp-2",
        )}
      >
        <code>{content}</code>
      </pre>
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          onCopy();
        }}
        aria-label={copyLabel}
        className={cn(
          "absolute right-1.5 top-1.5 rounded border border-border bg-background p-1 text-muted-foreground shadow-sm transition-opacity hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          copied ? "opacity-100" : "opacity-0 group-hover/cmd:opacity-100",
        )}
      >
        <Copy className="size-3.5" aria-hidden />
      </button>
    </div>
  );
}

// A file tool's `tool_target` is now a source-backed path (absolute when the
// runtime provides it, #484) which can be ~90 chars — long enough to blow out
// the row. Keep the basename fully visible, middle-ellipsis the leading
// directories (never right-truncate, which would eat the basename), and expose
// the full path on hover (`title`). Display-only: the value is the BE
// source-backed target verbatim; we never reconstruct or leak raw input.
function ToolTargetPath({ value }: { value: string }) {
  const idx = value.lastIndexOf("/");
  // Keep the leading "/" on the tail so the ellipsis reads "…/basename".
  const head = idx > 0 ? value.slice(0, idx) : "";
  const tail = idx > 0 ? value.slice(idx) : value;
  return (
    <span
      title={value}
      className="flex min-w-0 items-baseline text-[13px] text-muted-foreground"
    >
      {head ? <span className="min-w-0 truncate">{head}</span> : null}
      <span className="shrink-0">{tail}</span>
    </span>
  );
}

function ActivitySpineDot({
  tone,
  className,
}: {
  tone: keyof typeof TONE_DOT;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "relative z-[1] mt-1.5 size-1.5 shrink-0 rounded-full",
        TONE_DOT[tone],
        className,
      )}
      aria-hidden
    />
  );
}

function ActivityRow({
  event,
  time,
  timeTitle,
  decayed = false,
  compact = false,
}: {
  event: ActivityEvent;
  time: string;
  /** Exact clock time, shown on hover so precision isn't lost to the relative format. */
  timeTitle?: string;
  /** task #13: true once a failure row is old or superseded — dims it like MergedIdleRow. */
  decayed?: boolean;
  compact?: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const [detailOverflowed, setDetailOverflowed] = useState(false);
  const [showDetailFade, setShowDetailFade] = useState(false);
  const detailRef = useRef<HTMLDivElement>(null);
  const updateDetailOverflowRef = useRef<() => void>(() => {});
  const detailId = useId();
  const presentation = activityPresentation(event);
  const isDecayedFailureRow = decayed && presentation.tone === "failure";
  const dotTone = isDecayedFailureRow ? "neutral" : presentation.tone;
  const labelClass = isDecayedFailureRow
    ? "text-[13.5px] font-medium text-muted-foreground"
    : "text-[13.5px] font-semibold text-foreground";
  // Activity is English-only (Frank 2026-07-14): the label/fixed-subtext come
  // from the canonical English maps, not a locale lookup.
  const rawLabel = ACTIVITY_LABEL_EN[presentation.labelKey];
  // Canonical values are base form (no ellipsis). The trailing "…" is raft's
  // in-progress signal, appended at render for an active tool action only —
  // never on settled rows or non-tool states (wake / compaction / reply).
  const label =
    event.activity_kind === "tool_call" && presentation.tone === "active" ? `${rawLabel}…` : rawLabel;
  const subtext = presentation.subtextKey
    ? ACTIVITY_SUBTEXT_EN[presentation.subtextKey]
    : presentation.subtext;
  // Full Activity exposes only source-backed detail; compact Profile Recent
  // remains a non-interactive, scannable summary surface.
  const expansion = compact ? undefined : activityExpansionContent(event, presentation);
  const isCommand = expansion?.kind === "command";

  const handleCopyCommand = async () => {
    if (expansion?.kind !== "command") return;

    if (await copyText(expansion.content)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  // Route the non-command tool subtext by its kind (#v0 照实显示): a file tool's
  // target is a PATH (basename-preserving middle-ellipsis, #484/#385); everything
  // else is a plain truncate. COMMAND kind is handled inline below.
  const subtextNode = subtext ? (
    presentation.subtextKind === "path" ? (
      <ToolTargetPath value={subtext} />
    ) : presentation.subtextKind === "command" ? (
      <span className="line-clamp-2 min-w-0 break-all font-mono text-[12px] text-muted-foreground">
        {subtext}
      </span>
    ) : (
      <span className="truncate text-[13px] text-muted-foreground">{subtext}</span>
    )
  ) : null;

  // Activity chrome is English-only (canonical map, not i18n) — see ACTIVITY_CHROME_EN.
  const copyLabel = copied
    ? ACTIVITY_CHROME_EN.command_copied
    : ACTIVITY_CHROME_EN.copy_command;

  const updateDetailOverflow = useCallback(() => {
    const detail = detailRef.current;
    if (!detail) return;

    const overflowed = detail.scrollHeight > detail.clientHeight + ACTIVITY_DETAIL_SCROLL_EPSILON;
    const atBottom =
      detail.scrollTop + detail.clientHeight >=
      detail.scrollHeight - ACTIVITY_DETAIL_SCROLL_EPSILON;
    setDetailOverflowed((previous) => (previous === overflowed ? previous : overflowed));
    setShowDetailFade((previous) => {
      const next = overflowed && !atBottom;
      return previous === next ? previous : next;
    });
  }, []);
  updateDetailOverflowRef.current = updateDetailOverflow;

  useLayoutEffect(() => {
    if (!expanded) {
      setDetailOverflowed(false);
      setShowDetailFade(false);
      return;
    }

    updateDetailOverflowRef.current();
  }, [expanded]);

  useEffect(() => {
    if (!expanded) return;

    const detail = detailRef.current;
    if (!detail) return;

    const handleDetailOverflow = () => updateDetailOverflowRef.current();
    const resizeObserver =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(handleDetailOverflow);
    resizeObserver?.observe(detail);
    window.addEventListener("resize", handleDetailOverflow);

    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener("resize", handleDetailOverflow);
    };
  }, [expanded]);

  const timestampClass = TIMESTAMP_CLASS;

  if (expansion) {
    return (
      <div
        className="py-1"
        data-testid="activity-row"
        data-activity-kind={event.activity_kind}
      >
        <div className="flex items-start gap-2">
          <span className={cn("flex shrink-0 justify-center", SPINE_COL)}>
            <ActivitySpineDot tone={dotTone} className="mt-2" />
          </span>
          <div className="min-w-0 flex-1 pt-0.5">
            <button
              type="button"
              onClick={() => setExpanded((value) => !value)}
              aria-expanded={expanded}
              aria-controls={expanded ? detailId : undefined}
              className="group flex min-h-11 w-full items-start gap-2 rounded-md text-left transition-colors hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:min-h-0"
            >
              <span className="min-w-0 flex-1">
                {/* LRM-560 tier 1: action label weight 600 */}
                <span className={cn("block leading-snug", labelClass)}>
                  {label}
                </span>
                {!expanded && !isCommand && subtext ? (
                  <span className="mt-0.5 block line-clamp-1 text-[13px] text-muted-foreground">
                    {subtext}
                  </span>
                ) : null}
              </span>
              <span className={cn(timestampClass, "pt-0.5")} title={timeTitle}>{time}</span>
              <span
                className="mt-0.5 shrink-0 text-muted-foreground/70 transition-colors group-hover:text-foreground"
                aria-hidden
              >
                {expanded ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
              </span>
            </button>
            {!expanded && isCommand && expansion.kind === "command" ? (
              <CommandBlock
                content={expansion.content}
                clamped
                copied={copied}
                copyLabel={copyLabel}
                onCopy={() => {
                  void handleCopyCommand();
                }}
              />
            ) : null}
            {expanded ? (
              <div
                id={detailId}
                ref={detailRef}
                data-testid="activity-expanded-detail"
                className={cn(
                  "relative mt-1 text-xs text-foreground",
                  ACTIVITY_DETAIL_SCROLL_HEIGHT_CLASS,
                  detailOverflowed ? "overflow-y-auto overscroll-contain" : "overflow-visible",
                )}
                tabIndex={detailOverflowed ? 0 : undefined}
                role={detailOverflowed ? "region" : undefined}
                aria-label={
                  detailOverflowed ? ACTIVITY_CHROME_EN.expanded_detail_scrollable : undefined
                }
                onScroll={updateDetailOverflow}
              >
                {expansion?.kind === "freshness_hold" ? (
                  <div className="flex flex-col gap-1" data-testid="activity-freshness-hold">
                    {expansion.target ? (
                      <div className="flex items-center gap-1">
                        <span className="text-muted-foreground/70">
                          {ACTIVITY_CHROME_EN.hold_target_label}
                        </span>
                        <ChannelChip name={expansion.target} />
                      </div>
                    ) : null}
                    {expansion.newCount != null ? (
                      <div>
                        <span className="text-muted-foreground/70">
                          {ACTIVITY_CHROME_EN.hold_new_messages_label}
                        </span>{" "}
                        {expansion.newCount}{" "}
                        {expansion.newCount === 1
                          ? ACTIVITY_CHROME_EN.hold_newer_message
                          : ACTIVITY_CHROME_EN.hold_newer_messages}
                      </div>
                    ) : null}
                    <div>
                      <span className="text-muted-foreground/70">
                        {ACTIVITY_CHROME_EN.hold_decision_label}
                      </span>{" "}
                      {ACTIVITY_CHROME_EN.hold_decision_value}
                    </div>
                  </div>
                ) : isCommand && expansion.kind === "command" ? (
                  <CommandBlock
                    content={normalizeActivityExpandedText(expansion.content)}
                    clamped={false}
                    copied={copied}
                    copyLabel={copyLabel}
                    onCopy={() => {
                      void handleCopyCommand();
                    }}
                  />
                ) : (
                  // LRM-560 rule 4: normalize + muted surface + 2px brand bar
                  <div
                    className="rounded-r-md border-l-2 border-brand bg-muted px-2.5 py-2 text-foreground"
                    data-testid="activity-expanded-surface"
                  >
                    <MemoizedMarkdown
                      mode="minimal"
                      enableStickerShortcodes={false}
                      className="activity-expanded-markdown break-words text-xs leading-5 whitespace-pre-wrap text-foreground [&_p:first-child]:mt-0 [&_p:last-child]:mb-0"
                    >
                      {normalizeActivityExpandedText(expansion.content)}
                    </MemoizedMarkdown>
                  </div>
                )}
                {showDetailFade && (
                  <div
                    className="pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-background via-background/95 to-transparent pb-1.5 pt-12"
                    data-testid="activity-detail-scroll-fade"
                  />
                )}
              </div>
            ) : null}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div
      className={cn("flex items-baseline gap-2", compact ? "py-0.5" : "py-1")}
      data-testid="activity-row"
      data-activity-kind={event.activity_kind}
    >
      <span className={cn("flex shrink-0 justify-center self-start", SPINE_COL)}>
        <ActivitySpineDot tone={dotTone} />
      </span>
      {compact ? (
        // LRM-650: Profile compact = state type only — suppress command/path detail.
        <div className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className={cn("min-w-0 truncate", labelClass)}>
            {label}
          </span>
          <span className={timestampClass} title={timeTitle}>{time}</span>
        </div>
      ) : (
        <div className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className={cn("shrink-0", labelClass)}>{label}</span>
          {subtextNode}
          <span className={timestampClass} title={timeTitle}>{time}</span>
        </div>
      )}
    </div>
  );
}

/**
 * LRM-566 方案 B — a run of consecutive Idle status rows merged into one
 * de-emphasized line. Idle (end-of-round presence) is low-signal next to the
 * action rows around it, and a stack of bare `Idle · <time>` lines painted a
 * vertical middle-gap void (SoT LRM-567). The merge collapses that stack to a
 * single row (`Idle · N`, latest timestamp) and dims it
 * (`text-[12px] font-medium text-muted-foreground py-0.5`); content rows keep
 * their existing `py-1` weight. No invented copy — N is the merged count.
 */
function MergedIdleRow({ events, tz }: { events: ActivityEvent[]; tz: string }) {
  const count = events.length;
  const label =
    count > 1 ? `${ACTIVITY_LABEL_EN.idle} · ${count}` : ACTIVITY_LABEL_EN.idle;
  // Events arrive chronological; the last in the run is the most recent idle.
  const latest = events[events.length - 1]!;
  const time = formatActivityRelativeTime(latest.occurred_at);
  const timeTitle = formatActivityTime(latest.occurred_at, tz);
  return (
    <div
      className="flex items-baseline gap-2 py-0.5"
      data-testid="activity-idle-row"
      data-idle-count={count}
      data-activity-kind="custom"
    >
      <span className={cn("flex shrink-0 justify-center self-start", SPINE_COL)}>
        <ActivitySpineDot tone="neutral" />
      </span>
      <span className="shrink-0 text-[12px] font-medium text-muted-foreground">
        {label}
      </span>
      <span className={TIMESTAMP_CLASS} title={timeTitle}>{time}</span>
    </div>
  );
}

function ActivityTimelineLoading() {
  return (
    <div
      className="flex flex-col gap-3 py-1"
      data-testid="activity-timeline-loading"
      aria-busy="true"
      aria-label={ACTIVITY_CHROME_EN.loading}
    >
      {LOADING_SKELETON_WIDTHS.map((width) => (
        <Skeleton key={width} className={cn("h-3.5 rounded-md", width)} />
      ))}
    </div>
  );
}

function ActivityTimelineEmpty() {
  return (
    <div
      className="flex flex-col items-center justify-center gap-2 px-4 py-16 text-center"
      data-testid="activity-timeline-empty"
    >
      <Clock className="size-8 text-muted-foreground/50" aria-hidden />
      <p className="text-sm font-medium text-foreground">{ACTIVITY_CHROME_EN.timeline_empty}</p>
      <p className="max-w-xs text-xs text-muted-foreground">
        {ACTIVITY_CHROME_EN.timeline_empty_hint}
      </p>
    </div>
  );
}

function ActivityTimelineError({ onRetry }: { onRetry?: () => void }) {
  return (
    <div
      className="flex flex-col items-center justify-center gap-2 px-4 py-16 text-center"
      data-testid="activity-timeline-error"
    >
      <AlertCircle className="size-8 text-destructive" aria-hidden />
      <p className="text-sm font-medium text-foreground">
        {ACTIVITY_CHROME_EN.timeline_load_failed}
      </p>
      <p className="max-w-xs text-xs text-muted-foreground">
        {ACTIVITY_CHROME_EN.timeline_load_failed_hint}
      </p>
      {onRetry ? (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="mt-2"
          onClick={onRetry}
        >
          {ACTIVITY_CHROME_EN.retry}
        </Button>
      ) : null}
    </div>
  );
}

// The compact profile "Recent activity" surface shows only the most recent
// handful of narrative rows (§2.1 / #383). Layout-only delta — same projection.
const COMPACT_RECENT_LIMIT = 5;

/**
 * Read-only agent-activity narrative timeline (#267 / LRM-560 / LRM-563).
 * Spine + token nodes + three-tier weight when there are real events; full-tab
 * loading / empty / error are parameterized (no spine on loading — Frank).
 * Compact mode keeps the denser profile peek + simple empty string.
 */
export function ActivityTimeline({
  events,
  compact = false,
  isLoading = false,
  isError = false,
  onRetry,
}: {
  events: ActivityEvent[];
  /** Profile "Recent activity" compact mode: last N narrative rows, no expand. */
  compact?: boolean;
  /** Full-tab first paint — skeleton bars only, no spine (LRM-563). */
  isLoading?: boolean;
  /** Full-tab load failure with no rows — separate from empty (LRM-563). */
  isError?: boolean;
  onRetry?: () => void;
}) {
  const tz = useViewingTimezone();

  const shown = useMemo((): ActivityTimelineItem[] => {
    const narrative = events.filter(isNarrativeActivityEvent);
    if (compact) {
      // Compact peek surface drops settled "Idle" status rows — in a recent-activity
      // glance they read as status noise, not an action/result (#465②,
      // Barry/Ronan/Iris 2026-07-15). The full timeline keeps them (the historical
      // "went idle" fact). Filter via the shared presentation, not a duplicated
      // status check.
      return narrative
        .filter((event) => activityPresentation(event).labelKey !== "idle")
        .slice(-COMPACT_RECENT_LIMIT)
        .map((event) => ({ kind: "event", event }));
    }
    // Full timeline (LRM-566 方案 B): consecutive Idle status rows merge into one
    // de-emphasized `Idle · N` line so a stack of empty-gap Idle rows no longer
    // reads as a blank vertical stretch (SoT LRM-567).
    return collapseConsecutiveIdle(narrative);
  }, [events, compact]);

  // Compact profile peek keeps the legacy one-line empty; loading/error belong
  // to the full Activity tab only.
  if (!compact) {
    if (isError) return <ActivityTimelineError onRetry={onRetry} />;
    if (isLoading) return <ActivityTimelineLoading />;
    if (shown.length === 0) return <ActivityTimelineEmpty />;
  } else if (shown.length === 0) {
    return (
      <p className="text-xs italic text-muted-foreground/60">
        {ACTIVITY_CHROME_EN.timeline_empty}
      </p>
    );
  }

  return (
    <div className="relative flex flex-col" data-testid="activity-timeline">
      {/* LRM-560 rule 1: continuous 1.5px spine behind hang-off nodes — only when
          there are real event nodes (LRM-563: never on loading skeleton). */}
      {!compact ? (
        <div
          className="pointer-events-none absolute bottom-2 left-[5px] top-2 w-[1.5px] bg-border"
          aria-hidden
          data-testid="activity-timeline-spine"
        />
      ) : null}
      {shown.map((item, index) =>
        item.kind === "idle" ? (
          <MergedIdleRow
            key={`idle-${item.events[0]!.id}`}
            events={item.events}
            tz={tz}
          />
        ) : (
          <ActivityRow
            key={item.event.id}
            event={item.event}
            time={formatActivityRelativeTime(item.event.occurred_at)}
            timeTitle={formatActivityTime(item.event.occurred_at, tz)}
            decayed={isDecayedFailure(item.event.occurred_at, index < shown.length - 1)}
            compact={compact}
          />
        ),
      )}
    </div>
  );
}
