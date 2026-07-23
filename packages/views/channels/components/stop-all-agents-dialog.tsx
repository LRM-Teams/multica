"use client";

import { AlertTriangle } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

/**
 * LRM-405 — confirm step for the channel-header "Stop all agents" entry.
 * Matches Frank's attached mock: uppercase title, warning callout with the
 * current channel name, Cancel / Stop All Agents, and an X close. Confirm
 * is the only path that stops; cancel/close must be side-effect free.
 */
export interface StopAllAgentsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channelName: string;
  onConfirm: () => void;
  confirming?: boolean;
}

export function StopAllAgentsDialog({
  open,
  onOpenChange,
  channelName,
  onConfirm,
  confirming = false,
}: StopAllAgentsDialogProps) {
  const { t } = useT("channels");
  const channelLabel = channelName.startsWith("#") ? channelName : `#${channelName}`;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="gap-0 sm:max-w-md"
        data-testid="stop-all-agents-dialog"
        // Clicks inside must not bubble to any clickable ancestor (header
        // actions / drawer rows).
        onClick={(e) => e.stopPropagation()}
      >
        <DialogHeader className="pr-8">
          <DialogTitle className="text-base font-bold uppercase tracking-wide">
            {t(($) => $.stop_all_agents.dialog_title)}
          </DialogTitle>
          <DialogDescription className="sr-only">
            {t(($) => $.stop_all_agents.dialog_warning_before)}
            {channelLabel}
            {t(($) => $.stop_all_agents.dialog_warning_after)}
          </DialogDescription>
        </DialogHeader>

        <div
          role="alert"
          className="mt-4 flex items-start gap-2.5 rounded-md border border-foreground/80 bg-[#FCE8E4] px-3 py-2.5 text-sm leading-5 text-foreground dark:border-destructive/50 dark:bg-destructive/10"
        >
          <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <span>
            {t(($) => $.stop_all_agents.dialog_warning_before)}
            <strong className="font-semibold">{channelLabel}</strong>
            {t(($) => $.stop_all_agents.dialog_warning_after)}
          </span>
        </div>

        <DialogFooter className="mt-4 border-0 bg-transparent p-0 sm:justify-end">
          <Button
            type="button"
            variant="outline"
            className="border-foreground/80 shadow-[2px_2px_0_0_var(--foreground)]"
            onClick={() => onOpenChange(false)}
            disabled={confirming}
          >
            {t(($) => $.stop_all_agents.dialog_cancel)}
          </Button>
          <Button
            type="button"
            className="border border-foreground/80 bg-[#E8916A] text-foreground shadow-[2px_2px_0_0_var(--foreground)] hover:bg-[#E07F55] hover:text-foreground"
            disabled={confirming}
            onClick={() => {
              onOpenChange(false);
              onConfirm();
            }}
            data-testid="stop-all-agents-confirm"
          >
            {t(($) => $.stop_all_agents.dialog_confirm)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
