// FE read-model for the agent-activity narrative timeline (#267). The BE (#302)
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

export interface ActivityEvent {
  id: string;
  /** ISO-8601 timestamp. */
  occurred_at: string;
  /** BE-tagged. Default rows are `user_facing`; `diagnostic_only` sits behind the toggle. */
  visibility: ActivityVisibility;
  /** Human narrative label (BE `action_label`) — never a raw command. */
  label: string;
  /** Optional one-line human detail (BE `summary`). */
  subtext?: string;
  tone: ActivityTone;
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
