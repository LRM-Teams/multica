"use client";

import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import {
  CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH,
  CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY,
  clampChatWindowSidebarWidth,
} from "./chat-window-layout";

function readStoredWidth(): number {
  if (typeof window === "undefined") return CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH;
  try {
    const raw = window.localStorage.getItem(CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY);
    if (!raw) return CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH;
    const parsed = Number(raw);
    return Number.isFinite(parsed)
      ? clampChatWindowSidebarWidth(parsed)
      : CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH;
  } catch {
    return CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH;
  }
}

/**
 * Desktop drag width for the Notes assistant rail.
 * Persists across refresh. Mobile uses the fullscreen sheet and should
 * not attach this handle.
 */
export function useNoteBubbleSidebarWidth() {
  const [width, setWidth] = useState(CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH);
  const widthRef = useRef(width);
  widthRef.current = width;

  useEffect(() => {
    setWidth(readStoredWidth());
  }, []);

  const persist = useCallback((next: number) => {
    const clamped = clampChatWindowSidebarWidth(next);
    setWidth(clamped);
    try {
      window.localStorage.setItem(CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY, String(clamped));
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
        liveWidth = clampChatWindowSidebarWidth(startWidth + (startX - moveEvent.clientX));
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
