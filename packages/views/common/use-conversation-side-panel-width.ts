"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
} from "react";
import {
  clampConversationSidePanelWidth,
  CONVERSATION_SIDE_PANEL_DEFAULT_PX,
  readConversationSidePanelWidth,
  writeConversationSidePanelWidth,
} from "./conversation-side-panel-width";

/**
 * Pixel width + left-edge drag for the docked conversation side panel.
 * Persists on pointer-up (localStorage). Re-clamps when the container shrinks.
 */
export function useConversationSidePanelWidth(
  containerRef: RefObject<HTMLElement | null>,
) {
  const [width, setWidth] = useState(CONVERSATION_SIDE_PANEL_DEFAULT_PX);
  const [isDragging, setIsDragging] = useState(false);
  const widthRef = useRef(width);
  widthRef.current = width;

  // Hydrate from localStorage after mount (SSR-safe).
  useEffect(() => {
    setWidth(readConversationSidePanelWidth());
  }, []);

  // Re-clamp when the conversation+side container resizes (list divider,
  // window resize) so a remembered width cannot exceed 45% of a narrower box.
  useEffect(() => {
    const el = containerRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;

    const reclamp = () => {
      const next = clampConversationSidePanelWidth(
        widthRef.current,
        el.clientWidth,
      );
      if (next !== widthRef.current) setWidth(next);
    };

    reclamp();
    const ro = new ResizeObserver(reclamp);
    ro.observe(el);
    return () => ro.disconnect();
  }, [containerRef]);

  const startResize = useCallback(
    (e: ReactPointerEvent<HTMLElement>) => {
      e.preventDefault();
      e.stopPropagation();
      const startX = e.clientX;
      const startWidth = widthRef.current;
      setIsDragging(true);

      const onMove = (ev: PointerEvent) => {
        const containerW = containerRef.current?.clientWidth;
        // Drag left edge leftward → wider panel.
        const next = clampConversationSidePanelWidth(
          startWidth + (startX - ev.clientX),
          containerW,
        );
        widthRef.current = next;
        setWidth(next);
      };

      const onUp = () => {
        setIsDragging(false);
        writeConversationSidePanelWidth(widthRef.current);
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };

      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
    },
    [containerRef],
  );

  return { width, isDragging, startResize };
}
