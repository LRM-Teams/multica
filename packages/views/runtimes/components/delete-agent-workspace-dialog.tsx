"use client";

import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
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
import { useT } from "../../i18n/use-t";
import type { WorkspaceRowStatus } from "./machine-workspaces";

export type DeleteAgentWorkspaceTarget = {
  dirName: string;
  displayName: string;
  status: WorkspaceRowStatus;
  displayPath: string;
};

type Props = {
  target: DeleteAgentWorkspaceTarget | null;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: (dirName: string) => void;
};

/**
 * LRM-1148 / LRM-1095 — second-confirm delete for an on-disk agent workspace.
 * ACTIVE/ARCHIVED share the "memory loss" body; ORPHANED uses reclaim copy.
 */
export function DeleteAgentWorkspaceDialog({
  target,
  pending,
  onOpenChange,
  onConfirm,
}: Props) {
  const { t } = useT("runtimes");
  const orphaned = target?.status === "orphaned";

  return (
    <AlertDialog
      open={!!target}
      onOpenChange={(open) => {
        if (!open) onOpenChange(false);
      }}
    >
      <AlertDialogContent data-testid="delete-agent-workspace-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {orphaned
              ? t(($) => $.machine.workspace_delete_confirm_orphaned_title, {
                  name: target?.displayName ?? "",
                })
              : t(($) => $.machine.workspace_delete_confirm_title, {
                  name: target?.displayName ?? "",
                })}
          </AlertDialogTitle>
          <AlertDialogDescription className="space-y-2">
            <span className="block">
              {orphaned
                ? t(($) => $.machine.workspace_delete_confirm_orphaned_body)
                : t(($) => $.machine.workspace_delete_confirm_body)}
            </span>
            {target?.displayPath ? (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <span className="block rounded-md border bg-muted/40 px-2 py-1.5 font-mono text-[11.5px] text-muted-foreground" />
                  }
                >
                  {target.displayPath}
                </TooltipTrigger>
                <TooltipContent side="top">{target.displayPath}</TooltipContent>
              </Tooltip>
            ) : null}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>
            {t(($) => $.machine.workspace_delete_confirm_cancel)}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={pending || !target}
            data-testid="delete-agent-workspace-confirm"
            onClick={(e) => {
              e.preventDefault();
              if (!target || pending) return;
              onConfirm(target.dirName);
            }}
          >
            {pending
              ? t(($) => $.machine.workspace_delete_confirm_submitting)
              : t(($) => $.machine.workspace_delete_confirm_action)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
