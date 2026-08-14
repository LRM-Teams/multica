/**
 * LRM-1475 AC1 — 30 node kinds map to a card family; unknown kinds degrade to
 * generic and never crash.
 */
import { describe, expect, it } from "vitest";
import type { ResearchV6UnknownKindDiagnostic } from "@multica/core/types/research-v6";
import { RESEARCH_V6_NODE_KINDS } from "@multica/core/research-v6/registry";
import {
  NODE_KIND_TO_FAMILY,
  classifyNodeFamily,
  familyForNodeKind,
  KNOWN_NODE_KINDS,
  NODE_KIND_FAMILIES,
} from "./node-kind-registry";

const RECOGNISED = [
  "goal",
  "task",
  "attempt",
  "result_artifact",
  "search_plan",
  "query_execution",
  "source_candidate",
  "screening_decision",
  "source_snapshot",
  "observation",
  "claim",
  "question",
  "hypothesis",
  "branch",
  "insight",
  "insight_derivation",
  "integration_round",
  "integration_contribution",
  "dispute",
  "dispute_position",
  "deliberation",
  "deliberation_turn",
  "decision",
  "team_formation",
  "team_membership",
  "divergence_pass",
  "capability_observation",
  "report_revision",
  "evaluation_defect",
  "monitoring_cycle",
  "episode",
];

describe("node-kind-registry — V6 kinds → family mapping (AC1)", () => {
  it("registers every canonical kind from the real V6 registry", () => {
    expect(RESEARCH_V6_NODE_KINDS).toHaveLength(31);
    expect(new Set(RESEARCH_V6_NODE_KINDS).size).toBe(31);
    expect(RECOGNISED).toHaveLength(31);
  });

  it("every recognised kind maps to a non-generic family", () => {
    for (const kind of RECOGNISED) {
      const family = familyForNodeKind(kind);
      expect(NODE_KIND_FAMILIES).toContain(family);
      expect(family).not.toBe("generic");
    }
  });

  it("KNOWN_NODE_KINDS mirrors the canonical V6 kind set", () => {
    expect(new Set(KNOWN_NODE_KINDS)).toEqual(new Set(RESEARCH_V6_NODE_KINDS));
  });

  it("each of the 6 design families is referenced by at least one kind", () => {
    const used = new Set(NODE_KIND_TO_FAMILY.values());
    for (const family of NODE_KIND_FAMILIES) {
      expect(used.has(family)).toBe(true);
    }
  });

  it("design §1 spot-checks: execution kinds → execution, evidence kinds → evidence", () => {
    expect(familyForNodeKind("attempt")).toBe("execution");
    expect(familyForNodeKind("result_artifact")).toBe("evidence");
    expect(familyForNodeKind("insight")).toBe("cognition");
    expect(familyForNodeKind("team_formation")).toBe("collaboration");
    expect(familyForNodeKind("report_revision")).toBe("governance");
    expect(familyForNodeKind("task")).toBe("structure");
  });
});

describe("node-kind-registry — unknown kind degradation (AC1)", () => {
  it("unknown kind maps to generic family without throwing", () => {
    const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
    const surface = classifyNodeFamily(
      { id: "run:x:y", node_kind: "some_future_kind", run_id: "run:x" },
      diagnostics,
    );
    expect(surface.isGeneric).toBe(true);
    expect(surface.family).toBe("generic");
    expect(surface.label).toBe("未知节点");
  });

  it("classifyNodeFamily keeps a known kind + its registry group", () => {
    const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
    const surface = classifyNodeFamily(
      { id: "run:t:1", node_kind: "task", run_id: "run:t" },
      diagnostics,
    );
    expect(surface.isGeneric).toBe(false);
    expect(surface.family).toBe("structure");
    expect(surface.group).toMatch(/./);
  });
});
