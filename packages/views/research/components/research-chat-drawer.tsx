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
 * LRM-1061 / LRM-1056 v2 — fleet chat float:
 * - Desktop: floating overlay (does not shrink the canvas as a permanent dock).
 * - Narrow: full-viewport sheet; canvas stays full-bleed underneath.
 * Default open=false is owned by the research UI store.
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
      data-placement="float"
      className={cn(
        "pointer-events-auto absolute inset-y-3 right-3 z-40 flex w-[min(100%,380px)] flex-col overflow-hidden rounded-xl border border-border/60 bg-card/95 shadow-xl backdrop-blur-md",
        className,
      )}
    >
      {children}
    </aside>
  );
}
