/**
 * Research V6 — node card state matrix (UI-01 / LRM-1475).
 *
 * LRM-1469 §4: the eight visual states. All six families share this matrix;
 * state is encoded by border / accent bar / badge shape+text / wash, NEVER by
 * color alone. The conflicting-importance priority from the design is:
 *
 *   failed > running > stale > selected > default
 *
 * When several signals would demand conflicting emphasis the highest-priority
 * state wins so we never render two competing highlights on one card.
 */

/** The 8 visual states from LRM-1469 §4. */
export type NodeCardState =
  | "default"
  | "selected"
  | "loading"
  | "running"
  | "failed"
  | "stale"
  | "unknown"
  | "terminal";

export const NODE_CARD_STATES: readonly NodeCardState[] = [
  "default",
  "selected",
  "loading",
  "running",
  "failed",
  "stale",
  "unknown",
  "terminal",
] as const;

export interface NodeStateVisual {
  state: NodeCardState;
  /** Outer shell border classes (semantic only). */
  borderClass: string;
  /** Accent bar override (or null = family colour). */
  accentBarClass: string | null;
  /** Status badge label (display only). */
  label: string;
  /** Badge text tone. */
  badgeToneClass: string;
  /** Extra shell wash / animation. */
  shellClass?: string;
  /** Title decoration (e.g. strikethrough for superseded/stale). */
  titleClass?: string;
  /** Terminal corner check (done/accepted). */
  cornerCheck?: boolean;
  /** Renders a small status glyph dot (only for port/status, never on task body). */
  statusGlyph?: "spinner" | "pulse" | "failure" | "stale-dot" | "check" | "none";
  "aria-busy"?: boolean;
}

const STATE_VISUALS: Record<NodeCardState, NodeStateVisual> = {
  default: {
    state: "default",
    borderClass: "border-border",
    accentBarClass: null, // use family colour
    label: "default",
    badgeToneClass: "text-muted-foreground",
    statusGlyph: "none",
  },
  selected: {
    state: "selected",
    borderClass: "ring-2 ring-primary/60",
    accentBarClass: null,
    label: "selected",
    badgeToneClass: "text-primary",
    shellClass: "bg-card",
    statusGlyph: "none",
  },
  loading: {
    state: "loading",
    borderClass: "border-border",
    accentBarClass: "bg-primary/40",
    label: "loading",
    badgeToneClass: "text-muted-foreground",
    shellClass: "animate-pulse",
    statusGlyph: "spinner",
    "aria-busy": true,
  },
  running: {
    state: "running",
    borderClass: "border-brand/60",
    accentBarClass: "bg-brand",
    label: "running",
    badgeToneClass: "text-brand",
    shellClass: "bg-card",
    statusGlyph: "pulse",
    "aria-busy": true,
  },
  failed: {
    state: "failed",
    borderClass: "border-destructive/60",
    accentBarClass: "bg-destructive",
    label: "failed",
    badgeToneClass: "text-destructive",
    shellClass: "bg-card",
    statusGlyph: "failure",
  },
  stale: {
    state: "stale",
    borderClass: "border-muted-foreground/30 border-dashed",
    accentBarClass: "bg-muted-foreground/40",
    label: "stale",
    badgeToneClass: "text-muted-foreground",
    shellClass: "bg-muted/60",
    titleClass: "line-through decoration-muted-foreground/50",
    statusGlyph: "stale-dot",
  },
  unknown: {
    state: "unknown",
    borderClass: "border-border",
    accentBarClass: "bg-muted-foreground/50",
    label: "unknown",
    badgeToneClass: "text-muted-foreground",
    shellClass: "bg-muted/50",
    statusGlyph: "none",
  },
  terminal: {
    state: "terminal",
    borderClass: "border-border",
    accentBarClass: "bg-success/70",
    label: "terminal",
    badgeToneClass: "text-success",
    cornerCheck: true,
    statusGlyph: "check",
  },
};

/** Conflict priority — highest wins. */
const PRIORITY: Record<NodeCardState, number> = {
  failed: 6,
  running: 5,
  stale: 4,
  selected: 3,
  terminal: 2,
  loading: 2,
  unknown: 1,
  default: 0,
};

/**
 * Resolve a card state, applying LRM-1469 §4 conflict priority.
 * Pass the candidate signals; the highest-priority state is returned.
 */
export function resolveCardState(signals: NodeCardState[]): NodeCardState {
  const [first, ...rest] = signals;
  if (!first) return "default";
  return rest.reduce((acc, s) => (PRIORITY[s] > PRIORITY[acc] ? s : acc), first);
}

/** Visual pair for a resolved state. */
export function stateVisualFor(state: NodeCardState): NodeStateVisual {
  return STATE_VISUALS[state];
}
