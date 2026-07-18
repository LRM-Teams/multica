"use client";

import { useId, useMemo, useState } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { MemoizedMarkdown } from "../../../common/markdown";
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
} from "./activity-event";

// All dots are STATIC — no `animate-pulse` (#404 follow-up). A perpetually
// pulsing dot made settled/historical rows look like they were still loading
// live; real "is it live" now comes from the header/hover latest-state (#521)
// and the avatar pulse, not a blinking or colored dot. Tone → color lives in
// ONE shared table (ACTIVITY_TONE_DOT_CLASS) so the header can't drift from it.
const TONE_DOT = ACTIVITY_TONE_DOT_CLASS;

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

function ActivityRow({
  event,
  time,
  compact = false,
}: {
  event: ActivityEvent;
  time: string;
  compact?: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
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

  if (expansion) {
    return (
      <div
        className="py-1"
        data-testid="activity-row"
        data-activity-kind={event.activity_kind}
      >
        <button
          type="button"
          onClick={() => setExpanded((value) => !value)}
          aria-expanded={expanded}
          aria-controls={detailId}
          className="group flex min-h-11 w-full items-start gap-3 rounded-md py-1 text-left transition-colors hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:min-h-0"
        >
          <span className="w-12 shrink-0 pt-1 font-mono text-[11px] tabular-nums text-muted-foreground/70">
            {time}
          </span>
          <span
            className={cn("mt-2.5 size-1.5 shrink-0 rounded-full", TONE_DOT[presentation.tone])}
            aria-hidden
          />
          <span className="min-w-0 flex-1 pt-0.5">
            <span className="block text-sm text-foreground">{label}</span>
            {subtext && (
              <span
                className={cn(
                  "mt-0.5 block text-xs text-muted-foreground",
                  isCommand ? "line-clamp-2 break-words font-mono" : "line-clamp-1",
                )}
              >
                {subtext}
              </span>
            )}
          </span>
          <span className="mt-1 shrink-0 text-muted-foreground/70 transition-colors group-hover:text-foreground" aria-hidden>
            {expanded ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
          </span>
        </button>
        {expanded && (
          <div
            id={detailId}
            data-testid="activity-expanded-detail"
            className="ml-[4.875rem] mt-1 rounded-md border bg-muted/20 px-3 py-2 text-sm text-foreground sm:ml-[5.25rem]"
          >
            {isCommand ? (
              <div className="relative">
                <pre className="overflow-x-auto pr-14 font-mono text-xs leading-5 whitespace-pre-wrap break-words">
                  <code>{expansion.content}</code>
                </pre>
                <button
                  type="button"
                  onClick={handleCopyCommand}
                  aria-label={copyLabel}
                  className="absolute right-0 top-0 rounded border bg-background px-1.5 py-0.5 text-[11px] leading-none text-muted-foreground shadow-sm transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {copyLabel}
                </button>
              </div>
            ) : (
              <MemoizedMarkdown
                mode="minimal"
                enableStickerShortcodes={false}
                className="activity-expanded-markdown break-words text-sm leading-6 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0"
              >
                {expansion.content}
              </MemoizedMarkdown>
            )}
          </div>
        )}
      </div>
    );
  }

  return (
    <div
      className={cn("flex items-baseline gap-3", compact ? "py-0.5" : "py-1")}
      data-testid="activity-row"
      data-activity-kind={event.activity_kind}
    >
      <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground/70">
        {time}
      </span>
      <span
        className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", TONE_DOT[presentation.tone])}
        aria-hidden
      />
      {presentation.subtextKind === "command" && compact ? (
        // Compact Profile Recent: clamp + no expand / copy (title-only).
        <div className="min-w-0 flex-1">
          <div className="line-clamp-2 break-words text-sm leading-[1.45] text-foreground">
            <span>{label} </span>
            <span className="font-mono text-xs text-muted-foreground">{subtext}</span>
          </div>
        </div>
      ) : (
        <div className="flex min-w-0 items-baseline gap-2">
          <span className="shrink-0 text-sm text-foreground">{label}</span>
          {subtextNode}
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
 * each row = `time · source dot · human label · optional subtext`. Shows only
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
    <div className="flex flex-col">
      {shown.map((event) => (
        <ActivityRow
          key={event.id}
          event={event}
          time={formatActivityTime(event.occurred_at, tz)}
          compact={compact}
        />
      ))}
    </div>
  );
}
