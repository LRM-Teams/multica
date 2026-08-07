"use client";

import { useMemo, useReducer, useState } from "react";
import {
  Ban,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Circle,
  FileText,
  Flag,
  ListChecks,
  ListTodo,
  Pause,
  Pencil,
  Play,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import type { ChannelGoal, UpdateChannelGoalRequest } from "@multica/core/types";
import {
  channelGoalOptions,
  channelGoalProcessesOptions,
  channelGoalSubgoalsOptions,
  channelMemberRole,
  channelMembersOptions,
  useCreateChannelGoal,
  useUpdateChannelGoal,
  workGraphOptions,
} from "@multica/core/channels";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { GOAL_PROCESS_PANEL_ID, GoalProcessPanel } from "./goal-process-panel";
import { GoalSubgoalPanel } from "./goal-subgoal-panel";
import { countOpenSubgoals, GOAL_SUBGOAL_PANEL_ID } from "./goal-subgoal-utils";

interface ChannelGoalCardProps {
  channelId: string;
  canManage: boolean;
  archived?: boolean;
}

type GoalIntentDraft = {
  title: string;
  objective: string;
  criteriaText: string;
};

type GoalProgressDraft = {
  progressSummary: string;
  currentStep: string;
  blocker: string;
  evidenceText: string;
  completedCriteria: string[];
};

type GoalCardUIState = {
  expanded: boolean;
  intentMode: "create" | "edit" | null;
  progressOpen: boolean;
  confirmAction: "complete" | "cancel" | null;
  actionError: string | null;
};

const emptyIntent: GoalIntentDraft = { title: "", objective: "", criteriaText: "" };
function lines(value: string): string[] {
  return [...new Set(value.split("\n").flatMap((item) => {
    const trimmed = item.trim();
    return trimmed ? [trimmed] : [];
  }))];
}

function mergeState<State>(state: State, patch: Partial<State>): State {
  return { ...state, ...patch };
}

function mutationMessage(error: unknown, fallback: string, stale: string): string {
  if (error && typeof error === "object" && "status" in error && error.status === 409) return stale;
  return fallback;
}

function GoalIntentDialog({
  open,
  onOpenChange,
  mode,
  initial,
  pending,
  error,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mode: "create" | "edit";
  initial: GoalIntentDraft;
  pending: boolean;
  error: string | null;
  onSubmit: (draft: GoalIntentDraft) => void;
}) {
  const { t } = useT("channels");
  const [draft, setDraft] = useReducer(mergeState<GoalIntentDraft>, initial);
  const valid = draft.title.trim() && draft.objective.trim() && lines(draft.criteriaText).length > 0;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{mode === "create" ? t(($) => $.goal.create_title) : t(($) => $.goal.edit_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.goal.create_description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="goal-title">{t(($) => $.goal.title_label)}</Label>
            <Input id="goal-title" value={draft.title} onChange={(event) => setDraft({ title: event.target.value })} placeholder={t(($) => $.goal.title_placeholder)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-objective">{t(($) => $.goal.objective_label)}</Label>
            <Textarea id="goal-objective" value={draft.objective} onChange={(event) => setDraft({ objective: event.target.value })} placeholder={t(($) => $.goal.objective_placeholder)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-criteria">{t(($) => $.goal.criteria_label)}</Label>
            <Textarea id="goal-criteria" value={draft.criteriaText} onChange={(event) => setDraft({ criteriaText: event.target.value })} placeholder={t(($) => $.goal.criteria_placeholder)} />
          </div>
          {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t(($) => $.goal.cancel)}</Button>
          <Button disabled={pending || !valid} onClick={() => onSubmit(draft)}>
            {mode === "create" ? t(($) => $.goal.activate) : t(($) => $.goal.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function GoalProgressDialog({
  goal,
  open,
  onOpenChange,
  pending,
  error,
  onSubmit,
}: {
  goal: ChannelGoal;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pending: boolean;
  error: string | null;
  onSubmit: (draft: GoalProgressDraft) => void;
}) {
  const { t } = useT("channels");
  const [draft, setDraft] = useReducer(mergeState<GoalProgressDraft>, {
    progressSummary: goal.progress_summary,
    currentStep: goal.current_step,
    blocker: goal.blocker,
    evidenceText: goal.evidence_refs.join("\n"),
    completedCriteria: goal.completed_criteria,
  });
  const completed = new Set(draft.completedCriteria);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t(($) => $.goal.update_progress)}</DialogTitle>
          <DialogDescription>{t(($) => $.goal.progress_description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>{t(($) => $.goal.criteria_label)}</Label>
            {goal.success_criteria.map((criterion) => (
              <label key={criterion} className="flex cursor-pointer items-start gap-2 rounded-md border p-2.5 text-sm">
                <Checkbox
                  checked={completed.has(criterion)}
                  onCheckedChange={(checked) => setDraft({
                    completedCriteria: checked
                      ? [...draft.completedCriteria, criterion]
                      : draft.completedCriteria.filter((item) => item !== criterion),
                  })}
                />
                <span>{criterion}</span>
              </label>
            ))}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-progress">{t(($) => $.goal.progress)}</Label>
            <Textarea id="goal-progress" value={draft.progressSummary} onChange={(event) => setDraft({ progressSummary: event.target.value })} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-step">{t(($) => $.goal.current_step)}</Label>
            <Input id="goal-step" value={draft.currentStep} onChange={(event) => setDraft({ currentStep: event.target.value })} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-blocker">{t(($) => $.goal.blocker_label)}</Label>
            <Input id="goal-blocker" value={draft.blocker} onChange={(event) => setDraft({ blocker: event.target.value })} placeholder={t(($) => $.goal.blocker_placeholder)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-evidence">{t(($) => $.goal.evidence)}</Label>
            <Textarea id="goal-evidence" value={draft.evidenceText} onChange={(event) => setDraft({ evidenceText: event.target.value })} placeholder={t(($) => $.goal.evidence_placeholder)} />
          </div>
          {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t(($) => $.goal.cancel)}</Button>
          <Button disabled={pending} onClick={() => onSubmit(draft)}>{t(($) => $.goal.save)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ChannelGoalCard({
  channelId,
  canManage,
  archived = false,
}: ChannelGoalCardProps) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const { data, isPending, isError, refetch } = useQuery(channelGoalOptions(channelId));
  const goal = data?.goal ?? null;
  const { data: members = [] } = useQuery(channelMembersOptions(channelId));
  const { data: processesData } = useQuery({
    ...channelGoalProcessesOptions(channelId),
    enabled: !!goal,
  });
  const { data: subgoalsData } = useQuery({
    ...channelGoalSubgoalsOptions(channelId),
    enabled: !!goal,
  });
  const { data: workGraph } = useQuery(workGraphOptions(goal?.work_graph?.id));
  const managers = useMemo(
    () =>
      members.filter(
        (member) => member.member_type === "agent" && channelMemberRole(member) === "manager",
      ),
    [members],
  );
  const [processOpen, setProcessOpen] = useState(false);
  const [subgoalsOpen, setSubgoalsOpen] = useState(false);
  const hasProcessUpdates =
    !processOpen && (processesData?.processes.some((doc) => doc.content.trim()) ?? false);
  const openSubgoalCount = countOpenSubgoals(subgoalsData?.subgoals ?? []);
  const createGoal = useCreateChannelGoal(channelId);
  const updateGoal = useUpdateChannelGoal(channelId);
  const [ui, setUI] = useReducer(mergeState<GoalCardUIState>, {
    expanded: false,
    intentMode: null,
    progressOpen: false,
    confirmAction: null,
    actionError: null,
  });
  const { expanded, intentMode, progressOpen, confirmAction, actionError } = ui;

  const runUpdate = (input: Omit<UpdateChannelGoalRequest, "expected_version">, close?: () => void) => {
    if (!goal) return;
    setUI({ actionError: null });
    updateGoal.mutate(
      { expected_version: goal.version, ...input },
      {
        onSuccess: () => close?.(),
        onError: (error) => setUI({ actionError: mutationMessage(error, t(($) => $.goal.update_failed), t(($) => $.goal.stale)) }),
      },
    );
  };

  if (isPending) {
    return (
      <div className="flex h-10 shrink-0 items-center gap-3 border-b border-border/40 px-4" data-testid="channel-goal-loading">
        <Skeleton className="size-4 rounded" />
        <Skeleton className="h-3 w-40" />
      </div>
    );
  }

  if (isError) {
    return (
      <div className="flex min-h-10 shrink-0 items-center justify-between gap-3 border-b border-border/40 bg-destructive/5 px-4 py-1.5">
        <span className="text-xs text-destructive">{t(($) => $.goal.load_failed)}</span>
        <Button size="sm" variant="ghost" className="h-7 text-xs" onClick={() => void refetch()}>{t(($) => $.goal.retry)}</Button>
      </div>
    );
  }

  if (!goal) {
    if (!canManage || archived) return null;
    return (
      <>
        <div className="flex shrink-0 items-center gap-1 border-b border-border/40 bg-muted/15 px-4 py-1.5">
          <Flag className="size-3.5 shrink-0 text-muted-foreground/70" />
          <span className="min-w-0 flex-1 text-xs text-muted-foreground">
            {t(($) => $.goal.empty_nl)}
            {" · "}
            <button
              type="button"
              className="text-brand underline underline-offset-2 hover:opacity-90"
              onClick={() => setUI({ intentMode: "create", actionError: null })}
            >
              {t(($) => $.goal.set_manually)}
            </button>
          </span>
        </div>
        {intentMode === "create" ? (
          <GoalIntentDialog
            key="create-goal"
            open
            onOpenChange={(open) => !open && setUI({ intentMode: null })}
            mode="create"
            initial={emptyIntent}
            pending={createGoal.isPending}
            error={actionError}
            onSubmit={(draft) => {
              setUI({ actionError: null });
              createGoal.mutate(
                { title: draft.title.trim(), objective: draft.objective.trim(), success_criteria: lines(draft.criteriaText) },
                {
                  onSuccess: () => setUI({ intentMode: null }),
                  onError: (error) => setUI({ actionError: mutationMessage(error, t(($) => $.goal.create_failed), t(($) => $.goal.already_active)) }),
                },
              );
            }}
          />
        ) : null}
      </>
    );
  }

  const completed = new Set(goal.completed_criteria);
  const allCompleted = goal.success_criteria.every((criterion) => completed.has(criterion));
  const canComplete =
    allCompleted && goal.evidence_refs.length > 0 && openSubgoalCount === 0 &&
    (!goal.work_graph || (goal.work_graph.completed === goal.work_graph.total && goal.work_graph.stale === 0));
  const completeDisabledReason =
    openSubgoalCount > 0
      ? t(($) => $.goal.subgoals_complete_blocked, { count: openSubgoalCount })
      : t(($) => $.goal.complete_disabled);
  const workGraphSummary = goal.work_graph
    ? `v${goal.work_graph.version} · ${goal.work_graph.completed}/${goal.work_graph.total}${goal.work_graph.running > 0 ? ` · ▶${goal.work_graph.running}` : ""}${goal.work_graph.stale > 0 ? ` · ⚠${goal.work_graph.stale}` : ""}`
    : null;
  return (
    <>
      <section className={cn("shrink-0 border-b border-border/50 bg-primary/[0.035]", goal.status === "paused" && "opacity-75")} data-testid="channel-goal-card">
        <div className="flex min-h-10 items-center gap-3 px-4 py-2">
          <Flag className="size-4 shrink-0 text-primary" />
          <button type="button" className="min-w-0 flex-1 text-left" onClick={() => setUI({ expanded: !expanded })}>
            <div className="flex items-center gap-2">
              <span className="truncate text-sm font-semibold">{goal.title}</span>
              <Badge variant="secondary" className="h-5 px-1.5 text-[10px]">{goal.completed_criteria.length}/{goal.success_criteria.length}</Badge>
              {goal.status === "paused" ? <Badge variant="outline" className="h-5 px-1.5 text-[10px]">{t(($) => $.goal.paused)}</Badge> : null}
              {goal.blocker ? <Badge variant="destructive" className="h-5 px-1.5 text-[10px]">{t(($) => $.goal.blocked)}</Badge> : null}
              {goal.work_graph ? (
                <Badge variant="outline" className="h-5 gap-1 px-1.5 text-[10px]" data-testid="channel-goal-work-graph-summary">
                  <ListTodo className="size-3" />
                  {workGraphSummary}
                </Badge>
              ) : null}
            </div>
            {goal.current_step ? <p className="truncate text-xs text-muted-foreground">{goal.current_step}</p> : null}
          </button>
          <button
            type="button"
            data-testid="channel-goal-subgoals-entry"
            aria-expanded={subgoalsOpen}
            aria-controls={GOAL_SUBGOAL_PANEL_ID}
            onClick={() => {
              setSubgoalsOpen((open) => !open);
              if (!subgoalsOpen) setProcessOpen(false);
            }}
            className={cn(
              "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-full border px-2.5 text-xs font-semibold transition-colors",
              subgoalsOpen
                ? "border-brand/45 bg-brand-soft text-brand"
                : "border-blue-300 bg-background text-blue-700 hover:bg-blue-50",
            )}
          >
            <ListTodo className="size-3.5" />
            {isMobile ? null : <span>{openSubgoalCount > 0 ? t(($) => $.goal.subgoals_open) : t(($) => $.goal.subgoals_none_chip)}</span>}
            {openSubgoalCount > 0 ? (
              <span className="rounded-full bg-brand px-1.5 text-[10px] font-bold text-white">{openSubgoalCount}</span>
            ) : null}
          </button>
          <button
            type="button"
            data-testid="channel-goal-process-entry"
            aria-expanded={processOpen}
            aria-controls={GOAL_PROCESS_PANEL_ID}
            onClick={() => {
              setProcessOpen((open) => !open);
              if (!processOpen) setSubgoalsOpen(false);
            }}
            className={cn(
              "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border border-input px-2.5 text-xs font-semibold transition-colors",
              processOpen
                ? "border-brand/45 bg-brand-soft text-brand"
                : "bg-background text-foreground hover:bg-muted/60",
            )}
          >
            {isMobile ? (
              <>
                <FileText className="size-3.5" />
                {managers.length > 0 ? <span>{managers.length}</span> : null}
              </>
            ) : (
              <>
                {managers.length > 0 ? (
                  <span className="flex items-center pl-1">
                    {managers.slice(0, 3).map((manager, index) => (
                      <span
                        key={manager.member_id}
                        className={cn("relative inline-flex", index > 0 && "-ml-1")}
                        style={{ zIndex: 3 - index }}
                      >
                        <ActorAvatar
                          actorType="agent"
                          actorId={manager.member_id}
                          size={18}
                          className="ring-2 ring-background"
                          name={manager.display_name || manager.name}
                          avatarUrlHint={manager.avatar_url}
                          showStatusDot={false}
                          profileLink={false}
                        />
                      </span>
                    ))}
                  </span>
                ) : null}
                <span>{t(($) => $.goal.process)}</span>
                {hasProcessUpdates ? (
                  <span
                    className="size-1.5 rounded-full bg-running"
                    aria-label={t(($) => $.goal.process_updating)}
                  />
                ) : null}
              </>
            )}
          </button>
          <Button size="icon" variant="ghost" className="size-7" aria-label={expanded ? t(($) => $.goal.collapse) : t(($) => $.goal.expand)} onClick={() => setUI({ expanded: !expanded })}>
            {expanded ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
          </Button>
        </div>
        {processOpen ? (
          <GoalProcessPanel
            key={goal.id}
            channelId={channelId}
            goal={goal}
            onClose={() => setProcessOpen(false)}
          />
        ) : null}
        {subgoalsOpen ? (
          <GoalSubgoalPanel
            key={`subgoals-${goal.id}`}
            channelId={channelId}
            canManage={canManage && !archived}
            asSheet={isMobile}
            onClose={() => setSubgoalsOpen(false)}
          />
        ) : null}
        {expanded ? (
          <div className="space-y-3 border-t border-border/40 px-4 py-3 text-sm">
            <p className="text-muted-foreground">{goal.objective}</p>
            <div className="space-y-1.5">
              {goal.success_criteria.map((criterion) => (
                <div key={criterion} className="flex items-start gap-2">
                  {completed.has(criterion) ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-primary" /> : <Circle className="mt-0.5 size-4 shrink-0 text-muted-foreground" />}
                  <span>{criterion}</span>
                </div>
              ))}
            </div>
            {goal.progress_summary ? <p><span className="font-medium">{t(($) => $.goal.progress)}:</span> {goal.progress_summary}</p> : null}
            {workGraph ? (
              <div className="space-y-1.5" data-testid="channel-goal-work-graph-detail">
                {workGraph.nodes.map((node) => (
                  <div key={node.id} className="flex items-center gap-2 rounded-md border border-border/60 px-2.5 py-1.5 text-xs">
                    <span className="min-w-0 flex-1 truncate">{node.objective || node.issue_id}</span>
                    <span className="text-muted-foreground">{node.role}</span>
                    <Badge variant={node.effective_completion === "satisfied" ? "secondary" : node.effective_completion === "pending" ? "outline" : "destructive"} className="h-5 px-1.5 text-[10px]">
                      {node.effective_completion}
                    </Badge>
                  </div>
                ))}
              </div>
            ) : null}
            {goal.blocker ? <p className="text-destructive"><span className="font-medium">{t(($) => $.goal.blocked)}:</span> {goal.blocker}</p> : null}
            {goal.evidence_refs.length ? (
              <div><span className="font-medium">{t(($) => $.goal.evidence)}:</span>
                <ul className="mt-1 list-disc space-y-0.5 pl-5 text-muted-foreground">
                  {goal.evidence_refs.map((evidence) => <li key={evidence} className="break-all">{evidence}</li>)}
                </ul>
              </div>
            ) : null}
            {actionError ? <p role="alert" className="text-destructive">{actionError}</p> : null}
            {canManage && !archived ? (
              <div className="flex flex-wrap gap-2 pt-1">
                <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setUI({ intentMode: "edit", actionError: null })}><Pencil className="size-3.5" />{t(($) => $.goal.edit)}</Button>
                <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setUI({ progressOpen: true, actionError: null })}><ListChecks className="size-3.5" />{t(($) => $.goal.update_progress)}</Button>
                <Button size="sm" variant="outline" className="gap-1.5" disabled={updateGoal.isPending} onClick={() => runUpdate({ status: goal.status === "paused" ? "active" : "paused" })}>
                  {goal.status === "paused" ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
                  {goal.status === "paused" ? t(($) => $.goal.resume) : t(($) => $.goal.pause)}
                </Button>
                <Button size="sm" className="gap-1.5" disabled={!canComplete || updateGoal.isPending} title={!canComplete ? completeDisabledReason : undefined} onClick={() => setUI({ confirmAction: "complete" })}>
                  <CheckCircle2 className="size-3.5" />{t(($) => $.goal.complete)}
                </Button>
                {openSubgoalCount > 0 ? (
                  <p className="basis-full text-xs text-muted-foreground">
                    {t(($) => $.goal.subgoals_complete_blocked, { count: openSubgoalCount })}
                  </p>
                ) : null}
                <Button size="sm" variant="ghost" className="gap-1.5 text-destructive hover:text-destructive" onClick={() => setUI({ confirmAction: "cancel" })}>
                  <Ban className="size-3.5" />{t(($) => $.goal.cancel_goal)}
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}
      </section>
      {intentMode === "edit" ? (
        <GoalIntentDialog
          key={`edit-goal-${goal.version}`}
          open
          onOpenChange={(open) => !open && setUI({ intentMode: null })}
          mode="edit"
          initial={{ title: goal.title, objective: goal.objective, criteriaText: goal.success_criteria.join("\n") }}
          pending={updateGoal.isPending}
          error={actionError}
          onSubmit={(draft) => runUpdate({
            title: draft.title.trim(),
            objective: draft.objective.trim(),
            success_criteria: lines(draft.criteriaText),
          }, () => setUI({ intentMode: null }))}
        />
      ) : null}
      {progressOpen ? (
        <GoalProgressDialog
          key={`progress-goal-${goal.version}`}
          goal={goal}
          open
          onOpenChange={(open) => !open && setUI({ progressOpen: false })}
          pending={updateGoal.isPending}
          error={actionError}
          onSubmit={(draft) => runUpdate({
            progress_summary: draft.progressSummary.trim(),
            current_step: draft.currentStep.trim(),
            blocker: draft.blocker.trim(),
            evidence_refs: lines(draft.evidenceText),
            completed_criteria: draft.completedCriteria,
          }, () => setUI({ progressOpen: false }))}
        />
      ) : null}
      <AlertDialog open={confirmAction !== null} onOpenChange={(open) => !open && setUI({ confirmAction: null })}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmAction === "complete" ? t(($) => $.goal.complete_title) : t(($) => $.goal.cancel_title)}</AlertDialogTitle>
            <AlertDialogDescription>{confirmAction === "complete" ? t(($) => $.goal.complete_description) : t(($) => $.goal.cancel_description)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.goal.back)}</AlertDialogCancel>
            <AlertDialogAction
              disabled={updateGoal.isPending}
              className={confirmAction === "cancel" ? "bg-destructive text-destructive-foreground hover:bg-destructive/90" : undefined}
              onClick={(event) => {
                event.preventDefault();
                runUpdate({ status: confirmAction === "complete" ? "completed" : "cancelled" }, () => setUI({ confirmAction: null }));
              }}
            >
              {confirmAction === "complete" ? t(($) => $.goal.complete) : t(($) => $.goal.cancel_goal)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
