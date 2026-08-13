import type {
  ResearchMessage,
  ResearchProductRoundCard,
  ResearchProductRoundDecision,
} from "@multica/core/types";

const DECISIONS = new Set<ResearchProductRoundDecision>([
  "continue",
  "stop_enough",
  "stop_budget",
]);

function metaRecord(message: ResearchMessage): Record<string, unknown> | null {
  return message.meta && typeof message.meta === "object"
    ? (message.meta as Record<string, unknown>)
    : null;
}

function requiredNonNegativeInteger(
  meta: Record<string, unknown>,
  key: string,
): number | null {
  const value = meta[key];
  return typeof value === "number" && Number.isInteger(value) && value >= 0
    ? value
    : null;
}

/**
 * Adapts a canonical product-round process receipt into the interactive card.
 * Incomplete receipts deliberately return null so chat can show the raw
 * process message without inventing a round, decision, or budget.
 */
export function productRoundCardFromProcessMessage(
  message: ResearchMessage,
): ResearchProductRoundCard | null {
  if (message.card_kind !== "process") return null;
  const meta = metaRecord(message);
  if (!meta || meta.op !== "product_round_judgment") return null;

  const round = requiredNonNegativeInteger(meta, "round");
  const budgetUsed = requiredNonNegativeInteger(meta, "budget_used");
  const budgetRemaining = requiredNonNegativeInteger(meta, "budget_remaining");
  const decision = meta.decision;
  if (
    round === null ||
    round < 1 ||
    budgetUsed === null ||
    budgetRemaining === null ||
    typeof decision !== "string" ||
    !DECISIONS.has(decision) ||
    !Array.isArray(meta.coverage_gaps)
  ) {
    return null;
  }

  return {
    id: `process-${message.id}`,
    session_id: message.session_id,
    round_number: round,
    decision,
    coverage_gaps: meta.coverage_gaps,
    confidence_note: message.body,
    budget_used: budgetUsed,
    budget_remaining: budgetRemaining,
    goal_patch_proposal: null,
    next_round_focus: null,
    decided_by_agent_id: message.sender_id,
    created_at: message.created_at,
  };
}
