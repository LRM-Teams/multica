import type { ResearchGraphNode } from "@multica/core/types";

/** System nodes open detail directly — no action ring (LRM-848). */
export const SYSTEM_NODE_TYPES = new Set([
  "roster_change",
  "stage_gate",
  "agent_activity",
]);

export type NodeRingAction =
  | "detail"
  | "locate_source"
  | "copy_prompt"
  | "continue"
  | "fork"
  | "retry"
  | "reassign";

/** LRM-981 — dead-end / refuted / failed nodes offer a scannable retry entry. */
export function nodeOffersRetry(node: ResearchGraphNode): boolean {
  if (node.node_type === "dead_end" || node.node_type === "refuted") return true;
  const s = (node.status || "").toLowerCase();
  return s === "failed" || s === "error";
}

/** Retry/reassign are task operations in the backend contract. */
export function nodeHasTaskAnchor(node: ResearchGraphNode): boolean {
  const kind = node.node_type.toLowerCase();
  if (kind === "task" || kind === "attempt") return true;
  if (!node.payload || typeof node.payload !== "object") return false;
  const payload = node.payload as Record<string, unknown>;
  if (typeof payload.task_id === "string" && payload.task_id.trim()) return true;
  const details = payload.details;
  return Boolean(
    details &&
      typeof details === "object" &&
      typeof (details as Record<string, unknown>).task_id === "string" &&
      ((details as Record<string, unknown>).task_id as string).trim(),
  );
}

export type NodeRingGroup = "primary" | "explore" | "recover" | "view";
export type NodeRingItem = { id: NodeRingAction; group: NodeRingGroup; primary?: boolean; confirm?: boolean; disabled?: boolean; candidate?: boolean };

export function ringActionsForNode(node: ResearchGraphNode): NodeRingItem[] {
  const retryable = nodeOffersRetry(node);
  const taskAnchor = nodeHasTaskAnchor(node);
  const status = (node.status || "").toLowerCase();
  const running = ["active", "running", "in_progress", "queued", "pending"].includes(status);
  const terminal = ["done", "completed", "success", "succeeded"].includes(status);
  const commandAnchor = !SYSTEM_NODE_TYPES.has(node.node_type);
  const hasSource =
    node.node_type === "finding" &&
    !!node.payload &&
    typeof node.payload === "object" &&
    "source_id" in (node.payload as object);

  const items: NodeRingItem[] = [];
  if (commandAnchor && terminal) items.push({ id: "continue", group: "primary", primary: true });
  if (commandAnchor && !running && !retryable) items.push({ id: "fork", group: "explore" });
  if (retryable && taskAnchor) items.push({ id: "retry", group: "recover", primary: true });
  if (commandAnchor && taskAnchor && (running || retryable)) {
    items.push({ id: "reassign", group: "recover", confirm: true });
  }
  items.push({ id: "detail", group: "view", primary: items.length === 0 });
  if (hasSource) items.push({ id: "locate_source", group: "view" });
  items.push({ id: "copy_prompt", group: "view" });
  return items;
}
