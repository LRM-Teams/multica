"use client";

import type { ReactNode } from "react";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/**
 * LRM-779 — research fleet chat shell:
 * - Desktop: ~320–400px side drawer (does not cover canvas).
 * - Narrow: full-viewport bottom sheet so the canvas is not squeezed.
 */
export function ResearchChatDrawer({
  open,
  onClose,
  children,
  className,
}: {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  className?: string;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();

  if (isMobile) {
    return (
      <Sheet
        open={open}
        onOpenChange={(next) => {
          if (!next) onClose();
        }}
      >
        <SheetContent
          side="bottom"
          showCloseButton={false}
          data-testid="research-chat-drawer"
          data-placement="sheet"
          className={cn(
            // Full-screen sheet on narrow: canvas stays full-bleed underneath.
            // Sheet primitive sets `data-[side=bottom]:h-auto` — force viewport height.
            "flex !h-[100dvh] !max-h-[100dvh] min-h-[100dvh] w-full flex-col gap-0 overflow-hidden rounded-none border-0 p-0 sm:max-w-none",
            className,
          )}
        >
          <SheetHeader className="sr-only">
            <SheetTitle>{t(($) => $.panel.chat)}</SheetTitle>
            <SheetDescription>{t(($) => $.panel.chat)}</SheetDescription>
          </SheetHeader>
          <div className="flex min-h-0 flex-1 flex-col bg-card/95 backdrop-blur-sm">
            {children}
          </div>
        </SheetContent>
      </Sheet>
    );
  }

  if (!open) return null;

  return (
    <aside
      data-testid="research-chat-drawer"
      data-placement="aside"
      className={cn(
        // LRM-971: drawer shell matches homepage card language (not flat gray slab).
        "relative z-[1] flex w-[min(100%,380px)] shrink-0 flex-col border-l border-border/55 bg-card/95 backdrop-blur-sm",
        className,
      )}
    >
      {children}
    </aside>
  );
}
