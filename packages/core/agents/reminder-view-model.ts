/**
 * View-model the Agent Card Reminders tab renders against — deliberately
 * decoupled from the raw wire shape task #655's read API returns. `adaptRow`
 * is the one place that translates raw server fields into this type; when
 * #655's real field names land, only that function (and `RawReminderRow`)
 * need to change, not the component.
 *
 * Endpoint contract this currently targets (per the V2 product spec,
 * `docs/superpowers/specs/2026-07-22-raft-reminder-parity.md`):
 *   GET /api/agents/{agentId}/reminders?status=scheduled|fired&cursor=&limit=
 * "scheduled" -> Upcoming tab section, ordered by next_fire_at.
 * "fired" -> History tab section, ordered newest-first, cursor-paginated.
 */

export type ReminderCadence =
  | { kind: "one_shot" }
  // "calendar" = `daily@HH:MM` / `weekly:<days>@HH:MM` — resolves against a
  // real IANA timezone, locked at schedule time (see `ReminderRow.timezone`).
  // "interval" = `every:<N>m|h|d` — an elapsed-time rule with no calendar
  // zone at all (per the V2 spec: "`every:*` is elapsed interval, not
  // participate in calendar zone"). This distinction, not a blanket
  // one-shot-vs-recurring split, is what decides whether the timezone tag
  // renders — showing a timezone next to an interval cadence would imply a
  // calendar meaning it doesn't have.
  | { kind: "recurring"; family: "calendar" | "interval"; description: string };

export type ReminderAnchor =
  | { available: true; label: string; url: string }
  | { available: false };

export interface ReminderRow {
  id: string;
  title: string;
  cadence: ReminderCadence;
  /** IANA zone the schedule was authored in — locked at schedule time, never the viewer's current zone. */
  timezone: string;
  anchor: ReminderAnchor;
}

export interface UpcomingReminderRow extends ReminderRow {
  nextFireAt: string;
}

export interface FiredReminderRow extends ReminderRow {
  firedAt: string;
  /**
   * The PARENT DEFINITION's own current state — independent of this specific
   * fire occurrence. A recurring reminder stays `scheduled` after firing
   * (it'll fire again); only a one-shot's single fire makes the definition
   * itself terminal. History renders occurrences, never the definition's
   * state — but must not IMPLY a still-active recurring definition is done
   * just because one row shows a past fire. See the product spec note:
   * "History 的 row 是 occurrence, 不要把 recurring definition 因一次 fired
   * 错渲成 terminal fired."
   */
  definitionState: "scheduled" | "fired" | "cancelled";
}

/** Raw row shape this adapter currently expects — a placeholder contract pending task #655's real field names; keep in lockstep with the endpoint, not speculatively ahead of it. */
export interface RawReminderRow {
  id: string;
  title: string;
  /** The occurrence-level status this row represents (what section it belongs to), NOT the parent definition's own state — see `definition_state`. */
  status: "scheduled" | "fired";
  /** The parent reminder DEFINITION's own current state, independent of `status` above — a recurring definition stays "scheduled" even for rows describing a past fired occurrence. */
  definition_state: "scheduled" | "fired" | "cancelled";
  recurrence: string | null; // null => one-shot; non-null => human-readable cadence description
  timezone: string;
  next_fire_at: string | null;
  fired_at: string | null;
  anchor_available: boolean;
  anchor_label: string | null;
  anchor_url: string | null;
}

export interface RawReminderPage {
  reminders: RawReminderRow[];
  next_cursor: string | null;
}

function adaptAnchor(raw: RawReminderRow): ReminderAnchor {
  if (raw.anchor_available && raw.anchor_label && raw.anchor_url) {
    return { available: true, label: raw.anchor_label, url: raw.anchor_url };
  }
  return { available: false };
}

// Matches the canonical Raft-parity grammar (V2 spec): `daily@HH:MM` and
// `weekly:<weekday[,weekday...]>@HH:MM` are calendar rules; `every:<N>m|h|d`
// is a zone-free elapsed interval. Anything else is treated as calendar
// (the conservative choice: showing a timezone tag that turns out to be
// irrelevant is a smaller honesty violation than hiding one that mattered).
function cadenceFamily(recurrence: string): "calendar" | "interval" {
  return recurrence.startsWith("every:") ? "interval" : "calendar";
}

function adaptCadence(raw: RawReminderRow): ReminderCadence {
  return raw.recurrence
    ? { kind: "recurring", family: cadenceFamily(raw.recurrence), description: raw.recurrence }
    : { kind: "one_shot" };
}

/** Adapts one "scheduled"-status raw row for the Upcoming section. Returns `null` for a malformed row (missing the field Upcoming depends on) rather than rendering a broken one. */
export function adaptUpcomingRow(raw: RawReminderRow): UpcomingReminderRow | null {
  if (!raw.next_fire_at) return null;
  return {
    id: raw.id,
    title: raw.title,
    cadence: adaptCadence(raw),
    timezone: raw.timezone,
    anchor: adaptAnchor(raw),
    nextFireAt: raw.next_fire_at,
  };
}

/** Adapts one "fired"-status raw row for the History section. Returns `null` for a malformed row rather than rendering a broken one. */
export function adaptFiredRow(raw: RawReminderRow): FiredReminderRow | null {
  if (!raw.fired_at) return null;
  return {
    id: raw.id,
    title: raw.title,
    cadence: adaptCadence(raw),
    timezone: raw.timezone,
    anchor: adaptAnchor(raw),
    firedAt: raw.fired_at,
    definitionState: raw.definition_state,
  };
}
