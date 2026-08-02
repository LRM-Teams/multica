import type { ResearchMessage, ResearchStage } from "@multica/core/types";

/** Canonical stage order for the research session timeline (LRM-819). */
export const RESEARCH_STAGE_ORDER = [
  "s1_plan",
  "s2_sources",
  "s3_validation",
  "s4_delivery",
] as const satisfies readonly ResearchStage[];

export type ResearchStageId = (typeof RESEARCH_STAGE_ORDER)[number];

export type StageStepState = "done" | "current" | "upcoming";

export function stageIndex(stage: string): number {
  return RESEARCH_STAGE_ORDER.indexOf(stage as ResearchStageId);
}

export function resolveStageStepState(
  stage: string,
  currentStage: string,
  sessionStatus: string,
): StageStepState {
  if (sessionStatus === "completed" || sessionStatus === "archived") {
    return "done";
  }
  const idx = stageIndex(stage);
  const cur = stageIndex(currentStage);
  if (idx < 0) return "upcoming";
  if (cur < 0) return idx === 0 ? "current" : "upcoming";
  if (idx < cur) return "done";
  if (idx === cur) return "current";
  return "upcoming";
}

/** DOM id for the chat-area stage anchor. */
export function stageAnchorId(stage: string): string {
  return `research-stage-${stage}`;
}

/** DOM id on a chat message wrapper used as a scroll/highlight target (LRM-824). */
export function stageAnchorTargetId(messageId: string): string {
  return `research-msg-${messageId}`;
}

/**
 * Pick the first message that should own a stage anchor.
 * Prefer explicit `meta.stage`; fall back to process ops that name a stage.
 */
export function messageStageKey(message: ResearchMessage): string | null {
  const meta = message.meta;
  if (meta && typeof meta === "object") {
    const stage = (meta as Record<string, unknown>).stage;
    if (typeof stage === "string" && stageIndex(stage) >= 0) return stage;
    const op = (meta as Record<string, unknown>).op;
    if (typeof op === "string" && stageIndex(op) >= 0) return op;
  }
  return null;
}

/**
 * Build stage → first message id map for scroll anchoring.
 * Stages without a tagged message still get a synthetic list marker in the UI.
 */
export function buildStageMessageAnchors(
  messages: ResearchMessage[],
): Map<string, string> {
  const map = new Map<string, string>();
  for (const m of messages) {
    const stage = messageStageKey(m);
    if (stage && !map.has(stage)) map.set(stage, m.id);
  }
  return map;
}
