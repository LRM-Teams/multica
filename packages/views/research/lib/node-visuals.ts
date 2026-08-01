import type {
  ResearchGraphEdgeType,
  ResearchGraphNode,
  ResearchGraphNodeType,
} from "@multica/core/types";

/**
 * LRM-798 / LRM-972 — node/edge palette via semantic tokens (LRM-793 lock).
 * Forbidden: hardcoded hex, Tailwind palette-*-500 (sky/emerald/amber/…).
 * Allowed: brand / primary / success / warning / destructive / muted-foreground.
 */

export type NodeVisual = {
  ringClass: string;
  accentBarClass: string;
  labelTone: "default" | "success" | "warning" | "danger" | "info";
  /** Extra shell classes (opacity, dashed border, muted wash). */
  shellClass?: string;
  /** Title text modifiers (e.g. strikethrough for refuted). */
  titleClass?: string;
  /** Prefer type label over lane chip for scannable semantics. */
  emphasizeType?: boolean;
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
    emphasizeType: true,
  },
  finding: {
    ringClass: "ring-1 ring-success/50",
    accentBarClass: "bg-success",
    labelTone: "success",
    emphasizeType: true,
  },
  conflict: {
    ringClass: "ring-1 ring-warning/55",
    accentBarClass: "bg-warning",
    labelTone: "warning",
    emphasizeType: true,
  },
  // LRM-793: muted + dashed + 72% — not destructive red.
  dead_end: {
    ringClass: "ring-1 ring-muted-foreground/35",
    accentBarClass: "bg-muted-foreground/55",
    labelTone: "default",
    shellClass: "border-dashed bg-muted/90 opacity-[.72]",
    emphasizeType: true,
  },
  // LRM-793: muted 55% + strikethrough title.
  refuted: {
    ringClass: "ring-1 ring-muted-foreground/40",
    accentBarClass: "bg-muted-foreground/50",
    labelTone: "danger",
    shellClass: "bg-muted/80 opacity-55",
    titleClass: "line-through decoration-destructive",
    emphasizeType: true,
  },
  pivot: {
    ringClass: "ring-1 ring-brand/45",
    accentBarClass: "bg-brand",
    labelTone: "info",
    emphasizeType: true,
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
  strokeOpacity?: number;
  strokeWidth?: number;
  animated: boolean;
  /** Semantic role for canvas styling (LRM-793 / LRM-972). */
  role: "main" | "solid" | "dashed" | "recessed" | "active" | "source";
};

export function visualForEdgeType(edgeType: ResearchGraphEdgeType | string): EdgeVisual {
  switch (edgeType) {
    case "supports":
      return {
        stroke: "var(--success)",
        strokeOpacity: 0.55,
        strokeWidth: 2,
        animated: false,
        role: "source",
      };
    case "contradicts":
      return {
        stroke: "var(--destructive)",
        strokeDasharray: "6 5",
        animated: false,
        role: "dashed",
      };
    case "supersedes":
      return {
        stroke: "var(--warning)",
        strokeDasharray: "5 5",
        animated: false,
        role: "dashed",
      };
    // LRM-793 弯路：稀疏点线 40% · dasharray 2 4
    case "abandons":
      return {
        stroke: "var(--muted-foreground)",
        strokeDasharray: "2 4",
        strokeOpacity: 0.4,
        strokeWidth: 1.5,
        animated: false,
        role: "recessed",
      };
    case "leads_to":
      return {
        stroke: "var(--brand)",
        strokeWidth: 2.5,
        animated: true,
        role: "active",
      };
    default:
      return {
        stroke: "var(--muted-foreground)",
        animated: true,
        role: "main",
      };
  }
}

/** Edges into dead_end / refuted are historical detours (never deleted). */
export function isDetourTargetNodeType(nodeType: string | undefined): boolean {
  return nodeType === "dead_end" || nodeType === "refuted";
}

export function edgeVisualForConnection(
  edgeType: ResearchGraphEdgeType | string,
  toNodeType?: string,
): EdgeVisual {
  if (isDetourTargetNodeType(toNodeType) && edgeType !== "contradicts") {
    return visualForEdgeType("abandons");
  }
  return visualForEdgeType(edgeType);
}

/** Normalize 0–1 or 0–100 confidence into a 0–100 percentage. */
export function confidencePercent(confidence: number | null | undefined): number | null {
  if (confidence == null || Number.isNaN(Number(confidence))) return null;
  const n = Number(confidence);
  return n <= 1 ? n * 100 : n;
}

/** LRM-793: low confidence is a modifier (dashed + text), not a color alone. */
export function isLowConfidence(confidence: number | null | undefined): boolean {
  const pct = confidencePercent(confidence);
  return pct != null && pct < 50;
}

export function nodeConfidence(node: Pick<ResearchGraphNode, "confidence" | "payload">): number | null {
  if (typeof node.confidence === "number") return node.confidence;
  const payload = node.payload;
  if (payload && typeof payload === "object" && !Array.isArray(payload)) {
    const raw = (payload as Record<string, unknown>).confidence
      ?? (payload as Record<string, unknown>).confidence_score;
    if (typeof raw === "number") return raw;
  }
  return null;
}

export function shouldPulseNode(status: string, nodeType: string): boolean {
  if (nodeType === "dead_end" || nodeType === "refuted") return false;
  return status === "active" && nodeType !== "goal" && nodeType !== "stage_gate";
}

/** Pulse when the node is in-flight or its actor has live compact activity. */
export function nodeIsVisuallyBusy(
  status: string,
  nodeType: string,
  actorHasActivity: boolean,
): boolean {
  if (nodeType === "dead_end" || nodeType === "refuted") return false;
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
