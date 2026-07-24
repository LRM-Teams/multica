"use client";

import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ChevronDown, ChevronUp, Copy } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { MemoizedMarkdown } from "../../../common/markdown";
import { ChannelChip } from "../../../channels/components/channel-chip";
import { useViewingTimezone } from "../../../common/use-viewing-timezone";
import {
  type ActivityEvent,
  ACTIVITY_LABEL_EN,
  ACTIVITY_SUBTEXT_EN,
  ACTIVITY_CHROME_EN,
  ACTIVITY_TONE_DOT_CLASS,
  activityExpansionContent,
  activityPresentation,
  formatActivityTime,
  isNarrativeActivityEvent,
  normalizeActivityExpandText,
} from "./activity-event";

// All dots are STATIC — no `animate-pulse` (#404 follow-up). A perpetually
// pulsing dot made settled/historical rows look like they were still loading
// live; real "is it live" now comes from the header/hover latest-state (#521)
// and the avatar pulse, not a blinking or colored dot. Tone → color lives in
// ONE shared table (ACTIVITY_TONE_DOT_CLASS) so the header can't drift from it.
const TONE_DOT = ACTIVITY_TONE_DOT_CLASS;

// Spine column width — continuous 1.5px border line + hanging nodes (LRM-560).
const SPINE_COL_CLASS = "relative w-3 shrink-0 self-stretch";
const SPINE_LINE_CLASS =
  "pointer-events-none absolute left-1/2 w-[1.5px] -translate-x-1/2 bg-border";

// Keep long Activity details on the exact existing history-message baseline.
// The detail header is already the sole expand/collapse control, so this is a
// scroll boundary only — never a second "Expand" affordance.
const ACTIVITY_DETAIL_SCROLL_HEIGHT_CLASS =
  "max-h-[min(260px,55vh)] md:max-h-[360px]";
const ACTIVITY_DETAIL_SCROLL_EPSILON = 1;

// A file tool's `tool_target` is now a source-backed path (absolute when the
// runtime provides it, #484) which can be ~90 chars — long enough to blow out
// the row. Keep the basename fully visible, middle-ellipsis the leading
// directories (never right-truncate, which would eat the basename), and expose
// the full path on hover (`title`). Display-only: the value is the BE
// source-backed target verbatim; we never reconstruct or leak raw input.
// (Click-to-copy is the tracked #385-FE follow-up — deferred to keep the
// overflow hotfix non-interactive.) Used by both the Activity tab and the
// compact Profile Recent surface (the same shared row).
function ToolTargetPath({ value }: { value: string }) {
  const idx = value.lastIndexOf("/");
  // Keep the leading "/" on the tail so the ellipsis reads "…/basename".
  const head = idx > 0 ? value.slice(0, idx) : "";
  const tail = idx > 0 ? value.slice(idx) : value;
  return (
    <span
      title={value}
      className="flex min-w-0 items-baseline text-xs text-muted-foreground"
    >
      {head ? <span className="min-w-0 truncate">{head}</span> : null}
      <span className="shrink-0">{tail}</span>
    </span>
  );
}

/** Hanging node + per-row spine segment so the column reads as one continuous line. */
function TimelineSpine({
  tone,
  isFirst,
  isLast,
  compact,
}: {
  tone: keyof typeof TONE_DOT;
  isFirst: boolean;
  isLast: boolean;
  compact: boolean;
}) {
  return (
    <span className={SPINE_COL_CLASS} aria-hidden>
      {!compact && !isFirst ? (
        <span className={cn(SPINE_LINE_CLASS, "top-0 h-[calc(0.625rem+1px)]")} />
      ) : null}
      <span
        className={cn(
          "relative z-10 mt-2.5 block size-1.5 rounded-full",
          TONE_DOT[tone],
        )}
      />
      {!compact && !isLast ? (
        <span className={cn(SPINE_LINE_CLASS, "top-[calc(0.625rem+7px)] bottom-0")} />
      ) : null}
    </span>
  );
}

