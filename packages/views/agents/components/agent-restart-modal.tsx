"use client";

import { useState } from "react";
import { Loader2 } from "lucide-react";
import {
  agentLifecycleActionState,
  resolveLifecycleDisabledReasonKey,
  useAgentLifecycle,
} from "@multica/core/agents";
import type { AgentLifecycleActionKind } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { RadioGroup, RadioGroupItem } from "@multica/ui/components/ui/radio-group";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

const TIERS: AgentLifecycleActionKind[] = [
  "restart",
  "reset_session_restart",
  "full_reset_restart",
];

// Compare handles ignoring a leading "@": the handle renders as `@name`
// elsewhere, so typing the "@" the user sees must still match (no dead-end).
const normalizeHandle = (value: string) => value.trim().replace(/^@+/, "");


/**
 * Agent lifecycle three-tier restart modal (#633). Reads the server-authoritative
 * per-action preflight (never `agent.status`) for enable/disable +
 * immediate-vs-scheduled. Full reset is destructive and idle-only: it requires
 * typing the agent's stable @handle to confirm and can never be triggered by
 * Enter. Operation status is BE truth (no optimistic success); a failure shows
 * the reason + Retry. While dormant (before #677 D6 advertises the capability)
 * every tier renders disabled with a reason — correct with zero UI change once
 * the daemon activates.
 */
export function AgentRestartModal({
  agentId,
  agentHandle,
  agentName,
  open,
  onOpenChange,
}: {
  agentId: string;
  agentHandle: string;
  agentName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("agents");
  const lifecycle = useAgentLifecycle(agentId, open);
  const [selected, setSelected] = useState<AgentLifecycleActionKind>("restart");
  const [confirmText, setConfirmText] = useState("");

  const selectedState = agentLifecycleActionState(lifecycle.preflight, selected);
  const op = lifecycle.operation;
  const isActive =
    op?.status === "scheduled" ||
    op?.status === "running" ||
    lifecycle.start.isPending;
  const isTerminalSuccess = op?.status === "succeeded";
  const isTerminalFailed = op?.status === "failed";
  const isFullReset = selected === "full_reset_restart";
  const handleConfirmed =
    !isFullReset || normalizeHandle(confirmText) === normalizeHandle(agentHandle);
  const canSubmit =
    selectedState.supported && handleConfirmed && !isActive && !isTerminalSuccess;

  const reasonLabel = (reason: string | null | undefined): string =>
    t(($) => $.restart_modal.disabled_reason[resolveLifecycleDisabledReasonKey(reason)]);

  const close = () => {
    if (op) lifecycle.refreshAfterTerminal();
    lifecycle.reset();
    setConfirmText("");
    setSelected("restart");
    onOpenChange(false);
  };

  const submit = () => {
    if (!canSubmit) return;
    lifecycle.start.mutate(selected);
  };

  const retry = () => {
    lifecycle.reset();
    lifecycle.start.mutate(selected);
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !isActive && close()}>
      <DialogContent className="w-[calc(100vw-2rem)] sm:max-w-md" showCloseButton={!isActive}>
        <DialogTitle>{t(($) => $.restart_modal.title)}</DialogTitle>
        <DialogDescription>
          {t(($) => $.restart_modal.description, { name: agentName })}
        </DialogDescription>

        <RadioGroup
          value={selected}
          onValueChange={(value) => {
            setSelected(value as AgentLifecycleActionKind);
            setConfirmText("");
          }}
          className="flex flex-col gap-2"
        >
          {TIERS.map((kind) => {
            const state = agentLifecycleActionState(lifecycle.preflight, kind);
            const destructive = kind === "full_reset_restart";
            return (
              <label
                key={kind}
                data-disabled={!state.supported || undefined}
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-md border p-3 text-sm transition-colors",
                  "has-[:checked]:border-primary has-[:checked]:bg-accent/40",
                  destructive && "has-[:checked]:border-destructive/50",
                  !state.supported && "cursor-not-allowed opacity-60",
                )}
              >
                <RadioGroupItem
                  value={kind}
                  disabled={!state.supported}
                  className="mt-0.5 shrink-0"
                />
                <div className="min-w-0">
                  <div className={cn("font-medium", destructive && "text-destructive")}>
                    {t(($) => $.restart_modal.tier[kind].title)}
                  </div>
                  <div className="mt-0.5 text-xs text-muted-foreground">
                    {t(($) => $.restart_modal.tier[kind].scope)}
                  </div>
                  {!state.supported ? (
                    <div className="mt-1 text-xs text-muted-foreground">
                      {reasonLabel(state.disabled_reason)}
                    </div>
                  ) : state.execution_mode === "after_current_run" ? (
                    <div className="mt-1 text-xs text-muted-foreground">
                      {t(($) => $.restart_modal.after_current_run)}
                    </div>
                  ) : null}
                </div>
              </label>
            );
          })}
        </RadioGroup>

        {isFullReset && selectedState.supported && !op && (
          <div className="rounded-md border border-destructive/20 bg-destructive/5 p-3">
            <p className="text-xs leading-relaxed text-destructive">
              {t(($) => $.restart_modal.full_reset_confirm, { handle: agentHandle })}
            </p>
            <input
              type="text"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              // #633: the destructive confirm must never fire on Enter — swallow it
              // so a held Enter can't submit a workspace wipe.
              onKeyDown={(e) => {
                if (e.key === "Enter") e.preventDefault();
              }}
              placeholder={t(($) => $.restart_modal.full_reset_confirm_placeholder, {
                handle: agentHandle,
              })}
              aria-label={t(($) => $.restart_modal.full_reset_confirm_placeholder, {
                handle: agentHandle,
              })}
              className="mt-2 w-full rounded border bg-background px-2 py-1 text-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          </div>
        )}

        <output aria-live="polite" className="block text-xs leading-relaxed empty:hidden">
          {isActive && (
            <span className="inline-flex items-center gap-1.5 text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              {t(($) => $.restart_modal.status[op?.status === "scheduled" ? "scheduled" : "running"])}
            </span>
          )}
          {isTerminalSuccess && (
            <span className="text-success">{t(($) => $.restart_modal.status.succeeded)}</span>
          )}
          {isTerminalFailed && (
            <span className="text-destructive">
              {op?.reason_code
                ? t(($) => $.restart_modal.status.failed_reason, { reason: op.reason_code ?? "" })
                : t(($) => $.restart_modal.status.failed)}
            </span>
          )}
        </output>

        <DialogFooter>
          {isTerminalSuccess ? (
            <Button onClick={close}>{t(($) => $.restart_modal.done)}</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={close} disabled={isActive}>
                {t(($) => $.restart_modal.cancel)}
              </Button>
              <Button
                variant={isFullReset ? "destructive" : "default"}
                onClick={isTerminalFailed ? retry : submit}
                disabled={!isTerminalFailed && !canSubmit}
                // Enter-protection: a full reset must be an explicit click.
                onKeyDown={(e) => {
                  if (isFullReset && e.key === "Enter") e.preventDefault();
                }}
              >
                {isActive && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                {isTerminalFailed
                  ? t(($) => $.restart_modal.retry)
                  : t(($) => $.restart_modal.cta[selected])}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
