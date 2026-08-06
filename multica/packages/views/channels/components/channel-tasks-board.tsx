"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Columns3, List } from "lucide-react";
import { channelIssuesInfiniteOptions, channelProjectOptions } from "@multica/core/channels";
import type { Issue, IssueStatus } from "@multica/core/types";
import { BOARD_STATUSES } from "@multica/core/issues/config";
import { myIssueListOptions, type MyIssuesFilter } from "@multica/core/issues/queries";
import { useLoadMoreByStatus } from "@multica/core/issues/mutations";
import { createIssueViewStore } from "@multica/core/issues/stores/view-store";
import { ViewStoreProvider } from "@multica/core/issues/stores/view-store-context";
import { useWorkspacePaths } from "@multica/core/paths";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@multica/ui/components/ui/toggle-group";
import { cn } from "@multica/ui/lib/utils";
import { useIsNarrow } from "../hooks/use-is-narrow";
import { AppLink } from "../../navigation";
import { ActorAvatar } from "../../common/actor-avatar";
import { BoardCardContent } from "../../issues/components/board-card";
import { BoardColumnShell, BoardStatusHeading } from "../../issues/components/board-column";
import { PriorityIcon } from "../../issues/components/priority-icon";
import { StatusHeading } from "../../issues/components/status-heading";
import { buildBoardGroups, buildColumns } from "../../issues/utils/drag-utils";
import { LabelChip } from "../../labels/label-chip";
import { ProjectChip } from "../../projects/components/project-chip";
import { useT } from "../../i18n";

/** Which set of issues the Tasks tab is currently showing. "group" (the
 *  default, always) is today's behavior — issues created from a source
 *  message in THIS channel. "project" widens the scope to every issue under
 *  the channel's bound project (#576 follow-up) — only offered when a
 *  project is actually bound. */
export type ChannelTasksScope = "group" | "project";

/** Presentational view mode for the channel Issues tab (LRM-553 / LRM-552 P1).
 *  Default stays Board; List is the compact single-row alternative. Session-local
 *  only — persistence is P3. */
export type ChannelTasksViewMode = "board" | "list";
/** Cache identity for the whole-project scope — deliberately the SAME
 *  `scope`/`filter` shape Project Detail's board uses
 *  (`project-detail.tsx`'s `projectScope`/`projectFilter`), so this view
 *  shares that exact cache entry rather than minting a parallel one. */
function projectMyIssuesOpts(projectId: string): { scope: string; filter: MyIssuesFilter } {
  return { scope: `project:${projectId}`, filter: { project_id: projectId } };
}

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

interface RenderedColumn {
  status: IssueStatus;
  issues: Issue[];
}

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
 * Compact List row (LRM-552 P1): priority · identifier · title · labels ·
 * assignee avatar. Read-only link out — same contract as `ChannelTaskCard`.
 * Hover uses `bg-sidebar` (LRM-545); focus uses `bg-primary/[0.08]`.
 */
function ChannelTaskListRow({ issue }: { issue: Issue }) {
  const paths = useWorkspacePaths();
  const labels = issue.labels ?? [];
  const showAssignee = !!issue.assignee_type && !!issue.assignee_id;
  return (
    <AppLink
      href={paths.issueDetail(issue.id)}
      className="group/row flex h-9 items-center gap-2 px-4 text-sm transition-colors hover:bg-sidebar focus-visible:bg-primary/[0.08] focus-visible:outline-none"
    >
      <PriorityIcon priority={issue.priority} />
      <span className="w-16 shrink-0 text-xs text-muted-foreground tabular-nums">
        {issue.identifier}
      </span>
      <span className="min-w-0 flex-1 truncate">{issue.title}</span>
      {labels.length > 0 ? (
        <span className="ml-1.5 hidden max-w-[260px] shrink-0 items-center gap-1 overflow-hidden md:inline-flex">
          {labels.slice(0, 3).map((label) => (
            <LabelChip key={label.id} label={label} />
          ))}
          {labels.length > 3 ? (
            <span className="text-[11px] text-muted-foreground">+{labels.length - 3}</span>
          ) : null}
        </span>
      ) : null}
      {showAssignee ? (
        <ActorAvatar
          actorType={issue.assignee_type!}
          actorId={issue.assignee_id!}
          size={24}
          enableHoverCard
        />
      ) : null}
    </AppLink>
  );
}

