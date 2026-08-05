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
      disabledReason: "当前节点不可创建探索分支，请选择已完成的研究节点",
    },
    {
      id: "retry_failed",
      enabled: retryable && canWrite,
      disabledReason: !retryable
        ? "仅失败或无结果的节点可重试"
        : !canWrite
          ? "当前账号无写入权限，请联系工作区管理员"
          : undefined,
    },
    {
      id: "reassign",
      enabled: false,
      needConfirm: true,
      disabledReason: "当前节点不可改派，请等待任务开始或失败后再试",
    },
    {
      id: "cancel_run",
      enabled: false,
      danger: true,
      needConfirm: true,
      disabledReason: running
        ? "请在会话顶部停止整个调研"
        : "节点当前未在执行",
    },
  ];
}
