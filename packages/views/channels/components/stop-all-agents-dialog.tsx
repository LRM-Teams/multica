"use client";

import { AlertTriangle, X } from "lucide-react";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

/**
 * LRM-405 — confirm before bulk-stopping channel agents (Frank attachment).
 * Content mirrors the product shot; chrome uses channel AlertDialog tokens
 * (not a parallel neo-brutalist skin).
 */
export function StopAllAgentsConfirmDialog({
  open,
  onOpenChange,
  channelName,
  confirming,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channelName: string;
  confirming?: boolean;
  onConfirm: () => void;
}) {
  const { t } = useT("channels");

  const handleOpenChange = (next: boolean) => {
    if (confirming) return;
    onOpenChange(next);
  };

  return (
    <AlertDialog open={open} onOpenChange={handleOpenChange}>
      <AlertDialogContent
        className="w-[calc(100vw-2rem)] !max-w-[440px] gap-0 overflow-hidden rounded-lg p-0"
        data-testid="stop-all-agents-dialog"
      >
        <div className="relative px-5 pb-4 pt-5">
          <AlertDialogTitle className="pr-10 text-base font-bold uppercase tracking-wide">
            {t(($) => $.agent_status.stop_all_confirm_title)}
          </AlertDialogTitle>
          <Button
            type="button"
            variant="outline"
            size="icon-sm"
            className="absolute top-4 right-4"
            aria-label={t(($) => $.agent_status.stop_all_confirm_close)}
            disabled={confirming}
            onClick={() => handleOpenChange(false)}
          >
            <X className="size-4" />
          </Button>
          <AlertDialogDescription className="sr-only">
            {t(($) => $.agent_status.stop_all_confirm_warning_prefix)} #
            {channelName}
            {t(($) => $.agent_status.stop_all_confirm_warning_suffix)}
          </AlertDialogDescription>
          <div
            role="status"
            className="mt-4 flex items-start gap-2.5 rounded-md border border-border bg-destructive/5 px-3 py-2.5 text-sm leading-5 text-foreground"
          >
            <AlertTriangle
              className="mt-0.5 size-4 shrink-0 text-destructive"
              aria-hidden="true"
            />
            <p>
              {t(($) => $.agent_status.stop_all_confirm_warning_prefix)}{" "}
              <strong className="font-semibold">#{channelName}</strong>
              {t(($) => $.agent_status.stop_all_confirm_warning_suffix)}
            </p>
          </div>
        </div>
        <div className="flex justify-end gap-2 border-t bg-muted/25 px-5 py-3">
          <Button
            type="button"
            variant="outline"
            disabled={confirming}
            onClick={() => handleOpenChange(false)}
          >
            {t(($) => $.agent_status.stop_all_confirm_cancel)}
          </Button>
          <Button
            type="button"
            variant="destructive"
            className="bg-destructive text-white hover:bg-destructive/90"
            disabled={confirming}
            onClick={onConfirm}
            data-testid="stop-all-agents-confirm"
          >
            {t(($) => $.agent_status.stop_all_confirm_action)}
          </Button>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  );
}
