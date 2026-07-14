"use client";

import { useMemo, useState } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { useViewingTimezone } from "../../../common/use-viewing-timezone";
import { useT } from "../../../i18n";
import {
  type ActivityEvent,
  type ActivityDotTone,
  activityPresentation,
  formatActivityTime,
  isNarrativeActivityEvent,
} from "./activity-event";

// All dots are STATIC — no `animate-pulse` (#404 follow-up). A perpetually
// pulsing dot made settled/historical rows look like they were still loading
// live; real "is it live" now comes from the header/hover latest-state (#521),
// not a blinking dot. Command rows use raft's solid amber; others by tone.
const TONE_DOT: Record<ActivityDotTone, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-brand",
  // Command rows (any `Running command`) get raft's solid amber dot — type-based,
  // NOT status-based: a settled/idle command still shows amber (raft parity,
  // #404), so it never reads as a grey "idle" row.
  running: "bg-[#F5B301]",
  waiting: "bg-warning",
  failure: "bg-destructive",
};

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
  const { t } = useT("agents");
  // Per-row expand — shared by thinking/text (§2.1) and command rows (entries[].command).
  // Branches are mutually exclusive so one boolean is enough.
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const presentation = activityPresentation(event);
  const rawLabel = t(($) => $.tab_body.activity.labels[presentation.labelKey]);
  // Locale values are base form (no ellipsis). The trailing "…" is raft's
  // in-progress signal, appended at render for an active tool action only —
  // never on settled rows or non-tool states (wake / compaction / reply).
  const label =
    event.activity_kind === "tool_call" && presentation.tone === "active" ? `${rawLabel}…` : rawLabel;
  const subtext = presentation.subtextKey
    ? t(($) => $.tab_body.activity.subtexts[presentation.subtextKey!])
    : presentation.subtext;
  // Thinking and reply Output carry the model's full text (§2.1: collapse to the
  // first line, click to expand the full content block). Fixed / short subtexts
  // (tool target, reasons, "Message received") stay inline. The compact profile
  // surface never expands — it single-line truncates instead (§2.1: full expand
  // is the Activity tab's job).
  const expandable =
    !compact &&
    !!subtext &&
    !presentation.subtextKey &&
    (event.activity_kind === "thinking" || event.activity_kind === "text");

  // Command rows: BE already redacts (`sk_agent_<redacted>`); FE shows
  // `entries[].command` via presentation.subtextFull / subtext verbatim.
  const isCommand = presentation.subtextKind === "command" && !!subtext;
  const commandFull = presentation.subtextFull ?? subtext ?? "";
  const canExpandCommand = isCommand && !compact;

  const handleCopyCommand = async () => {
    if (await copyText(commandFull)) {
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

  const copyLabel = t(($) =>
    copied ? $.tab_body.activity.command_copied : $.tab_body.activity.copy_command,
  );

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
      {expandable ? (
        <div className="min-w-0">
          <span className="text-sm text-foreground">{label}</span>
          {subtext && (
            <button
              type="button"
              onClick={() => setExpanded((prev) => !prev)}
              aria-expanded={expanded}
              className={cn(
                "mt-0.5 block w-full whitespace-pre-wrap text-left text-xs text-muted-foreground transition-colors hover:text-foreground",
                !expanded && "line-clamp-1",
              )}
            >
              {subtext}
            </button>
          )}
        </div>
      ) : canExpandCommand ? (
        // Command row: label + command as ONE full-width hanging block (#404).
        // Collapsed = line-clamp-2 tail truncate. Click main row or caret →
        // drop clamp, pre-wrap + break-all full command (entries[].command),
        // caret flips; small 「复制」 when open. In-place text only — no card.
        <div className="relative min-w-0 flex-1">
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            aria-expanded={expanded}
            className="flex w-full items-start gap-1.5 text-left"
          >
            <span
              className={cn(
                "min-w-0 flex-1 text-sm leading-[1.45] text-foreground",
                expanded ? "break-all whitespace-pre-wrap" : "line-clamp-2 break-all",
              )}
            >
              <span>{label} </span>
              <span className="font-mono text-xs text-muted-foreground">
                {expanded ? commandFull : subtext}
              </span>
            </span>
            {expanded ? (
              <ChevronUp
                className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/70"
                aria-hidden
              />
            ) : (
              <ChevronDown
                className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/70"
                aria-hidden
              />
            )}
          </button>
          {expanded && (
            <button
              type="button"
              onClick={handleCopyCommand}
              aria-label={copyLabel}
              className="absolute right-5 top-0 text-[11px] leading-none text-muted-foreground transition-colors hover:text-foreground"
            >
              {copyLabel}
            </button>
          )}
        </div>
      ) : isCommand && compact ? (
        // Compact Profile Recent: clamp + no expand / copy (title-only).
        <div className="min-w-0 flex-1">
          <div className="line-clamp-2 break-all text-sm leading-[1.45] text-foreground">
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
  const { t } = useT("agents");
  const tz = useViewingTimezone();

  const shown = useMemo(() => {
    const narrative = events.filter(isNarrativeActivityEvent);
    return compact ? narrative.slice(-COMPACT_RECENT_LIMIT) : narrative;
  }, [events, compact]);

  if (shown.length === 0) {
    return (
      <p className="text-xs italic text-muted-foreground/60">
        {t(($) => $.tab_body.activity.timeline_empty)}
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
