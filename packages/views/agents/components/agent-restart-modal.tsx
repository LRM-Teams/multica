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
 * Agent lifecycle restart modal (#633 / #26 / #27).
 * Default path: Restart one-click. Three short selectable blocks (Raft-like);
 * long scope copy removed. Full-reset handle confirm only after Full is selected.
 * `scheduled` is non-blocking (#26); force semantics are BE (#1900).
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
  // Only hard-block while actually running — scheduled never auto-promotes (#26).
  const isBlocking =
    op?.status === "running" || lifecycle.start.isPending;
  const isScheduled = op?.status === "scheduled";
  const isTerminalSuccess = op?.status === "succeeded";
  const isTerminalFailed = op?.status === "failed";
  const isFullReset = selected === "full_reset_restart";
  const handleConfirmed =
    !isFullReset || normalizeHandle(confirmText) === normalizeHandle(agentHandle);
  const canSubmit =
    selectedState.supported &&
    handleConfirmed &&
    !isBlocking &&
    !isScheduled &&
    !isTerminalSuccess;

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
    <Dialog open={open} onOpenChange={(next) => !next && !isBlocking && close()}>
      <DialogContent className="w-[calc(100vw-2rem)] sm:max-w-sm" showCloseButton={!isBlocking}>
        <DialogTitle>{t(($) => $.restart_modal.title)}</DialogTitle>
        <DialogDescription>
          {t(($) => $.restart_modal.description_short, { name: agentName })}
        </DialogDescription>

        {/* Three short blocks — title only (Frank: not dense radio + long copy) */}
        <div
          role="radiogroup"
          aria-label={t(($) => $.restart_modal.title)}
          className="grid grid-cols-1 gap-2"
          data-testid="restart-tier-blocks"
        >
          {TIERS.map((kind) => {
            const state = agentLifecycleActionState(lifecycle.preflight, kind);
            const destructive = kind === "full_reset_restart";
            const isSelected = selected === kind;
            const primary = kind === "restart";
            return (
              <button
                key={kind}
                type="button"
                role="radio"
                aria-checked={isSelected}
                disabled={!state.supported || isBlocking || isScheduled || isTerminalSuccess}
                data-testid={`restart-tier-${kind}`}
                data-selected={isSelected || undefined}
                data-disabled={!state.supported || undefined}
                onClick={() => {
                  setSelected(kind);
                  setConfirmText("");
                }}
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
                {!state.supported ? (
                  <div className="mt-1 text-xs font-normal text-muted-foreground">
                    {reasonLabel(state.disabled_reason)}
                  </div>
                ) : null}
              </button>
            );
          })}
        </div>

        {/* Full-reset confirm only after Full is selected */}
        {isFullReset && selectedState.supported && !op && (
          <div className="rounded-md border border-destructive/20 bg-destructive/5 p-3">
            <p className="text-xs leading-relaxed text-destructive">
              {t(($) => $.restart_modal.full_reset_confirm, { handle: agentHandle })}
            </p>
            <input
              type="text"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
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
          {isBlocking && (
            <span className="inline-flex items-center gap-1.5 text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              {t(($) => $.restart_modal.status.running)}
            </span>
          )}
          {isScheduled && (
            <span className="text-muted-foreground">
              {t(($) => $.restart_modal.status.scheduled)}
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
          {isTerminalSuccess || isScheduled ? (
            <Button onClick={close}>{t(($) => $.restart_modal.done)}</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={close} disabled={isBlocking}>
                {t(($) => $.restart_modal.cancel)}
              </Button>
              <Button
                variant={isFullReset ? "destructive" : "default"}
                onClick={isTerminalFailed ? retry : submit}
                disabled={!isTerminalFailed && !canSubmit}
                onKeyDown={(e) => {
                  if (isFullReset && e.key === "Enter") e.preventDefault();
                }}
              >
                {isBlocking && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
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
