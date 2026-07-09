import {
  Archive,
  Circle,
  CircleDot,
  CircleSlash,
  Clock,
  Loader2,
  PlugZap,
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
// Color tokens map to project semantic tokens (no hardcoded Tailwind colors):
//
//   AVAILABILITY (drives the dot everywhere a dot appears):
//     online    → success         (green)
//     unstable  → warning         (amber) — pairs with the runtime card's amber
//     offline   → muted-foreground (gray)
//
//   WORKLOAD (drives the optional workload chip on focused surfaces):
//     working   → brand           (blue)  has activity
//     queued    → warning         (amber) anomaly: nothing running but tasks
//                                          waiting (typically stuck on offline
//                                          runtime; brief flash on online is
//                                          a harmless race)
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
  unstable: {
    label: "Unstable",
    dotClass: "bg-warning",
    textClass: "text-warning",
    icon: PlugZap,
  },
  offline: {
    label: "Offline",
    dotClass: "bg-muted-foreground/40",
    textClass: "text-muted-foreground",
    icon: CircleSlash,
  },
  // Lifecycle state, not a runtime state — a retired agent. Gray like
  // offline (it can't take work) but labelled distinctly so the user reads
  // "this agent is archived", not "temporarily unreachable".
  archived: {
    label: "Archived",
    dotClass: "bg-muted-foreground/40",
    textClass: "text-muted-foreground",
    icon: Archive,
  },
};

// Order used by availability filter chips so colours read in a natural
// progression rather than alphabetical.
export const availabilityOrder: AgentAvailability[] = [
  "online",
  "unstable",
  "offline",
];

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
    textClass: "text-brand",
    icon: Loader2,
  },
  queued: {
    // Amber chip: nothing running but tasks waiting. On an offline runtime
    // this is the "stuck" signal we explicitly surface (replacing the old
    // misleading "Running 0/N +Mq" copy).
    label: "Queued",
    textClass: "text-warning",
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

// The single rule for an agent's one-line status *word* on cards/pills: it must
// agree with the availability dot. A workload word ("Idle"/"Working") only
// while online; otherwise the availability word ("Offline"/"Unstable"/
// "Archived"). Returns a discriminated token so every surface localizes (and
// picks the matching config) from ONE shared decision — never from a raw status
// string or its own drifting copy of this rule. This is why the presence dot
// and the pill can never disagree again (#288).
export type PresenceStatusToken =
  | { kind: "workload"; value: Workload }
  | { kind: "availability"; value: AgentAvailability };

export function presenceStatusToken(
  presence: AgentPresenceDetail | "loading" | null | undefined,
): PresenceStatusToken | null {
  if (!presence || presence === "loading") return null;
  return presence.availability === "online"
    ? { kind: "workload", value: presence.workload }
    : { kind: "availability", value: presence.availability };
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
