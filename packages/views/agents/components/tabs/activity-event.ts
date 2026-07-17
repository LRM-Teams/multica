import type { AgentActivityTimelineEvent } from "@multica/core/types";
import { stripMentionMarkdown } from "../../../common/strip-mention-markdown";

// FE read-model for the agent-activity narrative timeline (#267 / #302 / #389).
// The BE supplies source-backed facts (`activity_kind`/`detail_kind`, `text`,
// `reason_code`, refs, `entries`); display labels and dot tone are FE projection,
// not API fields. Mainline vs diagnostic is driven by `activity_kind` semantics
// (raft-aligned #389), NOT a `visibility` flag (removed in the cutover).

// Keep the palette intentionally quiet: only failures get color; every other
// state is neutral gray. "Is it live?" now reads via the avatar's breathing
// pulse (actor-avatar `animate-ping`), not a dot color.
export type ActivityDotTone = "neutral" | "active" | "running" | "waiting" | "failure" | "radar";

// SINGLE source for the tone → dot-color map. Both the Activity timeline and the
// name-row live-status header project the SAME latest Activity row, so the dot
// must read from ONE table — never two hand-kept copies that can drift (they
// did: this used to be duplicated verbatim in activity-timeline.tsx and
// resolve-agent-live-status.ts). Slack-style reduction: active / running /
// waiting / radar all collapse to neutral gray (distinguished by label + the
// avatar pulse), and only a failure keeps an accent (destructive red).
export const ACTIVITY_TONE_DOT_CLASS: Record<ActivityDotTone, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-muted-foreground/40",
  running: "bg-muted-foreground/40",
  waiting: "bg-muted-foreground/40",
  failure: "bg-destructive",
  radar: "bg-muted-foreground/40",
};

// The FE Activity read-model IS the BE #302 timeline event
// (`AgentActivityTimelineEvent`, packages/core/types/events.ts): id /
// occurred_at / activity_kind / detail_kind / text / reason / refs / entries
// drive the rendered row. Aliasing to the BE type keeps the four layers
// (daemon -> server -> API -> FE) one raw shape; presentation stays in the
// component layer.
export type ActivityEvent = AgentActivityTimelineEvent;

// Labels are i18n KEYS resolved in the component — the raft-exact source strings
// live in `agents.json` (`tab_body.activity.labels`). A fixed subtext (e.g.
// "Message received") is also a key (`…subtexts`); a dynamic subtext (tool target
// path, reply text, block reason) is passed through verbatim and NEVER translated.
export type ActivityLabelKey =
  | "thinking"
  | "output"
  | "completed"
  | "radar_executed"
  | "working"
  | "idle"
  | "failed"
  | "waiting"
  | "running_command"
  | "writing_file"
  | "editing_file"
  | "reading_file"
  | "searching_files"
  | "searching_code"
  | "searching_web"
  | "sending_message"
  | "send_held_by_freshness";

export type ActivitySubtextKey =
  | "message_received"
  | "compacting_context"
  | "compaction_finished"
  | "subagent_activity";

// Activity is ENGLISH-ONLY for now (Frank 2026-07-14): labels/subtexts render
// from these canonical maps — the raft-exact source strings — instead of a
// locale i18n lookup. The `tab_body.activity.*` locale keys are intentionally
// kept so a locale layer can be re-added later over the SAME semantic keys
// without touching this projection (Parker).
export const ACTIVITY_LABEL_EN: Record<ActivityLabelKey, string> = {
  thinking: "Thinking",
  output: "Output",
  completed: "Completed",
  radar_executed: "Radar",
  working: "Working",
  idle: "Idle",
  failed: "Failed",
  waiting: "Waiting",
  running_command: "Running command",
  writing_file: "Writing file",
  editing_file: "Editing file",
  reading_file: "Reading file",
  searching_files: "Searching files",
  searching_code: "Searching code",
  searching_web: "Searching web",
  sending_message: "Sending message",
  send_held_by_freshness: "Send held by freshness check",
};

export const ACTIVITY_SUBTEXT_EN: Record<ActivitySubtextKey, string> = {
  message_received: "Message received",
  compacting_context: "Compacting context",
  compaction_finished: "Context compaction finished",
  subagent_activity: "Subagent activity",
};

