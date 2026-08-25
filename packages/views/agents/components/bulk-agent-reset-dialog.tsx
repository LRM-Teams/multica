"use client";

import { useState } from "react";
import type { AgentRestartMode } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
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

const MODES: AgentRestartMode[] = ["restart", "session", "full"];

export function BulkAgentResetDialog({
  count,
  open,
  busy,
  onOpenChange,
  onSubmit,
}: {
  count: number;
  open: boolean;
  busy: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (mode: AgentRestartMode) => Promise<void>;
}) {
  const { t } = useT("runtimes");
  const [mode, setMode] = useState<AgentRestartMode>("restart");

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!busy) onOpenChange(next);
      }}
    >
      <DialogContent className="w-[calc(100vw-2rem)] sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>
            {t(($) => $.machine.agents_bulk_reset_title, { count })}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.machine.agents_bulk_reset_description)}
          </DialogDescription>
        </DialogHeader>
        <div role="radiogroup" className="grid gap-2">
          {MODES.map((candidate) => (
            <button
              key={candidate}
              type="button"
              role="radio"
              aria-checked={mode === candidate}
              disabled={busy}
              onClick={() => setMode(candidate)}
              className={cn(
                "rounded-lg border px-3 py-3 text-left transition-colors",
                mode === candidate
                  ? candidate === "full"
                    ? "border-destructive/50 bg-destructive/5"
                    : "border-primary bg-accent/40"
                  : "border-border hover:bg-muted/40",
              )}
            >
              <span className="text-sm font-medium">
                {t(($) => $.machine.agents_bulk_reset_mode[candidate].title)}
              </span>
              <span className="mt-1 block text-xs text-muted-foreground">
                {t(($) => $.machine.agents_bulk_reset_mode[candidate].description)}
              </span>
            </button>
          ))}
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            disabled={busy}
            onClick={() => onOpenChange(false)}
          >
            {t(($) => $.machine.agents_cancel)}
          </Button>
          <Button
            type="button"
            variant={mode === "full" ? "destructive" : "default"}
            disabled={busy}
            onClick={() => void onSubmit(mode)}
          >
            {busy
              ? t(($) => $.machine.agents_bulk_lifecycle_applying)
              : t(($) => $.machine.agents_bulk_reset_apply, { count })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