/** Command surface: muted + mono + clamp-2 + hover copy (LRM-560). */
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
          "overflow-x-auto px-2.5 py-1.5 pr-9 font-mono text-xs leading-5 break-words whitespace-pre-wrap text-muted-foreground",
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
          "absolute right-1.5 top-1.5 rounded p-1 text-muted-foreground transition-opacity hover:bg-background/80 hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          copied ? "opacity-100" : "opacity-0 group-hover/cmd:opacity-100",
        )}
      >
        <Copy className="size-3.5" aria-hidden />
      </button>
    </div>
  );
}

function ActivityRow({
  event,
  time,
  compact = false,
  isFirst,
  isLast,
}: {
  event: ActivityEvent;
  time: string;
  compact?: boolean;
  isFirst: boolean;
  isLast: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const [detailOverflowed, setDetailOverflowed] = useState(false);
  const [showDetailFade, setShowDetailFade] = useState(false);
  const detailRef = useRef<HTMLDivElement>(null);
  const updateDetailOverflowRef = useRef<() => void>(() => {});
  const detailId = useId();
  const presentation = activityPresentation(event);
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
  const isIdle = presentation.labelKey === "idle";

  const handleCopyCommand = async () => {
    if (expansion?.kind !== "command") return;

    if (await copyText(expansion.content)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  // Route the non-command tool subtext by its kind (#v0 照实显示): a file tool's
  // target is a PATH (basename-preserving middle-ellipsis, #484/#385); everything
  // else is a plain truncate. COMMAND kind is handled inline below (ActivityRow
  // expand state — no separate component).
  const subtextNode = subtext ? (
    presentation.subtextKind === "path" ? (
      <ToolTargetPath value={subtext} />
    ) : (
      <span className="truncate text-xs text-muted-foreground">{subtext}</span>
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

  // Measure after the sibling detail mounts, then keep the boundary truthful as
  // Markdown reflows or the viewport changes. The initial layout effect avoids
  // a visible unbounded frame for long detail on expand.
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

    // Keep browser subscriptions stable for this mounted sibling. The current
    // measurement function lives in a ref so a future handler change cannot
    // cause listener churn (React Doctor's event-handler-ref contract).
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

  const timeNode = (
    <span
      className={cn(
        "ml-auto shrink-0 pt-0.5 font-mono text-[11px] tabular-nums",
        isIdle ? "text-muted-foreground/40" : "text-muted-foreground/55",
      )}
    >
      {time}
    </span>
  );

  const labelClass = cn(
    "text-[13.5px] font-semibold leading-[1.45]",
    isIdle ? "text-muted-foreground" : "text-foreground",
  );

  if (expansion) {
    return (
      <div
        className="py-1"
        data-testid="activity-row"
        data-activity-kind={event.activity_kind}
      >
        <div className="flex items-start gap-3">
          <TimelineSpine
            tone={presentation.tone}
            isFirst={isFirst}
            isLast={isLast}
            compact={false}
          />
          <div className="min-w-0 flex-1 pt-0.5">
            <button
              type="button"
              onClick={() => setExpanded((value) => !value)}
              aria-expanded={expanded}
              aria-controls={expanded ? detailId : undefined}
              className="group flex min-h-11 w-full items-baseline gap-2 rounded-md text-left transition-colors hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:min-h-0"
            >
              <span className={labelClass}>{label}</span>
              {timeNode}
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
            {!expanded && !isCommand && subtext ? (
              <span className="mt-0.5 block line-clamp-1 text-xs text-muted-foreground">
                {subtext}
              </span>
            ) : null}
            {expanded ? (
              <div
                id={detailId}
                ref={detailRef}
                data-testid="activity-expanded-detail"
                className={cn(
                  "relative mt-1 text-xs text-muted-foreground",
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
                  <div
                    className="rounded-md border-l-2 border-l-brand bg-muted px-2.5 py-2"
                    data-testid="activity-freshness-hold"
                  >
                    <div className="flex flex-col gap-1">
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
                  </div>
                ) : isCommand && expansion.kind === "command" ? (
                  <CommandBlock
                    content={expansion.content}
                    clamped={false}
                    copied={copied}
                    copyLabel={copyLabel}
                    onCopy={() => {
                      void handleCopyCommand();
                    }}
                  />
                ) : (
                  <div
                    className="rounded-md border-l-2 border-l-brand bg-muted px-2.5 py-2 whitespace-pre-wrap"
                    data-testid="activity-expand-surface"
                  >
                    <MemoizedMarkdown
                      mode="minimal"
                      enableStickerShortcodes={false}
                      className="activity-expanded-markdown break-words text-xs leading-5 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0"
                    >
                      {normalizeActivityExpandText(expansion.content)}
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
      className={cn(
        "flex items-start gap-3",
        compact ? "py-0.5" : "py-1",
        isIdle && "opacity-70",
      )}
      data-testid="activity-row"
      data-activity-kind={event.activity_kind}
    >
      <TimelineSpine
        tone={presentation.tone}
        isFirst={isFirst}
        isLast={isLast}
        compact={compact}
      />
      {presentation.subtextKind === "command" && compact ? (
        // Compact Profile Recent: clamp + no expand / copy (title-only).
        <div className="flex min-w-0 flex-1 items-baseline gap-2 pt-0.5">
          <div className="min-w-0 flex-1 line-clamp-2 break-words text-[13.5px] font-semibold leading-[1.45] text-foreground">
            <span>{label} </span>
            <span className="font-mono text-xs font-normal text-muted-foreground">{subtext}</span>
          </div>
          {timeNode}
        </div>
      ) : (
        <div className="flex min-w-0 flex-1 items-baseline gap-2 pt-0.5">
          <span className={cn(labelClass, "shrink-0")}>{label}</span>
          {subtextNode}
          {timeNode}
        </div>
      )}
    </div>
  );
}

// The compact profile "Recent activity" surface shows only the most recent
// handful of narrative rows (§2.1 / #383). Layout-only delta — same projection.
const COMPACT_RECENT_LIMIT = 5;

/**
 * Read-only agent-activity narrative timeline (#267). One time-ordered stream —
 * each row = `spine · human label · optional subtext · time` (LRM-560). Shows only
 * mainline narrative events (kept by `isNarrativeActivityEvent`, driven by raft
 * `activity_kind` semantics #389); diagnostic kinds (transport / telemetry /
 * internal_progress / runtime_diagnostic / …) stay out. Never renders raw
 * command/output for tool rows.
 *
 * Rendered by the Activity tab (agent overview page + channel side panel) and,
 * in `compact` mode, the profile "Recent activity" hover surface (#383) — the
 * SAME `activityPresentation` (labels/tone/folding/active-…), the only delta is
 * layout: last N rows, dense, single-line truncated subtext, no click-to-expand.
 */
export function ActivityTimeline({
  events,
  compact = false,
}: {
  events: ActivityEvent[];
  /** Profile "Recent activity" compact mode: last N narrative rows, no expand. */
  compact?: boolean;
}) {
  const tz = useViewingTimezone();

  const shown = useMemo(() => {
    const narrative = events.filter(isNarrativeActivityEvent);
    if (!compact) return narrative;
    // Compact peek surface drops settled "Idle" status rows — in a recent-activity
    // glance they read as status noise, not an action/result (#465②,
    // Barry/Ronan/Iris 2026-07-15). The full timeline keeps them (the historical
    // "went idle" fact). Filter via the shared presentation, not a duplicated
    // status check.
    return narrative
      .filter((event) => activityPresentation(event).labelKey !== "idle")
      .slice(-COMPACT_RECENT_LIMIT);
  }, [events, compact]);

  if (shown.length === 0) {
    return (
      <p className="text-xs italic text-muted-foreground/60">
        {ACTIVITY_CHROME_EN.timeline_empty}
      </p>
    );
  }

  return (
    <div className="flex flex-col" data-testid="activity-timeline">
      {shown.map((event, index) => (
        <ActivityRow
          key={event.id}
          event={event}
          time={formatActivityTime(event.occurred_at, tz)}
          compact={compact}
          isFirst={index === 0}
          isLast={index === shown.length - 1}
        />
      ))}
    </div>
  );
}
