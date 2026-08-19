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
 * surface uses a text 「完成」/Done trailing control instead of X. When no
 * `doneLabel` is supplied the page still renders an X (LRM-1185): a `"page"`
 * surface must never ship an empty dismiss slot.
 */
export function ConversationSidePanelShell({
  leading,
  onClose,
  variant = "panel",
  /**
   * Header chrome shape. Defaults to `"bar"` (a full-width header row with
   * leading content + close control) — the original #645 shared chrome.
   *
   * `"floating"` (LRM-542) drops the header row entirely so the panel's own
   * first child (e.g. an agent identity block) can sit flush at the top,
   * and renders the close control as a small ghost button floating at the
   * panel's top-right corner. Used by the agent profile panel, whose header
   * is now the avatar + name row itself rather than a separate close bar.
   */
  header = "bar",
  closeAriaLabel,
  doneLabel,
  /** Optional controls before Close (e.g. Message on human profile — LRM-619). */
  actions,
  /**
   * Members Directory (ADR 0013): embed panel as a page column without
   * dock Close/Done — selection changes via the left rail.
   */
  hideDismiss = false,
  children,
}: {
  leading?: ReactNode;
  onClose: () => void;
  variant?: "panel" | "page";
  header?: "bar" | "floating";
  closeAriaLabel: string;
  /** When set with `variant="page"`, renders a text Done control. */
  doneLabel?: string;
  actions?: ReactNode;
  hideDismiss?: boolean;
  children: ReactNode;
}) {
  const closeControl = hideDismiss ? null : variant === "panel" ? (
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
    ) : (
      // LRM-1185 / LRM-974 gate rule "page 禁止空 chrome": a `"page"` host that
      // does not supply a Done label used to get `closeControl = null`, so the
      // mobile actor profile (agent `header="floating"`, member bar) rendered a
      // dismiss slot with nothing in it — users read that as "there is no close
      // button". Match the copied chrome icon buttons (`size="icon"` / 32×32)
      // so X sits in the same row as Message / Start / Restart.
      <Button
        type="button"
        variant="ghost"
        size="icon"
        onClick={onClose}
        aria-label={closeAriaLabel}
        className="shrink-0"
        data-testid="side-panel-page-close"
      >
        <X className="size-4" />
      </Button>
    );

  // Floating header (LRM-542): no chrome row — close control floats at the
  // top-right corner over whatever the caller renders first. The caller's
  // first child owns the top padding, so the close button is positioned with
  // `absolute top-2.5 right-2.5` to clear a 56px avatar row.
  if (header === "floating") {
    return (
      <aside
        className={cn(
          "relative flex h-full min-h-0 min-w-0 w-full flex-col bg-background",
          variant === "panel" && "border-l",
        )}
      >
        <div className="absolute right-2.5 top-2.5 z-20 flex shrink-0 items-center gap-0.5">
          {actions}
          {closeControl}
        </div>
        {children}
      </aside>
    );
  }

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
          {closeControl}
        </div>
      </div>
      {children}
    </aside>
  );
}
