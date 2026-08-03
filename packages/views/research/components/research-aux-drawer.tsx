"use client";

import { useId, type ReactNode } from "react";
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
import { useOverlayPanelA11y } from "../hooks/use-overlay-panel-a11y";
import type { ResearchAuxPanelId } from "./research-module-rail";

/**
 * LRM-1061 / LRM-1056 v2 — single right aux drawer (trajectory | sources | detail).
 * Desktop: overlay that does not shrink the canvas flex row.
 * Narrow: full-height sheet from the right.
 *
 * LRM-1100: the desktop overlay is a bare <aside>, so Escape-to-close, the
 * accessible name and focus move-in/restore are wired explicitly here — the
 * narrow Sheet branch already gets all three from Radix.
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
  const titleId = useId();
  const { bindPanel } = useOverlayPanelA11y({
    active: open && !isMobile,
    onClose,
  });

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
          // LRM-1109 / LRM-1118 SoT: beat Sheet's `sm:max-w-sm` with !max-w-none —
          // no `sm:*` inside the isMobile branch (forbids 640–767 layout flip).
          className="flex w-full !max-w-none flex-col gap-0 overflow-hidden p-0"
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
      ref={bindPanel}
      tabIndex={-1}
      aria-labelledby={titleId}
      data-testid="research-aux-drawer"
      data-panel={panel ?? undefined}
      className={cn(
        "pointer-events-auto absolute inset-y-0 right-0 z-40 flex w-[min(100%,360px)] flex-col border-l border-border/55 bg-card/95 shadow-xl backdrop-blur-md outline-none",
      )}
    >
      <DrawerChrome title={title} titleId={titleId} onClose={onClose}>
        {children}
      </DrawerChrome>
    </aside>
  );
}

function DrawerChrome({
  title,
  titleId,
  onClose,
  children,
}: {
  title: string;
  /** Set on the desktop branch so the <aside> can point aria-labelledby here. */
  titleId?: string;
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
        <div
          id={titleId}
          className="truncate text-sm font-semibold text-foreground"
        >
          {title}
        </div>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          aria-label={t(($) => $.panel.aux_close)}
          data-testid="research-aux-drawer-close"
          data-autofocus="true"
          onClick={onClose}
        >
          <X className="size-4" />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">{children}</div>
    </div>
  );
}
