"use client";

import { type MouseEvent as ReactMouseEvent } from "react";
import { createPortal } from "react-dom";
import { Copy, Quote as QuoteIcon } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { ResolvedMessageSelection } from "../lib/selection-quote";

/** Keep the selection alive so the click handler still reads it (pure helper). */
function swallowMouseDown(event: ReactMouseEvent) {
  event.preventDefault();
}

/**
 * Presentational floating pill (LRM-695). Rendered through a portal to
 * document.body so a transformed/overflowing scroll container can't trap it in
 * a local stacking context. Hidden when `resolved` is null.
 */
export function SelectionQuoteMenu({
  resolved,
  onQuote,
  onCopy,
}: {
  resolved: ResolvedMessageSelection | null;
  onQuote: () => void;
  onCopy: () => void;
}) {
  const { t } = useT("channels");
  if (!resolved || typeof document === "undefined") return null;

  // Computed lazily (after the null check) so hidden menus never touch the
  // i18n accessor — tests with partial locale fixtures aren't forced to provide
  // these keys when no selection is active.
  const quoteLabel = t(($) => $.quote.action);
  const copyLabel = t(($) => $.message.copy_action);

  // Center the pill above the selection; clamp into the viewport.
  const MENU_ESTIMATE_WIDTH = 132;
  const GAP = 8;
  const top = Math.max(resolved.rect.top - 36 - GAP, 4);
  let left = resolved.rect.left + resolved.rect.width / 2 - MENU_ESTIMATE_WIDTH / 2;
  left = Math.max(4, Math.min(left, window.innerWidth - MENU_ESTIMATE_WIDTH - 4));

  return createPortal(
    <div
      role="toolbar"
      tabIndex={-1}
      aria-label={quoteLabel}
      data-testid="selection-quote-menu"
      // pointer-events-auto + a high z-index so the pill sits above message
      // content and the hover action bar while a selection is live.
      // LRM-695 frozen v3 spec (UI Designer): bg-popover / rounded-md / h-8 items,
      // horizontal Slack-style pill above the selection.
      className="pointer-events-auto fixed z-50 flex items-center gap-0.5 rounded-md border border-border/70 bg-popover p-0.5 text-popover-foreground shadow-md"
      style={{ top, left }}
      // A pointerdown starting on the pill must not be treated as an outside
      // dismiss (the menu buttons own their clicks).
      onMouseDown={swallowMouseDown}
    >
      <MenuButton
        icon={<QuoteIcon className="size-3.5" />}
        label={quoteLabel}
        onClick={onQuote}
        testId="selection-quote-quote"
      />
      <span aria-hidden className="h-4 w-px bg-border/70" />
      <MenuButton
        icon={<Copy className="size-3.5" />}
        label={copyLabel}
        onClick={onCopy}
        testId="selection-quote-copy"
      />
    </div>,
    document.body,
  );
}

function MenuButton({
  icon,
  label,
  onClick,
  testId,
}: {
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
  testId: string;
}) {
  return (
    <button
      type="button"
      data-testid={testId}
      onClick={onClick}
      onMouseDown={swallowMouseDown}
      className={cn(
        "inline-flex h-8 items-center gap-1 rounded-md px-2 text-xs font-medium",
        "text-popover-foreground/90 transition-colors",
        "hover:bg-muted hover:text-foreground",
        "focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none",
      )}
    >
      {icon}
      <span>{label}</span>
    </button>
  );
}
