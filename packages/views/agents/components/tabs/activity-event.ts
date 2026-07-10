import type { AgentActivityTimelineEvent } from "@multica/core/types";

// FE read-model for the agent-activity narrative timeline (#267 / #302). The BE
// tags each event's `visibility` and supplies safe human `label`/`subtext` — the
// FE never derives these from raw tool/command/output text (that would be the
// P1-8 heuristic trap). Raw payload stays in the diagnostic/transcript layer.
export type ActivityVisibility = "user_facing" | "diagnostic_only";

// Coarse semantic tone driving the row's status dot colour. Derived BE-side from
// the event's source/result; kept small + closed so the dot reads as a
// glanceable "shape of the timeline" (one red in a column of green = the failing
// step), not a per-tool icon.
export type ActivityTone =
  | "wake" // woke up / claimed / assigned
  | "action" // ran / read / edited / searched / replied
  | "progress" // working / waiting
  | "success" // completed / online / recovered
  | "failure" // failed
  | "muted"; // offline / no-reply / suppressed / neutral

// The FE Activity read-model IS the BE #302 timeline event
// (`AgentActivityTimelineEvent`, packages/core/types/events.ts): id /
// occurred_at / visibility / label / subtext / tone drive the rendered row,
// while raw `kind` + `target_ref`/`source_refs` + `reason_*` let the timeline
// derive the directed 3-state block and deep-links itself. Aliasing to the BE
// type keeps the four layers (daemon → server → API → FE) one shape with zero
// translation.
export type ActivityEvent = AgentActivityTimelineEvent;

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
