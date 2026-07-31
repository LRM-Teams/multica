"use client";

import { useEffect, useMemo, useState } from "react";
import {
  Ban,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Circle,
  Flag,
  ListChecks,
  Pause,
  Pencil,
  Play,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import type { ChannelGoal, UpdateChannelGoalRequest } from "@multica/core/types";
import {
  channelGoalOptions,
  useCreateChannelGoal,
  useUpdateChannelGoal,
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
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

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

const emptyIntent: GoalIntentDraft = { title: "", objective: "", criteriaText: "" };
const emptyProgress: GoalProgressDraft = {
  progressSummary: "",
  currentStep: "",
  blocker: "",
  evidenceText: "",
  completedCriteria: [],
};

function lines(value: string): string[] {
  return [...new Set(value.split("\n").map((item) => item.trim()).filter(Boolean))];
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
  const [draft, setDraft] = useState(initial);
  useEffect(() => {
    if (open) setDraft(initial);
  }, [initial, open]);
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
            <Input id="goal-title" value={draft.title} onChange={(event) => setDraft({ ...draft, title: event.target.value })} placeholder={t(($) => $.goal.title_placeholder)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-objective">{t(($) => $.goal.objective_label)}</Label>
            <Textarea id="goal-objective" value={draft.objective} onChange={(event) => setDraft({ ...draft, objective: event.target.value })} placeholder={t(($) => $.goal.objective_placeholder)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-criteria">{t(($) => $.goal.criteria_label)}</Label>
            <Textarea id="goal-criteria" value={draft.criteriaText} onChange={(event) => setDraft({ ...draft, criteriaText: event.target.value })} placeholder={t(($) => $.goal.criteria_placeholder)} />
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
  const initial = useMemo<GoalProgressDraft>(() => ({
    progressSummary: goal.progress_summary,
    currentStep: goal.current_step,
    blocker: goal.blocker,
    evidenceText: goal.evidence_refs.join("\n"),
    completedCriteria: goal.completed_criteria,
  }), [goal]);
  const [draft, setDraft] = useState(emptyProgress);
  useEffect(() => {
    if (open) setDraft(initial);
  }, [initial, open]);
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
                    ...draft,
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
            <Textarea id="goal-progress" value={draft.progressSummary} onChange={(event) => setDraft({ ...draft, progressSummary: event.target.value })} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-step">{t(($) => $.goal.current_step)}</Label>
            <Input id="goal-step" value={draft.currentStep} onChange={(event) => setDraft({ ...draft, currentStep: event.target.value })} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-blocker">{t(($) => $.goal.blocker_label)}</Label>
            <Input id="goal-blocker" value={draft.blocker} onChange={(event) => setDraft({ ...draft, blocker: event.target.value })} placeholder={t(($) => $.goal.blocker_placeholder)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="goal-evidence">{t(($) => $.goal.evidence)}</Label>
            <Textarea id="goal-evidence" value={draft.evidenceText} onChange={(event) => setDraft({ ...draft, evidenceText: event.target.value })} placeholder={t(($) => $.goal.evidence_placeholder)} />
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

export function ChannelGoalCard({ channelId, canManage, archived = false }: ChannelGoalCardProps) {
  const { t } = useT("channels");
  const { data, isPending, isError, refetch } = useQuery(channelGoalOptions(channelId));
  const goal = data?.goal ?? null;
  const createGoal = useCreateChannelGoal(channelId);
  const updateGoal = useUpdateChannelGoal(channelId);
  const [expanded, setExpanded] = useState(false);
  const [intentMode, setIntentMode] = useState<"create" | "edit" | null>(null);
  const [progressOpen, setProgressOpen] = useState(false);
  const [confirmAction, setConfirmAction] = useState<"complete" | "cancel" | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const runUpdate = (input: Omit<UpdateChannelGoalRequest, "expected_version">, close?: () => void) => {
    if (!goal) return;
    setActionError(null);
    updateGoal.mutate(
      { expected_version: goal.version, ...input },
      {
        onSuccess: () => close?.(),
        onError: (error) => setActionError(mutationMessage(error, t(($) => $.goal.update_failed), t(($) => $.goal.stale))),
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
        <div className="flex shrink-0 items-center justify-between border-b border-border/40 bg-muted/15 px-4 py-1.5">
          <span className="text-xs text-muted-foreground">{t(($) => $.goal.empty)}</span>
          <Button size="sm" variant="ghost" className="h-7 gap-1.5 text-xs" onClick={() => setIntentMode("create")}>
            <Flag className="size-3.5" />
            {t(($) => $.goal.set)}
          </Button>
        </div>
        <GoalIntentDialog
          open={intentMode === "create"}
          onOpenChange={(open) => setIntentMode(open ? "create" : null)}
          mode="create"
          initial={emptyIntent}
          pending={createGoal.isPending}
          error={actionError}
          onSubmit={(draft) => {
            setActionError(null);
            createGoal.mutate(
              { title: draft.title.trim(), objective: draft.objective.trim(), success_criteria: lines(draft.criteriaText) },
              {
                onSuccess: () => setIntentMode(null),
                onError: (error) => setActionError(mutationMessage(error, t(($) => $.goal.create_failed), t(($) => $.goal.already_active))),
              },
            );
          }}
        />
      </>
    );
  }

  const completed = new Set(goal.completed_criteria);
  const allCompleted = goal.success_criteria.every((criterion) => completed.has(criterion));
  const canComplete = allCompleted && goal.evidence_refs.length > 0;
  return (
    <>
      <section className={cn("shrink-0 border-b border-border/50 bg-primary/[0.035]", goal.status === "paused" && "opacity-75")} data-testid="channel-goal-card">
        <div className="flex min-h-10 items-center gap-3 px-4 py-2">
          <Flag className="size-4 shrink-0 text-primary" />
          <button type="button" className="min-w-0 flex-1 text-left" onClick={() => setExpanded((value) => !value)}>
            <div className="flex items-center gap-2">
              <span className="truncate text-sm font-semibold">{goal.title}</span>
              <Badge variant="secondary" className="h-5 px-1.5 text-[10px]">{goal.completed_criteria.length}/{goal.success_criteria.length}</Badge>
              {goal.status === "paused" ? <Badge variant="outline" className="h-5 px-1.5 text-[10px]">{t(($) => $.goal.paused)}</Badge> : null}
              {goal.blocker ? <Badge variant="destructive" className="h-5 px-1.5 text-[10px]">{t(($) => $.goal.blocked)}</Badge> : null}
            </div>
            {goal.current_step ? <p className="truncate text-xs text-muted-foreground">{goal.current_step}</p> : null}
          </button>
          <Button size="icon" variant="ghost" className="size-7" aria-label={expanded ? t(($) => $.goal.collapse) : t(($) => $.goal.expand)} onClick={() => setExpanded((value) => !value)}>
            {expanded ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
          </Button>
        </div>
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
                <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setIntentMode("edit")}><Pencil className="size-3.5" />{t(($) => $.goal.edit)}</Button>
                <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setProgressOpen(true)}><ListChecks className="size-3.5" />{t(($) => $.goal.update_progress)}</Button>
                <Button size="sm" variant="outline" className="gap-1.5" disabled={updateGoal.isPending} onClick={() => runUpdate({ status: goal.status === "paused" ? "active" : "paused" })}>
                  {goal.status === "paused" ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
                  {goal.status === "paused" ? t(($) => $.goal.resume) : t(($) => $.goal.pause)}
                </Button>
                <Button size="sm" className="gap-1.5" disabled={!canComplete || updateGoal.isPending} title={!canComplete ? t(($) => $.goal.complete_disabled) : undefined} onClick={() => setConfirmAction("complete")}>
                  <CheckCircle2 className="size-3.5" />{t(($) => $.goal.complete)}
                </Button>
                <Button size="sm" variant="ghost" className="gap-1.5 text-destructive hover:text-destructive" onClick={() => setConfirmAction("cancel")}>
                  <Ban className="size-3.5" />{t(($) => $.goal.cancel_goal)}
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}
      </section>
      <GoalIntentDialog
        open={intentMode === "edit"}
        onOpenChange={(open) => setIntentMode(open ? "edit" : null)}
        mode="edit"
        initial={{ title: goal.title, objective: goal.objective, criteriaText: goal.success_criteria.join("\n") }}
        pending={updateGoal.isPending}
        error={actionError}
        onSubmit={(draft) => runUpdate({
          title: draft.title.trim(),
          objective: draft.objective.trim(),
          success_criteria: lines(draft.criteriaText),
        }, () => setIntentMode(null))}
      />
      <GoalProgressDialog
        goal={goal}
        open={progressOpen}
        onOpenChange={setProgressOpen}
        pending={updateGoal.isPending}
        error={actionError}
        onSubmit={(draft) => runUpdate({
          progress_summary: draft.progressSummary.trim(),
          current_step: draft.currentStep.trim(),
          blocker: draft.blocker.trim(),
          evidence_refs: lines(draft.evidenceText),
          completed_criteria: draft.completedCriteria,
        }, () => setProgressOpen(false))}
      />
      <AlertDialog open={confirmAction !== null} onOpenChange={(open) => !open && setConfirmAction(null)}>
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
                runUpdate({ status: confirmAction === "complete" ? "completed" : "cancelled" }, () => setConfirmAction(null));
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
