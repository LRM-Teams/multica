"use client";

import { useId, useState } from "react";
import type {
  TrajectoryCommit,
  TrajectoryStatus,
} from "@multica/core/research";
import { Button } from "@multica/ui/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { cn } from "@multica/ui/lib/utils";
import {
  AlertTriangle,
  Check,
  CircleDot,
  Clipboard,
  ExternalLink,
  GitBranch,
  GitMerge,
  Library,
  RouteOff,
} from "lucide-react";
import { useT } from "../../i18n/use-t";

export type TrajectoryCommitCardSize = "compact" | "selected" | "aggregate";
export interface TrajectoryCommitCardProps {
  commit: TrajectoryCommit;
  size?: TrajectoryCommitCardSize;
  selected?: boolean;
  aggregateCount?: number;
  defaultOpen?: boolean;
  onViewNode?: (commit: TrajectoryCommit) => void;
  onViewEvidence?: (commit: TrajectoryCommit) => void;
  onFilterBranch?: (branchId: string) => void;
  onCopyLink?: (commit: TrajectoryCommit) => void;
}

const statusIcon = {
  running: CircleDot,
  success: Check,
  detour: RouteOff,
  failed: AlertTriangle,
  merged: GitMerge,
} satisfies Record<TrajectoryStatus, typeof CircleDot>;
const statusTone = {
  running: "border-brand/40 bg-brand/10 text-brand",
  success: "border-success/40 bg-success/10 text-success-strong",
  detour: "border-warning/40 bg-warning/10 text-warning-strong",
  failed: "border-destructive/40 bg-destructive/10 text-destructive-strong",
  merged: "border-info/40 bg-info/10 text-info-strong",
} satisfies Record<TrajectoryStatus, string>;
const commitTimeFormatter = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

