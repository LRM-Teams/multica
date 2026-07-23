"use client";

import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";

/** Align with docked channel/DM ResizablePanel defaults (LRM-481). */
export const PROFILE_PANEL_WIDTH_DEFAULT = 440;
export const PROFILE_PANEL_WIDTH_MIN = 360;
export const PROFILE_PANEL_WIDTH_MAX = 640;
export const PROFILE_PANEL_WIDTH_STORAGE_KEY = "multica_profile_panel_width";

function clampWidth(value: number): number {
  return Math.min(
    PROFILE_PANEL_WIDTH_MAX,
    Math.max(PROFILE_PANEL_WIDTH_MIN, Math.round(value)),
  );
}

function readStoredWidth(): number {
  if (typeof window === "undefined") return PROFILE_PANEL_WIDTH_DEFAULT;
  try {
    const raw = window.localStorage.getItem(PROFILE_PANEL_WIDTH_STORAGE_KEY);
    if (!raw) return PROFILE_PANEL_WIDTH_DEFAULT;
    const parsed = Number(raw);
    return Number.isFinite(parsed) ? clampWidth(parsed) : PROFILE_PANEL_WIDTH_DEFAULT;
  } catch {
    return PROFILE_PANEL_WIDTH_DEFAULT;
  }
}

/**
 * Desktop-only drag width for the global (overlay) Profile panel.
 * Persists across refresh via localStorage. Mobile uses full-screen page route
 * and should not call this hook's drag handle.
 */
export function useProfilePanelWidth() {
  const [width, setWidth] = useState(PROFILE_PANEL_WIDTH_DEFAULT);
  const widthRef = useRef(width);
  widthRef.current = width;

  useEffect(() => {
    setWidth(readStoredWidth());
  }, []);

  const persist = useCallback((next: number) => {
    const clamped = clampWidth(next);
    setWidth(clamped);
    try {
      window.localStorage.setItem(PROFILE_PANEL_WIDTH_STORAGE_KEY, String(clamped));
    } catch {
      // ignore quota / private mode
    }
  }, []);

  const onResizePointerDown = useCallback(
    (event: ReactPointerEvent<HTMLElement>) => {
      if (event.button !== 0) return;
      event.preventDefault();
      const startX = event.clientX;
      const startWidth = widthRef.current;
      let liveWidth = startWidth;
      const target = event.currentTarget;
      try {
        target.setPointerCapture(event.pointerId);
      } catch {
        // jsdom / older browsers may not support pointer capture
      }

      const onMove = (moveEvent: PointerEvent) => {
        // Dragging the left edge: move left → wider panel.
        const delta = startX - moveEvent.clientX;
        liveWidth = clampWidth(startWidth + delta);
        setWidth(liveWidth);
      };
      const onUp = (upEvent: PointerEvent) => {
        try {
          target.releasePointerCapture(upEvent.pointerId);
        } catch {
          // ignore
        }
        target.removeEventListener("pointermove", onMove);
        target.removeEventListener("pointerup", onUp);
        target.removeEventListener("pointercancel", onUp);
        persist(liveWidth);
      };

      target.addEventListener("pointermove", onMove);
      target.addEventListener("pointerup", onUp);
      target.addEventListener("pointercancel", onUp);
    },
    [persist],
  );

  return { width, onResizePointerDown };
}
