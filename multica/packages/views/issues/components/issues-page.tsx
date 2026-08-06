"use client";

import { useCallback, useEffect, useMemo } from "react";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ListTodo, Plus } from "lucide-react";
import type { UpdateIssueRequest } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useModalStore } from "@multica/core/modals";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useQuery } from "@tanstack/react-query";
import { useIssueViewStore, useClearFiltersOnWorkspaceChange } from "@multica/core/issues/stores/view-store";
import { useIssuesScopeStore } from "@multica/core/issues/stores/issues-scope-store";
import { ViewStoreProvider } from "@multica/core/issues/stores/view-store-context";
import { filterIssues } from "../utils/filter";
import { BOARD_STATUSES } from "@multica/core/issues/config";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueAssigneeGroupsOptions, issueListOptions, childIssueProgressOptions, type AssigneeGroupedIssuesFilter } from "@multica/core/issues/queries";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useUpdateIssue } from "@multica/core/issues/mutations";
import { useIssueSelectionStore } from "@multica/core/issues/stores/selection-store";
import { PageHeader } from "../../layout/page-header";
import { IssuesHeader, ViewToggle } from "./issues-header";
import { BoardView } from "./board-view";
import { ListView } from "./list-view";
import { SwimLaneView } from "./swimlane-view";
import { BatchActionToolbar } from "./batch-action-toolbar";
import type { ChildProgress } from "./list-row";
import { useT } from "../../i18n";

const EMPTY_CHILD_PROGRESS = new Map<string, ChildProgress>();

