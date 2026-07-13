import type { AgentActivityTimelineEvent } from "@multica/core/types";
import { stripMentionMarkdown } from "../../../common/strip-mention-markdown";

// FE read-model for the agent-activity narrative timeline (#267 / #302 / #389).
// The BE supplies source-backed facts (`activity_kind`/`detail_kind`, `text`,
// `reason_code`, refs, `entries`); display labels and dot tone are FE projection,
// not API fields. Mainline vs diagnostic is driven by `activity_kind` semantics
// (raft-aligned #389), NOT a `visibility` flag (removed in the cutover).

// Keep the palette intentionally quiet: only failures and waiting states get
// color; the normal narrative stream stays neutral.
export type ActivityTone = "neutral" | "active" | "waiting" | "failure";

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
  | "working"
  | "failed"
  | "waiting"
  | "running_command"
  | "writing_file"
  | "editing_file"
  | "reading_file"
  | "searching_files"
  | "searching_code"
  | "searching_web"
  | "sending_message";

export type ActivitySubtextKey =
  | "message_received"
  | "compacting_context"
  | "compaction_finished"
  | "subagent_activity";

export interface ActivityPresentation {
  labelKey: ActivityLabelKey;
  /** Fixed subtext, resolved via i18n (Message received, Compacting context…). */
  subtextKey?: ActivitySubtextKey;
  /** Dynamic subtext (tool target, reply text, reason) — rendered verbatim. */
  subtext?: string;
  tone: ActivityTone;
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
export function isNarrativeActivityEvent(event: ActivityEvent): boolean {
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
      return event.detail_kind.includes("subagent");
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

function statusTone(event: ActivityEvent): ActivityTone {
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
  send_message: "sending_message",
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

function toolPresentation(event: ActivityEvent): ActivityPresentation {
  const subtext = toolTarget(event);
  const semantic = TOOL_SEMANTIC[normalizedTool(event)];
  // Rendered tool rows are always mapped (see `isNarrativeActivityEvent`); the
  // "working" branch is an unreachable type guard, never a real fallback label.
  const labelKey = (semantic && TOOL_ACTION_KEY[semantic]) || "working";
  return { labelKey, subtext, tone: statusTone(event) };
}

export function activityPresentation(event: ActivityEvent): ActivityPresentation {
  switch (event.activity_kind) {
    case "thinking":
      return { labelKey: "thinking", subtext: narrativeText(event.text), tone: "neutral" };
    case "text":
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
      return { labelKey: "waiting", subtext: reasonText(event) || narrativeText(event.text), tone: "waiting" };
    case "custom":
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
