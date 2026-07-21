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
 * `"page"` variant drops the close button — used when the same body is
 * hosted full-width (a mobile Drawer sub-page) instead of docked next to
 * the conversation.
 */
export function ConversationSidePanelShell({
  leading,
  onClose,
  variant = "panel",
  closeAriaLabel,
  children,
}: {
  leading: ReactNode;
  onClose: () => void;
  variant?: "panel" | "page";
  closeAriaLabel: string;
  children: ReactNode;
}) {
  return (
    <aside
      className={cn(
        "flex h-full min-h-0 min-w-0 w-full flex-col bg-background",
        variant === "panel" && "border-l",
      )}
    >
      <div className="flex items-center justify-between gap-3 border-b p-4">
        <div className="flex min-w-0 items-center gap-2.5">{leading}</div>
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
        ) : null}
      </div>
      {children}
    </aside>
  );
}