// Activity chrome (non-event UI: empty state, command Copy, jump-to-latest,
// diagnostics toggle) is English-only too (Frank 2026-07-14 "整条 Activity 全英文"),
// so a zh/ja/ko viewer never sees English event rows framed by localized controls.
// One canonical map, shared by every Activity chrome render site (timeline empty +
// command Copy here, jump-to-latest / diagnostics toggle in their own components).
// The `locales/*/agents.json` chrome keys stay in place for a future one-layer
// re-localize of the whole timeline — same "先" (for-now) posture as the labels.
export const ACTIVITY_CHROME_EN = {
  copy_command: "Copy",
  command_copied: "Copied",
  timeline_empty: "No activity yet",
  jump_to_latest: "Jump to latest",
  view_diagnostics: "View diagnostic details",
  hide_diagnostics: "Hide diagnostic details",
} as const;

export interface ActivityPresentation {
  labelKey: ActivityLabelKey;
  /** Fixed subtext, resolved via i18n (Message received, Compacting context…). */
  subtextKey?: ActivitySubtextKey;
  /** Dynamic subtext (tool target, reply text, reason) — rendered verbatim. */
  subtext?: string;
  /**
   * How to render `subtext` (#v0 照实显示): a file `path` gets the
   * basename-preserving middle-ellipsis treatment; a shell `command` renders as
   * a plain single-line clip with the full redacted command on hover/copy (never
   * the path treatment — a command contains `/` but is NOT a path); everything
   * else is plain `text`. Undefined for non-tool rows.
   */
  subtextKind?: "path" | "command" | "text";
  /**
   * Full untruncated value for the hover tooltip / copy affordance when the
   * inline `subtext` is a clip — e.g. the full redacted command from
   * `entries[].command` while `subtext` shows the BE's compact clip.
   */
  subtextFull?: string;
  tone: ActivityDotTone;
}

function reasonText(event: ActivityEvent): string {
  return event.reason_code?.trim().replaceAll("_", " ") ?? "";
}

// Free-form model/message text (Output body, thinking prose, subagent detail)
// is authored markdown — it still carries mention syntax like
// `[@Frank An](mention://member/id)`. The Activity row shows a plain-text
// preview, so normalize mentions to their display name (`@Frank An`) before
// display; real markdown links (`[docs](https://…)`) are left untouched (#387:
// the raw `mention://` URI was leaking into the Output preview). Tool targets
// are BE-provided safe summaries (basename / clipped query) and never carry
// mentions, so they skip this.
function narrativeText(text: string | null | undefined): string | undefined {
  const trimmed = text ?? undefined;
  return trimmed === undefined ? undefined : stripMentionMarkdown(trimmed);
}

// Mainline vs diagnostic is driven by the raft `activity_kind` semantics (#389),
// NOT a `visibility` flag (that field was removed in the raft-alignment cutover).
// The BE already prefilters the default page to mainline narrative; this predicate
// is the FE-side guard that keeps the split identical (shared table, Iris keeper).
function isRadarActionEvent(event: ActivityEvent): boolean {
  if (event.reason_code?.trim() === "radar_untrusted_target") {
    return false;
  }
  if (
    event.detail_kind === "radar_action_executed" &&
    event.reason_code?.trim() === "no_action"
  ) {
    return false;
  }
  return (
    event.detail_kind === "radar_action_executed" || event.detail_kind === "radar_action_failed"
  );
}

export function isNarrativeActivityEvent(event: ActivityEvent): boolean {
  // A `message_sent` event is the RESULT of an agent sending a message: the sent
  // content lives in the chat stream, and the `multica message send` CLI already
  // shows as its own "Running command" row — so an "Output · <sent content>" row
  // here is a redundant duplicate (Frank/#404, raft parity: a send produces no
  // separate Output row). Keep it diagnostic. Field-driven (`detail_kind`), never
  // string-sniffed. Non-send `text`/Output (e.g. a radar decision) is NOT
  // `message_sent` and stays mainline (Frank: observability is fine).
  if (event.detail_kind === "message_sent") return false;
  switch (event.activity_kind) {
    case "tool_output":
    case "transport":
    case "telemetry":
    case "turn_end":
    case "session_init":
    case "internal_progress":
    case "runtime_diagnostic":
      return false;
    case "tool_call":
      // Only surface a tool row we can label with a canonical Raft action. An
      // un-mapped tool (BE didn't canonicalize it, or a parse artifact like a
      // status leaking into `tool`) drops out of the user-facing timeline
      // entirely — never faked as "Working" (#384). BE emits an
      // `unmapped_tool_name` gap event so the miss is fixed at the source.
      return isMappedTool(event);
    case "custom":
      return (
        // Agent status → timeline: keep IDLE only, drop WORKING. "Working" the
        // agent already conveys via the actual work rows (thinking / Running
        // command / Writing file…) and the wake "Working · Message received" row
        // that opens the round — a bare "Working" status row right after the wake
        // row is the same transition written twice (Frank: "为什么突然多了一个
        // working"). Raft models status as presence (a dot / header projection of
        // the latest activity kind), not a text row, so a redundant "Working" line
        // is an invented duplicate. "Idle" IS kept — end-of-round is independent
        // info not otherwise visible in the timeline. The WORKING event still
        // exists in the stream for the header/hover latest-state projection; we
        // just don't give it its own timeline row.
        (event.detail_kind === "agent_status_changed" && event.status !== "working") ||
        event.detail_kind.includes("subagent") ||
        isRadarActionEvent(event)
      );
    default:
      return true;
  }
}

