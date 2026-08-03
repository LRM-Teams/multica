"use client";

import { useCallback, useEffect, useEffectEvent, useRef } from "react";

/**
 * LRM-1100 — keyboard/focus contract for the research session desktop side
 * overlays (aux drawer, fleet chat drawer).
 *
 * The narrow-viewport branches of those panels render a Radix `Sheet`, which
 * already gives Escape-to-close plus focus move-in/restore. The desktop
 * branches render a bare `<aside>` overlay on top of the canvas and had none of
 * it, so keyboard users could not dismiss the panel and lost their focus point.
 *
 * The panel is intentionally **non-modal** (the canvas beside it stays
 * interactive), so this hook does not trap focus. It only:
 * - closes on Escape while active,
 * - moves focus into the panel when it becomes active,
 * - restores focus to the previously focused element when it deactivates.
 */
export function useOverlayPanelA11y({
  active,
  onClose,
}: {
  /** True while the desktop overlay branch is mounted and visible. */
  active: boolean;
  onClose: () => void;
}) {
  const panelRef = useRef<HTMLElement | null>(null);
  const restoreRef = useRef<HTMLElement | null>(null);

  const closeFromEscape = useEffectEvent(() => {
    onClose();
  });

  useEffect(() => {
    if (!active) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      event.preventDefault();
      closeFromEscape();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [active]);

  useEffect(() => {
    if (!active) return;
    const doc = panelRef.current?.ownerDocument ?? globalThis.document;
    const previous = doc?.activeElement;
    restoreRef.current =
      previous instanceof HTMLElement && previous !== doc.body ? previous : null;

    const panel = panelRef.current;
    if (panel && !panel.contains(doc.activeElement)) {
      const firstFocusable = panel.querySelector<HTMLElement>(
        '[data-autofocus="true"], button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      );
      (firstFocusable ?? panel).focus({ preventScroll: true });
    }

    return () => {
      const target = restoreRef.current;
      restoreRef.current = null;
      if (target?.isConnected) target.focus({ preventScroll: true });
    };
  }, [active]);

  const bindPanel = useCallback((node: HTMLElement | null) => {
    panelRef.current = node;
  }, []);

  return { bindPanel };
}
