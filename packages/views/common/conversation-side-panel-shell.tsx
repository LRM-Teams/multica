"use client";

import type { ReactNode } from "react";
import { X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";

/**
 * #645 — shared chrome for every docked right-side conversation panel
 * (Agent profile, Group settings, ...): a bordered `<aside>` with a
 * header row (caller-supplied leading content + a close X in `"panel"`
 * variant) and a scrollable body. Extracted from `AgentSidePanel` so
 * Group Settings reads as the same surface family instead of a
 * one-off card, per Frank/Iris's "布局要收敛" direction.
 *
 * `"page"` variant — LRM-494 Slack channel details: full-page mobile
 * surface uses a text 「完成」/Done trailing control instead of X.
 */
export function ConversationSidePanelShell({
  leading,
  onClose,
  variant = "panel",
  closeAriaLabel,
  doneLabel,
  /** Optional controls before Close (e.g. Message on human profile — LRM-619). */
  actions,
  children,
}: {
  leading: ReactNode;
  onClose: () => void;
  variant?: "panel" | "page";
  closeAriaLabel: string;
  /** When set with `variant="page"`, renders a text Done control. */
  doneLabel?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <aside
      className={cn(
        "flex h-full min-h-0 min-w-0 w-full flex-col bg-background",
        variant === "panel" && "border-l",
      )}
    >
      <div className="flex items-center justify-between gap-3 border-b px-4 py-3">
        <div className="flex min-w-0 items-center gap-2.5">{leading}</div>
        <div className="flex shrink-0 items-center gap-0.5">
          {actions}
          {variant === "panel" ? (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={onClose}
              aria-label={closeAriaLabel}
            >
              <X className="size-4" />
            </Button>
          ) : doneLabel ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onClose}
              className="shrink-0 px-2 font-semibold text-primary"
              data-testid="channel-details-done"
            >
              {doneLabel}
            </Button>
          ) : null}
        </div>
      </div>
      {children}
    </aside>
  );
}
