"use client";

import { useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { channelIssuesInfiniteOptions } from "@multica/core/channels";
import type { Issue, IssueStatus } from "@multica/core/types";
import { BOARD_STATUSES } from "@multica/core/issues/config";
import { createIssueViewStore } from "@multica/core/issues/stores/view-store";
import { ViewStoreProvider } from "@multica/core/issues/stores/view-store-context";
import { useWorkspacePaths } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { AppLink } from "../../navigation";
import { BoardCardContent } from "../../issues/components/board-card";
import { BOARD_STATUS_DOT } from "../../issues/components/board-status-dot";
import { BOARD_COL_WIDTH } from "../../issues/components/board-column";
import { buildBoardGroups, buildColumns } from "../../issues/utils/drag-utils";
import { useT } from "../../i18n";

// Status grouping never resolves actor names (that's the assignee-grouping
// path), so the shared `buildBoardGroups` gets a no-op resolver + label.
const NO_ACTOR_NAME = () => "";
const NO_ASSIGNEE_LABEL = "";

// An isolated view-store instance — deliberately NOT the global issues view
// store. It only supplies `BoardCardContent`'s `cardProperties` reads with
// defaults so the REAL board card renders (read-only) here, without ever
// touching or mutating the workspace issues board's filters / grouping / sort.
// Module-scope + a read-only surface makes sharing one instance safe (mirrors
// project-detail's `projectViewStore` and `actorIssuesViewStore`).
const channelTasksViewStore = createIssueViewStore("multica_channel_tasks_view");

/**
 * A channel-scoped task card. Reuses the issues board's real card
 * (`BoardCardContent`) read-only so this visually IS the issues board card,
 * wrapped in a link to the task detail — the channel is the discussion context,
 * not the task owner (1:1 source relation), so the card never edits/duplicates
 * task state, it links out to it. `editable` is left false: no inline pickers,
 * no update mutation, no drag — nothing that could write back.
 */
function ChannelTaskCard({ issue }: { issue: Issue }) {
  const paths = useWorkspacePaths();
  return (
    <div className="group/card">
      <AppLink href={paths.issueDetail(issue.id)} className="group block">
        <BoardCardContent issue={issue} />
      </AppLink>
    </div>
  );
}

/**
 * One horizontal status column: the board's status header (dot + localized
 * status label + count) reused via `BOARD_STATUS_DOT` and the issues `status`
 * locale, with its task cards stacked below at the real board column width.
 */
function ChannelBoardColumn({ status, issues }: { status: IssueStatus; issues: Issue[] }) {
  const { t } = useT("issues");
  const { t: tc } = useT("channels");
  return (
    <div style={{ width: BOARD_COL_WIDTH }} className="flex shrink-0 flex-col">
      <div className="mb-2 flex items-center gap-2 px-1.5">
        <span className={cn("size-2 shrink-0 rounded-full", BOARD_STATUS_DOT[status])} />
        <span className="truncate text-sm font-semibold text-foreground">{t(($) => $.status[status])}</span>
        <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[11px] font-medium tabular-nums text-muted-foreground">
          {issues.length}
        </span>
      </div>
      <div className="min-h-[200px] flex-1 space-y-2 overflow-y-auto rounded-lg p-1">
        {issues.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">{tc(($) => $.tasks.column_empty)}</p>
        ) : (
          issues.map((issue) => <ChannelTaskCard key={issue.id} issue={issue} />)
        )}
      </div>
    </div>
  );
}

interface RenderedColumn {
  status: IssueStatus;
  issues: Issue[];
}

/**
 * The channel Tasks TAB (#562): the tasks created from a source message in THIS
 * channel, presented full-width as a real status board — horizontal status
 * columns (`BOARD_STATUSES`) + the issues board's card, fixed-scoped to the
 * channel via `channelId`.
 *
 * Composed from the board's presentational pieces rather than reusing
 * `board-view.tsx` directly: the board's status columns are hard-coupled to the
 * workspace/scoped `byStatus` issues cache (`useLoadMoreByStatus` paginates via
 * `api.listIssues`), which would show the wrong workspace totals and load-more
 * the wrong data for a channel scope. This never touches the global issues view
 * store, never writes back a global filter, and the cards are read-only.
 *
 * The #684 endpoint caps a page at 100 and returns `total`, so this pages via
 * `channelIssuesInfiniteOptions` and groups only the ALREADY-LOADED set — a
 * single "load more" appends the next offset page rather than truncating at 100.
 */
export function ChannelTasksBoard({ channelId }: { channelId: string }) {
  const { t } = useT("channels");
  const { data, isPending, isError, hasNextPage, fetchNextPage, isFetchingNextPage } =
    useInfiniteQuery(channelIssuesInfiniteOptions(channelId));

  const loadedIssues = useMemo(
    () => (data?.pages ?? []).flatMap((page) => page.issues),
    [data?.pages],
  );

  const columns = useMemo<RenderedColumn[]>(() => {
    // Reuse the board's grouping definition: one group per board status, in
    // order, then map the loaded issue ids into each column.
    const groups = buildBoardGroups(loadedIssues, BOARD_STATUSES, "status", NO_ACTOR_NAME, NO_ASSIGNEE_LABEL);
    const byGroup = buildColumns(loadedIssues, groups, "status");
    const byId = new Map(loadedIssues.map((issue) => [issue.id, issue]));
    return groups.flatMap((group) =>
      group.status
        ? [
            {
              status: group.status,
              issues: (byGroup[group.id] ?? []).flatMap((id) => {
                const issue = byId.get(id);
                return issue ? [issue] : [];
              }),
            },
          ]
        : [],
    );
  }, [loadedIssues]);

  const total = data?.pages[0]?.total ?? 0;
  const remaining = Math.max(0, total - loadedIssues.length);

  if (isPending) {
    return (
      <div className="flex flex-1 min-h-0 gap-4 overflow-hidden p-4">
        {Array.from({ length: 4 }).map((_, col) => (
          <div key={col} style={{ width: BOARD_COL_WIDTH }} className="flex shrink-0 flex-col gap-2">
            <Skeleton className="h-5 w-24" />
            <Skeleton className="h-20" />
            <Skeleton className="h-20" />
          </div>
        ))}
      </div>
    );
  }

  if (isError) {
    return (
      <div className="flex flex-1 min-h-0 items-center justify-center">
        <p className="text-sm text-muted-foreground">{t(($) => $.tasks.error)}</p>
      </div>
    );
  }

  if (loadedIssues.length === 0) {
    return (
      <div className="flex flex-1 min-h-0 items-center justify-center">
        <p className="text-sm text-muted-foreground">{t(($) => $.tasks.empty)}</p>
      </div>
    );
  }

  return (
    <ViewStoreProvider store={channelTasksViewStore}>
      <div className="flex flex-1 min-h-0 flex-col">
        <div className="flex flex-1 min-h-0 gap-4 overflow-x-auto p-4">
          {columns.map((column) => (
            <ChannelBoardColumn key={column.status} status={column.status} issues={column.issues} />
          ))}
        </div>
        {hasNextPage ? (
          <div className="shrink-0 border-t border-border/40 px-4 py-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="text-xs text-muted-foreground"
              disabled={isFetchingNextPage}
              onClick={() => void fetchNextPage()}
            >
              {isFetchingNextPage
                ? t(($) => $.tasks.loading_more)
                : t(($) => $.tasks.load_more, { count: remaining })}
            </Button>
          </div>
        ) : null}
      </div>
    </ViewStoreProvider>
  );
}
