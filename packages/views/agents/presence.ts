import {
  Circle,
  CircleDot,
  CircleSlash,
  Clock,
  Loader2,
  type LucideIcon,
} from "lucide-react";
import type { TFunction } from "i18next";
import type {
  AgentAvailability,
  AgentPresenceDetail,
  Workload,
} from "@multica/core/agents";

// LRM-248 — live presence is Online / Offline only.
//
// Runtime diagnostic grace states and entity lifecycle are kept outside this
// live presence vocabulary.
//
// Workload words (Working / Queued / Idle) are never live presence labels.
// Activity timeline history rows keep their own non-live event copy.

export interface AvailabilityVisual {
  label: string;
  // Background fill for the dot indicator.
  dotClass: string;
  // Foreground colour for the label text alongside the dot.
  textClass: string;
  // Icon used in larger badge contexts (detail header, hover card).
  icon: LucideIcon;
}

/** Live user-facing availability — two states only (LRM-248). */
export type LiveAvailability = "online" | "offline";

/**
 * Normalize optional availability into the live Online/Offline axis.
 */
export function toLiveAvailability(
  availability: AgentAvailability | null | undefined,
): LiveAvailability | null {
  if (!availability) return null;
  return availability;
}

export const availabilityConfig: Record<AgentAvailability, AvailabilityVisual> = {
  online: {
    label: "Online",
    dotClass: "bg-success",
    textClass: "text-success",
    icon: CircleDot,
  },
  offline: {
    label: "Offline",
    dotClass: "bg-muted-foreground/40",
    textClass: "text-muted-foreground",
    icon: CircleSlash,
  },
};

// Filter chips: Online / Offline only.
export const availabilityOrder: LiveAvailability[] = ["online", "offline"];

export interface WorkloadVisual {
  label: string;
  // Foreground colour for icon + label text.
  textClass: string;
  // Icon used inline.
  icon: LucideIcon;
}

export const workloadConfig: Record<Workload, WorkloadVisual> = {
  working: {
    label: "Working",
    textClass: "text-muted-foreground",
    icon: Loader2,
  },
  queued: {
    label: "Queued",
    textClass: "text-muted-foreground",
    icon: Clock,
  },
  idle: {
    label: "Idle",
    textClass: "text-muted-foreground",
    icon: Circle,
  },
};

export const workloadOrder: Workload[] = ["working", "queued", "idle"];

/**
 * Live presence token — Online or Offline only (LRM-248).
 * Never returns workload words or Unstable/Archived as live presence.
 */
export type PresenceStatusToken = { kind: "availability"; value: LiveAvailability };

export function presenceStatusToken(
  presence: AgentPresenceDetail | "loading" | null | undefined,
): PresenceStatusToken | null {
  if (!presence || presence === "loading") return null;
  const live = toLiveAvailability(presence.availability);
  if (!live) return null;
  return { kind: "availability", value: live };
}

/**
 * One-line localized live status word: Online / Offline only.
 * Returns null while loading or unknown.
 */
export function formatPresenceStatus(
  presence: AgentPresenceDetail | "loading" | null | undefined,
  t: TFunction<"agents">,
): string | null {
  const token = presenceStatusToken(presence);
  if (!token) return null;
  return t(($) => $.availability[token.value]);
}

/** Visual config for the live Online/Offline token. */
export function presenceStatusVisual(
  presence: AgentPresenceDetail | "loading" | null | undefined,
): AvailabilityVisual | null {
  const token = presenceStatusToken(presence);
  if (!token) return null;
  return availabilityConfig[token.value];
}

/** Compact status-dot fill for live Online/Offline. */
export function presenceStatusDotClass(
  presence: AgentPresenceDetail | "loading" | null | undefined,
): string | null {
  const token = presenceStatusToken(presence);
  if (!token) return null;
  return availabilityConfig[token.value].dotClass;
}

/**
 * Match an agent against an Online/Offline filter chip.
 * Presence filters use the same binary availability contract.
 */
export function matchesLiveAvailabilityFilter(
  availability: AgentAvailability | null | undefined,
  filter: LiveAvailability,
): boolean {
  return toLiveAvailability(availability) === filter;
}
