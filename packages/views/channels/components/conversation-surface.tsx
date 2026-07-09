"use client";

import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { DrawerContent } from "@multica/ui/components/ui/drawer";
import { cn } from "@multica/ui/lib/utils";

// The composer shell moved into its own module (`composer.tsx`) as the unified
// `<Composer surface=... />` (#198 B1). Re-exported here so existing importers
// keep working while call sites migrate.
export { Composer, type ComposerSurface, type ComposerProps } from "./composer";
export { ReadOnlyConversationBanner } from "./read-only-conversation-banner";

export function ConversationHeader({
  isMobile,
  leading,
  title,
  meta,
  badges,
  actions,
}: {
  isMobile: boolean;
  leading: ReactNode;
  title: ReactNode;
  meta?: ReactNode;
  badges?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header
      className={cn(
        "flex min-h-14 shrink-0 items-center justify-between gap-3 border-b border-border/25 bg-background/95 py-1.5",
        isMobile ? "px-2" : "px-5",
      )}
    >
      {/* Desktop `pl-2` aligns the header avatar + title with the message
          column (message rows sit at px-5 + the bubble's px-2 = a matching
          left edge), so the header avatar and every message avatar share one
          vertical line. Mobile keeps its tighter gutter. */}
      <div
        className={cn(
          "flex min-w-0 items-center",
          isMobile ? "gap-2" : "gap-2.5 pl-2",
        )}
      >
        {leading}
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-1.5 text-sm font-semibold leading-5">
            <span className="truncate">{title}</span>
            {badges}
          </div>
          {meta && (
            <p className="truncate text-[11px] leading-4 text-muted-foreground/75">
              {meta}
            </p>
          )}
        </div>
      </div>
      {actions && (
        <div className="flex shrink-0 items-center gap-1 text-muted-foreground">
          {actions}
        </div>
      )}
    </header>
  );
}

type MobileVisualViewportStyle = Pick<CSSProperties, "top" | "bottom" | "height" | "maxHeight">;

export function getMobileVisualViewportStyle(
  viewport: Pick<VisualViewport, "height" | "offsetTop">,
): MobileVisualViewportStyle {
  return {
    top: Math.max(0, Math.round(viewport.offsetTop)),
    bottom: "auto",
    height: Math.max(0, Math.round(viewport.height)),
    maxHeight: Math.max(0, Math.round(viewport.height)),
  };
}

function useMobileVisualViewportStyle(active: boolean): MobileVisualViewportStyle | undefined {
  const [style, setStyle] = useState<MobileVisualViewportStyle>();

  useEffect(() => {
    if (!active || typeof window === "undefined" || !window.visualViewport) {
      setStyle(undefined);
      return;
    }

    const viewport = window.visualViewport;
    const syncStyle = () => {
      const next = getMobileVisualViewportStyle(viewport);
      setStyle((current) =>
        current?.top === next.top && current?.height === next.height ? current : next,
      );
    };

    syncStyle();
    viewport.addEventListener("resize", syncStyle);
    viewport.addEventListener("scroll", syncStyle);
    window.addEventListener("orientationchange", syncStyle);

    return () => {
      viewport.removeEventListener("resize", syncStyle);
      viewport.removeEventListener("scroll", syncStyle);
      window.removeEventListener("orientationchange", syncStyle);
    };
  }, [active]);

  return style;
}

export function MobileThreadDrawerContent({
  open,
  children,
}: {
  open: boolean;
  children: ReactNode;
}) {
  const viewportStyle = useMobileVisualViewportStyle(open);

  return (
    <DrawerContent
      className="h-[calc(100dvh-env(safe-area-inset-top))] max-h-[calc(100dvh-env(safe-area-inset-top))] min-w-0 rounded-t-none p-0"
      style={viewportStyle}
    >
      {children}
    </DrawerContent>
  );
}

