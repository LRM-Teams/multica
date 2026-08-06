/**
 * Research V6 — card family visual tokens (UI-01 / LRM-1475).
 *
 * Six card families each carry ONE semantic hue + icon + type-badge strategy.
 * Per LRM-1469 §6 / LRM-793: NO hardcoded hex, NO palette-*-500. Only the
 * canonical semantic tokens already used by the research canvas:
 *   brand / primary / success / warning / destructive / muted-foreground.
 *
 * Color is never the only encoding — every family also renders its icon and a
 * text type badge, so the family stays legible for colour-blind users and at
 * 40%–160% zoom (LRM-1469 编码语义铁律).
 */

import type { LucideIcon } from "lucide-react";
import {
  FileSearch,
  GitBranch,
  Lightbulb,
  Play,
  Shield,
  Users,
} from "lucide-react";
import type { NodeKindFamily } from "./node-kind-registry";

export interface NodeFamilyVisual {
  family: NodeKindFamily;
  /** Top accent bar + ring color (semantic token only). */
  accentBarClass: string;
  ringClass: string;
  /** Badge text tone (semantic token only). */
  badgeTextClass: string;
  /** Icon fill/text tone. */
  iconClass: string;
  /** Lucide icon for the family badge. */
  icon: LucideIcon;
}

const FAMILY_VISUALS: Record<NodeKindFamily, NodeFamilyVisual> = {
  // F1 规划/结构 — primary structural base
  structure: {
    family: "structure",
    accentBarClass: "bg-primary",
    ringClass: "ring-1 ring-primary/40",
    badgeTextClass: "text-primary",
    iconClass: "text-primary",
    icon: GitBranch,
  },
  // F2 执行 — brand (vivid blue, the canvas execution/info tone)
  execution: {
    family: "execution",
    accentBarClass: "bg-brand",
    ringClass: "ring-1 ring-brand/50",
    badgeTextClass: "text-brand",
    iconClass: "text-brand",
    icon: Play,
  },
  // F3 证据 — success (green)
  evidence: {
    family: "evidence",
    accentBarClass: "bg-success",
    ringClass: "ring-1 ring-success/50",
    badgeTextClass: "text-success",
    iconClass: "text-success",
    icon: FileSearch,
  },
  // F4 认知/结论 — warning (amber, judgment/insight)
  cognition: {
    family: "cognition",
    accentBarClass: "bg-warning",
    ringClass: "ring-1 ring-warning/55",
    badgeTextClass: "text-warning",
    iconClass: "text-warning",
    icon: Lightbulb,
  },
  // F5 协作 — neutral (calm "people" tone; no cyan token → muted-foreground)
  collaboration: {
    family: "collaboration",
    accentBarClass: "bg-muted-foreground",
    ringClass: "ring-1 ring-muted-foreground/35",
    badgeTextClass: "text-muted-foreground",
    iconClass: "text-muted-foreground",
    icon: Users,
  },
  // F6 治理/时限 — destructive (hard limit / governance)
  governance: {
    family: "governance",
    accentBarClass: "bg-destructive",
    ringClass: "ring-1 ring-destructive/50",
    badgeTextClass: "text-destructive",
    iconClass: "text-destructive",
    icon: Shield,
  },
  // Generic / unknown — neutral, muted, never crashes
  generic: {
    family: "generic",
    accentBarClass: "bg-muted-foreground/50",
    ringClass: "ring-1 ring-border",
    badgeTextClass: "text-muted-foreground",
    iconClass: "text-muted-foreground",
    icon: FileSearch as LucideIcon,
  },
};

const DEFAULT_FAMILY_VISUAL: NodeFamilyVisual = FAMILY_VISUALS.generic;

/** Resolve the visual pair for a family; unknown families degrade to generic. */
export function familyVisualFor(family: NodeKindFamily): NodeFamilyVisual {
  return FAMILY_VISUALS[family] ?? DEFAULT_FAMILY_VISUAL;
}

/** All family visuals, ordered (for demo grids / tests). */
export const ALL_FAMILY_VISUALS: readonly NodeFamilyVisual[] = (
  [
    "structure",
    "execution",
    "evidence",
    "cognition",
    "collaboration",
    "governance",
    "generic",
  ] as const
).map((f) => FAMILY_VISUALS[f]);
