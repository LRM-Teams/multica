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
 * LRM-405 / LRM-447 / LRM-480 — confirm step for the channel-header
 * "Stop all agents" entry. Function copy stays frozen (uppercase title,
 * warning with channel name, Cancel / Stop All Agents, X close).
 * Visual: destructive token wash + 1px border — never Frank-fixture pink
 * (#FCE8E4) / coral (#E8916A) neo-brutal chrome (LRM-480 / 447 A).
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
        className="gap-0 border-border sm:max-w-md"
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
          className="mt-4 flex items-start gap-2.5 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2.5 text-sm leading-5 text-foreground"
        >
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden="true" />
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
            onClick={() => onOpenChange(false)}
            disabled={confirming}
          >
            {t(($) => $.stop_all_agents.dialog_cancel)}
          </Button>
          <Button
            type="button"
            variant="outline"
            className="border-destructive/35 bg-destructive/10 text-foreground hover:bg-destructive/15 hover:text-foreground"
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
