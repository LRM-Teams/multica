import type { AgentActivityTimelineEvent } from "@multica/core/types";

// FE read-model for the agent-activity narrative timeline (#267 / #302). The BE
// supplies source-backed facts (`kind`, `text`, `reason_code`, refs, and
// `visibility`); display labels and dot tone are FE projection, not API fields.
export type ActivityVisibility = "user_facing" | "diagnostic_only";

// Keep the palette intentionally quiet: only failures and waiting states get
// color; the normal narrative stream stays neutral.
export type ActivityTone = "neutral" | "waiting" | "failure";

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

export function activityPresentation(event: ActivityEvent): ActivityPresentation {
  switch (event.kind) {
    case "thinking":
      return { label: "Thinking", subtext: event.text, tone: "neutral" };
    case "text":
      return { label: "Sent a message", subtext: event.text, tone: "neutral" };
    case "tool_call":
      return { label: "Ran a command", tone: "neutral" };
    case "turn_end":
      return { label: "Done", tone: "neutral" };
    case "session_init":
      return { label: "Run started", tone: "neutral" };
    case "wake_attempt":
      return { label: "Woken", tone: "neutral" };
    case "error": {
      const reason = reasonText(event);
      return {
        label: reason ? `Failed · ${reason}` : "Failed",
        subtext: event.text,
        tone: "failure",
      };
    }
    case "blocked": {
      const reason = reasonText(event);
      return { label: reason ? `Waiting · ${reason}` : "Waiting", tone: "waiting" };
    }
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
