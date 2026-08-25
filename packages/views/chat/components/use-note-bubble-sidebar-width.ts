"use client";

import { useChatStore } from "@multica/core/chat";
import { useCallback, useRef, type PointerEvent as ReactPointerEvent } from "react";
import { clampChatWindowSidebarWidth } from "./chat-window-layout";

/**
 * Desktop drag width for the Notes assistant rail.
 * Lives in the chat store so the page dock and the rail share one value —
 * widening and narrowing both recenter the note body.
 */
export function useNoteBubbleSidebarWidth() {
  const width = useChatStore((s) => s.noteBubbleSidebarWidth);
  const setNoteBubbleSidebarWidth = useChatStore((s) => s.setNoteBubbleSidebarWidth);
  const widthRef = useRef(width);
  widthRef.current = width;

  const persist = useCallback(
    (next: number) => {
      setNoteBubbleSidebarWidth(clampChatWindowSidebarWidth(next));
    },
    [setNoteBubbleSidebarWidth],
  );

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
        persist(liveWidth);
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

  return { width: clampChatWindowSidebarWidth(width), onResizePointerDown };
}
