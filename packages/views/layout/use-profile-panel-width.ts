"use client";

import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";

/** Align with docked channel/DM side-dock defaults (LRM-481). */
export const PROFILE_PANEL_WIDTH_DEFAULT = 440;
export const PROFILE_PANEL_WIDTH_MIN = 360;
export const PROFILE_PANEL_WIDTH_MAX = 640;
export const PROFILE_PANEL_WIDTH_STORAGE_KEY = "multica_profile_panel_width";
/** Channel/DM thread·profile·details dock — separate from the global overlay panel. */
export const CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY = "multica_channel_detail_side_width";
/**
 * Default width for the in-channel Thread / agent / details side dock.
 * Wider than the global profile overlay (440) — Frank: Thread 默认偏窄.
 */
export const CHANNEL_DETAIL_SIDE_WIDTH_DEFAULT = 520;

function clampWidth(value: number): number {
  return Math.min(
    PROFILE_PANEL_WIDTH_MAX,
    Math.max(PROFILE_PANEL_WIDTH_MIN, Math.round(value)),
  );
}

function defaultWidthFor(storageKey: string): number {
  return storageKey === CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY
    ? CHANNEL_DETAIL_SIDE_WIDTH_DEFAULT
    : PROFILE_PANEL_WIDTH_DEFAULT;
}

function readStoredWidth(storageKey: string): number {
  const fallback = defaultWidthFor(storageKey);
  if (typeof window === "undefined") return fallback;
  try {
    const raw = window.localStorage.getItem(storageKey);
    if (!raw) return fallback;
    const parsed = Number(raw);
    return Number.isFinite(parsed) ? clampWidth(parsed) : fallback;
  } catch {
    return fallback;
  }
}

/**
 * Desktop-only drag width for Profile / channel side docks.
 * Persists across refresh via localStorage. Mobile uses full-screen page route
 * and should not call this hook's drag handle.
 *
 * Pass `CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY` for the in-channel dock so it
 * does not share width with the global overlay panel.
 */
export function useProfilePanelWidth(
  storageKey: string = PROFILE_PANEL_WIDTH_STORAGE_KEY,
) {
  const [width, setWidth] = useState(() => defaultWidthFor(storageKey));
  const widthRef = useRef(width);
  widthRef.current = width;

  useEffect(() => {
    setWidth(readStoredWidth(storageKey));
  }, [storageKey]);

  const persist = useCallback((next: number) => {
    const clamped = clampWidth(next);
    setWidth(clamped);
    try {
      window.localStorage.setItem(storageKey, String(clamped));
    } catch {
      // ignore quota / private mode
    }
  }, [storageKey]);

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
