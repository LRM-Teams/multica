/**
 * View-model the Agent Card Reminders tab renders against — deliberately
 * decoupled from the raw wire shape task #655's read API returns. `adaptUpcomingRow`/
 * `adaptFiredRow` are the only places that translate raw server fields into
 * this type; if the endpoint shape moves again, only those (and the `Raw*`
 * types) need to change, not the component.
 *
 * Endpoint contract (from task #655, `server/internal/handler/agent_reminder_read.go`,
 * committed to product baseline `product/654-reminder-parity@4937f3841` — the
 * read-page shape is locked independent of #870's fire/migration-correctness
 * blockers, per Parker):
 *   GET /api/agents/{agentId}/reminders?status=scheduled|fired|all&cursor=&limit=
 * `status=scheduled` -> only `definitions` populated (Upcoming section).
 * `status=fired` -> only `occurrences` populated, cursor-paginated newest-first (History section).
 */

export type ReminderCadence =
  | { kind: "one_shot" }
  // "calendar" = `daily@HH:MM` / `weekly:<days>@HH:MM` — resolves against a
  // real IANA timezone, locked at schedule time. "interval" = `every:<N>m|h|d`
  // — a zone-free elapsed-time rule. Derived from whether the server sent a
  // `schedule_timezone` at all: `reminderTimezonePtr` on the backend returns
  // nil for anything that isn't a daily/weekly cadence, INCLUDING a one-shot
  // row that retains a hidden lifetime-locked timezone in the DB (kept only
  // so a future recurring re-conversion can restore it) — that retained
  // value never reaches this wire, so presence-of-timezone is a safe signal
  // here, not a re-parse of the cadence string ourselves.
  | { kind: "recurring"; family: "calendar" | "interval"; description: string; timezone?: string };

export type ReminderAnchor =
  | { available: true; kind: "channel" | "thread"; label: string; href: string }
  | { available: false };

/** The reminder DEFINITION's own lifecycle state — independent of any one occurrence row. `"firing"` is a real transient state (mid-fire-transaction) the FE must accept as-is, never coerced to `"fired"`. */
export type ReminderDefinitionStatus = "scheduled" | "firing" | "fired" | "cancelled";

export interface ReminderRow {
  id: string;
  title: string;
  cadence: ReminderCadence;
  anchor: ReminderAnchor;
}

export interface UpcomingReminderRow extends ReminderRow {
  nextFireAt: string;
}

export interface FiredReminderRow extends ReminderRow {
  firedAt: string;
  /**
   * The PARENT DEFINITION's own current state — independent of this specific
   * fire occurrence. A recurring reminder stays `scheduled` (or `firing`)
   * after firing; only a one-shot's single fire makes the definition itself
   * terminal. History renders occurrences, never the definition's state —
   * but must not IMPLY a still-active recurring definition is done just
   * because one row shows a past fire.
   */
  definitionStatus: ReminderDefinitionStatus;
}

/** `humanReminderAnchor` (Go: `server/internal/handler/agent_reminder_read.go`). Server computes a fully-authorized, ready-to-navigate internal href — FE never constructs one from raw ids. */
export interface RawReminderAnchor {
  available: boolean;
  kind?: "channel" | "thread";
  display?: string;
  href?: string;
}

/** `humanReminderDefinition` — one row in the Upcoming section. */
export interface RawReminderDefinition {
  id: string;
  title: string;
  status: ReminderDefinitionStatus;
  schedule_kind: "recurring" | "one_shot";
  next_fire_at: string;
  last_fire_at?: string;
  cadence?: string;
  schedule_timezone?: string;
  snooze_count: number;
  anchor: RawReminderAnchor;
}

/** `humanReminderOccurrence` — one row in the History section. */
export interface RawReminderOccurrence {
  id: string;
  reminder_id: string;
  title: string;
  status: string;
  definition_status: ReminderDefinitionStatus;
  schedule_kind: "recurring" | "one_shot";
  cadence_scheduled_for: string;
  due_at: string;
  fired_at: string;
  cadence?: string;
  schedule_timezone?: string;
  anchor: RawReminderAnchor;
}

