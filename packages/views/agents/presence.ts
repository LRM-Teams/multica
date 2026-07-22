import {
  Archive,
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

// Visual mapping for the two presence dimensions, kept in matching shape
// so consumers can pick which to render. The two are independent — the
// dot reads only from availabilityConfig, the workload chip reads only
// from workloadConfig.
//
// Slack-style palette (2026-07 reduction): only ONLINE keeps an accent (success
// green). Every other presence state is neutral gray and is distinguished by its
// ICON / word / motion, never by a second colour — amber and blue are retired
// here so the only surviving accents workspace-wide are mention=blue,
// error=red, online=green.
//
//   AVAILABILITY (drives the dot everywhere a dot appears):
//     online    → success         (green)
//     unstable  → muted-foreground (gray) — distinguished by the PlugZap icon
//     offline   → muted-foreground (gray)
//
//   WORKLOAD (drives the optional workload chip on focused surfaces):
//     working   → muted-foreground (gray) — "actively running" reads via the
//                                          avatar pulse + the spinning Loader2
//                                          glyph, not a blue colour
//     queued    → muted-foreground (gray) anomaly: nothing running but tasks
//                                          waiting (Clock glyph carries it)
//     idle      → muted           (gray)  nothing on the plate
//
// `failed` / `completed` / `cancelled` deliberately have no top-level visual
// — those are historical context, surfaced via Recent Work + Inbox, not
// list-level summary state.

export interface AvailabilityVisual {
  label: string;
  // Background fill for the dot indicator.
  dotClass: string;
  // Foreground colour for the label text alongside the dot.
  textClass: string;
  // Icon used in larger badge contexts (detail header, hover card).
  icon: LucideIcon;
}

export const availabilityConfig: Record<AgentAvailability, AvailabilityVisual> = {
  online: {
    label: "Online",
    dotClass: "bg-success",
    textClass: "text-success",
    icon: CircleDot,
  },
  // Raw `unstable` still exists in the derivation model, but live UI maps it
  // through `toLivePresence` → Online (green). Keep visual parity with online
  // so any leftover direct lookup cannot paint "Unstable" chrome (LRM-248).
  unstable: {
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
  // Lifecycle — not a third live presence. Avatar goes grayscale with NO live
  // dot; detail may show a muted "Archived" subline (LRM-248).
  archived: {
    label: "Archived",
    dotClass: "bg-muted-foreground/40",
    textClass: "text-muted-foreground",
    icon: Archive,
  },
};

// Order used by availability filter chips (LRM-248: Online / Offline only).
export const availabilityOrder: AgentAvailability[] = ["online", "offline"];

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
    // Neutral: "actively working" reads via the avatar pulse + the spinning
    // Loader2 glyph, not a blue colour (Slack-style reduction).
    textClass: "text-muted-foreground",
    icon: Loader2,
  },
  queued: {
    // Neutral chip: nothing running but tasks waiting. On an offline runtime
    // this is the "stuck" signal we explicitly surface (replacing the old
    // misleading "Running 0/N +Mq" copy); the Clock glyph carries it now that
    // amber is retired.
    label: "Queued",
    textClass: "text-muted-foreground",
    icon: Clock,
  },
  idle: {
    // Calm, neutral glyph — "available, nothing on the plate". The old
    // AlertCircle read like a warning for a benign resting state. Only shown
    // while online (offline surfaces the availability word instead).
    label: "Idle",
    textClass: "text-muted-foreground",
    icon: Circle,
  },
};

// Order used in any future workload chip group; actionable signals first.
export const workloadOrder: Workload[] = ["working", "queued", "idle"];

// LRM-248: live presence is a two-state word (Online / Offline). Backend may
// still emit `unstable` / workload; those fold here so every card/pill/dot
// agrees. Activity timeline event rows keep their own non-live vocabulary.
export type LivePresence = "online" | "offline" | "archived";

/** Map raw availability → user-visible live presence (LRM-248). */
export function toLivePresence(availability: AgentAvailability): LivePresence {
  if (availability === "archived") return "archived";
  if (availability === "offline") return "offline";
  // `online` and `unstable` (and any reconnecting-equivalent) → Online.
  return "online";
}

export type PresenceStatusToken =
  | { kind: "workload"; value: Workload }
  | { kind: "availability"; value: AgentAvailability };

export function presenceStatusToken(
  presence: AgentPresenceDetail | "loading" | null | undefined,
): PresenceStatusToken | null {
  if (!presence || presence === "loading") return null;
  const live = toLivePresence(presence.availability);
  // Live status never surfaces workload (Working / Queued / Idle) or Unstable.
  if (live === "archived") {
    return { kind: "availability", value: "archived" };
  }
  return {
    kind: "availability",
    value: live === "online" ? "online" : "offline",
  };
}

/**
 * One-line localized status word for cards/pills/hover profiles.
 *
 * Token rule stays in `presenceStatusToken` (#288); this is just the shared
 * token → copy step so every surface doesn't re-implement the i18n branch.
 * Returns null while presence is loading/unknown.
 *
 * `t` is the agents-namespace translator (`useT("agents").t`).
 */
export function formatPresenceStatus(
  presence: AgentPresenceDetail | "loading" | null | undefined,
  t: TFunction<"agents">,
): string | null {
  const token = presenceStatusToken(presence);
  if (!token) return null;
  return token.kind === "workload"
    ? t(($) => $.workload[token.value])
    : t(($) => $.availability[token.value]);
}

/** Visual config (icon / textClass / optional dot) for the same status token. */
export function presenceStatusVisual(
  presence: AgentPresenceDetail | "loading" | null | undefined,
): AvailabilityVisual | WorkloadVisual | null {
  const token = presenceStatusToken(presence);
  if (!token) return null;
  return token.kind === "workload"
    ? workloadConfig[token.value]
    : availabilityConfig[token.value];
}

/**
 * Compact status-dot fill for name-row / timeline chips.
 *
 * Availability reuses `availabilityConfig.dotClass` so the word matches the
 * avatar presence dot. Workload has no list-level dot palette — every workload
 * dot is neutral gray now (working reads via the avatar pulse, not a colour).
 */
export function presenceStatusDotClass(
  presence: AgentPresenceDetail | "loading" | null | undefined,
): string | null {
  const token = presenceStatusToken(presence);
  if (!token) return null;
  if (token.kind === "availability") {
    return availabilityConfig[token.value].dotClass;
  }
  switch (token.value) {
    case "working":
    case "queued":
    case "idle":
      // All neutral now: working reads via the avatar pulse, queued via its
      // glyph — no workload state carries a dot colour (Slack-style reduction).
      return "bg-muted-foreground/40";
  }
}