function normalizedTool(event: ActivityEvent): string {
  return event.tool?.trim().toLowerCase() ?? "";
}

// Subtext is ONLY the BE-provided safe summary (`tool_target`: a path basename /
// clipped query / pattern). Never fall back to the raw `event.tool` slug — for an
// unknown provider tool that would leak the raw name into the row (#382 gate:
// unknown tools show no raw slug in label OR subtext).
function toolTarget(event: ActivityEvent): string | undefined {
  return event.tool_target?.trim() || undefined;
}

function isActiveStatus(status: string | undefined): boolean {
  return (
    status === "queued" ||
    status === "dispatched" ||
    status === "running" ||
    status === "waiting_local_directory"
  );
}

function statusTone(event: ActivityEvent): ActivityDotTone {
  return isActiveStatus(event.status) ? "active" : "neutral";
}

// Multica providers pass their own tool slugs — Codex `exec_command`/`patch_apply`,
// OpenCode `bash`/`read`/`write`/`glob`, Grok `read_file`, Claude capitalized
// `Read`, etc. — so `event.tool` is NOT a stable Raft key set (confirmed with BE,
// #382). Normalize the provider slug to a Raft semantic action, then use the
// source-backed gerund label (raft `TOOL_DISPLAY_METADATA`). An un-mapped slug
// (BE didn't canonicalize it, or a parse artifact) is dropped from the
// user-facing timeline by `isMappedTool`/`isNarrativeActivityEvent` — never
// faked as "Working" and never echoing the raw name (#384).
const TOOL_SEMANTIC: Record<string, string> = {
  bash: "command",
  exec_command: "command",
  exec: "command",
  shell: "command",
  terminal: "command",
  command: "command",
  write: "write",
  write_file: "write",
  // A runtime's native file-create tool (raft folds `create`/`create_file` into
  // the `write_file` family, #413). Without these aliases the slug is un-mapped →
  // the row is dropped from the timeline (worse than the old "Running command
  // create"): map to `write` so it shows "Writing file · <path>" whether the BE
  // forwards `tool=write_file` or the raw `tool=create`.
  create: "write",
  create_file: "write",
  createfile: "write",
  patch_apply: "edit",
  edit: "edit",
  edit_file: "edit",
  file_edit: "edit",
  multi_edit: "edit",
  read: "read",
  read_file: "read",
  open: "read",
  cat: "read",
  glob: "glob",
  grep: "grep",
  rg: "grep",
  search: "grep",
  web_search: "web_search",
  websearch: "web_search",
  send_message: "send_message",
};

const TOOL_ACTION_KEY: Record<string, ActivityLabelKey> = {
  command: "running_command",
  write: "writing_file",
  edit: "editing_file",
  read: "reading_file",
  glob: "searching_files",
  grep: "searching_code",
  web_search: "searching_web",
  // NOTE: no `send_message` → "sending_message" entry. A `multica message send`
  // (which the daemon canonicalizes to `send_message`) is a CLI command and is
  // shown as "Running command · <command>" via the command branch in
  // `toolPresentation` — Frank's #v0 rule: don't invent a "Sending message"
  // label, show the real command like any other CLI.
};

// A tool row only reaches the user-facing timeline when its slug maps to a
// canonical Raft action (see `isNarrativeActivityEvent`). An un-mapped tool is a
// canonicalization gap — the BE didn't canonicalize the name, or a parse
// artifact leaked a non-tool string into `tool`. We keep it diagnostic-only
// rather than papering over it with a fake "Working" row (#384); the source-side
// fix is BE emitting an `unmapped_tool_name` gap event.
function isMappedTool(event: ActivityEvent): boolean {
  return !!TOOL_SEMANTIC[normalizedTool(event)];
}

