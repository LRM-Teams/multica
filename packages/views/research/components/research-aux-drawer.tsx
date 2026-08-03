"use client";

import type { ReactNode } from "react";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { Button } from "@multica/ui/components/ui/button";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { X } from "lucide-react";
import { useT } from "../../i18n/use-t";
import type { ResearchAuxPanelId } from "./research-module-rail";

/**
 * LRM-1061 / LRM-1056 v2 — single right aux drawer (trajectory | sources | detail).
 * Desktop: overlay that does not shrink the canvas flex row.
 * Narrow: full-height sheet from the right.
 */
export function ResearchAuxDrawer({
  panel,
  onClose,
  children,
}: {
  panel: ResearchAuxPanelId | null;
  onClose: () => void;
  children: ReactNode;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const open = panel != null;

  const title =
    panel === "trajectory"
      ? t(($) => $.panel.module_trajectory)
      : panel === "sources"
        ? t(($) => $.panel.module_sources)
        : panel === "detail"
          ? t(($) => $.panel.module_detail)
          : t(($) => $.panel.module_detail);

  if (isMobile) {
    return (
      <Sheet
        open={open}
        onOpenChange={(next) => {
          if (!next) onClose();
        }}
      >
        <SheetContent
          side="right"
          showCloseButton={false}
          data-testid="research-aux-drawer"
          data-panel={panel ?? undefined}
          className="flex w-full max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-w-md"
        >
          <SheetHeader className="sr-only">
            <SheetTitle>{title}</SheetTitle>
            <SheetDescription>{title}</SheetDescription>
          </SheetHeader>
          <DrawerChrome title={title} onClose={onClose}>
            {children}
          </DrawerChrome>
        </SheetContent>
      </Sheet>
    );
  }

  if (!open) return null;

  return (
    <aside
      data-testid="research-aux-drawer"
      data-panel={panel ?? undefined}
      className={cn(
        "pointer-events-auto absolute inset-y-0 right-0 z-40 flex w-[min(100%,360px)] flex-col border-l border-border/55 bg-card/95 shadow-xl backdrop-blur-md",
      )}
    >
      <DrawerChrome title={title} onClose={onClose}>
        {children}
      </DrawerChrome>
    </aside>
  );
}

function DrawerChrome({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const { t } = useT("research");
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        className="flex items-center justify-between gap-2 border-b px-3 py-2.5"
        data-testid="research-aux-drawer-header"
      >
        <div className="truncate text-sm font-semibold text-foreground">
          {title}
        </div>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          aria-label={t(($) => $.panel.aux_close)}
          data-testid="research-aux-drawer-close"
          onClick={onClose}
        >
          <X className="size-4" />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">{children}</div>
    </div>
  );
}
