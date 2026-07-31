import type { ResearchGraphEdgeType, ResearchGraphNodeType } from "@multica/core/types";

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
    ringClass: "ring-1 ring-sky-500/50",
    accentBarClass: "bg-sky-500",
    labelTone: "info",
  },
  finding: {
    ringClass: "ring-1 ring-emerald-500/50",
    accentBarClass: "bg-emerald-500",
    labelTone: "success",
  },
  conflict: {
    ringClass: "ring-1 ring-amber-500/55",
    accentBarClass: "bg-amber-500",
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
    ringClass: "ring-1 ring-orange-500/55",
    accentBarClass: "bg-orange-500",
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
    ringClass: "ring-1 ring-teal-500/50",
    accentBarClass: "bg-teal-500",
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
      return { stroke: "#10b981", animated: false };
    case "contradicts":
      return { stroke: "#ef4444", strokeDasharray: "6 4", animated: false };
    case "supersedes":
      return { stroke: "#f97316", strokeDasharray: "5 5", animated: false };
    case "abandons":
      return { stroke: "#f87171", strokeDasharray: "2 6", animated: false };
    case "leads_to":
    default:
      return { stroke: "hsl(var(--muted-foreground))", animated: true };
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
  if (actorHasActivity && status === "active") return true;
  return shouldPulseNode(status, nodeType);
}
