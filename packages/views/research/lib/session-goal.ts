import { truncateOneLine } from "./session-list-filter";
import { stripCreateParamsTrailer } from "./research-create-params";

/** Visual state for SessionGoalCard (LRM-898 / LRM-1008 scheme D). */
export type SessionGoalVisualState =
  | "empty"
  | "loading"
  | "ready"
  | "updated"
  | "pending_substantive"
  | "error";

export type SessionGoalModel = {
  /** Full current goal (create-params trailer stripped). */
  text: string;
  /** Single-line truncated summary for the compact card. */
  summary: string;
  previousText: string | null;
  state: SessionGoalVisualState;
  /** Soft note under the popover title (e.g. optimized from user words). */
  note: string | null;
  /** Pending substantive proposal awaiting user confirm (if any). */
  substantiveProposal: string | null;
};

export function normalizeSessionGoalText(raw: string | null | undefined): string {
  return stripCreateParamsTrailer(raw ?? "").trim();
}

/**
 * Build the display model for the Goal Card.
 * Pulse / "updated" is supplied by the caller (only after a user-driven goal write).
 */
export function resolveSessionGoalModel(input: {
  goal: string | null | undefined;
  previousGoal?: string | null;
  pendingSubstantive?: string | null;
  loading?: boolean;
  error?: boolean;
  justUpdated?: boolean;
  summaryMaxChars?: number;
}): SessionGoalModel {
  const text = normalizeSessionGoalText(input.goal);
  const previousRaw = normalizeSessionGoalText(input.previousGoal);
  const previousText =
    previousRaw && previousRaw !== text ? previousRaw : null;
  const substantive = normalizeSessionGoalText(input.pendingSubstantive);

  let state: SessionGoalVisualState = "ready";
  if (input.loading) state = "loading";
  else if (input.error) state = "error";
  else if (!text) state = "empty";
  else if (substantive) state = "pending_substantive";
  else if (input.justUpdated) state = "updated";

  const summarySource =
    state === "pending_substantive" && substantive
      ? substantive
      : text;
  const summaryMax = input.summaryMaxChars ?? 28;

  return {
    text,
    summary:
      state === "empty"
        ? ""
        : state === "loading"
          ? ""
          : truncateOneLine(summarySource || text, summaryMax),
    previousText,
    state,
    note: previousText ? "optimized" : null,
    substantiveProposal: substantive || null,
  };
}