// The full redacted command lives in `entries[].command` (#389 two-tier: the
// compact clip is `tool_target` inline, the full command is for hover/copy).
// Pull the first entry that carries one.
function fullCommand(event: ActivityEvent): string | undefined {
  return event.entries?.find((e) => e.command?.trim())?.command?.trim() || undefined;
}

// DOM-size safety bound for the inline command string. The VISUAL truncation is
// CSS `line-clamp-2` (raft-parity two-line preview + trailing ellipsis, #404);
// this cap just keeps a pathologically long command out of the DOM text node.
// The full redacted command always stays reachable via click-to-expand + copy
// (`subtextFull` from `entries[].command`).
const COMMAND_INLINE_CAP = 500;

function toolPresentation(event: ActivityEvent): ActivityPresentation {
  // Frank's rule (#v0 「不发明新东西」): anything run as a CLI command — bash, and
  // any multica subcommand the daemon canonicalized to a semantic tool
  // (`send_message`, …) — is shown FAITHFULLY as "Running command · <command>",
  // never a product-invented label ("Sending message"). The redacted CLI lives in
  // `entries[].command`; its presence is the signal that this row is a command.
  // Main row = the command wrapped to two lines (CSS line-clamp); click expands
  // to pin the full redacted command + Copy. The dot is raft's amber `running`
  // tone — type-based, so a settled command still reads as a command, never a
  // grey idle row (#404). Native structured tools (read_file/glob/grep) carry
  // no command and keep their real label + real object below.
  const command = fullCommand(event);
  if (command) {
    return {
      labelKey: "running_command",
      subtext: command.length > COMMAND_INLINE_CAP ? command.slice(0, COMMAND_INLINE_CAP) : command,
      subtextKind: "command",
      subtextFull: command,
      tone: "running",
    };
  }

  const subtext = toolTarget(event);
  const semantic = TOOL_SEMANTIC[normalizedTool(event)];
  // Rendered tool rows are always mapped (see `isNarrativeActivityEvent`); the
  // "working" branch is an unreachable type guard, never a real fallback label.
  const labelKey = (semantic && TOOL_ACTION_KEY[semantic]) || "working";
  // Classify the subtext so the row renders it correctly (#v0 照实显示). A file
  // tool's target is a PATH (basename-preserving middle-ellipsis). A command
  // tool whose clip arrived without an attached full command still renders as a
  // plain command clip — never the path treatment, which middle-ellipsises on
  // the last `/` and mangles a command that merely contains a slash (the #v0
  // "命令看不全/云里雾里" bug). Everything else is plain text.
  const isPathTool = semantic === "read" || semantic === "write" || semantic === "edit";
  const isCommand = semantic === "command";
  const subtextKind: ActivityPresentation["subtextKind"] = isPathTool
    ? "path"
    : isCommand
      ? "command"
      : "text";
  // A command row (even one whose full command didn't arrive) is amber `running`
  // like every command (#404); other tools keep the status-driven tone.
  return { labelKey, subtext, subtextKind, tone: isCommand ? "running" : statusTone(event) };
}

/**
 * Compose the held-freshness detail subtext (#441) from the BE's structured
 * `details` — the 10 allowlist scalar keys only, never raw payload (BE #580
 * narrow contract). English-only like the rest of Activity; we STATE the facts
 * (how many newer messages, where, the shown/omitted split) rather than parse or
 * invent prose. Every key is optional — the BE may omit any — so degrade
 * gracefully to whatever facts are present, and return undefined when `details`
 * is absent (the status row's label alone still conveys the hold). Internal ids /
 * seqs / decision codes are not surfaced: they're diagnostic, not reader-facing.
 */
function freshnessHoldDetail(event: ActivityEvent): string | undefined {
  const d = event.details;
  if (!d) return undefined;
  const asString = (key: string): string | undefined => {
    const v = d[key];
    return typeof v === "string" && v.trim() ? v.trim() : undefined;
  };
  const asCount = (key: string): number | undefined => {
    const v = d[key];
    return typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : undefined;
  };
  const target = asString("target");
  const newCount = asCount("new_message_count");
  const shown = asCount("shown_message_count");
  const omitted = asCount("omitted_message_count");

  const lead =
    newCount != null
      ? `${newCount} newer message${newCount === 1 ? "" : "s"}${target ? ` in ${target}` : ""}`
      : target
        ? `Newer messages in ${target}`
        : "Newer messages arrived";

  const breakdown: string[] = [];
  if (shown != null) breakdown.push(`${shown} shown`);
  if (omitted != null) breakdown.push(`${omitted} not yet shown`);
  const detail = breakdown.length ? ` (${breakdown.join(", ")})` : "";

  return `${lead}${detail} — send held until the newer context is reviewed.`;
}

