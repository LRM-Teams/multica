"use client";

import { useState } from "react";
import { Loader2 } from "lucide-react";
import {
  agentRestartModeState,
  resolveRestartDisabledReasonKey,
  useAgentRestart,
} from "@multica/core/agents";
import type { AgentRestartMode } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

const TIERS: AgentRestartMode[] = [
  "restart",
  "session",
  "full",
];

/**
 * Raft-aligned Agent restart modal.
 * Three selectable blocks with short title + one-line scope (Frank: each
 * restart kind must be clear). Full reset: no type-to-confirm — select Full
 * and click CTA.
 *
 * #34 (Frank): on successful start, close immediately — progress lives on the
 * agent's current Runner Activity, not in-modal
 * running/succeeded chrome that trapped users on "Done".
 */
export function AgentRestartModal({
  agentId,
  agentHandle: _agentHandle,
  agentName,
  open,
  onOpenChange,
}: {
  agentId: string;
  /** Kept for call-site stability; type-to-confirm removed. */
  agentHandle: string;
  agentName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  void _agentHandle;
  const { t } = useT("agents");
  const restart = useAgentRestart(agentId, open);
  const [selected, setSelected] = useState<AgentRestartMode>("restart");

  const selectedState = agentRestartModeState(restart.preflight, selected);
  const isSubmitting = restart.resetAgent.isPending;
  const isFullReset = selected === "full";
  const canSubmit = selectedState.supported && !isSubmitting;

  const reasonLabel = (reason: string | null | undefined): string =>
    t(($) => $.restart_modal.disabled_reason[resolveRestartDisabledReasonKey(reason)]);

  const close = () => {
    restart.clear();
    setSelected("restart");
    onOpenChange(false);
  };

  /** R1/R3/R5: start accepted → dismiss modal; agent surfaces show Starting. */
  const dismissAfterStart = () => {
    restart.refreshAfterRequest();
    restart.clear();
    setSelected("restart");
    onOpenChange(false);
  };

  const submit = () => {
    if (!canSubmit) return;
    restart.resetAgent.mutate(selected, {
      onSuccess: () => {
        dismissAfterStart();
      },
    });
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !isSubmitting && close()}>
      <DialogContent className="w-[calc(100vw-2rem)] sm:max-w-sm" showCloseButton={!isSubmitting}>
        <DialogTitle>{t(($) => $.restart_modal.title)}</DialogTitle>
        <DialogDescription>
          {t(($) => $.restart_modal.description_short, { name: agentName })}
        </DialogDescription>

        <div
          role="radiogroup"
          aria-label={t(($) => $.restart_modal.title)}
          className="grid grid-cols-1 gap-2"
          data-testid="restart-tier-blocks"
        >
          {TIERS.map((kind) => {
            const state = agentRestartModeState(restart.preflight, kind);
            const destructive = kind === "full";
            const isSelected = selected === kind;
            const primary = kind === "restart";
            return (
              <button
                key={kind}
                type="button"
                role="radio"
                aria-checked={isSelected}
                disabled={!state.supported || isSubmitting}
                data-testid={`restart-tier-${kind}`}
                data-selected={isSelected || undefined}
                data-disabled={!state.supported || undefined}
                onClick={() => setSelected(kind)}
                className={cn(
                  "rounded-lg border px-3 py-3 text-left text-sm font-medium transition-colors",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  isSelected && !destructive && "border-primary bg-accent/40",
                  isSelected && destructive && "border-destructive/50 bg-destructive/5 text-destructive",
                  !isSelected && "border-border hover:bg-muted/40",
                  primary && !isSelected && "border-primary/40",
                  !state.supported && "cursor-not-allowed opacity-50",
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <span>{t(($) => $.restart_modal.tier[kind].title_short)}</span>
                  {primary ? (
                    <span className="text-[10px] font-normal uppercase tracking-wide text-muted-foreground">
                      {t(($) => $.restart_modal.recommended)}
                    </span>
                  ) : null}
                </div>
                <div className="mt-1 text-xs font-normal leading-snug text-muted-foreground">
                  {t(($) => $.restart_modal.tier[kind].scope)}
                </div>
                {!state.supported ? (
                  <div className="mt-1 text-xs font-normal text-muted-foreground">
                    {reasonLabel(state.disabled_reason)}
                  </div>
                ) : null}
              </button>
            );
          })}
        </div>

        {restart.resetAgent.isError ? (
          <p role="alert" className="text-sm text-destructive">
            {t(($) => $.restart_modal.request_failed)}
          </p>
        ) : null}

        <DialogFooter>
          <Button variant="ghost" onClick={close} disabled={isSubmitting}>
            {t(($) => $.restart_modal.cancel)}
          </Button>
          <Button
            variant={isFullReset ? "destructive" : "default"}
            onClick={submit}
            disabled={!canSubmit}
            data-testid="restart-modal-submit"
          >
            {isSubmitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t(($) => $.restart_modal.cta[selected])}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
