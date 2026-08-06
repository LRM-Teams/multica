"use client";

/**
 * LRM-1476 — 显示分组卡（DisplayGroupCard）。
 *
 * 表达摘要模式下被折叠成单个显示分组的子树（`CollapsedGroup`）。
 * 它 **不是** 真实 Insight —— 视觉上用虚线、muted 底色与「显示分组」标签明确
 * 区分，绝不冒充归纳结果；分组内节点数如实标注（memberCount）。
 *
 * 交互：点击展开该组（drill 进成员节点）。若分组位于 stale 受影响路径上，
 * 显示失效标记；点击行为由上层（InsightTreeView）传入。
 */

import { Dices } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { CollapsedGroup } from "../insight-tree-layout";

export function DisplayGroupCard({
  group,
  expanded = false,
  onToggle,
  onSelect,
  labels,
}: {
  group: CollapsedGroup;
  expanded?: boolean;
  onToggle?: () => void;
  onSelect?: () => void;
  labels?: {
    displayGroupLabel?: string;
    nodeLabel?: string;
    staleLabel?: string;
    expandLabel?: string;
    collapseLabel?: string;
  };
}) {
  const L = labels ?? {};
  const displayGroupLabel = L.displayGroupLabel ?? "显示分组";
  const nodeLabel = L.nodeLabel ?? "节点";
  const staleLabel = L.staleLabel ?? "已失效";
  const expandLabel = L.expandLabel ?? "展开改组";
  const collapseLabel = L.collapseLabel ?? "折叠改组";

  const clickable = Boolean(onToggle || onSelect);
  const ariaLabel = [
    displayGroupLabel,
    `${group.memberCount} ${nodeLabel}`,
    group.onStalePath ? staleLabel : undefined,
  ]
    .filter(Boolean)
    .join("，");

  return (
    <button
      type="button"
      disabled={!clickable}
      onClick={(e) => {
        e.stopPropagation();
        if (onToggle) onToggle();
        else onSelect?.();
      }}
      aria-expanded={clickable ? expanded : undefined}
      aria-label={clickable ? ariaLabel : undefined}
      className={cn(
        "display-group-card flex w-full flex-col items-start gap-1.5 rounded-lg border-2 border-dashed px-3 py-2.5 text-left transition-colors duration-150",
        group.onStalePath
          ? "border-destructive/40 bg-destructive/5"
          : "border-border/70 bg-muted/60",
        clickable
          ? "cursor-pointer hover:border-primary/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          : "cursor-default",
        expanded ? "ring-1 ring-primary/40" : "",
      )}
      data-testid="display-group-card"
      data-group-id={group.groupId}
      data-member-count={group.memberCount}
      data-on-stale-path={group.onStalePath ? "true" : "false"}
      data-expanded={expanded ? "true" : "false"}
    >
      <span className="inline-flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
        <Dices className="size-3.5" aria-hidden />
        {displayGroupLabel}
      </span>
      <span className="text-sm text-foreground" data-testid="display-group-count">
        {group.memberCount} {nodeLabel}
      </span>
      <span className="flex items-center justify-between gap-2 self-stretch">
        {group.onStalePath ? (
          <span
            className="inline-flex items-center rounded-4xl bg-destructive/15 px-2 text-[10px] font-semibold text-destructive"
            data-testid="display-group-stale"
          >
            {staleLabel}
          </span>
        ) : (
          <span className="text-[10px] text-muted-foreground/60" data-testid="display-group-hint">
            非真实 Insight
          </span>
        )}
        {clickable ? (
          <span className="text-[11px] font-medium text-primary" data-testid="display-group-action">
            {expanded ? collapseLabel : expandLabel}
          </span>
        ) : null}
      </span>
    </button>
  );
}
