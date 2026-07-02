"use client";

import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { Send } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { DrawerContent } from "@multica/ui/components/ui/drawer";
import { cn } from "@multica/ui/lib/utils";

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
      <div className={cn("flex min-w-0 items-center", isMobile ? "gap-2" : "gap-2.5")}>
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

export function ComposerShell({
  children,
  isMobile = false,
}: {
  children: ReactNode;
  isMobile?: boolean;
}) {
  return (
    <div
      className={cn(
        "shrink-0",
        isMobile ? "px-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]" : "px-5 pb-4",
      )}
    >
      <div
        className="composer-shell min-w-0 rounded-lg border border-border/35 bg-background shadow-none"
        data-slot="composer-shell"
      >
        {children}
      </div>
    </div>
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

export function ChannelComposer({
  editor,
  sendLabel,
  sendDisabled,
  sending = false,
  onSend,
  isMobile,
  prefix,
  leadingActions,
}: {
  editor: ReactNode;
  sendLabel: string;
  sendDisabled: boolean;
  sending?: boolean;
  onSend: () => void;
  isMobile: boolean;
  prefix?: ReactNode;
  leadingActions?: ReactNode;
}) {
  return (
    <ComposerShell isMobile={isMobile}>
      {prefix}
      <div
        className={cn(
          "composer-editor-scroll min-h-16 overflow-y-auto px-4 pt-3 overscroll-contain",
          isMobile ? "max-h-[28dvh]" : "max-h-40",
        )}
        data-slot="composer-editor-scroll"
      >
        {editor}
      </div>
      <div
        className={cn("flex items-center justify-between px-2 pb-2", isMobile && "gap-2")}
        data-slot="composer-action-row"
      >
        <div className="flex min-h-8 min-w-0 flex-1 items-center gap-0.5 overflow-x-auto text-muted-foreground">
          {leadingActions}
        </div>
        <Button
          onClick={onSend}
          disabled={sendDisabled || sending}
          size="sm"
          className={cn("shrink-0", isMobile && "min-h-10 px-4")}
        >
          <Send className="size-4" /> {sendLabel}
        </Button>
      </div>
    </ComposerShell>
  );
}

export function ReadOnlyConversationBanner({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <div className="flex items-center gap-2 border-t border-border/25 px-5 py-3 text-sm text-muted-foreground">
      {children}
    </div>
  );
}
