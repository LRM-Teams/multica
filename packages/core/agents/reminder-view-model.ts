export type ReminderCadence =
  | { kind: "one_shot" }
  | {
      kind: "recurring";
      family: "calendar" | "interval";
      description: string;
      timezone?: string;
    };

export type ReminderAnchor =
  | { available: true; kind: "channel" | "thread"; label: string; href: string }
  | { available: false };

export type ReminderOrigin = { kind: "agent" };

export interface ReminderRow {
  id: string;
  title: string;
  cadence: ReminderCadence;
  anchor: ReminderAnchor;
  origin: ReminderOrigin;
}

export interface ReminderDefinitionRow extends ReminderRow {
  nextFireAt: string;
  lastFireAt?: string;
  status: "scheduled" | "firing";
}

export interface AgentReminderAnchorResponse {
  available: boolean;
  kind?: string;
  displayName?: string;
  href?: string;
}

export interface AgentReminderDefinitionResponse {
  id: string;
  title: string;
  status: string;
  scheduleKind: string;
  nextFireAt?: string;
  lastFireAt?: string;
  cadence?: string;
  scheduleTimezone?: string;
  snoozeCount: number;
  anchor: AgentReminderAnchorResponse;
}

export interface AgentReminderListResponse {
  definitions: AgentReminderDefinitionResponse[];
}

export function isBareWorkspaceShortIdLabel(label: string): boolean {
  return /^#[^\s#:]+:[0-9a-fA-F-]{4,36}$/.test(label.trim());
}

export function reminderAnchorLabel(raw: AgentReminderAnchorResponse): string | undefined {
  const label = (raw.displayName || "").trim();
  return label || undefined;
}

function adaptAnchor(raw: AgentReminderAnchorResponse): ReminderAnchor {
  const kind = raw.kind;
  const label = reminderAnchorLabel(raw);
  if (
    raw.available &&
    (kind === "channel" || kind === "thread") &&
    label &&
    raw.href &&
    !isBareWorkspaceShortIdLabel(label)
  ) {
    return { available: true, kind, label, href: raw.href };
  }
  return { available: false };
}

function adaptCadence(
  scheduleKind: string,
  cadence: string | undefined,
  scheduleTimezone: string | undefined,
): ReminderCadence | null {
  if (scheduleKind === "one_shot") return { kind: "one_shot" };
  if (scheduleKind !== "recurring" || !cadence) return null;
  return {
    kind: "recurring",
    family: scheduleTimezone ? "calendar" : "interval",
    description: cadence,
    timezone: scheduleTimezone,
  };
}

export function toUpcomingReminderRow(
  raw: AgentReminderDefinitionResponse,
): ReminderDefinitionRow | null {
  const cadence = adaptCadence(raw.scheduleKind, raw.cadence, raw.scheduleTimezone);
  if (!cadence || !raw.nextFireAt) return null;
  if (raw.status !== "scheduled" && raw.status !== "firing") return null;
  return {
    id: raw.id,
    title: raw.title,
    cadence,
    anchor: adaptAnchor(raw.anchor),
    origin: { kind: "agent" },
    nextFireAt: raw.nextFireAt,
    lastFireAt: raw.lastFireAt,
    status: raw.status,
  };
}
