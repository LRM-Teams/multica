/**
 * Research V6 — node kind → card family registry (UI-01 / LRM-1475).
 *
 * Bridges the canonical V6 registry (`@multica/core/research-v6/registry`) into
 * the 6-card-family visual grammar from LRM-1469. This module ONLY supplies
 * display grouping; it never writes grouping back to canonical research state
 * and never infers canonical facts from chat / animation / display state.
 *
 * Every `ResearchV6NodeKind` maps to exactly one family; unknown future kinds
 * degrade to the `generic` family so the page never crashes.
 */

import type {
  ResearchV6NodeKind,
  ResearchV6UnknownKindDiagnostic,
} from "@multica/core/types/research-v6";
import { classifyNodeKind } from "@multica/core/research-v6/registry";

/** The 6 visual card families from LRM-1469 §1. */
export type NodeKindFamily =
  | "structure"
  | "execution"
  | "evidence"
  | "cognition"
  | "collaboration"
  | "governance"
  /** Unknown future kinds land here (generic fallback). */
  | "generic";

export const NODE_KIND_FAMILIES: readonly NodeKindFamily[] = [
  "structure",
  "execution",
  "evidence",
  "cognition",
  "collaboration",
  "governance",
] as const;

/**
 * Family → human label (display only). Kept local to the render module so it
 * does not touch the shared i18n locale files owned by sibling issues.
 */
export const NODE_KIND_FAMILY_LABELS: Record<NodeKindFamily, string> = {
  structure: "规划/结构",
  execution: "执行",
  evidence: "证据",
  cognition: "认知/结论",
  collaboration: "协作",
  governance: "治理/时限",
  generic: "未知",
};

function k(kind: ResearchV6NodeKind): ResearchV6NodeKind {
  return kind;
}

/**
 * LRM-1469 §1 mapping: 30 node_kind → 6 families.
 * `episode` (run summary / report umbrella) is governed by F6 Governance —
 * see LRM-1469 §1 note about the report-revision variant.
 */
export const NODE_KIND_TO_FAMILY: ReadonlyMap<ResearchV6NodeKind, NodeKindFamily> = new Map<
  ResearchV6NodeKind,
  NodeKindFamily
>([
  // F1 规划/结构 Structure — primary/brand · GitBranch/Workflow
  [k("goal"), "structure"],
  [k("task"), "structure"],
  [k("search_plan"), "structure"],
  [k("branch"), "structure"],
  // F2 执行 Execution — brand · Play/Cpu
  [k("attempt"), "execution"],
  [k("query_execution"), "execution"],
  // F3 证据 Evidence — success · FileSearch/Link2
  [k("result_artifact"), "evidence"],
  [k("source_candidate"), "evidence"],
  [k("source_snapshot"), "evidence"],
  [k("observation"), "evidence"],
  [k("screening_decision"), "evidence"],
  // F4 认知/结论 Cognition — warning/amber · Lightbulb/Scale
  [k("claim"), "cognition"],
  [k("question"), "cognition"],
  [k("hypothesis"), "cognition"],
  [k("insight"), "cognition"],
  [k("insight_derivation"), "cognition"],
  [k("decision"), "cognition"],
  [k("deliberation"), "cognition"],
  [k("deliberation_turn"), "cognition"],
  [k("dispute"), "cognition"],
  [k("dispute_position"), "cognition"],
  [k("evaluation_defect"), "cognition"],
  // F5 协作 Collaboration — info/cyan · Users
  [k("team_formation"), "collaboration"],
  [k("team_membership"), "collaboration"],
  [k("integration_round"), "collaboration"],
  [k("integration_contribution"), "collaboration"],
  [k("divergence_pass"), "collaboration"],
  [k("capability_observation"), "collaboration"],
  // F6 治理/时限 Governance — destructive/neutral · Shield/FileText
  [k("monitoring_cycle"), "governance"],
  [k("report_revision"), "governance"],
  [k("episode"), "governance"],
]);

/**
 * Resolve the family for a raw node kind string. Uses the canonical V6
 * registry classification first (known / generic), then the family map.
 * Unknown kinds always fall back to `generic` — never throws.
 */
export function familyForNodeKind(rawKind: string): NodeKindFamily {
  return NODE_KIND_TO_FAMILY.get(rawKind as ResearchV6NodeKind) ?? "generic";
}

export interface NodeKindFamilySurface {
  kind: ResearchV6NodeKind | string;
  label: string;
  group: string;
  family: NodeKindFamily;
  /** True when the raw kind was not recognised by the canonical registry. */
  isGeneric: boolean;
  diagnostic?: ResearchV6UnknownKindDiagnostic;
}

/**
 * Classify one projection node into its render surface (kind + registry label +
 * family + generic diagnostic). This is the single funnel every renderer uses,
 * matching the canonical registry's unknown-kind degradation contract.
 */
export function classifyNodeFamily(
  ray: {
    id: string;
    node_kind: string;
    run_id: string;
  },
  diagnostics: ResearchV6UnknownKindDiagnostic[],
): NodeKindFamilySurface {
  // Delegate to the canonical V6 registry for degradation + labels.
  const surface = classifyNodeKind(ray.node_kind, ray.id, ray.run_id, diagnostics);
  if (surface.isGeneric) {
    return {
      kind: surface.kind,
      label: surface.label,
      group: surface.group,
      family: "generic",
      isGeneric: true,
      diagnostic: surface.diagnostic,
    };
  }
  return {
    kind: surface.kind,
    label: surface.label,
    group: surface.group,
    family: familyForNodeKind(surface.kind),
    isGeneric: false,
  };
}

/** All canonical V6 kinds registered for tests/demo. */
export const KNOWN_NODE_KINDS: readonly ResearchV6NodeKind[] = [
  ...NODE_KIND_TO_FAMILY.keys(),
];
