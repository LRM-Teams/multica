"use client";

import { useRef, type ReactNode } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useConversationSidePanelWidth } from "./use-conversation-side-panel-width";

/**
 * Desktop conversation + optional right dock (Profile / thread / details).
 *
 * Keeps the conversation column mounted when the dock opens/closes (LRM-400 /
 * LRM-388: no PanelGroup ↔ plain-div swap, no blank half-pane). The dock is
 * pixel-resizable via a left-edge handle (LRM-481); width persists in
 * localStorage. Mobile callers should not use this — they use full-width /
 * drawer surfaces instead.
 */
export function ConversationSideDock({
  conversation,
  sidePanel,
  sidePanelTestId,
}: {
  conversation: ReactNode;
  sidePanel: ReactNode | null;
  sidePanelTestId?: string;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const { width, isDragging, startResize } =
    useConversationSidePanelWidth(containerRef);

  return (
    <div ref={containerRef} className="flex min-h-0 min-w-0 flex-1">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">{conversation}</div>
      {sidePanel ? (
        <div
          data-testid={sidePanelTestId}
          className="relative flex shrink-0 flex-col border-l border-border/30 bg-background"
          style={{ width }}
        >
          <button
            type="button"
            data-testid="conversation-side-panel-resize-handle"
            aria-label="Resize side panel"
            aria-orientation="vertical"
            tabIndex={-1}
            onPointerDown={startResize}
            className={cn(
              "absolute inset-y-0 left-0 z-20 w-2 -translate-x-1/2 cursor-col-resize touch-none",
              "before:absolute before:inset-y-0 before:left-1/2 before:w-px before:-translate-x-1/2 before:bg-transparent before:transition-colors",
              "hover:before:bg-foreground/15",
              isDragging && "before:bg-foreground/15",
            )}
          />
          {sidePanel}
        </div>
      ) : null}
    </div>
  );
}
