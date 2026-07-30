"use client";

import { useState } from "react";
import { MoreHorizontal, Square, Trash2 } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { researchKeys } from "@multica/core/research";
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n/use-t";

interface ResearchSessionRowActionsProps {
  session: ResearchSession;
}

const STOPPABLE = new Set(["drafting", "running", "awaiting_user_confirm"]);

/**
 * Per-row ⋯ menu for research sessions. Stop pauses the fleet; delete hard-removes
 * the session. Clicks stop propagation so the row link does not navigate.
 */
export function ResearchSessionRowActions({ session }: ResearchSessionRowActionsProps) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const [confirmDelete, setConfirmDelete] = useState(false);

  const canStop = STOPPABLE.has(session.status);

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
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, session.id) });
      toast.success(t(($) => $.actions.delete_done));
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
            variant="destructive"
            disabled={del.isPending}
            onClick={() => setConfirmDelete(true)}
          >
            <Trash2 className="h-3.5 w-3.5" />
            {t(($) => $.actions.delete)}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {confirmDelete ? (
        <AlertDialog
          open
          onOpenChange={(v) => {
            if (!v) setConfirmDelete(false);
          }}
        >
          <AlertDialogContent onClick={(e) => e.stopPropagation()}>
            <AlertDialogHeader>
              <AlertDialogTitle>{t(($) => $.actions.delete_title)}</AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.actions.delete_desc)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t(($) => $.actions.cancel)}</AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                disabled={del.isPending}
                onClick={() => del.mutate()}
              >
                {t(($) => $.actions.delete_confirm)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}
    </>
  );
}
