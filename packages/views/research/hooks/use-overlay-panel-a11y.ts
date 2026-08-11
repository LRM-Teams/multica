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
 *
 * LRM-1177: restore cannot rely on node identity alone. The desktop chat
 * float's opener may unmount while the panel is open and return as a different
 * DOM node, so the old node is detached by the time we restore.
 * (`id`, else `data-testid`) and re-find the control on the way out.
 */

type RestoreKey =
  | { kind: "id"; value: string }
  | { kind: "testId"; value: string };

function restoreKeyFor(element: HTMLElement): RestoreKey | null {
  if (element.id) return { kind: "id", value: element.id };
  const testId = element.dataset.testid;
  if (testId) return { kind: "testId", value: testId };
  return null;
}

/**
 * Resolved without building a selector string so arbitrary ids / test ids
 * cannot produce an invalid or injected selector.
 */
function resolveRestoreKey(
  doc: Document,
  key: RestoreKey | null,
): HTMLElement | null {
  if (!key) return null;
  if (key.kind === "id") return doc.getElementById(key.value);
  for (const candidate of doc.querySelectorAll<HTMLElement>("[data-testid]")) {
    if (candidate.dataset.testid === key.value) return candidate;
  }
  return null;
}

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
  const restoreKeyRef = useRef<RestoreKey | null>(null);

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

  // Track the last control focused OUTSIDE the panel while it is closed.
  // Capturing only at activation time is too late: React may remove the opener in
  // the same commit that opens the panel, so by the time our activation effect
  // runs `document.activeElement` is already `<body>` and there is nothing left
  // to remember.
  useEffect(() => {
    if (active) return;
    const doc = panelRef.current?.ownerDocument ?? globalThis.document;
    const remember = () => {
      const el = doc.activeElement;
      if (!(el instanceof HTMLElement) || el === doc.body) return;
      restoreRef.current = el;
      restoreKeyRef.current = restoreKeyFor(el);
    };
    remember();
    doc.addEventListener("focusin", remember);
    return () => doc.removeEventListener("focusin", remember);
  }, [active]);

  useEffect(() => {
    if (!active) return;
    const doc = panelRef.current?.ownerDocument ?? globalThis.document;
    const previous = doc?.activeElement;
    // Only overwrite when there is still a live focused control; otherwise keep
    // whatever the tracker above captured before the opener was removed.
    if (previous instanceof HTMLElement && previous !== doc.body) {
      restoreRef.current = previous;
      restoreKeyRef.current = restoreKeyFor(previous);
    }

    const panel = panelRef.current;
    if (panel && !panel.contains(doc.activeElement)) {
      const firstFocusable = panel.querySelector<HTMLElement>(
        '[data-autofocus="true"], button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      );
      (firstFocusable ?? panel).focus({ preventScroll: true });
    }

    return () => {
      const target = restoreRef.current;
      const key = restoreKeyRef.current;
      restoreRef.current = null;
      restoreKeyRef.current = null;
      const resolved = target?.isConnected
        ? target
        : resolveRestoreKey(doc, key);
      // No re-locator and the original node is gone: leave focus where it is
      // rather than grabbing an unrelated control.
      resolved?.focus({ preventScroll: true });
    };
  }, [active]);

  const bindPanel = useCallback((node: HTMLElement | null) => {
    panelRef.current = node;
  }, []);

  return { bindPanel };
}
