"use client";

import { memo, useMemo, type ReactNode } from "react";
import { EyeOff, MoreHorizontal, Plus, UserMinus } from "lucide-react";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useDroppable } from "@dnd-kit/core";
import { SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import type { Issue, IssueAssigneeType, IssueStatus } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@multica/ui/components/ui/dropdown-menu";
import { useModalStore } from "@multica/core/modals";
import { useViewStoreApi } from "@multica/core/issues/stores/view-store-context";
import { DraggableBoardCard } from "./board-card";
import { BOARD_STATUS_DOT } from "./board-status-dot";
import type { ChildProgress } from "./list-row";
import { useT } from "../../i18n";
import { ActorAvatar } from "../../common/actor-avatar";

// Insertion-position prediction intentionally omitted. The server's
// ORDER BY uses PostgreSQL's en_US.utf8 collation (glibc), which
// cannot be faithfully replicated in JavaScript (ICU/V8). Showing an
// inaccurate indicator is worse than showing none.

export const BOARD_COL_WIDTH = 300;
export const BOARD_CARD_WIDTH = BOARD_COL_WIDTH - 8; // col(300) - droppable p-1(8)

export interface BoardColumnGroup {
  id: string;
  title: string;
  status?: IssueStatus;
  assigneeType?: IssueAssigneeType | null;
  assigneeId?: string | null;
  totalCount?: number;
  createData?: Record<string, unknown>;
}

export const BoardColumn = memo(function BoardColumn({
  group,
  issueIds,
  issueMap,
  childProgressMap,
  totalCount,
  footer,
  projectId,
  sortLabel,
}: {
  group: BoardColumnGroup;
  issueIds: string[];
  issueMap: Map<string, Issue>;
  childProgressMap?: Map<string, ChildProgress>;
  totalCount?: number;
  footer?: ReactNode;
  /** When set, the per-column "+" pre-fills the project on the create form. */
  projectId?: string;
  sortLabel?: string | null;
}) {
  const status = group.status;
  const { setNodeRef, isOver } = useDroppable({ id: group.id });
  const viewStoreApi = useViewStoreApi();
  const { t } = useT("issues");

  // Resolve IDs to Issue objects, preserving parent-provided order
  const resolvedIssues = useMemo(
    () =>
      issueIds.flatMap((id) => {
        const issue = issueMap.get(id);
        return issue ? [issue] : [];
      }),
    [issueIds, issueMap],
  );

  return (
    <BoardColumnShell
      heading={<BoardGroupHeading group={group} count={totalCount ?? issueIds.length} />}
      actions={
        /* Right: add + menu */
        <div className="flex items-center gap-1">
          {status && (
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button variant="ghost" size="icon-sm" className="rounded-full text-muted-foreground">
                    <MoreHorizontal className="size-3.5" />
                  </Button>
                }
              />
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => viewStoreApi.getState().hideStatus(status)}>
                  <EyeOff className="size-3.5" />
                  {t(($) => $.board.hide_column)}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="rounded-full text-muted-foreground"
                  onClick={() => {
                    const data = {
                      ...(group.createData ?? {}),
                      ...(projectId ? { project_id: projectId } : {}),
                    };
                    useModalStore.getState().open("create-issue", data);
                  }}
                >
                  <Plus className="size-3.5" />
                </Button>
              }
            />
            <TooltipContent>{t(($) => $.board.add_issue_tooltip)}</TooltipContent>
          </Tooltip>
        </div>
      }
    >
      <div className="relative min-h-[200px] flex-1 rounded-lg">
        {isOver && sortLabel && (
          <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-lg bg-background/40">
            <span className="rounded-md bg-popover px-2.5 py-1 text-xs font-medium text-popover-foreground shadow-sm border border-border">
              {sortLabel}
            </span>
          </div>
        )}
        <div
          ref={setNodeRef}
          className={`absolute inset-0 space-y-2 overflow-y-auto rounded-lg p-1 transition-colors ${
            isOver && sortLabel
              ? "ring-2 ring-brand/25 bg-accent/15"
              : isOver
                ? "bg-accent/60"
                : ""
          }`}
        >
          <SortableContext items={issueIds} strategy={verticalListSortingStrategy}>
            {resolvedIssues.map((issue) => (
              <DraggableBoardCard key={issue.id} issue={issue} childProgress={childProgressMap?.get(issue.id)} disableSorting={!!sortLabel} />
            ))}
          </SortableContext>
          {issueIds.length === 0 && (
            <p className="py-8 text-center text-xs text-muted-foreground">
              {t(($) => $.board.empty_column)}
            </p>
          )}
          {footer}
        </div>
      </div>
    </BoardColumnShell>
  );
});

/**
 * The presentational shell shared by the editable issues board column and the
 * read-only channel Tasks board column (#562): the fixed-width column container
 * plus the header row (heading on the left, optional actions on the right). The
 * body — a drag surface or a plain read-only card stack — is the child. One
 * definition keeps the two boards from drifting on column width / header
 * spacing; it carries NO drag / view-store wiring, so consumers add that around
 * it (the editable column wraps a droppable body, the channel board a scroller).
 */
export function BoardColumnShell({
  heading,
  actions,
  children,
  widthClassName,
}: {
  heading: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  /**
   * Overrides the column's fixed desktop width. The editable issues board omits
   * it, keeping the inline fixed `BOARD_COL_WIDTH` (unchanged). The read-only
   * channel Tasks board (#562 mobile) passes a responsive width class so columns
   * stack full-width on a phone and become the fixed 300px column at ≥768px.
   * When set, the inline fixed-width style is dropped so the class owns width.
   */
  widthClassName?: string;
}) {
  return (
    <div
      style={widthClassName ? undefined : { width: BOARD_COL_WIDTH }}
      className={cn("flex flex-col", widthClassName ?? "shrink-0")}
    >
      <div className="mb-2 flex items-center justify-between px-1.5">
        {heading}
        {actions}
      </div>
      {children}
    </div>
  );
}

export function BoardStatusHeading({ status, count }: { status: IssueStatus; count: number }) {
  const { t } = useT("issues");
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className={cn("size-2 shrink-0 rounded-full", BOARD_STATUS_DOT[status])} />
      <span className="truncate text-sm font-semibold text-foreground">{t(($) => $.status[status])}</span>
      <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[11px] font-medium tabular-nums text-muted-foreground">
        {count}
      </span>
    </div>
  );
}

function BoardGroupHeading({
  group,
  count,
}: {
  group: BoardColumnGroup;
  count: number;
}) {
  if (group.status) {
    return <BoardStatusHeading status={group.status} count={count} />;
  }

  const actorIcon =
    group.assigneeType && group.assigneeId ? (
      <ActorAvatar
        actorType={group.assigneeType}
        actorId={group.assigneeId}
        size={18}
        showStatusDot={group.assigneeType === "agent"}
      />
    ) : (
      <span className="flex size-[18px] shrink-0 items-center justify-center rounded-full bg-background text-muted-foreground">
        <UserMinus className="size-3.5" />
      </span>
    );

  return (
    <div className="flex min-w-0 items-center gap-2">
      {actorIcon}
      <span className="truncate text-sm font-medium" title={group.title}>
        {group.title}
      </span>
      <span className="shrink-0 rounded-full bg-background px-1.5 py-0.5 text-[11px] font-medium tabular-nums text-muted-foreground">
        {count}
      </span>
    </div>
  );
}
