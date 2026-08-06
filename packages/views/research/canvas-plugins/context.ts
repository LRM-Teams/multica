"use client";

import type {
  ResearchCanvasNodeContext,
  ResearchCanvasPluginContext,
} from "./types";

/**
 * FE-10 — build the read-only view snapshot handed to slots.
 *
 * Accepts ALREADY-DERIVED display nodes (id/kind/title/status + selection)
 * and wraps them into an immutable context. It never receives the committed
 * Projection and cannot author canonical nodes — callers pass the display
 * view models the canvas already owns (AC #3).
 */
export function buildCanvasPluginContext(
  nodes: readonly ResearchCanvasNodeContext[],
  selectedNodeId: string | null,
  reducedMotion: boolean,
): ResearchCanvasPluginContext {
  return {
    // Copy so later mutation by a caller cannot bleed into a slot read.
    nodes: nodes.map((n) => ({ ...n })),
    selectedNodeId,
    reducedMotion,
  };
}
