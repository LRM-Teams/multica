"use client";

import { useCallback, useMemo, memo } from "react";
import { AppLink } from "../../navigation";
import { useSortable, defaultAnimateLayoutChanges } from "@dnd-kit/sortable";
import type { AnimateLayoutChanges } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Issue, UpdateIssueRequest } from "@multica/core/types";
import { formatDateOnly, isPastDateOnly } from "@multica/core/issues/date";
import { CalendarClock, CalendarDays } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { ActorAvatar } from "../../common/actor-avatar";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { PRIORITY_CONFIG } from "@multica/core/issues/config";
import { cn } from "@multica/ui/lib/utils";
import { useUpdateIssue } from "@multica/core/issues/mutations";
import { useWorkspacePaths } from "@multica/core/paths";
import { useWorkspaceId } from "@multica/core/hooks";
import { useActorName } from "@multica/core/workspace/hooks";
import { useTimeAgo } from "../../i18n";
import { projectListOptions } from "@multica/core/projects/queries";
import { ProjectIcon } from "../../projects/components/project-icon";
import { PriorityIcon } from "./priority-icon";
import { PriorityPicker, AssigneePicker, StartDatePicker, DueDatePicker } from "./pickers";
import { useViewStore } from "@multica/core/issues/stores/view-store-context";
import { ProgressRing } from "./progress-ring";
import type { ChildProgress } from "./list-row";
import { IssueActionsContextMenu } from "../actions";
import { LabelChip } from "../../labels/label-chip";
import { IssueAgentActivityIndicator } from "./issue-agent-activity-indicator";
import { useT } from "../../i18n";

function formatDate(date: string): string {
  return formatDateOnly(date, { month: "short", day: "numeric" }, "en-US");
}

