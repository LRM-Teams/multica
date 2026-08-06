/**
 * LRM-979 — canvas body mode when the graph has zero nodes.
 * In-flight sessions stay on a forming skeleton (not a blank pit / gray stub).
 */
export type CanvasBodyMode = "ready" | "forming" | "empty";

export function resolveCanvasBodyMode(
  nodeCount: number,
  sessionStatus?: string | null,
): CanvasBodyMode {
  if (nodeCount > 0) return "ready";
  if (sessionStatus === "running" || sessionStatus === "paused") return "forming";
  return "empty";
}
