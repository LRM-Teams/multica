"use client";

import { useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { channelIssuesInfiniteOptions } from "@multica/core/channels";
import type { Issue, IssueStatus } from "@multica/core/types";
import { BOARD_STATUSES } from "@multica/core/issues/config";
import { createIssueViewStore } from "@multica/core/issues/stores/view-store";
import { ViewStoreProvider } from "@multica/core/issues/stores/view-store-context";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { AppLink } from "../../navigation";
import { BoardCardContent } from "../../issues/components/board-card";
import { BoardColumnShell, BoardStatusHeading } from "../../issues/components/board-column";
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
 * One read-only status section, reusing the board's shared shell +
 * `BoardStatusHeading` (dot + localized status label + count) verbatim from the
 * editable issues board, with its task cards stacked below. No drag / view-store
 * wiring — just the presentational shell around a plain stack of read-only cards.
 *
 * Responsive (#562 mobile, Iris-gated): the SAME shell/heading/card — only the
 * arrangement changes. On mobile (<768px) `widthClassName` makes the section
 * full-width and the body flows at content height so the whole board scrolls as
 * one vertical stack; at ≥768px (`md:`) it is the fixed 300px column with its own
 * internal scroll, identical to the editable board's desktop columns.
 */
function ChannelBoardColumn({ status, issues }: { status: IssueStatus; issues: Issue[] }) {
  const { t: tc } = useT("channels");
  // useMemo the heading element so it isn't a fresh JSX node on every render
  // (react:doctor `jsx-no-jsx-as-prop`).
  const heading = useMemo(
    () => <BoardStatusHeading status={status} count={issues.length} />,
    [status, issues.length],
  );
  return (
    // md:w-[300px] must equal BOARD_COL_WIDTH (Tailwind needs a literal class).
    <BoardColumnShell heading={heading} widthClassName="w-full shrink-0 md:w-[300px]">
      <div className="space-y-2 rounded-lg p-1 md:min-h-[200px] md:flex-1 md:overflow-y-auto">
        {issues.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">{tc(($) => $.tasks.column_empty)}</p>
        ) : (
          issues.map((issue) => <ChannelTaskCard key={issue.id} issue={issue} />)
        )}
      </div>
    </BoardColumnShell>
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
      <div className="flex flex-1 min-h-0 flex-col gap-4 overflow-y-auto p-4 md:flex-row md:overflow-hidden">
        {Array.from({ length: 4 }).map((_, col) => (
          <div key={col} className="flex w-full shrink-0 flex-col gap-2 md:w-[300px]">
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
        <div className="flex flex-1 min-h-0 flex-col gap-4 overflow-y-auto p-4 md:flex-row md:overflow-x-auto md:overflow-y-visible">
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
