import type { ResearchGraphNode } from "@multica/core/types";
import { nodeOffersRetry } from "./node-action-ring";

/**
 * LRM-1116 card ··· menu. Only wire existing capabilities;
 * missing APIs stay disabled with an explicit reason.
 */
export type CardMenuActionId =
  | "view_evidence"
  | "view_io"
  | "fork_from"
  | "retry_failed"
  | "reassign"
  | "cancel_run";

export type CardMenuItem = {
  id: CardMenuActionId;
  enabled: boolean;
  danger?: boolean;
  needConfirm?: boolean;
  /** Shown when disabled — never a silent dead control. */
  disabledReason?: string;
};

export function cardMenuItemsForNode(
  node: ResearchGraphNode,
  options?: { canWrite?: boolean },
): CardMenuItem[] {
  const canWrite = options?.canWrite !== false;
  const retryable = nodeOffersRetry(node);
  const running =
    (node.status || "").toLowerCase() === "running" ||
    (node.status || "").toLowerCase() === "active" ||
    (node.status || "").toLowerCase() === "in_progress";
  const hasSource =
    node.node_type === "finding" &&
    !!node.payload &&
    typeof node.payload === "object" &&
    "source_id" in (node.payload as object);

  return [
    {
      id: "view_evidence",
      enabled: true,
      disabledReason: hasSource ? undefined : undefined,
    },
    {
      id: "view_io",
      enabled: true,
    },
    {
      id: "fork_from",
      enabled: false,
      disabledReason: "Fork-from-here API is not available yet",
    },
    {
      id: "retry_failed",
      enabled: retryable && canWrite,
      disabledReason: !retryable
        ? "Retry is only available on failed / dead-end nodes"
        : !canWrite
          ? "Read-only — no write permission"
          : undefined,
    },
    {
      id: "reassign",
      enabled: false,
      needConfirm: true,
      disabledReason: "Reassign API is not available on graph nodes yet",
    },
    {
      id: "cancel_run",
      enabled: false,
      danger: true,
      needConfirm: true,
      disabledReason: running
        ? "Cancel-run API is not available on graph nodes yet"
        : "Node is not running",
    },
  ];
}
