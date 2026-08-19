"use client";

import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n/use-t";
import type {
  TrajectoryLayoutCommit,
  TrajectoryLaneLayout,
} from "@multica/core/research";
import { GIT_BRANCH_COLORS } from "../lib/git-topology";

/**
 * Status text label per lane-layout tone (ok/run/fail/wait/mute). Status is
 * always double-encoded: a semantic badge/color *plus* a text label, so merge/
 * branch/fail is never conveyed by color alone (LRM-1394 T04–T07).
 */
const STATUS_BADGE: Record<string, string> = {
  ok: "border-success/35 bg-success/10 text-success-strong",
  run: "border-brand/35 bg-brand/10 text-brand",
  fail: "border-destructive/35 bg-destructive/10 text-destructive",
  wait: "border-warning/40 bg-warning/10 text-foreground",
  mute: "border-muted/40 bg-muted/30 text-muted-foreground",
};

export function TrajectoryCommitCard({
  layout,
  commit,
  selected,
  tabIndex,
  onSelect,
  onOpenDetail,
}: {
  layout: TrajectoryLaneLayout;
  commit: TrajectoryLayoutCommit;
  selected: boolean;
  tabIndex: 0 | -1;
  onSelect: (id: string) => void;
  onOpenDetail: (id: string) => void;
}) {
  const { t } = useT("research");
  const lane = layout.lanes[commit.lane];
  const color = GIT_BRANCH_COLORS[commit.colorSlot % GIT_BRANCH_COLORS.length];
  const isMerge = layout.junctions.some((j) => j.commitId === commit.id);
  const badge = STATUS_BADGE[commit.status] ?? STATUS_BADGE.wait;
  const statusLabel = (() => {
    switch (commit.status) {
      case "ok":
        return t((s) => s.trajectory_explorer.status_ok);
      case "run":
        return t((s) => s.trajectory_explorer.status_run);
      case "fail":
        return t((s) => s.trajectory_explorer.status_fail);
      case "wait":
        return t((s) => s.trajectory_explorer.status_wait);
      case "mute":
        return t((s) => s.trajectory_explorer.status_mute);
      default:
        return commit.status;
    }
  })();
  const mergeLabel = t((s) => s.trajectory_explorer.merge);

  return (
    <button
      type="button"
      data-testid="trajectory-commit-card"
      data-commit-id={commit.id}
      data-lane={commit.lane}
      data-branch={commit.branchKey}
      data-status={commit.status}
      data-selected={selected ? "true" : "false"}
      aria-label={`${commit.label.text}, ${statusLabel}${
        lane ? `, ${lane.accessibleLabel}` : ""
      }${isMerge ? `, ${mergeLabel}` : ""}`}
      aria-pressed={selected}
      tabIndex={tabIndex}
      className={cn(
        "group flex min-h-0 w-full cursor-pointer flex-col gap-1 rounded-md border bg-card p-2 text-left transition-colors",
        selected
          ? "border-border ring-2 ring-brand/50"
          : "border-border/70 hover:border-border hover:bg-muted/40",
      )}
      onClick={() => onSelect(commit.id)}
      onDoubleClick={() => onOpenDetail(commit.id)}
    >
      <div className="flex items-start justify-between gap-2">
        <Tooltip>
          <TooltipTrigger
            render={<span className="line-clamp-2 text-xs font-medium leading-snug text-foreground" />}
          >
            {commit.label.text}
          </TooltipTrigger>
          <TooltipContent side="top">{commit.label.text}</TooltipContent>
        </Tooltip>
        <span
          data-testid="trajectory-commit-status"
          className={cn(
            "shrink-0 rounded border px-1 text-[10px] font-medium leading-4",
            badge,
          )}
        >
          {statusLabel}
        </span>
      </div>
      <div className="flex items-center gap-1.5">
        <span
          aria-hidden="true"
          className="inline-block h-1.5 w-1.5 shrink-0 rounded-full"
          style={{ backgroundColor: color }}
        />
        <span
          data-testid="trajectory-commit-branch"
          className="truncate text-[10px] text-muted-foreground"
          style={{ color }}
        >
          {lane?.accessibleLabel ?? commit.branchKey}
        </span>
        {isMerge ? (
          <span
            className="ml-auto shrink-0 rounded border border-border/60 px-1 text-[10px] leading-4 text-foreground"
            data-testid="trajectory-commit-merge"
          >
            {mergeLabel}
          </span>
        ) : null}
      </div>
    </button>
  );
}
