"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { Copy, Quote as QuoteIcon } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  buildSelectionQuoteMarkdown,
  isFinePointerViewport,
  resolveMessageSelection,
  type ResolvedMessageSelection,
} from "../lib/selection-quote";

export interface UseSelectionQuoteMenuOptions {
  /**
   * Conversation scroll container the menu floats over. Selections are only
   * honored when both endpoints live inside this element.
   */
  containerRef: RefObject<HTMLElement | null>;
  /**
   * Append the built quote markdown to the active composer (channel or thread).
   * The caller decides which editor receives it.
   */
  onQuote: (markdown: string) => void;
}

export interface SelectionQuoteMenuHandle {
  /** React node (the floating menu) to render inside the conversation area. */
  menu: React.ReactNode;
}

/**
 * LRM-695 — drives the text-selection Quote/Copy mini-menu.
 *
 * Lifecycle (desktop / fine pointer only — mobile keeps the OS selection menu):
 *  - `pointerup` inside the container → resolve the live selection; show the
 *    menu positioned over the selection range, else dismiss.
 *  - `selectionchange` (rAF-debounced) → dismiss once the selection collapses
 *    or leaves a message body. We never SHOW on selectionchange (avoids jitter).
 *  - `contextmenu` on the container → suppress the browser's native menu WHILE a
 *    message-body selection exists; with no selection, right-click stays native.
 *  - scroll / resize → dismiss (the selection rect goes stale).
 *
 * The Quote/Copy buttons swallow `mousedown` (`preventDefault`) so clicking them
 * does NOT collapse the selection before the click handler reads it.
 */
export function useSelectionQuoteMenu({
  containerRef,
  onQuote,
}: UseSelectionQuoteMenuOptions): SelectionQuoteMenuHandle {
  const { t } = useT("channels");
  const [resolved, setResolved] = useState<ResolvedMessageSelection | null>(null);
  // Latest callback in a ref so document listeners (bound once) stay current.
  const onQuoteRef = useRef(onQuote);
  onQuoteRef.current = onQuote;
  // The live Selection captured at pointerup. We snapshot the resolved DATA into
  // state for rendering, but keep the live Selection so Quote/Copy can clear it
  // after acting (clean visual exit).
  const liveSelectionRef = useRef<Selection | null>(null);
  const rafRef = useRef<ReturnType<typeof requestAnimationFrame> | null>(null);

  const dismiss = useCallback(() => {
    setResolved(null);
    liveSelectionRef.current = null;
  }, []);

  const resolveCurrent = useCallback((): ResolvedMessageSelection | null => {
    if (!isFinePointerViewport()) return null;
    return resolveMessageSelection(
      typeof window !== "undefined" ? window.getSelection() : null,
      containerRef.current,
    );
  }, [containerRef]);

  const handlePointerUp = useCallback(() => {
    const next = resolveCurrent();
    if (next) {
      liveSelectionRef.current =
        typeof window !== "undefined" ? window.getSelection() : null;
      setResolved(next);
    } else {
      // pointerup landed with no usable selection (e.g. a plain click) → hide.
      setResolved(null);
      liveSelectionRef.current = null;
    }
  }, [resolveCurrent]);

  const handleSelectionChange = useCallback(() => {
    if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
    rafRef.current = requestAnimationFrame(() => {
      rafRef.current = null;
      // Only ever HIDE here. Showing is pointerup's job.
      if (liveSelectionRef.current === null) return;
      const sel = typeof window !== "undefined" ? window.getSelection() : null;
      if (!sel || sel.isCollapsed) {
        setResolved(null);
        liveSelectionRef.current = null;
      }
    });
  }, []);

  const handleScrollOrResize = useCallback(() => {
    setResolved(null);
    liveSelectionRef.current = null;
  }, []);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const doc = container.ownerDocument;
    doc.addEventListener("pointerup", handlePointerUp);
    doc.addEventListener("selectionchange", handleSelectionChange);
    // Capture-phase scroll on the document catches Virtuoso's internal scroller
    // (scroll events do not bubble), so the menu dismisses on any scroll without
    // needing the container itself to be the scroller.
    doc.addEventListener("scroll", handleScrollOrResize, true);
    doc.defaultView?.addEventListener("resize", handleScrollOrResize);
    return () => {
      doc.removeEventListener("pointerup", handlePointerUp);
      doc.removeEventListener("selectionchange", handleSelectionChange);
      doc.removeEventListener("scroll", handleScrollOrResize, true);
      doc.defaultView?.removeEventListener("resize", handleScrollOrResize);
      if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
    };
  }, [
    containerRef,
    handlePointerUp,
    handleSelectionChange,
    handleScrollOrResize,
  ]);

  const handleQuote = useCallback(() => {
    const current = resolved;
    if (!current) return;
    onQuoteRef.current(buildSelectionQuoteMarkdown(current.author, current.text));
    liveSelectionRef.current?.removeAllRanges();
    dismiss();
  }, [resolved, dismiss]);

  const handleCopy = useCallback(async () => {
    const current = resolved;
    if (!current) return;
    if (await copyText(current.text)) {
      toast.success(t(($) => $.message.copied_toast));
    } else {
      showErrorToast(t(($) => $.message.copy_failed_toast));
    }
    liveSelectionRef.current?.removeAllRanges();
    dismiss();
  }, [resolved, dismiss, t]);

  const menu = (
    <SelectionQuoteMenu
      resolved={resolved}
      onQuote={handleQuote}
      onCopy={handleCopy}
    />
  );

  return { menu };
}

/**
 * Presentational floating pill. Rendered through a portal to document.body so a
 * transformed/overflowing scroll container can't trap it in a local stacking
 * context. Hidden when `resolved` is null.
 */
function SelectionQuoteMenu({
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

  const swallowMouseDown = (event: ReactMouseEvent) => {
    // Keep the selection alive so the click handler still reads it.
    event.preventDefault();
  };

  return createPortal(
    <div
      role="toolbar"
      aria-label={quoteLabel}
      data-testid="selection-quote-menu"
      // pointer-events-auto + a high z-index so the pill sits above message
      // content and the hover action bar while a selection is live.
      className="pointer-events-auto fixed z-50 flex items-center gap-0.5 rounded-lg border border-border/70 bg-popover p-0.5 text-popover-foreground shadow-md"
      style={{ top, left }}
      // A pointerdown starting on the pill must not be treated as an outside
      // dismiss (the menu buttons own their clicks).
      onMouseDown={swallowMouseDown}
    >
      <MenuButton
        icon={<QuoteIcon className="size-3.5" />}
        label={quoteLabel}
        onClick={onQuote}
        onMouseDown={swallowMouseDown}
        testId="selection-quote-quote"
      />
      <span aria-hidden className="h-4 w-px bg-border/70" />
      <MenuButton
        icon={<Copy className="size-3.5" />}
        label={copyLabel}
        onClick={onCopy}
        onMouseDown={swallowMouseDown}
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
  onMouseDown,
  testId,
}: {
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
  onMouseDown: (event: ReactMouseEvent) => void;
  testId: string;
}) {
  return (
    <button
      type="button"
      data-testid={testId}
      onClick={onClick}
      onMouseDown={onMouseDown}
      className={cn(
        "inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs font-medium",
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
