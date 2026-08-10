/**
 * LRM-1477 — Semantic mapping & terminal-state regression tests (AC1, Rule ①/②).
 *
 * AC1: every one of the 10 transition_kind resolves, deterministically, to a
 * display spec (group + verb + static marker). Rule ①: the map is exhaustive by
 * construction (Record), so an unknown kind cannot compile.
 */
import { describe, expect, it } from "vitest";
import {
  resolveSemanticDisplay,
  effectiveVerb,
  SEMANTIC_TRANSITION_KIND_MAP,
  type SemanticTransitionKind,
} from "./semantic-mapping";
import { ALL_TRANSITION_KINDS } from "./fixture";
import { DEFAULT_MOTION_PROFILE } from "./transition-queue";

const REDUCED_PROFILE = { ...DEFAULT_MOTION_PROFILE, reducedMotion: true };

describe("SEMANTIC_TRANSITION_KIND_MAP — AC1 exhaustive & deterministic", () => {
  it("maps exactly all 13 kinds (exhaustive over the declared union)", () => {
    const kinds = Object.keys(SEMANTIC_TRANSITION_KIND_MAP).sort();
    expect(kinds).toEqual(
      [
        "branch_spawned",
        "task_dispatched",
        "result_accepted",
        "integration_formed",
        "insight_staled",
        "dispute_opened",
        "deliberation_progressed",
        "lead_escalated",
        "team_membership_changed",
        "report_revised",
        // D5 lifecycle kinds (LRM-1537 §2):
        "node_retired",
        "task_restarted",
        "goal_modified",
      ].sort(),
    );
  });

  it("resolves each kind to a deterministic spec (same input → same output)", () => {
    for (const kind of ALL_TRANSITION_KINDS) {
      const spec = resolveSemanticDisplay(kind);
      expect(spec).toEqual(resolveSemanticDisplay(kind));
      expect(spec.group).toBeTruthy();
      expect(spec.verb).toBeTruthy();
      expect(spec.marker).toBeDefined();
    }
  });

  it("maintains the 4 distinguishable super-class signatures (AC1 visual distinction)", () => {
    const groupOf = (kinds: SemanticTransitionKind[]) =>
      kinds.map((k) => resolveSemanticDisplay(k).group);

    // Distinct super-categories so appear/merge/conflict/escalate are separable.
    expect(groupOf(["branch_spawned", "task_dispatched", "result_accepted"])).toEqual([
      "appear",
      "appear",
      "advance",
    ]);
    expect(groupOf(["integration_formed"])).toEqual(["merge"]);
    expect(groupOf(["dispute_opened"])).toEqual(["conflict"]);
    expect(groupOf(["deliberation_progressed", "lead_escalated"])).toEqual([
      "escalate",
      "escalate",
    ]);
    expect(groupOf(["insight_staled"])).toEqual(["stale"]);
    expect(groupOf(["report_revised"])).toEqual(["advance"]);
  });

  it("keeps a persistent static marker for conflict/escalate/stale/revise (Rule ②)", () => {
    expect(resolveSemanticDisplay("dispute_opened").marker).toBe("conflict-border");
    expect(resolveSemanticDisplay("lead_escalated").marker).toBe("escalate-emphasis");
    expect(resolveSemanticDisplay("deliberation_progressed").marker).toBe(
      "escalate-emphasis",
    );
    expect(resolveSemanticDisplay("insight_staled").marker).toBe("stale-grey");
    expect(resolveSemanticDisplay("report_revised").marker).toBe("revise-pulse");
    // result_accepted keeps an accepted-check marker.
    expect(resolveSemanticDisplay("result_accepted").marker).toBe("accepted-check");
  });

  describe("D5 lifecycle kinds (LRM-1537 §2, Rule ①/②)", () => {
    it("maps the three new kinds to their own verbs and persistent static markers", () => {
      const retired = resolveSemanticDisplay("node_retired");
      expect(retired.verb).toBe("retire");
      expect(retired.marker).toBe("tombstone"); // grey-out + kept, never gone
      expect(retired.group).toBe("stale");

      const restarted = resolveSemanticDisplay("task_restarted");
      expect(restarted.verb).toBe("restart");
      expect(restarted.marker).toBe("restart-relation");
      expect(restarted.group).toBe("appear");

      const regoal = resolveSemanticDisplay("goal_modified");
      expect(regoal.verb).toBe("regoal");
      expect(regoal.marker).toBe("regoal-highlight");
      expect(regoal.group).toBe("advance");
    });

    it("keeps the static markers persistent (Rule ②) for the D5 verbs", () => {
      expect(resolveSemanticDisplay("node_retired").marker).toBe("tombstone");
      expect(resolveSemanticDisplay("task_restarted").marker).toBe("restart-relation");
      expect(resolveSemanticDisplay("goal_modified").marker).toBe("regoal-highlight");
    });
  });
});

describe("effectiveVerb — Rule ④ reduced-motion collapse", () => {
  it("preserves the true display verb under the default profile", () => {
    for (const kind of ALL_TRANSITION_KINDS) {
      const spec = resolveSemanticDisplay(kind);
      expect(effectiveVerb(spec, DEFAULT_MOTION_PROFILE)).toBe(spec.verb);
    }
  });

  it("collapses every displacement verb to reappear under reduced motion", () => {
    for (const kind of ALL_TRANSITION_KINDS) {
      const spec = resolveSemanticDisplay(kind);
      expect(effectiveVerb(spec, REDUCED_PROFILE)).toBe("reappear");
    }
  });
});