export function activityPresentation(event: ActivityEvent): ActivityPresentation {
  switch (event.activity_kind) {
    case "thinking":
      return { labelKey: "thinking", subtext: narrativeText(event.text), tone: "neutral" };
    case "text":
      // #441 held-freshness detail row: same label as the status row, plus the
      // structured English detail composed from `details` (waiting tone — Iris:
      // 暖色, not red/blue). Falls through to plain Output for any other text.
      if (event.detail_kind === "send_freshness_hold_detail") {
        return {
          labelKey: "send_held_by_freshness",
          subtext: freshnessHoldDetail(event),
          tone: "waiting",
        };
      }
      return { labelKey: "output", subtext: narrativeText(event.text), tone: "neutral" };
    case "tool_call":
      return toolPresentation(event);
    case "turn_end":
    case "session_init":
      return { labelKey: "working", tone: "active" };
    case "compaction_started":
      return { labelKey: "working", subtextKey: "compacting_context", tone: "active" };
    case "compaction_finished":
      return { labelKey: "working", subtextKey: "compaction_finished", tone: "active" };
    case "wake_attempt":
      return { labelKey: "working", subtextKey: "message_received", tone: "active" };
    case "error":
      return {
        labelKey: "failed",
        subtext: narrativeText(event.text) ?? reasonText(event),
        tone: "failure",
      };
    case "blocked":
      // #441 held-freshness status row: canonical label, no subtext (the paired
      // detail `text` row carries the specifics). Any other block → generic wait.
      if (event.detail_kind === "send_freshness_hold") {
        return { labelKey: "send_held_by_freshness", tone: "waiting" };
      }
      return { labelKey: "waiting", subtext: reasonText(event) || narrativeText(event.text), tone: "waiting" };
    case "custom":
      if (event.detail_kind === "agent_status_changed") {
        // Agent presence transition (#411/#525). Raft's status palette: green =
        // idle, yellow = working. We keep it type-based and static (Frank: no
        // pulsing) — idle is a settled neutral row, working an active one. The
        // label IS the state (no subtext). This row also feeds the header/hover
        // latest-state via `projectLatestActivity`, so Idle correctly shows Idle.
        return event.status === "idle"
          ? { labelKey: "idle", tone: "neutral" }
          : { labelKey: "working", tone: "active" };
      }
      if (event.detail_kind === "radar_action_failed") {
        return {
          labelKey: "failed",
          subtext: narrativeText(event.text) ?? reasonText(event),
          tone: "failure",
        };
      }
      if (event.detail_kind === "radar_action_executed") {
        // Radar runs are the group manager's proactive sweeps, not task
        // completions — give them their own label/tone so they don't read as a
        // generic "Completed" row (#user-request).
        return { labelKey: "radar_executed", subtext: narrativeText(event.text), tone: "radar" };
      }
      if (event.detail_kind.includes("subagent")) {
        // Prefer the daemon's own subagent detail text; fall back to a fixed label.
        return event.text
          ? { labelKey: "working", subtext: narrativeText(event.text), tone: "active" }
          : { labelKey: "working", subtextKey: "subagent_activity", tone: "active" };
      }
      return { labelKey: "working", subtext: narrativeText(event.text), tone: "active" };
    default:
      // Unmapped narrative kind — a neutral working row, never the raw kind string.
      return { labelKey: "working", subtext: narrativeText(event.text), tone: "neutral" };
  }
}

// Building an Intl formatter is slow, so cache one per timezone rather than
// rebuilding on every row render.
const timeFormatters = new Map<string, Intl.DateTimeFormat>();

function timeFormatter(tz: string): Intl.DateTimeFormat {
  let formatter = timeFormatters.get(tz);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat("en-GB", {
      timeZone: tz,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
    });
    timeFormatters.set(tz, formatter);
  }
  return formatter;
}

// HH:MM:SS in the viewing timezone (24-hour) — the tabular timestamp Iris's spec
// calls for; matches raft's stream. Pure so it's testable.
export function formatActivityTime(iso: string, tz: string): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return "";
  return timeFormatter(tz).format(ms);
}