export function TrajectoryCommitCard({
  commit,
  size = "selected",
  selected = false,
  aggregateCount = 1,
  defaultOpen = false,
  onViewNode,
  onViewEvidence,
  onFilterBranch,
  onCopyLink,
}: TrajectoryCommitCardProps) {
  const { t } = useT("research");
  const nodeReasonId = useId();
  const evidenceReasonId = useId();
  const StatusIcon = statusIcon[commit.status];
  const status = t(($) => $.trajectory.status[commit.status]);
  const agent = commit.agentId ?? commit.branchId;
  const isAggregate = size === "aggregate";
  const evidenceCount = commit.evidenceRefs.length;
  const headline = isAggregate
    ? t(($) => $.trajectory.aggregate_count, { count: aggregateCount })
    : commit.title;
  const time = commit.timestamp
    ? commitTimeFormatter.format(new Date(commit.timestamp))
    : null;
  const [open, setOpen] = useState(defaultOpen);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <button
            type="button"
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                setOpen(true);
              }
            }}
            aria-label={[
              headline,
              t(($) => $.trajectory.by_agent, { agent }),
              status,
            ].join(", ")}
            aria-pressed={selected}
            data-size={size}
            data-trajectory-status={commit.status}
            className={cn(
              "grid min-h-11 min-w-0 grid-cols-[auto_1fr_auto] items-center gap-2 rounded-lg border bg-card px-2.5 py-2 text-left text-foreground outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-brand focus-visible:ring-offset-2 focus-visible:ring-offset-background motion-reduce:transition-none",
              size === "compact" && "w-52",
              size === "selected" &&
                "w-72 border-brand/60 ring-1 ring-brand/20",
              size === "aggregate" && "w-44 border-dashed",
              selected && "border-brand ring-2 ring-brand/25",
            )}
          />
        }
      >
        <span
          data-status-shape={commit.status}
          className={cn(
            "flex size-7 shrink-0 items-center justify-center border",
            commit.status === "merged"
              ? "rounded-sm rotate-45"
              : commit.status === "detour"
                ? "rounded-sm"
                : "rounded-full",
            statusTone[commit.status],
          )}
          aria-hidden
        >
          <StatusIcon
            className={cn(
              "size-3.5",
              commit.status === "merged" && "-rotate-45",
            )}
            aria-hidden
          />
        </span>
        <span className="min-w-0">
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <span className="truncate">{agent}</span>
            {time ? (
              <time dateTime={commit.timestamp ?? undefined}>{time}</time>
            ) : null}
          </span>
          <span className="mt-0.5 block truncate text-sm font-medium">
            {headline}
          </span>
          <span className="mt-1 flex min-w-0 items-center gap-1.5 text-xs">
            <span
              className={cn(
                "rounded-sm border px-1.5 py-0.5 font-medium",
                statusTone[commit.status],
              )}
            >
              {status}
            </span>
            {commit.parentIds.length > 1 ? (
              <span className="truncate text-muted-foreground">
                {t(($) => $.trajectory.parents, {
                  count: commit.parentIds.length,
                })}
              </span>
            ) : null}
          </span>
        </span>
        <span
          className="flex items-center gap-0.5 text-xs text-muted-foreground"
          aria-label={t(($) => $.trajectory.evidence_count, {
            count: evidenceCount,
          })}
        >
          <Library className="size-3.5" aria-hidden />
          <span>{evidenceCount > 99 ? "99+" : evidenceCount}</span>
          <span className="sr-only">
            {t(($) => $.trajectory.evidence_count, { count: evidenceCount })}
          </span>
        </span>
      </PopoverTrigger>
      <PopoverContent
        role="dialog"
        aria-label={t(($) => $.trajectory.details)}
        align="start"
        className="w-[min(22rem,calc(100vw-2rem))] motion-reduce:animate-none motion-reduce:transition-none"
      >
        <PopoverHeader>
          <PopoverTitle className="pr-6">{commit.title}</PopoverTitle>
          <PopoverDescription>
            {t(($) => $.trajectory.branch, { branch: commit.branchId })}
          </PopoverDescription>
        </PopoverHeader>
        {commit.summary ? (
          <p className="max-h-28 overflow-y-auto text-sm text-foreground">
            {commit.summary}
          </p>
        ) : null}
        <div className="grid gap-1">
          <ActionButton
            icon={ExternalLink}
            disabled={commit.sourceNodeIds.length === 0}
            reasonId={nodeReasonId}
            reason={
              commit.sourceNodeIds.length === 0
                ? t(($) => $.trajectory.no_node_ref)
                : null
            }
            onClick={() => onViewNode?.(commit)}
          >
            {t(($) => $.trajectory.view_node)}
          </ActionButton>
          <ActionButton
            icon={Library}
            disabled={evidenceCount === 0}
            reasonId={evidenceReasonId}
            reason={
              evidenceCount === 0
                ? t(($) => $.trajectory.no_evidence_ref)
                : null
            }
            onClick={() => onViewEvidence?.(commit)}
          >
            {t(($) => $.trajectory.view_evidence)}
          </ActionButton>
          <ActionButton
            icon={GitBranch}
            onClick={() => onFilterBranch?.(commit.branchId)}
          >
            {t(($) => $.trajectory.filter_branch)}
          </ActionButton>
          <ActionButton icon={Clipboard} onClick={() => onCopyLink?.(commit)}>
            {t(($) => $.trajectory.copy_link)}
          </ActionButton>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function ActionButton({
  icon: Icon,
  reason,
  reasonId,
  children,
  ...props
}: React.ComponentProps<typeof Button> & {
  icon: typeof CircleDot;
  reason?: string | null;
  reasonId?: string;
}) {
  return (
    <div>
      <Button
        type="button"
        variant="ghost"
        className="min-h-11 w-full justify-start"
        aria-describedby={reason ? reasonId : undefined}
        {...props}
      >
        <Icon className="size-4" aria-hidden />
        {children}
      </Button>
      {reason ? (
        <p id={reasonId} className="px-3 pb-1 text-xs text-muted-foreground">
          {reason}
        </p>
      ) : null}
    </div>
  );
}
