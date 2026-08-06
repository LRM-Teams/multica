"use client";

import { X } from "lucide-react";
import type { ResearchGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/**
 * Selected-commit detail panel: summary / source / owner / expand + the
 * "jump to canvas" action (AC2 core). Jumping closes the aux drawer and lets
 * the session page select/focus the same node.id on the canvas.
 */
export function TrajectoryDetail({
  node,
  statusTone,
  onJumpToCanvas,
  onClose,
  onOpenNodeDetail,
}: {
  node: ResearchGraphNode | null;
  statusTone?: string;
  onJumpToCanvas: (nodeId: string) => void;
  onClose: () => void;
  onOpenNodeDetail: (nodeId: string) => void;
}) {
  const { t } = useT("research");

  if (!node) {
    return (
      <div
        data-testid="trajectory-detail-empty"
        className="border-t border-border/55 px-3 py-2.5 text-xs text-muted-foreground"
      >
        {t((s) => s.panel.aux_detail_empty)}
      </div>
    );
  }

  return (
    <div
      data-testid="trajectory-detail"
      className="border-t border-border/55 bg-card px-3 py-2.5"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h4 className="line-clamp-1 text-sm font-medium text-foreground">
            {node.title || node.id}
          </h4>
          {statusTone ? (
            <span className="mt-0.5 inline-block rounded border border-border/50 px-1 text-[10px] leading-4 text-muted-foreground">
              {statusTone}
            </span>
          ) : null}
          {node.summary ? (
            <p className="mt-1 line-clamp-2 text-xs leading-snug text-muted-foreground">
              {node.summary}
            </p>
          ) : null}
          {node.actor_agent_id ? (
            <p className="mt-1 text-[10px] text-muted-foreground">
              {t((s) => s.node.agent_activity)}: {node.actor_agent_id}
            </p>
          ) : null}
        </div>
        <button
          type="button"
          aria-label={t((s) => s.overlay.detail_close)}
          onClick={onClose}
          className="shrink-0 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="mt-2 flex flex-wrap gap-2">
        <button
          type="button"
          data-testid="trajectory-detail-expand"
          onClick={() => onOpenNodeDetail(node.id)}
          className={cn(
            "rounded border border-border/60 px-2.5 py-1 text-xs text-foreground",
            "hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand",
          )}
        >
          {t((s) => s.trajectory_explorer.expand_detail)}
        </button>
        <button
          type="button"
          data-testid="trajectory-detail-jump"
          onClick={() => onJumpToCanvas(node.id)}
          className={cn(
            "rounded border border-brand/35 bg-brand/10 px-2.5 py-1 text-xs text-brand",
            "hover:bg-brand/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand",
          )}
        >
          {t((s) => s.trajectory_explorer.jump_to_canvas)}
        </button>
      </div>
    </div>
  );
}
