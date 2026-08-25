"use client";

import { useState } from "react";
import { Archive, MoreHorizontal, Square, Target, Trash2 } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { evictResearchSessionQueries, researchKeys } from "@multica/core/research";
import type { ResearchSession } from "@multica/core/types/research";
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
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n/use-t";
import { AppLink } from "../../navigation/app-link";

interface ResearchSessionRowActionsProps {
  session: ResearchSession;
  /** Optional session detail href for the goal dialog CTA. */
  href?: string;
}

const STOPPABLE = new Set(["drafting", "running", "awaiting_user_confirm"]);

/**
 * Per-row ⋯ menu. LRM-1106 D2: 「查看目标」 lives here (no inline goal chip).
 */
export function ResearchSessionRowActions({
  session,
  href,
}: ResearchSessionRowActionsProps) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [goalOpen, setGoalOpen] = useState(false);

  const canStop = STOPPABLE.has(session.status);
  const hasGoal = Boolean(session.goal?.trim());
  const archivesCanonicalFacts = session.orchestratorVersion === "research-run-v6";

  const stop = useMutation({
    mutationFn: () => api.stopResearchSession(session.id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, session.id) });
      toast.success(t(($) => $.actions.stop_done));
    },
    onError: (err) =>
      showErrorToast(err instanceof Error ? err.message : String(err)),
  });

  const del = useMutation({
    mutationFn: () => api.deleteResearchSession(session.id),
    onSuccess: async () => {
      await evictResearchSessionQueries(qc, wsId, session.id);
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      toast.success(
        archivesCanonicalFacts
          ? t(($) => $.actions.archive_done)
          : t(($) => $.actions.delete_done),
      );
      setConfirmDelete(false);
    },
    onError: (err) =>
      showErrorToast(err instanceof Error ? err.message : String(err)),
  });

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="size-8 md:size-7"
              aria-label={t(($) => $.actions.menu)}
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => e.stopPropagation()}
            />
          }
        >
          <MoreHorizontal className="h-4 w-4 text-muted-foreground" />
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="w-auto"
          onClick={(e) => e.stopPropagation()}
        >
          {hasGoal ? (
            <DropdownMenuItem
              onClick={() => setGoalOpen(true)}
              data-testid="research-session-view-goal"
            >
              <Target className="h-3.5 w-3.5" />
              {t(($) => $.actions.view_goal)}
            </DropdownMenuItem>
          ) : null}
          {hasGoal && canStop ? <DropdownMenuSeparator /> : null}
          {canStop ? (
            <DropdownMenuItem
              disabled={stop.isPending}
              onClick={() => stop.mutate()}
            >
              <Square className="h-3.5 w-3.5" />
              {t(($) => $.actions.stop)}
            </DropdownMenuItem>
          ) : null}
          {canStop ? <DropdownMenuSeparator /> : null}
          <DropdownMenuItem
            variant={archivesCanonicalFacts ? "default" : "destructive"}
            disabled={del.isPending}
            onClick={() => setConfirmDelete(true)}
          >
            {archivesCanonicalFacts ? (
              <Archive className="h-3.5 w-3.5" />
            ) : (
              <Trash2 className="h-3.5 w-3.5" />
            )}
            {archivesCanonicalFacts
              ? t(($) => $.actions.archive)
              : t(($) => $.actions.delete)}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {hasGoal ? (
        <Dialog open={goalOpen} onOpenChange={setGoalOpen}>
          <DialogContent
            className="sm:max-w-md"
            onClick={(e) => e.stopPropagation()}
          >
            <DialogHeader>
              <DialogTitle>{t(($) => $.list.goal_dialog_title)}</DialogTitle>
            </DialogHeader>
            <p className="max-h-[50vh] overflow-y-auto whitespace-pre-wrap text-sm leading-relaxed text-muted-foreground">
              {session.goal}
            </p>
            <DialogFooter>
              <Button variant="outline" onClick={() => setGoalOpen(false)}>
                {t(($) => $.list.goal_dialog_close)}
              </Button>
              {href ? (
                <AppLink
                  href={href}
                  className="inline-flex h-8 items-center justify-center rounded-lg bg-brand px-2.5 text-sm font-medium text-brand-foreground"
                  onClick={() => setGoalOpen(false)}
                >
                  {t(($) => $.list.goal_dialog_open)}
                </AppLink>
              ) : null}
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}

      {confirmDelete ? (
        <AlertDialog
          open
          onOpenChange={(v) => {
            if (!v) setConfirmDelete(false);
          }}
        >
          <AlertDialogContent onClick={(e) => e.stopPropagation()}>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {archivesCanonicalFacts
                  ? t(($) => $.actions.archive_title)
                  : t(($) => $.actions.delete_title)}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {archivesCanonicalFacts
                  ? t(($) => $.actions.archive_desc)
                  : t(($) => $.actions.delete_desc)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t(($) => $.actions.cancel)}</AlertDialogCancel>
              <AlertDialogAction
                variant={archivesCanonicalFacts ? "default" : "destructive"}
                // LRM-1246 S2 — keep Confirm focusable while delete is pending
                // (native `disabled` drops focus to <body>, same as LRM-1213).
                aria-disabled={del.isPending || undefined}
                className={del.isPending ? "opacity-50 cursor-not-allowed" : undefined}
                data-testid="research-session-delete-confirm"
                onClick={() => {
                  if (del.isPending) return;
                  del.mutate();
                }}
              >
                {archivesCanonicalFacts
                  ? t(($) => $.actions.archive_confirm)
                  : t(($) => $.actions.delete_confirm)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}
    </>
  );
}
