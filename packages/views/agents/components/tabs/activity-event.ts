import type { AgentActivityTimelineEvent } from "@multica/core/types";

// FE read-model for the agent-activity narrative timeline (#267 / #302). The BE
// supplies source-backed facts (`kind`, `text`, `reason_code`, refs, and
// `visibility`); display labels and dot tone are FE projection, not API fields.
export type ActivityVisibility = "user_facing" | "diagnostic_only";

// Keep the palette intentionally quiet: only failures and waiting states get
// color; the normal narrative stream stays neutral.
export type ActivityTone = "neutral" | "active" | "waiting" | "failure";

// The FE Activity read-model IS the BE #302 timeline event
// (`AgentActivityTimelineEvent`, packages/core/types/events.ts): id /
// occurred_at / visibility / kind/text/reason/source refs drive the rendered
// row. Aliasing to the BE type keeps the four layers (daemon -> server -> API ->
// FE) one raw shape; presentation stays in the component layer.
export type ActivityEvent = AgentActivityTimelineEvent;

export interface ActivityPresentation {
  label: string;
  subtext?: string;
  tone: ActivityTone;
}

function reasonText(event: ActivityEvent): string {
  return event.reason_code?.trim().replaceAll("_", " ") ?? "";
}

export function isNarrativeActivityEvent(event: ActivityEvent): boolean {
  if (event.visibility !== "user_facing") return false;
  switch (event.kind) {
    case "tool_output":
    case "transport":
    case "telemetry":
    case "turn_end":
    case "session_init":
      return false;
    case "custom":
      return event.event_type.includes("subagent");
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

function activeLabel(label: string, event: ActivityEvent): string {
  return isActiveStatus(event.status) ? label : label.replace(/…$/, "");
}

function statusTone(event: ActivityEvent): ActivityTone {
  return isActiveStatus(event.status) ? "active" : "neutral";
}

// Multica providers pass their own tool slugs — Codex `exec_command`/`patch_apply`,
// OpenCode `bash`/`read`/`write`/`glob`, Grok `read_file`, Claude capitalized
// `Read`, etc. — so `event.tool` is NOT a stable Raft key set (confirmed with BE,
// #382). Normalize the provider slug to a Raft semantic action, then use the
// source-backed gerund label (raft `TOOL_DISPLAY_METADATA`). An unknown slug
// falls back to a neutral working row rather than echoing the raw tool name.
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

const TOOL_ACTION_LABEL: Record<string, string> = {
  command: "Running command…",
  write: "Writing file…",
  edit: "Editing file…",
  read: "Reading file…",
  glob: "Searching files…",
  grep: "Searching code…",
  web_search: "Searching web…",
  send_message: "Sending message…",
};

function toolPresentation(event: ActivityEvent): ActivityPresentation {
  const subtext = toolTarget(event);
  const semantic = TOOL_SEMANTIC[normalizedTool(event)];
  const label = (semantic && TOOL_ACTION_LABEL[semantic]) || "Working…";
  return { label: activeLabel(label, event), subtext, tone: statusTone(event) };
}

export function activityPresentation(event: ActivityEvent): ActivityPresentation {
  switch (event.kind) {
    case "thinking":
      return { label: "Thinking", subtext: event.text, tone: "neutral" };
    case "text":
      return { label: "Output", subtext: event.text, tone: "neutral" };
    case "tool_call":
      return toolPresentation(event);
    case "turn_end":
      return { label: "Working", tone: "active" };
    case "session_init":
      return { label: "Working", tone: "active" };
    case "compaction_started":
      return { label: "Working", subtext: "Compacting context", tone: "active" };
    case "compaction_finished":
      return { label: "Working", subtext: "Compaction finished", tone: "active" };
    case "wake_attempt":
      return { label: "Working", subtext: "Message received", tone: "active" };
    case "error": {
      const reason = reasonText(event);
      return {
        label: "Failed",
        subtext: event.text ?? reason,
        tone: "failure",
      };
    }
    case "blocked": {
      const reason = reasonText(event);
      return { label: "Waiting", subtext: reason || event.text, tone: "waiting" };
    }
    case "custom":
      if (event.event_type.includes("subagent")) {
        return { label: "Working", subtext: event.text ?? "Subagent activity", tone: "active" };
      }
      return { label: "Working", subtext: event.text, tone: "active" };
    default:
      return { label: event.event_type || event.kind, subtext: event.text, tone: "neutral" };
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
