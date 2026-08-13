// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchMessage } from "@multica/core/types";
import { productRoundCardFromProcessMessage } from "./product-round-process-card";

function message(meta: Record<string, unknown>): ResearchMessage {
  return {
    id: "m1",
    session_id: "s1",
    sender_type: "system",
    sender_id: "agent-1",
    target_agent_id: null,
    body: "Open round 3",
    card_kind: "process",
    meta,
    created_at: "2026-08-13T10:00:00Z",
  };
}

describe("productRoundCardFromProcessMessage", () => {
  it("adapts a complete canonical product-round receipt", () => {
    expect(
      productRoundCardFromProcessMessage(
        message({
          op: "product_round_judgment",
          round: 2,
          decision: "continue",
          coverage_gaps: ["latency"],
          budget_used: 2,
          budget_remaining: 1,
        }),
      ),
    ).toMatchObject({
      round_number: 2,
      decision: "continue",
      coverage_gaps: ["latency"],
      budget_used: 2,
      budget_remaining: 1,
    });
  });

  it.each([
    { decision: "continue", coverage_gaps: [], budget_used: 1, budget_remaining: 2 },
    { round: 1, coverage_gaps: [], budget_used: 1, budget_remaining: 2 },
    { round: 1, decision: "future", coverage_gaps: [], budget_used: 1, budget_remaining: 2 },
    { round: 1, decision: "continue", coverage_gaps: [], budget_remaining: 2 },
    { round: 1, decision: "continue", coverage_gaps: [], budget_used: 1 },
    { round: 1.5, decision: "continue", coverage_gaps: [], budget_used: 1, budget_remaining: 2 },
    { round: 1, decision: "continue", coverage_gaps: {}, budget_used: 1, budget_remaining: 2 },
  ])("rejects incomplete or malformed canonical facts: %o", (meta) => {
    expect(
      productRoundCardFromProcessMessage(
        message({ op: "product_round_judgment", ...meta }),
      ),
    ).toBeNull();
  });

  it("does not reinterpret unrelated process messages", () => {
    expect(
      productRoundCardFromProcessMessage(message({ op: "research_nextstep" })),
    ).toBeNull();
  });
});
