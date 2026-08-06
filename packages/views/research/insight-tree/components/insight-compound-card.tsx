"use client";

/**
 * LRM-1476 — Insight 组合卡（InsightCompoundCard）。
 *
 * 渲染一个后端 Projection 提供的 canonical Insight/Claim 节点卡：
 * 层级徽标、输入数量、证据覆盖、矛盾徽标、结论文案、贡献 Agent 头像堆；
 * 并按 `computeStalePaths` 的受影响程度（direct / inherited）叠加失效视觉。
 *
 * 边界（与 insight-tree 合约一致）：
 *  - 本卡只读 `node.level / inputIds / freshness / conclusion / ...`，绝不计算
 *    canonical 层级或 freshness；失效视觉只来自传入的 `staleAffect` 事实。
 *  - 显示分组由 `DisplayGroupCard` 表达；本卡从不把分组冒充为 Insight。
 *
 * 本组件无副作用、不写回 canonical Graph；展开/合并只改可见集合。
 */

import { ChevronRight } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type {
  InsightDerivationNode,
  InsightStaleReason,
} from "../insight-derivation-contract";
import type { StaleAffectKind } from "../insight-tree-stale";

export type InsightStaleBadge = {
  /** 是否位于受影响的 stale 路径上。 */
  stale: boolean;
  /** 受影响程度（direct / inherited）。 */
  affect?: StaleAffectKind;
  /** canonical 失效原因之一（仅 stale 有值）。 */
  reason?: InsightStaleReason;
};

const STALE_REASON_LABEL: Record<InsightStaleReason, string> = {
  input_refuted: "输入被反驳",
  input_superseded: "输入被取代",
  scope_changed: "范围改变",
  access_revoked: "访问已撤销",
};

/** 取贡献 Agent id 的首字母作为头像腔（不依赖 workspace 身份解析，保持纯渲染）。 */
function initials(id: string | undefined): string {
  if (!id) return "?";
  const trimmed = id.trim();
  if (!trimmed) return "?";
  return trimmed.slice(0, 2).toUpperCase();
}