function descriptionPreview(markdown: string): string {
  return markdown
    .replace(/!file\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/[*_`~]+/g, "")
    .replace(/^[\s>#]+/gm, "")
    .replace(/\s+/g, " ")
    .trim();
}

/** Stops event from bubbling to Link/drag handlers */
function PickerWrapper({ children, className }: { children: React.ReactNode; className?: string }) {
  const stop = (e: React.SyntheticEvent) => {
    e.stopPropagation();
    e.preventDefault();
  };
  return (
    <div onClick={stop} onMouseDown={stop} onPointerDown={stop} className={className}>
      {children}
    </div>
  );
}

export const BoardCardContent = memo(function BoardCardContent({
  issue,
  editable = false,
  childProgress,
}: {
  issue: Issue;
  editable?: boolean;
  childProgress?: ChildProgress;
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const storeProperties = useViewStore((s) => s.cardProperties);
  const wsId = useWorkspaceId();
  const { data: projects = [] } = useQuery({
    ...projectListOptions(wsId),
    enabled: storeProperties.project && !!issue.project_id,
  });
  const project = issue.project_id ? projects.find((p) => p.id === issue.project_id) : undefined;
  const labels = issue.labels ?? [];

  // Operational state strip: surface "who's running / who needs me / who's
  // stuck / who's done" at a glance, the way an agent-driven board should.
  // A live running task wins over the static status; otherwise derive from
  // the issue status. Reuses the workspace-wide task snapshot the corner
  // activity indicator already subscribes to (same cache key, deduped).
  const { data: taskSnapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));
  const hasRunningAgent = useMemo(
    () => taskSnapshot.some((tk) => tk.issue_id === issue.id && tk.status === "running"),
    [taskSnapshot, issue.id],
  );
  const cardState: "running" | "attention" | "review" | "done" | null = hasRunningAgent
    ? "running"
    : issue.status === "blocked"
      ? "attention"
      : issue.status === "in_review"
        ? "review"
        : issue.status === "done"
          ? "done"
          : null;

  const updateIssueMutation = useUpdateIssue();
  const handleUpdate = useCallback(
    (updates: Partial<UpdateIssueRequest>) => {
      updateIssueMutation.mutate(
        { id: issue.id, ...updates },
        {
          onError: (err) =>
            showErrorToast(
              err instanceof Error && err.message
                ? err.message
                : t(($) => $.card.update_failed),
            ),
        },
      );
    },
    [issue.id, updateIssueMutation, t],
  );

  const showPriority = storeProperties.priority;
  const showDescription = storeProperties.description && issue.description;
  const showAssigneeSection = storeProperties.assignee;
  const hasAssignee = !!issue.assignee_type && !!issue.assignee_id;
  const showStartDate = storeProperties.startDate && issue.start_date;
  const showDueDate = storeProperties.dueDate && issue.due_date;
  const showProject = storeProperties.project && project;
  const showChildProgress = storeProperties.childProgress && childProgress;
  const showLabels = storeProperties.labels && labels.length > 0;

  const showAssigneeName = showAssigneeSection && hasAssignee && !showStartDate && !showDueDate;
  const showUpdatedHint = showAssigneeName && !showChildProgress && !cardState;
  const { getActorName } = useActorName();
  const assigneeName =
    showAssigneeName && issue.assignee_type && issue.assignee_id
      ? getActorName(issue.assignee_type, issue.assignee_id)
      : null;

  const priorityLabel = t(($) => $.priority[issue.priority]);
  const priorityCfg = PRIORITY_CONFIG[issue.priority];
  // Colored priority pill (icon + short label) for scannability. "none" stays
  // a bare muted dash with no label so empty priority doesn't add noise.
  const priorityPill = (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium leading-none",
        issue.priority === "none"
          ? "text-muted-foreground"
          : `${priorityCfg.badgeBg} ${priorityCfg.badgeText}`,
      )}
    >
      <PriorityIcon priority={issue.priority} inheritColor />
      {issue.priority !== "none" && <span>{priorityLabel}</span>}
    </span>
  );
  const priorityIconNode = showPriority ? (
    editable ? (
      <PickerWrapper>
        <PriorityPicker
          priority={issue.priority}
          onUpdate={handleUpdate}
          triggerRender={
            <button
              type="button"
              aria-label={priorityLabel}
              className="inline-flex items-center justify-center rounded hover:opacity-80"
            >
              {priorityPill}
            </button>
          }
        />
      </PickerWrapper>
    ) : (
      <span aria-label={priorityLabel} className="inline-flex items-center justify-center">
        {priorityPill}
      </span>
    )
  ) : null;

  // The parent row gives this container the leftover space; min-w-0 and
  // max-w-full make the nested picker trigger respect that limit.
  const assigneeContainerClass = assigneeName
    ? "flex min-w-0 max-w-full items-center"
    : "inline-flex items-center";

  const assigneeInner = hasAssignee ? (
    <span className="flex min-w-0 max-w-full items-center gap-1.5">
      <ActorAvatar
        actorType={issue.assignee_type!}
        actorId={issue.assignee_id!}
        size={20}
        enableHoverCard
        className="shrink-0"
      />
      {assigneeName && (
        <span className="min-w-0 truncate text-xs text-foreground">{assigneeName}</span>
      )}
    </span>
  ) : (
    <span className="text-xs text-muted-foreground">{t(($) => $.pickers.assignee.trigger_unassigned)}</span>
  );

  const assigneeNode = showAssigneeSection ? (
    editable ? (
      <PickerWrapper className={assigneeContainerClass}>
        <AssigneePicker
          assigneeType={issue.assignee_type}
          assigneeId={issue.assignee_id}
          onUpdate={handleUpdate}
          trigger={assigneeInner}
        />
      </PickerWrapper>
    ) : (
      <span className={assigneeContainerClass}>{assigneeInner}</span>
    )
  ) : null;

  const showMetaRow = showAssigneeSection || showStartDate || showDueDate || showChildProgress;
  const showRightMeta = !!showStartDate || !!showDueDate || !!showChildProgress || showUpdatedHint;

  return (
    <div
      className={cn(
        "rounded-[10px] border border-border/60 bg-card px-3.5 py-3 shadow-[0_1px_2px_0_rgba(0,0,0,0.04)] transition-all group-hover/card:border-border group-hover/card:shadow-[0_4px_12px_-4px_rgba(0,0,0,0.10)] group-data-[popup-open]/card:border-border group-data-[popup-open]/card:shadow-[0_4px_12px_-4px_rgba(0,0,0,0.10)]",
        cardState === "attention" && "border-destructive/30 bg-destructive/[0.03]",
        cardState === "done" && "border-transparent bg-muted/40 shadow-none",
      )}
    >
      {/* Row 1: priority + identifier (left), agent activity + assignee (right) */}
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 min-w-0">
          {priorityIconNode}
          <p className="text-xs text-muted-foreground truncate">{issue.identifier}</p>
        </div>
        <IssueAgentActivityIndicator issueId={issue.id} />
      </div>

      {/* Row 2: Title */}
      <p className="mt-1.5 text-[15px] font-semibold leading-snug text-foreground line-clamp-2">
        {issue.title}
      </p>

      {showDescription && (() => {
        const preview = descriptionPreview(issue.description!);
        if (!preview) return null;
        return (
          <p className="mt-1 text-xs text-muted-foreground line-clamp-1">
            {preview}
          </p>
        );
      })()}

      {/* Chip row: project + labels */}
      {(showProject || showLabels) && (
        <div className="mt-1.5 flex items-center gap-1.5 flex-wrap">
          {showProject && (
            <span className="inline-flex items-center gap-1 rounded-full bg-muted/60 px-1.5 py-0.5 text-[11px] text-muted-foreground max-w-[160px]">
              <ProjectIcon project={project} size="sm" />
              <span className="truncate">{project!.title}</span>
            </span>
          )}
          {showLabels && labels.map((label) => (
            <LabelChip key={label.id} label={label} />
          ))}
        </div>
      )}

      {/* Meta row: assignee (left), start date, due date, child progress (right) */}
      {showMetaRow && (
        <div className="mt-2 flex items-center justify-between gap-2">
          {showAssigneeSection && (
            <div className="min-w-0 flex-1">
              {assigneeNode}
            </div>
          )}
          {showRightMeta && (
            <div className="ml-auto flex shrink-0 items-center gap-2">
              {showStartDate && (
                editable ? (
                  <PickerWrapper className="shrink-0">
                    <StartDatePicker
                      startDate={issue.start_date}
                      onUpdate={handleUpdate}
                      trigger={
                        <span className="flex items-center gap-1 text-xs text-muted-foreground">
                          <CalendarClock className="size-3" />
                          {formatDate(issue.start_date!)}
                        </span>
                      }
                    />
                  </PickerWrapper>
                ) : (
                  <span className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                    <CalendarClock className="size-3" />
                    {formatDate(issue.start_date!)}
                  </span>
                )
              )}
              {showDueDate && (
                editable ? (
                  <PickerWrapper className="shrink-0">
                    <DueDatePicker
                      dueDate={issue.due_date}
                      onUpdate={handleUpdate}
                      trigger={
                        <span
                          className={`flex items-center gap-1 text-xs ${
                            isPastDateOnly(issue.due_date)
                              ? "text-destructive"
                              : "text-muted-foreground"
                          }`}
                        >
                          <CalendarDays className="size-3" />
                          {formatDate(issue.due_date!)}
                        </span>
                      }
                    />
                  </PickerWrapper>
                ) : (
                  <span
                    className={`flex shrink-0 items-center gap-1 text-xs ${
                      isPastDateOnly(issue.due_date)
                        ? "text-destructive"
                        : "text-muted-foreground"
                    }`}
                  >
                    <CalendarDays className="size-3" />
                    {formatDate(issue.due_date!)}
                  </span>
                )
              )}
              {showChildProgress && (
                <div className="inline-flex shrink-0 items-center gap-1">
                  <ProgressRing done={childProgress!.done} total={childProgress!.total} size={14} />
                  <span className="text-[11px] text-muted-foreground tabular-nums font-medium">
                    {childProgress!.done}/{childProgress!.total}
                  </span>
                </div>
              )}
              {showUpdatedHint && (
                <span className="shrink-0 text-xs text-muted-foreground">
                  {t(($) => $.card.updated_ago, { time: timeAgo(issue.updated_at) })}
                </span>
              )}
            </div>
          )}
        </div>
      )}

      {/* Operational state strip: running shows an activity bar, the rest a
          colored dot + label. Suppressed updated-hint above avoids redundancy. */}
      {cardState && (
        <div className="mt-2">
          {cardState === "running" ? (
            <div className="flex items-center gap-2">
              <span className="h-1 flex-1 overflow-hidden rounded-full bg-warning/15">
                <span className="block h-full w-1/3 animate-pulse rounded-full bg-warning" />
              </span>
              <span className="shrink-0 text-[11px] font-medium text-warning">
                {t(($) => $.card.state.running)}
              </span>
            </div>
          ) : (
            <div className="flex items-center gap-1.5 text-[11px] font-medium">
              <span
                className={cn(
                  "size-1.5 shrink-0 rounded-full",
                  cardState === "attention"
                    ? "bg-destructive"
                    : cardState === "review"
                      ? "bg-success"
                      : "bg-muted-foreground/50",
                )}
              />
              <span
                className={cn(
                  "truncate",
                  cardState === "attention"
                    ? "text-destructive"
                    : cardState === "review"
                      ? "text-success"
                      : "text-muted-foreground",
                )}
              >
                {cardState === "attention"
                  ? t(($) => $.card.state.attention)
                  : cardState === "review"
                    ? t(($) => $.card.state.review)
                    : t(($) => $.card.state.done, { time: timeAgo(issue.updated_at) })}
              </span>
            </div>
          )}
        </div>
      )}
    </div>
  );
});

const animateLayoutChanges: AnimateLayoutChanges = (args) => {
  const { isSorting, wasDragging } = args;
  if (isSorting || wasDragging) return false;
  return defaultAnimateLayoutChanges(args);
};

export const DraggableBoardCard = memo(function DraggableBoardCard({ issue, childProgress, disableSorting }: { issue: Issue; childProgress?: ChildProgress; disableSorting?: boolean }) {
  const p = useWorkspacePaths();
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: issue.id,
    data: { status: issue.status },
    animateLayoutChanges,
    disabled: disableSorting ? { droppable: true } : undefined,
  });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  return (
    <IssueActionsContextMenu issue={issue}>
      <div
        ref={setNodeRef}
        style={style}
        {...attributes}
        {...listeners}
        className={`group/card ${isDragging ? "opacity-30" : ""}`}
      >
        <AppLink
          href={p.issueDetail(issue.id)}
          className={`group block transition-colors ${isDragging ? "pointer-events-none" : ""}`}
        >
          <BoardCardContent issue={issue} editable childProgress={childProgress} />
        </AppLink>
      </div>
    </IssueActionsContextMenu>
  );
});
