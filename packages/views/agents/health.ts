import {
  CircleCheck,
  CircleDot,
  CircleSlash,
  PlugZap,
  RefreshCw,
  type LucideIcon,
} from "lucide-react";
import type { AgentHealthState, AgentHealthSummary } from "@multica/core/types";

// Visual mapping for the five runtime-connectivity health states (#178 /
// #266). The SINGLE source of truth for both surfaces that render health:
//   - the presence dot COLOR (actor-avatar.tsx) reads `dotClass`
//   - the Activity tab Health block reads `chipClass` / `icon`
// so the dot can never drift from the tab (Iris §1). Color tokens mirror
// availabilityConfig / the runtimes shared config (no hardcoded Tailwind
// colors):
//
//   online / recovered      → success  (green)  — link healthy
//   suspected_disconnect /
//   reconnecting            → warning  (amber)  — link wobbling
//   offline                 → muted    (gray)   — link down
//
// `labelKey` is the locale key under tab_body.activity.health.state_* — copy
// is derived ONLY from the HealthState, never from the internal BE event type
// codes (E5).
export interface HealthStateVisual {
  // Background fill for the dot indicator (matches availabilityConfig tokens).
  dotClass: string;
  // Soft chip background + foreground for the big state chip / timeline chips.
  chipClass: string;
  // Foreground-only color for inline text next to a bare dot.
  textClass: string;
  // Icon used inside the chip.
  icon: LucideIcon;
  // Locale key suffix — tab_body.activity.health.state_<labelKey>.
  labelKey: AgentHealthState;
}

export const healthStateConfig: Record<AgentHealthState, HealthStateVisual> = {
  online: {
    dotClass: "bg-success",
    chipClass: "bg-success/10 text-success-strong",
    textClass: "text-success",
    icon: CircleDot,
    labelKey: "online",
  },
  // A distinct green "just came back" state — kept in the timeline even after
  // the summary settles back to online (Iris §3c). Uses the success palette
  // (it IS healthy again) with a check icon to read as "recovered", not
  // "steady online".
  recovered: {
    dotClass: "bg-success",
    chipClass: "bg-success/10 text-success-strong",
    textClass: "text-success",
    icon: CircleCheck,
    labelKey: "recovered",
  },
  suspected_disconnect: {
    dotClass: "bg-warning",
    chipClass: "bg-warning/10 text-warning",
    textClass: "text-warning",
    icon: PlugZap,
    labelKey: "suspected_disconnect",
  },
  reconnecting: {
    dotClass: "bg-warning",
    chipClass: "bg-warning/10 text-warning",
    textClass: "text-warning",
    icon: RefreshCw,
    labelKey: "reconnecting",
  },
  offline: {
    dotClass: "bg-muted-foreground/40",
    chipClass: "bg-muted-foreground/10 text-muted-foreground",
    textClass: "text-muted-foreground",
    icon: CircleSlash,
    labelKey: "offline",
  },
};

// States we have explicitly decided render as Online green on the presence
// dot. `suspected_disconnect`/`reconnecting` are here on purpose (LRM-248:
// "not yet confirmed dead" reads as online, not offline) — this is a
// deliberate allowance, not a gap. Every other value, including any state
// the backend starts emitting before this list is updated for it (task #93:
// e.g. "restarting" from an active lifecycle operation), is NOT on this
// list and therefore falls to Offline gray below.
const GREEN_HEALTH_STATES = new Set<string>([
  "online",
  "recovered",
  "suspected_disconnect",
  "reconnecting",
]);

// Resolve the presence-dot COLOR class from the connectivity health summary.
// LRM-248: live badge is Online (green) or Offline (gray) only. Task #93:
// the fallback direction matters — an unrecognized state must default to
// Offline, not Online. The API response schema is loose (packages/core/api/schemas.ts),
// so a state string outside the FE's AgentHealthState union can reach here
// as a live runtime value despite the type claiming it can't; treating
// "not explicitly known to be online" as "confidently online" would be
// exactly the kind of invented-good-news the rest of this codebase avoids
// for missing/unknown facts.
// When the summary is unavailable, return `fallbackDotClass` (already folded
// through toLiveAvailability at the call site).
export function resolveHealthDotClass(
  summary: AgentHealthSummary | undefined,
  fallbackDotClass: string,
): string {
  if (!summary) return fallbackDotClass;
  if (GREEN_HEALTH_STATES.has(summary.state)) {
    return healthStateConfig.online.dotClass;
  }
  return healthStateConfig.offline.dotClass;
}

// Compact elapsed-duration formatter for the "在线 3h" / "疑似掉线 2m" head
// line. Locale-neutral units (m / h / d) so it needs no translation; the
// state word in front is localized by the caller. Pure — callers pass `now`
// for deterministic tests.
export function formatHealthDuration(sinceIso: string | null, now: number): string {
  // Barry's summary timestamps are nullable (runtime missing / no heartbeat /
  // no lifecycle event yet) — never fake a time; the caller hides the line.
  if (!sinceIso) return "";
  const since = new Date(sinceIso).getTime();
  if (Number.isNaN(since)) return "";
  const diffMs = Math.max(0, now - since);
  const minutes = Math.floor(diffMs / 60_000);
  if (minutes < 1) return "<1m";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

// "最后活跃 09:41" clock time. 24-hour HH:MM in the given locale's numbering.
// Pure wrapper over Intl so the Health block reads a single helper.
export function formatClockTime(iso: string | null, locale?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(locale, {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(d);
}