export function InsightCompoundCard({
  node,
  stale,
  selected = false,
  expanded = false,
  expandable = false,
  onToggleExpand,
  onSelect,
  labels,
}: {
  node: InsightDerivationNode;
  /** 是否位于受影响的 stale 路径上（由 computeStalePaths 提供的事实）。 */
  stale?: InsightStaleBadge | null;
  selected?: boolean;
  /** 是否已展开（显示直接输入）。 */
  expanded?: boolean;
  /** 是否有可展开的直接输入。 */
  expandable?: boolean;
  onToggleExpand?: () => void;
  onSelect?: () => void;
  labels?: {
    inputsLabel?: string;
    evidenceLabel?: string;
    contradictionLabel?: string;
    staleLabel?: string;
    inheritedLabel?: string;
    exploreLabel?: string;
    collapseLabel?: string;
    agentLabel?: string;
  };
}) {
  const L = labels ?? {};
  const inputsLabel = L.inputsLabel ?? "输入";
  const evidenceLabel = L.evidenceLabel ?? "证据覆盖";
  const staleLabel = L.staleLabel ?? "已失效";
  const inheritedLabel = L.inheritedLabel ?? "继承失效";
  const exploreLabel = L.exploreLabel ?? "展开到直接输入";
  const collapseLabel = L.collapseLabel ?? "折叠输入";
  const agentLabel = L.agentLabel ?? "贡献";

  const isClaim = node.level === 0;
  const staleAffect = stale?.stale ? (stale.affect ?? "direct") : undefined;
  const hasInputs = node.inputIds.length > 0;

  const ariaLabel = [
    isClaim ? "Claim" : "Insight",
    `level ${node.level}`,
    hasInputs ? `${node.inputIds.length} ${inputsLabel}` : undefined,
    node.evidenceCoverage ? `${evidenceLabel} ${node.evidenceCoverage}` : undefined,
    node.conclusion,
    stale?.stale ? (staleAffect === "inherited" ? `${inheritedLabel} · ${staleLabel}` : staleLabel) : undefined,
  ]
    .filter(Boolean)
    .join("，");

  return (
    <div
      role={onSelect ? "button" : undefined}
      tabIndex={onSelect ? 0 : undefined}
      onClick={onSelect ? () => onSelect() : undefined}
      onKeyDown={
        onSelect
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onSelect();
              }
            }
          : undefined
      }
      className={cn(
        "insight-compound-card group relative flex cursor-pointer flex-col rounded-lg border bg-card px-3 py-2.5 text-left transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        // 外层环反映失效：direct 更重、inherited 轻量描边。
        stale?.stale
          ? staleAffect === "inherited"
            ? "border-destructive/40 ring-1 ring-destructive/40"
            : "border-destructive/60 ring-1 ring-destructive/60"
          : isClaim
            ? "border-success/40 ring-1 ring-success/40"
            : "border-primary/30 ring-1 ring-primary/30",
        selected ? "shadow-[0_0_0_2px_var(--ring)]" : "",
      )}
      data-testid="insight-compound-card"
      data-node-id={node.id}
      data-level={node.level}
      data-freshness={node.freshness}
      data-stale={stale?.stale ? "true" : "false"}
      data-affect={staleAffect ?? "none"}
      aria-label={ariaLabel}
      aria-pressed={selected || undefined}
    >
      <div className="flex items-start gap-2">
        {/* 层级徽标 */}
        <span
          className={cn(
            "badge inline-flex h-5 shrink-0 items-center rounded-4xl px-2 text-[10px] font-semibold",
            isClaim
              ? "bg-success/15 text-success-strong"
              : "bg-primary/15 text-primary-foreground",
          )}
          data-testid="level-badge"
        >
          {isClaim ? "Claim" : `Level ${node.level}`}
        </span>

        {/* 输入数量 / 证据覆盖 / 矛盾 */}
        <div className="flex flex-1 flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
          {hasInputs && (
            <span data-testid="input-count">{node.inputIds.length} {inputsLabel}</span>
          )}
          {node.evidenceCoverage ? (
            <span className="truncate" data-testid="evidence-coverage">
              {evidenceLabel} {node.evidenceCoverage}
            </span>
          ) : null}
          {(node.contradictionCount ?? 0) > 0 && (
            <span
              className="inline-flex items-center rounded-4xl border border-warning/60 bg-warning/15 px-1.5 text-[10px] font-medium text-warning"
              data-testid="contradiction-badge"
            >
              {L.contradictionLabel ?? "矛盾"} {node.contradictionCount}
            </span>
          )}
        </div>

        {/* 失效徽标 */}
        {stale?.stale && (
          <span
            className="inline-flex items-center gap-1 rounded-4xl bg-destructive/15 px-2 text-[10px] font-semibold text-destructive line-through decoration-destructive"
            data-testid="stale-badge"
            data-affect={staleAffect}
            title={stale.reason ? STALE_REASON_LABEL[stale.reason] : undefined}
          >
            {staleAffect === "inherited" ? inheritedLabel : staleLabel}
          </span>
        )}
      </div>

      {/* 结论文案 */}
      <p
        className={cn(
          "mt-1.5 line-clamp-3 text-sm leading-snug text-foreground",
          stale?.stale ? "line-through decoration-destructive/70" : "",
        )}
        data-testid="conclusion"
      >
        {node.conclusion}
      </p>

      {/* 底部行：贡献者与展开开关 */}
      <div className="mt-2 flex items-center justify-between gap-2">
        {node.contributingAgentIds && node.contributingAgentIds.length > 0 ? (
          <div
            className="flex items-center gap-1"
            aria-label={agentLabel}
            data-testid="contributor-stack"
          >
            {node.contributingAgentIds.slice(0, 3).map((id) => (
              <span
                key={id}
                title={id}
                className="flex size-5 shrink-0 items-center justify-center rounded-full bg-primary/15 text-[9px] font-bold text-primary-foreground"
              >
                {initials(id)}
              </span>
            ))}
            {node.contributingAgentIds.length > 3 ? (
              <span className="text-[10px] text-muted-foreground">
                +{node.contributingAgentIds.length - 3}
              </span>
            ) : null}
          </div>
        ) : (
          <span className="text-[10px] text-muted-foreground">—</span>
        )}

        {expandable && !isClaim && hasInputs && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onToggleExpand?.();
            }}
            aria-expanded={expanded}
            aria-label={expanded ? collapseLabel : exploreLabel}
            className="inline-flex items-center gap-0.5 rounded-md px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
            data-testid="expand-toggle"
          >
            <ChevronRight
              className={cn("size-3.5 transition-transform", expanded ? "rotate-90" : "")}
              aria-hidden
            />
            {expanded ? collapseLabel : exploreLabel}
          </button>
        )}
      </div>
    </div>
  );
}