/**
 * List view body: status-grouped compact rows sharing the board's loaded
 * `columns` (same filter/scope source — switching views never refetches or
 * drops context). Mobile is a single full-width column (no right divider).
 */
function ChannelTasksListBody({ columns }: { columns: RenderedColumn[] }) {
  const nonEmpty = columns.filter((column) => column.issues.length > 0);
  return (
    <div className="flex-1 min-h-0 overflow-y-auto">
      {nonEmpty.map((column) => (
        <section key={column.status}>
          <div className="sticky top-0 z-10 flex h-8 items-center bg-sidebar px-4">
            <StatusHeading status={column.status} count={column.issues.length} />
          </div>
          <ul className="m-0 list-none p-0">
            {column.issues.map((issue) => (
              <li key={issue.id}>
                <ChannelTaskListRow issue={issue} />
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function ChannelTasksListSkeleton() {
  return (
    <div className="flex flex-1 min-h-0 flex-col overflow-y-auto py-1" aria-hidden>
      {Array.from({ length: 10 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-2 px-4">
          <Skeleton className="size-3.5 shrink-0 rounded" />
          <Skeleton className="h-3 w-12 shrink-0" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="size-6 shrink-0 rounded-full" />
        </div>
      ))}
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
 * arrangement changes. When narrow (≤768px) `widthClassName` makes the section
 * full-width and the body flows at content height; the narrow board renders just
 * ONE of these (the status picked by the segmented control, below) inside its own
 * vertical scroller — NOT all four stacked (that regressed to a grouped list).
 * At >768px (`min-[769px]:`) it is the fixed 300px column with its own internal
 * scroll, identical to the editable board's desktop columns.
 *
 * The CSS breakpoint is `min-[769px]:` (NOT `md:`/≥768) so it agrees with the JS
 * `useIsNarrow` single source of truth (≤768 = narrow, #685 closure): at exactly
 * 768 the JS renders the segmented tree AND the CSS must stay full-width — a `md:`
 * (768 ≥ 768) would snap this to 300px while `useIsNarrow` says narrow.
 */
function ChannelBoardColumn({
  status,
  issues,
  projectLoadMore,
}: {
  status: IssueStatus;
  issues: Issue[];
  /**
   * Present only in the "whole project" scope. Undefined in the (default)
   * group scope, which pages via ONE flat "Load more" bar below the whole
   * board instead (`ChannelTasksBoard`) — never both at once. When set, this
   * column pages independently via the same `useLoadMoreByStatus` mechanism
   * the real (editable) issues board uses, targeting the identical
   * `project:<id>` cache entry so the two views never drift apart.
   */
  projectLoadMore?: { scope: string; filter: MyIssuesFilter };
}) {
  const { t: tc } = useT("channels");
  const { loadMore, hasMore, isLoading, total } = useLoadMoreByStatus(status, projectLoadMore);
  const showLoadMore = !!projectLoadMore && hasMore;
  const remaining = Math.max(0, total - issues.length);
  // useMemo the heading element so it isn't a fresh JSX node on every render
  // (react:doctor `jsx-no-jsx-as-prop`).
  const heading = useMemo(
    () => <BoardStatusHeading status={status} count={issues.length} />,
    [status, issues.length],
  );
  return (
    // min-[769px]:w-[300px] must equal BOARD_COL_WIDTH (Tailwind needs a literal
    // class). Breakpoint is >768 (not md:/≥768) to match `useIsNarrow` (≤768).
    <BoardColumnShell heading={heading} widthClassName="w-full shrink-0 min-[769px]:w-[300px]">
      <div className="space-y-2 rounded-lg p-1 min-[769px]:min-h-[200px] min-[769px]:flex-1 min-[769px]:overflow-y-auto">
        {issues.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">{tc(($) => $.tasks.column_empty)}</p>
        ) : (
          issues.map((issue) => <ChannelTaskCard key={issue.id} issue={issue} />)
        )}
      </div>
      {showLoadMore ? (
        <div className="shrink-0 p-1">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="w-full text-xs text-muted-foreground"
            disabled={isLoading}
            onClick={() => void loadMore()}
          >
            {isLoading ? tc(($) => $.tasks.loading_more) : tc(($) => $.tasks.load_more, { count: remaining })}
          </Button>
        </div>
      ) : null}
    </BoardColumnShell>
  );
}

/**
 * Scope toggle shown above the board ONLY when the channel has a bound
 * project (#576 follow-up). "This group" (`scope_group`) is the default and
 * always available — today's channel-scoped behavior, byte-for-byte
 * unchanged. "Whole project" (`scope_project`) widens the Tasks tab to every
 * issue under the bound project; the project's name/icon rides along as
 * supplementary label text via the shared `ProjectChip` (never invents its
 * own chip styling), but the pill itself — not the chip — is what switches
 * scope. Disabled with a visible reason (never just a greyed hover tooltip —
 * matches `ChannelProjectSettingsPanel`'s honesty convention) when the
 * project-scoped query has errored, so a click can't lead into a silent 403.
 *
 * Built on this repo's shared `ToggleGroup`/`ToggleGroupItem` primitive
 * (single-select) rather than hand-rolled `<button aria-pressed>` pills —
 * that gets roving-tabindex arrow-key navigation and disabled handling for
 * free. Base UI's single-select `ToggleGroup` emits an EMPTY array from
 * `onValueChange` when the currently-pressed item is activated again
 * (re-toggle-off); that's ignored below so a scope is always selected —
 * this toggle can never end up with zero active scopes.
 *
 * Layout chrome (border/padding) lives on the shared toolbar host so this
 * can sit beside the List/Board view segment (LRM-553).
 */
function TasksScopeToggle({
  scope,
  onScopeChange,
  projectId,
  projectUnavailable,
}: {
  scope: ChannelTasksScope;
  onScopeChange: (scope: ChannelTasksScope) => void;
  projectId: string;
  projectUnavailable: boolean;
}) {
  const { t } = useT("channels");
  return (
    <div className="min-w-0">
      <ToggleGroup
        value={[scope]}
        onValueChange={(next) => {
          const [nextScope] = next;
          if (!nextScope) return; // re-click of the active item — stay pinned, never 0 selected.
          onScopeChange(nextScope as ChannelTasksScope);
        }}
        aria-label={t(($) => $.tasks.scope_toggle_aria)}
        variant="outline"
        size="sm"
        className="w-fit"
      >
        <ToggleGroupItem value="group">{t(($) => $.tasks.scope_group)}</ToggleGroupItem>
        <ToggleGroupItem
          value="project"
          disabled={projectUnavailable}
          className="inline-flex items-center gap-1.5"
        >
          <span>{t(($) => $.tasks.scope_project)}</span>
          <ProjectChip projectId={projectId} className="pointer-events-none" />
        </ToggleGroupItem>
      </ToggleGroup>
      {projectUnavailable ? (
        <p className="mt-1.5 text-xs text-muted-foreground">{t(($) => $.tasks.scope_project_unavailable)}</p>
      ) : null}
    </div>
  );
}

/**
 * List / Board segment control (LRM-552 P1): `bg-accent` track + selected
 * `bg-background` + `font-medium`. Shares the toolbar with the scope toggle;
 * switching never resets scope or reloads data. Same ToggleGroup contract as
 * the scope pills (re-click of the active item stays pinned).
 */
function TasksViewToggle({
  viewMode,
  onViewModeChange,
}: {
  viewMode: ChannelTasksViewMode;
  onViewModeChange: (mode: ChannelTasksViewMode) => void;
}) {
  const { t } = useT("channels");
  return (
    <ToggleGroup
      value={[viewMode]}
      onValueChange={(next) => {
        const [nextMode] = next;
        if (!nextMode) return;
        onViewModeChange(nextMode as ChannelTasksViewMode);
      }}
      aria-label={t(($) => $.tasks.view_toggle_aria)}
      size="sm"
      className="shrink-0 gap-0.5 rounded-md bg-accent p-0.5"
    >
      <ToggleGroupItem
        value="list"
        className={cn(
          "inline-flex items-center gap-1.5 rounded-sm border-0 px-2.5 py-1 text-xs shadow-none",
          viewMode === "list"
            ? "bg-background font-medium text-foreground shadow-sm"
            : "bg-transparent text-muted-foreground hover:bg-transparent hover:text-foreground",
        )}
      >
        <List className="size-3.5" aria-hidden />
        {t(($) => $.tasks.view_list)}
      </ToggleGroupItem>
      <ToggleGroupItem
        value="board"
        className={cn(
          "inline-flex items-center gap-1.5 rounded-sm border-0 px-2.5 py-1 text-xs shadow-none",
          viewMode === "board"
            ? "bg-background font-medium text-foreground shadow-sm"
            : "bg-transparent text-muted-foreground hover:bg-transparent hover:text-foreground",
        )}
      >
        <Columns3 className="size-3.5" aria-hidden />
        {t(($) => $.tasks.view_board)}
      </ToggleGroupItem>
    </ToggleGroup>
  );
}

/**
 * The channel Tasks TAB (#562, scope-toggle added #576 follow-up; List view
 * + view segment LRM-553): by default, the tasks created from a source
 * message in THIS channel, presented full-width as a real status board —
 * horizontal status columns (`BOARD_STATUSES`) + the issues board's card.
 * When the channel/group has a project bound, a scope toggle lets the view
 * widen to every issue under that project instead (never both mixed in one
 * render — the active scope's query is the SINGLE source columns are built
 * from). A List/Board segment switches presentation over the same loaded set.
 *
 * Composed from the board's presentational pieces rather than reusing
 * `board-view.tsx` directly: the board's status columns are hard-coupled to the
 * workspace/scoped `byStatus` issues cache, which would show the wrong
 * workspace totals and load-more the wrong data for the (default) channel
 * scope. This never touches the global issues view store, never writes back
 * a global filter, and the cards are read-only.
 *
 * Group scope pages via `channelIssuesInfiniteOptions` — the #684 endpoint
 * caps a page at 100 and returns `total`, so this pages through the whole
 * set and groups only the ALREADY-LOADED issues; a single "Load more" bar
 * appends the next offset page rather than truncating at 100. Project scope
 * instead rides the SAME bucketed `myIssueListOptions`/`useLoadMoreByStatus`
 * mechanism Project Detail's own board uses (`project:<id>` scope/filter) —
 * scoped server-side by `project_id`, never derived by client-side grouping
 * — with each status column paginating independently.
 */
export function ChannelTasksBoard({ channelId }: { channelId: string }) {
  const { t } = useT("channels");
  const isNarrow = useIsNarrow();
  const wsId = useWorkspaceId();

  // The channel/group's bound project — the SAME read the group settings
  // panel's project picker resolves (`channelProjectOptions`). "" = unbound.
  // Group and project are independent explicit properties (#576 contract):
  // this is never inferred from the loaded issues themselves.
  const { data: projectId = "" } = useQuery(channelProjectOptions(wsId, channelId));
  const hasProject = !!projectId;

  // "This group" (default, always) vs "Whole project" (only offered when a
  // project is bound). Resets to the default whenever the active channel
  // changes — a per-channel view, not a sticky global mode (mirrors the
  // `channelView` reset in channels-page.tsx: "adjust state during render"
  // against a ref, rather than an effect, so there's no extra render).
  const [scope, setScope] = useState<ChannelTasksScope>("group");
  const scopeChannelIdRef = useRef(channelId);
  if (scopeChannelIdRef.current !== channelId) {
    scopeChannelIdRef.current = channelId;
    setScope("group");
  }
  const isProjectScope = scope === "project" && hasProject;

  // List / Board (LRM-553). Default Board per LRM-552 lock. Session-local —
  // survives channel switches so the user's view preference isn't dropped;
  // persistence across reloads is P3.
  const [viewMode, setViewMode] = useState<ChannelTasksViewMode>("board");
  const isListView = viewMode === "list";

  // Mobile-only: which status column the segmented control has selected. `null`
  // = "follow the default" (first non-empty status) — derived below, not seeded
  // by an effect, so the default tracks the loaded set without an extra render.
  const [selectedStatus, setSelectedStatus] = useState<IssueStatus | null>(null);
  // The segmented pill row is horizontally scrollable and the default selection
  // is the first NON-empty status, which can sit off-screen to the right while
  // the row is scrolled left. Ref the active pill so we can bring it into view.
  const activePillRef = useRef<HTMLButtonElement>(null);

  const {
    data: channelData,
    isPending: channelPending,
    isError: channelIsError,
    hasNextPage,
    fetchNextPage,
    isFetchingNextPage,
    refetch: refetchChannel,
  } = useInfiniteQuery(channelIssuesInfiniteOptions(channelId));

  const groupLoadedIssues = useMemo(
    () => (channelData?.pages ?? []).flatMap((page) => page.issues),
    [channelData?.pages],
  );

  // Runs whenever a project is bound — not just while "Whole project" is the
  // active scope — so an access error surfaces BEFORE the toggle is clicked:
  // the toggle disables itself instead of leading into a silent 403. Shares
  // Project Detail's exact `project:<id>` cache identity, so this never
  // mints a second source of truth for the same project's issues.
  const projectOpts = hasProject ? projectMyIssuesOpts(projectId) : undefined;
  const {
    data: projectIssuesData,
    isPending: projectIssuesPending,
    isError: projectIssuesError,
    refetch: refetchProject,
  } = useQuery({
    ...myIssueListOptions(wsId, projectOpts?.scope ?? "", projectOpts?.filter ?? {}),
    enabled: !!projectOpts,
  });
  const projectUnavailable = hasProject && projectIssuesError;

  // The single source columns are built from — whichever scope is active.
  // The empty state, pending/error state and count all read this same value,
  // so they can never disagree about which scope they're describing. Memoized
  // so the `columns` useMemo below doesn't see a fresh array identity (and
  // thus re-derive) on every render while nothing scope-relevant changed.
  const activeIssues = useMemo(
    () => (isProjectScope ? (projectIssuesData ?? []) : groupLoadedIssues),
    [isProjectScope, projectIssuesData, groupLoadedIssues],
  );
  const isPending = isProjectScope ? projectIssuesPending : channelPending;
  const isError = isProjectScope ? projectIssuesError : channelIsError;

  const columns = useMemo<RenderedColumn[]>(() => {
    // Reuse the board's grouping definition: one group per board status, in
    // order, then map the loaded issue ids into each column. Purely a
    // presentational grouping of `activeIssues` — the scope switch above is
    // what picked the server-scoped source; this never re-derives scope.
    const groups = buildBoardGroups(activeIssues, BOARD_STATUSES, "status", NO_ACTOR_NAME, NO_ASSIGNEE_LABEL);
    const byGroup = buildColumns(activeIssues, groups, "status");
    const byId = new Map(activeIssues.map((issue) => [issue.id, issue]));
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
  }, [activeIssues]);

  const total = channelData?.pages[0]?.total ?? 0;
  const remaining = Math.max(0, total - groupLoadedIssues.length);

  // Mobile segmented control default lands on the first status that HAS issues
  // (better UX than an empty column); switching pills never refetches — it just
  // re-slices the same loaded set. Derived here (above the early returns) so the
  // scroll-into-view effect below keeps a stable hook order.
  const activeStatus =
    selectedStatus ?? columns.find((column) => column.issues.length > 0)?.status ?? columns[0]?.status;
  const activeColumn = columns.find((column) => column.status === activeStatus);

  // Bring the selected pill into view within the pill row (not the page) once the
  // default selection resolves and whenever it changes — otherwise a right-side
  // default pill stays hidden behind the left-anchored scroll. `inline: "center"`
  // centers it in the horizontal scroller; `block: "nearest"` avoids page scroll.
  useEffect(() => {
    if (!isNarrow || isListView || !activeStatus) return;
    activePillRef.current?.scrollIntoView({ inline: "center", block: "nearest" });
  }, [isNarrow, isListView, activeStatus]);

  const handleRetry = () => {
    if (isProjectScope) {
      void refetchProject();
    } else {
      void refetchChannel();
    }
  };

  // Toolbar always hosts the List/Board segment (right). Scope toggle (left)
  // only when a project is bound. Present in every state so switching view /
  // scope is never gated behind the other mode's data resolving first.
  const toolbar = (
    <div className="flex shrink-0 items-start justify-between gap-3 border-b border-border/40 px-4 py-2">
      {hasProject ? (
        <TasksScopeToggle
          scope={scope}
          onScopeChange={setScope}
          projectId={projectId}
          projectUnavailable={projectUnavailable}
        />
      ) : (
        <span />
      )}
      <TasksViewToggle viewMode={viewMode} onViewModeChange={setViewMode} />
    </div>
  );

  const loadMoreBar =
    !isProjectScope && hasNextPage ? (
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
    ) : null;

  let body: ReactNode;
  if (isPending) {
    body = isListView ? (
      <ChannelTasksListSkeleton />
    ) : (
      <div className="flex flex-1 min-h-0 flex-col gap-4 overflow-y-auto p-4 min-[769px]:flex-row min-[769px]:overflow-hidden">
        {Array.from({ length: 4 }).map((_, col) => (
          <div key={col} className="flex w-full shrink-0 flex-col gap-2 min-[769px]:w-[300px]">
            <Skeleton className="h-5 w-24" />
            <Skeleton className="h-20" />
            <Skeleton className="h-20" />
          </div>
        ))}
      </div>
    );
  } else if (isError) {
    body = (
      <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-3 px-4">
        <p className="text-sm text-muted-foreground">{t(($) => $.tasks.error)}</p>
        <Button type="button" variant="outline" size="sm" onClick={handleRetry}>
          {t(($) => $.tasks.retry)}
        </Button>
      </div>
    );
  } else if (activeIssues.length === 0) {
    body = (
      <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-1 px-4 text-center">
        <p className="text-sm text-foreground">
          {isProjectScope ? t(($) => $.tasks.empty_project) : t(($) => $.tasks.empty)}
        </p>
        <p className="text-xs text-muted-foreground">
          {isProjectScope ? t(($) => $.tasks.empty_project_hint) : t(($) => $.tasks.empty_hint)}
        </p>
      </div>
    );
  } else if (isListView) {
    body = (
      <>
        <ChannelTasksListBody columns={columns} />
        {loadMoreBar}
      </>
    );
  } else {
    // Mobile segmented control: one pill per status the desktop board shows —
    // ALL of them, in BOARD_STATUSES order (Iris ruling: pills mirror the desktop
    // columns exactly, including empty statuses with a `0` count). The selected
    // column below shows that status's cards (or its empty state).
    body = (
      <>
        {isNarrow ? (
          <>
            {/* Horizontally-scrollable status selector — keeps the "board" model
                on a phone (pick a column) instead of stacking all four. Mirrors
                the desktop columns exactly: a pill for EVERY status, count and all. */}
            <div className="flex shrink-0 gap-1 overflow-x-auto border-b border-border/40 px-4 py-2">
              {columns.map((column) => {
                const isActive = column.status === activeStatus;
                return (
                  <button
                    key={column.status}
                    ref={isActive ? activePillRef : undefined}
                    type="button"
                    aria-pressed={isActive}
                    onClick={() => setSelectedStatus(column.status)}
                    className={cn(
                      "shrink-0 rounded-full border px-3 py-1 transition-colors",
                      isActive
                        ? "border-brand bg-accent"
                        : "border-transparent bg-muted/40 hover:bg-accent/60",
                    )}
                  >
                    <BoardStatusHeading status={column.status} count={column.issues.length} />
                  </button>
                );
              })}
            </div>
            <div className="flex-1 min-h-0 overflow-y-auto p-4">
              {activeColumn ? (
                <ChannelBoardColumn
                  status={activeColumn.status}
                  issues={activeColumn.issues}
                  projectLoadMore={isProjectScope ? projectOpts : undefined}
                />
              ) : null}
            </div>
          </>
        ) : (
          <div className="flex flex-1 min-h-0 flex-col gap-4 overflow-y-auto p-4 min-[769px]:flex-row min-[769px]:overflow-x-auto min-[769px]:overflow-y-visible">
            {columns.map((column) => (
              <ChannelBoardColumn
                key={column.status}
                status={column.status}
                issues={column.issues}
                projectLoadMore={isProjectScope ? projectOpts : undefined}
              />
            ))}
          </div>
        )}
        {/* Group scope's single flat "Load more" bar — project scope paginates
            per-column instead (see `ChannelBoardColumn`'s own footer button),
            so this never shows while `isProjectScope`. */}
        {loadMoreBar}
      </>
    );
  }

  return (
    <ViewStoreProvider store={channelTasksViewStore}>
      <div className="flex flex-1 min-h-0 flex-col">
        {toolbar}
        {body}
      </div>
    </ViewStoreProvider>
  );
}