/** `humanReminderPage` — the single page shape both Upcoming and History requests return (only the relevant array is populated per `status` query param). */
export interface RawReminderPage {
  definitions: RawReminderDefinition[];
  occurrences: RawReminderOccurrence[];
  limit: number;
  has_more: boolean;
  next_cursor?: string;
}

function adaptAnchor(raw: RawReminderAnchor): ReminderAnchor {
  if (raw.available && raw.kind && raw.display && raw.href) {
    return { available: true, kind: raw.kind, label: raw.display, href: raw.href };
  }
  return { available: false };
}

// `schedule_kind` (server-authoritative) decides one-shot vs recurring; for
// recurring, whether the server sent a `schedule_timezone` at all is the
// family signal — NOT a client-side re-parse of the cadence string.
// `reminderTimezonePtr` on the backend already returns nil for anything that
// isn't daily/weekly (including a one-shot row that retains a hidden
// lifetime-locked timezone in the DB, kept only so a future recurring
// re-conversion can restore it) — that retained value never reaches this
// wire, so presence-of-timezone is a safe, already-server-decided signal.
// Re-deriving family from the cadence string here would duplicate grammar
// the backend owns and risk drifting from it (confirmed with Parker against
// the committed projection).
const KNOWN_DEFINITION_STATUSES = new Set<ReminderDefinitionStatus>([
  "scheduled",
  "firing",
  "fired",
  "cancelled",
]);

function isKnownDefinitionStatus(status: string): status is ReminderDefinitionStatus {
  return KNOWN_DEFINITION_STATUSES.has(status as ReminderDefinitionStatus);
}

// `schedule_kind` arrives from the network as a plain `string` (the runtime
// schema deliberately doesn't `z.enum()` it — see RawReminderPageSchema) so
// an unrecognized third value still parses instead of rejecting the whole
// page. This is the boundary that narrows it back down: an unknown value
// returns `null` (caller drops the row) rather than silently falling through
// to either known state — misclassifying an unknown kind as "recurring" or
// "one_shot" would render a wrong-but-plausible-looking row.
function adaptCadence(scheduleKind: string, cadence: string | undefined, scheduleTimezone: string | undefined): ReminderCadence | null {
  if (scheduleKind === "one_shot") return { kind: "one_shot" };
  if (scheduleKind !== "recurring") return null;
  if (!cadence) return { kind: "one_shot" };
  return {
    kind: "recurring",
    family: scheduleTimezone ? "calendar" : "interval",
    description: cadence,
    timezone: scheduleTimezone,
  };
}

/** Adapts one Upcoming-section definition row. Returns `null` for a malformed row (missing required field or an unrecognized `schedule_kind`) rather than rendering a broken one. */
export function adaptUpcomingRow(raw: RawReminderDefinition): UpcomingReminderRow | null {
  if (!raw.next_fire_at) return null;
  const cadence = adaptCadence(raw.schedule_kind, raw.cadence, raw.schedule_timezone);
  if (!cadence) return null;
  return {
    id: raw.id,
    title: raw.title,
    cadence,
    anchor: adaptAnchor(raw.anchor),
    nextFireAt: raw.next_fire_at,
  };
}

/** Adapts one History-section occurrence row. Returns `null` for a malformed row (missing required field, an unrecognized `schedule_kind`, or an unrecognized `definition_status`) rather than rendering a broken one. */
export function adaptFiredRow(raw: RawReminderOccurrence): FiredReminderRow | null {
  if (!raw.fired_at) return null;
  const cadence = adaptCadence(raw.schedule_kind, raw.cadence, raw.schedule_timezone);
  if (!cadence) return null;
  if (!isKnownDefinitionStatus(raw.definition_status)) return null;
  return {
    id: raw.id,
    title: raw.title,
    cadence,
    anchor: adaptAnchor(raw.anchor),
    firedAt: raw.fired_at,
    definitionStatus: raw.definition_status,
  };
}
