import type { ResearchGraphEdgeType, ResearchGraphNodeType } from "@multica/core/types";

/**
 * LRM-798 — node/edge palette via semantic tokens only.
 * Forbidden: hardcoded hex, Tailwind palette-*-500 (sky/emerald/amber/…).
 * Allowed: brand / primary / success / warning / destructive / muted-foreground.
 */

export type NodeVisual = {
  ringClass: string;
  accentBarClass: string;
  labelTone: "default" | "success" | "warning" | "danger" | "info";
};

const NODE_VISUALS: Record<string, NodeVisual> = {
  goal: {
    ringClass: "ring-2 ring-primary/50",
    accentBarClass: "bg-primary",
    labelTone: "default",
  },
  subquestion: {
    ringClass: "ring-1 ring-primary/30",
    accentBarClass: "bg-primary/70",
    labelTone: "default",
  },
  probe: {
    ringClass: "ring-1 ring-brand/50",
    accentBarClass: "bg-brand",
    labelTone: "info",
  },
  finding: {
    ringClass: "ring-1 ring-success/50",
    accentBarClass: "bg-success",
    labelTone: "success",
  },
  conflict: {
    ringClass: "ring-1 ring-warning/55",
    accentBarClass: "bg-warning",
    labelTone: "warning",
  },
  dead_end: {
    ringClass: "ring-1 ring-destructive/45",
    accentBarClass: "bg-destructive/80",
    labelTone: "danger",
  },
  refuted: {
    ringClass: "ring-1 ring-destructive/55",
    accentBarClass: "bg-destructive",
    labelTone: "danger",
  },
  pivot: {
    ringClass: "ring-1 ring-warning/55",
    accentBarClass: "bg-warning",
    labelTone: "warning",
  },
  stage_gate: {
    ringClass: "ring-1 ring-primary/40",
    accentBarClass: "bg-primary/80",
    labelTone: "default",
  },
  product_round_gate: {
    ringClass: "ring-1 ring-brand/45",
    accentBarClass: "bg-brand",
    labelTone: "info",
  },
  roster_change: {
    ringClass: "ring-1 ring-muted-foreground/35",
    accentBarClass: "bg-muted-foreground/60",
    labelTone: "default",
  },
  agent_activity: {
    ringClass: "ring-1 ring-brand/50",
    accentBarClass: "bg-brand",
    labelTone: "info",
  },
};

const DEFAULT_VISUAL: NodeVisual = {
  ringClass: "ring-1 ring-border",
  accentBarClass: "bg-muted-foreground/50",
  labelTone: "default",
};

export function visualForNodeType(nodeType: ResearchGraphNodeType | string): NodeVisual {
  return NODE_VISUALS[nodeType] ?? DEFAULT_VISUAL;
}

export type EdgeVisual = {
  stroke: string;
  strokeDasharray?: string;
  animated: boolean;
};

export function visualForEdgeType(edgeType: ResearchGraphEdgeType | string): EdgeVisual {
  switch (edgeType) {
    case "supports":
      return { stroke: "var(--success)", animated: false };
    case "contradicts":
      return { stroke: "var(--destructive)", strokeDasharray: "6 4", animated: false };
    case "supersedes":
      return { stroke: "var(--warning)", strokeDasharray: "5 5", animated: false };
    case "abandons":
      return {
        stroke: "color-mix(in oklch, var(--destructive) 70%, var(--muted-foreground))",
        strokeDasharray: "2 6",
        animated: false,
      };
    case "leads_to":
    default:
      return { stroke: "var(--muted-foreground)", animated: true };
  }
}

export function shouldPulseNode(status: string, nodeType: string): boolean {
  return status === "active" && nodeType !== "goal" && nodeType !== "stage_gate";
}

/** Pulse when the node is in-flight or its actor has live compact activity. */
export function nodeIsVisuallyBusy(
  status: string,
  nodeType: string,
  actorHasActivity: boolean,
): boolean {
  // LRM-775: presence / compact activity =「谁在工作」— pulse related nodes
  // unless the node is already terminal (done / abandoned / failed).
  if (actorHasActivity) {
    const key = normalizeNodeStatusKey(status);
    if (
      key !== "done" &&
      key !== "completed" &&
      key !== "resolved" &&
      key !== "abandoned" &&
      key !== "failed"
    ) {
      return true;
    }
  }
  return shouldPulseNode(status, nodeType);
}

/** Canonical graph-node status keys for i18n (`node.status.*`). */
export type NodeStatusKey =
  | "active"
  | "done"
  | "running"
  | "waiting"
  | "abandoned"
  | "failed"
  | "completed"
  | "resolved"
  | "pending"
  | "unknown";

export function normalizeNodeStatusKey(status: string): NodeStatusKey {
  const key = (status || "").toLowerCase().trim();
  switch (key) {
    case "active":
    case "done":
    case "running":
    case "waiting":
    case "abandoned":
    case "failed":
    case "completed":
    case "resolved":
    case "pending":
      return key;
    default:
      return "unknown";
  }
}
