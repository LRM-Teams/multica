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

function toolTarget(event: ActivityEvent): string | undefined {
  return event.tool_target?.trim() || event.tool?.trim() || undefined;
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

function isCommandTool(tool: string): boolean {
  return (
    tool.includes("exec") ||
    tool.includes("command") ||
    tool.includes("shell") ||
    tool.includes("terminal") ||
    tool.includes("bash")
  );
}

function isFileEditTool(tool: string): boolean {
  return (
    tool.includes("edit") ||
    tool.includes("write") ||
    tool.includes("patch") ||
    tool.includes("apply")
  );
}

function isFileReadTool(tool: string): boolean {
  return (
    tool.includes("read") ||
    tool.includes("open") ||
    tool.includes("file") ||
    tool.includes("list") ||
    tool.includes("cat")
  );
}

function isSearchTool(tool: string): boolean {
  return (
    tool.includes("search") ||
    tool.includes("query") ||
    tool.includes("grep") ||
    tool.includes("rg") ||
    tool.includes("find")
  );
}

function toolPresentation(event: ActivityEvent): ActivityPresentation {
  const tool = normalizedTool(event);
  const subtext = toolTarget(event);
  if (tool === "send_message") {
    return { label: activeLabel("Sending message…", event), subtext, tone: statusTone(event) };
  }
  if (isCommandTool(tool)) {
    return { label: activeLabel("Running command…", event), subtext, tone: statusTone(event) };
  }
  if (tool.includes("write")) {
    return { label: activeLabel("Writing file…", event), subtext, tone: statusTone(event) };
  }
  if (isFileEditTool(tool)) {
    return { label: activeLabel("Editing file…", event), subtext, tone: statusTone(event) };
  }
  if (isFileReadTool(tool)) {
    return { label: activeLabel("Reading file…", event), subtext, tone: statusTone(event) };
  }
  if (tool.includes("glob")) {
    return { label: activeLabel("Searching files…", event), subtext, tone: statusTone(event) };
  }
  if (tool.includes("web_search") || tool.includes("websearch")) {
    return { label: activeLabel("Searching web…", event), subtext, tone: statusTone(event) };
  }
  if (isSearchTool(tool)) {
    return { label: activeLabel("Searching code…", event), subtext, tone: statusTone(event) };
  }
  return { label: "Working", subtext, tone: "active" };
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