export function IssuesPage() {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();

  const scope = useIssuesScopeStore((s) => s.scope);
  const viewMode = useIssueViewStore((s) => s.viewMode);
  const grouping = useIssueViewStore((s) => s.grouping);
  const statusFilters = useIssueViewStore((s) => s.statusFilters);
  const priorityFilters = useIssueViewStore((s) => s.priorityFilters);
  const assigneeFilters = useIssueViewStore((s) => s.assigneeFilters);
  const includeNoAssignee = useIssueViewStore((s) => s.includeNoAssignee);
  const creatorFilters = useIssueViewStore((s) => s.creatorFilters);
  const projectFilters = useIssueViewStore((s) => s.projectFilters);
  const includeNoProject = useIssueViewStore((s) => s.includeNoProject);
  const labelFilters = useIssueViewStore((s) => s.labelFilters);
  const sortBy = useIssueViewStore((s) => s.sortBy);
  const sortDirection = useIssueViewStore((s) => s.sortDirection);
  const agentRunningFilter = useIssueViewStore((s) => s.agentRunningFilter);
  const sourceChannelId = useIssueViewStore((s) => s.sourceChannelId);
  const usesAssigneeBoard = viewMode === "board" && grouping === "assignee";

  const sort = useMemo(
    () => ({
      sort_by: sortBy,
      sort_direction: sortBy !== "position" ? sortDirection : undefined,
    } as const),
    [sortBy, sortDirection],
  );

  // Derive the set of issue ids that currently have at least one
  // `running` agent task. Used by the workspace agents-working filter
  // chip. Subscribing the page here (not deep in filter.ts) keeps the
  // filter pure and lets the snapshot stay cached at one workspace-
  // scoped place — every issue card already subscribes for its own
  // indicator, so this is a no-op extra fetch.
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));
  const runningIssueIds = useMemo(() => {
    const ids = new Set<string>();
    for (const t of snapshot) {
      if (t.status === "running" && t.issue_id) ids.add(t.issue_id);
    }
    return ids;
  }, [snapshot]);

  const assigneeGroupFilter = useMemo<AssigneeGroupedIssuesFilter>(() => {
    const filter: AssigneeGroupedIssuesFilter = {
      statuses: statusFilters.length > 0 ? statusFilters : [...BOARD_STATUSES],
      priorities: priorityFilters,
      assignee_filters: assigneeFilters,
      include_no_assignee: includeNoAssignee,
      creator_filters: creatorFilters,
      project_ids: projectFilters,
      include_no_project: includeNoProject,
      label_ids: labelFilters,
    };
    if (scope === "members") filter.assignee_types = ["member"];
    if (scope === "agents") filter.assignee_types = ["agent"];
    return filter;
  }, [assigneeFilters, creatorFilters, includeNoAssignee, includeNoProject, labelFilters, priorityFilters, projectFilters, scope, statusFilters]);

  const assigneeGroupsOptions = issueAssigneeGroupsOptions(wsId, assigneeGroupFilter, sort);
  // Source-channel filter (#476) is server-side — the list response has no
  // anchor data to filter on the client, so it threads into the query itself.
  // v0 covers the status board + list + gantt + swimlane (all use this query);
  // the assignee-grouped board hits a separate endpoint and is a follow-up.
  const statusIssuesQuery = useQuery({
    ...issueListOptions(wsId, sort, sourceChannelId ?? undefined),
    enabled: !usesAssigneeBoard,
  });
  const assigneeGroupsQuery = useQuery({
    ...assigneeGroupsOptions,
    enabled: usesAssigneeBoard,
  });
  const allIssues = useMemo(
    () => statusIssuesQuery.data ?? [],
    [statusIssuesQuery.data],
  );
  const assigneeIssues = useMemo(
    () => assigneeGroupsQuery.data?.groups.flatMap((group) => group.issues) ?? [],
    [assigneeGroupsQuery.data],
  );
  const loading = usesAssigneeBoard
    ? assigneeGroupsQuery.isLoading
    : statusIssuesQuery.isLoading;

  // Clear filter state when switching between workspaces (URL-driven).
  useClearFiltersOnWorkspaceChange(useIssueViewStore, wsId);

  useEffect(() => {
    useIssueSelectionStore.getState().clear();
  }, [viewMode, scope]);

  // Scope pre-filter: narrow by assignee type
  const scopedIssues = useMemo(() => {
    if (scope === "members")
      return allIssues.filter((i) => i.assignee_type === "member");
    if (scope === "agents")
      return allIssues.filter((i) => i.assignee_type === "agent");
    return allIssues;
  }, [allIssues, scope]);

  const headerIssues = usesAssigneeBoard ? assigneeIssues : scopedIssues;

  const issues = useMemo(
    () => filterIssues(scopedIssues, { statusFilters, priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, projectFilters, includeNoProject, labelFilters, agentRunningFilter, runningIssueIds }),
    [scopedIssues, statusFilters, priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, projectFilters, includeNoProject, labelFilters, agentRunningFilter, runningIssueIds],
  );

  // Status-unfiltered companion for Swimlane — same narrowing as `issues`
  // minus the status filter.
  const swimlaneIssues = useMemo(
    () => filterIssues(scopedIssues, { statusFilters: [], priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, projectFilters, includeNoProject, labelFilters, agentRunningFilter, runningIssueIds }),
    [scopedIssues, priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, projectFilters, includeNoProject, labelFilters, agentRunningFilter, runningIssueIds],
  );

  const activeFilters = useMemo(() => ({
    priorityFilters,
    assigneeFilters,
    includeNoAssignee,
    creatorFilters,
    projectFilters,
    includeNoProject,
    labelFilters,
    agentRunningFilter,
  }), [
    priorityFilters,
    assigneeFilters,
    includeNoAssignee,
    creatorFilters,
    projectFilters,
    includeNoProject,
    labelFilters,
    agentRunningFilter,
  ]);

  // Fetch sub-issue progress from the backend so counts are accurate
  // regardless of client-side pagination or filtering of done issues.
  const { data: childProgressMap = EMPTY_CHILD_PROGRESS } = useQuery(childIssueProgressOptions(wsId));

  const visibleStatuses = useMemo(() => {
    if (statusFilters.length > 0)
      return BOARD_STATUSES.filter((s) => statusFilters.includes(s));
    return BOARD_STATUSES;
  }, [statusFilters]);

  const hiddenStatuses = useMemo(() => {
    return BOARD_STATUSES.filter((s) => !visibleStatuses.includes(s));
  }, [visibleStatuses]);

  const updateIssueMutation = useUpdateIssue();
  const handleMoveIssue = useCallback(
    (issueId: string, updates: Pick<UpdateIssueRequest, "status" | "assignee_type" | "assignee_id" | "position" | "parent_issue_id">, onSettled?: () => void) => {
      updateIssueMutation.mutate(
        { id: issueId, ...updates },
        {
          onError: (err) =>
            showErrorToast(
              err instanceof Error && err.message
                ? err.message
                : t(($) => $.page.move_failed),
            ),
          onSettled: () => onSettled?.(),
        },
      );
    },
    [updateIssueMutation, t],
  );

  const contentSkeleton = viewMode === "list" ? (
    <div className="flex-1 min-h-0 overflow-y-auto p-2 space-y-1">
      {Array.from({ length: 4 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full rounded-lg" />
      ))}
    </div>
  ) : (
    <div className="flex flex-1 min-h-0 gap-4 overflow-x-auto p-4">
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="flex min-w-52 flex-1 flex-col gap-2">
          <Skeleton className="h-4 w-20" />
          <Skeleton className="h-24 w-full rounded-lg" />
          <Skeleton className="h-24 w-full rounded-lg" />
        </div>
      ))}
    </div>
  );

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <ViewStoreProvider store={useIssueViewStore}>
        <PageHeader className="justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <ListTodo className="h-[18px] w-[18px] text-foreground" />
            <h1 className="text-base font-semibold">{t(($) => $.page.breadcrumb_title)}</h1>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <ViewToggle />
            <Button
              size="sm"
              className="gap-1.5"
              onClick={() => useModalStore.getState().open("create-issue")}
            >
              <Plus className="size-4" />
              <span className="hidden sm:inline">{t(($) => $.page.new_issue)}</span>
            </Button>
          </div>
        </PageHeader>

        <IssuesHeader scopedIssues={headerIssues} hideViewToggle />

        {loading ? contentSkeleton : headerIssues.length === 0 ? (
          <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-2 text-muted-foreground">
            <ListTodo className="h-10 w-10 text-muted-foreground/40" />
            <p className="text-sm">{t(($) => $.page.empty_title)}</p>
            <p className="text-xs">{t(($) => $.page.empty_hint)}</p>
          </div>
        ) : (
          <div className="flex flex-col flex-1 min-h-0">
            {viewMode === "board" ? (
              <BoardView
                issues={usesAssigneeBoard ? assigneeIssues : issues}
                assigneeGroups={usesAssigneeBoard ? assigneeGroupsQuery.data?.groups : undefined}
                assigneeGroupQueryKey={usesAssigneeBoard ? assigneeGroupsOptions.queryKey : undefined}
                assigneeGroupFilter={usesAssigneeBoard ? assigneeGroupFilter : undefined}
                visibleStatuses={visibleStatuses}
                hiddenStatuses={hiddenStatuses}
                onMoveIssue={handleMoveIssue}
                childProgressMap={childProgressMap}
                sort={sort}
              />
            ) : viewMode === "swimlane" ? (
              <SwimLaneView
                issues={issues}
                unfilteredIssues={swimlaneIssues}
                activeFilters={activeFilters}
                visibleStatuses={visibleStatuses}
                hiddenStatuses={hiddenStatuses}
                onMoveIssue={handleMoveIssue}
                childProgressMap={childProgressMap}
                sort={sort}
              />
            ) : (
              <ListView issues={issues} visibleStatuses={visibleStatuses} childProgressMap={childProgressMap} sort={sort} onMoveIssue={handleMoveIssue} />
            )}
          </div>
        )}
        {viewMode === "list" && <BatchActionToolbar />}
      </ViewStoreProvider>
    </div>
  );
}
