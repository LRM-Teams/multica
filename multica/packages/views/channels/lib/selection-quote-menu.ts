"use client";

import {
  createElement,
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
} from "react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { copyText } from "@multica/ui/lib/clipboard";
import { useT } from "../../i18n/use-t";
import { SelectionQuoteMenu } from "../components/selection-quote-menu";
import {
  buildSelectionQuoteMarkdown,
  isFinePointerViewport,
  resolveMessageSelection,
  type ResolvedMessageSelection,
} from "./selection-quote";

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
 *  - scroll / resize → dismiss (the selection rect goes stale).
 *
 * The Quote/Copy buttons swallow `mousedown` (`preventDefault`) so clicking them
 * does NOT collapse the selection before the click handler reads it.
 *
 * Live in a `.ts` module (not the `.tsx` component file) so the component file
 * only exports components — keeps Fast Refresh clean (react-doctor).
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

  // Latest handlers in a ref so the document listeners bind ONCE on mount and
  // always invoke the current closures — no re-subscribe on handler change.
  const handlersRef = useRef({ handlePointerUp, handleSelectionChange, handleScrollOrResize });
  handlersRef.current = { handlePointerUp, handleSelectionChange, handleScrollOrResize };

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const doc = container.ownerDocument;
    const onPointerUp = () => handlersRef.current.handlePointerUp();
    const onSelectionChange = () => handlersRef.current.handleSelectionChange();
    const onScrollOrResize = () => handlersRef.current.handleScrollOrResize();
    doc.addEventListener("pointerup", onPointerUp);
    doc.addEventListener("selectionchange", onSelectionChange);
    // Capture-phase scroll on the document catches Virtuoso's internal scroller
    // (scroll events do not bubble), so the menu dismisses on any scroll without
    // needing the container itself to be the scroller.
    doc.addEventListener("scroll", onScrollOrResize, true);
    doc.defaultView?.addEventListener("resize", onScrollOrResize);
    return () => {
      doc.removeEventListener("pointerup", onPointerUp);
      doc.removeEventListener("selectionchange", onSelectionChange);
      doc.removeEventListener("scroll", onScrollOrResize, true);
      doc.defaultView?.removeEventListener("resize", onScrollOrResize);
      if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
    };
  }, [containerRef, handlersRef]);

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

  const menu = createElement(SelectionQuoteMenu, {
    resolved,
    onQuote: handleQuote,
    onCopy: handleCopy,
  });

  return { menu };
}
