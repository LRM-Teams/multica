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
  status,
  actions,
  /**
   * LRM-447 design gate A — desktop group header: left meta · center title ·
   * right action rail. Mobile keeps the existing flex row (back + title + ⋯).
   */
  layout = "default",
}: {
  isMobile: boolean;
  leading: ReactNode;
  title: ReactNode;
  meta?: ReactNode;
  badges?: ReactNode;
  /**
   * Optional live status (e.g. agent presence). Renders on the same row as
   * the name, tight after title/badges — Slack/IM style. Always `shrink-0`
   * so a long title truncates instead of painting over it. Narrow DM headers
   * may instead pass the cue via `meta` (under the name).
   */
  status?: ReactNode;
  actions?: ReactNode;
  layout?: "default" | "slots3";
}) {
  const titleBlock = (
    <div className="min-w-0 flex-1 overflow-hidden">
      {/* Name + badges share the semibold cluster; status is shrink-0 beside
          (or under via `meta` on narrow DM headers) so a long peer name can
          never paint over "处理中" / Working. overflow-hidden is required for
          truncate to kick in through compound title wrappers (button > span). */}
      <div className="flex min-w-0 items-center gap-1.5">
        <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden text-sm font-semibold leading-5">
          {/* Plain-string titles (thread) keep truncate here; compound
              titles (channel name + ▾) own their own truncate so the
              chevron is never clipped. */}
          <div
            className={cn(
              "min-w-0 flex-1 overflow-hidden",
              typeof title === "string" && "truncate",
            )}
          >
            {title}
          </div>
          {badges}
        </div>
        {status ? (
          <div className="shrink-0" data-testid="conversation-header-status">
            {status}
          </div>
        ) : null}
      </div>
      {meta && (
        // div (not p): Thread meta may include a clickable「在 #频道 查看」control
        // (LRM-572); interactive children inside <p> are invalid HTML.
        <div className="min-w-0 text-[11px] leading-4 text-muted-foreground/75">
          {typeof meta === "string" ? <p className="truncate">{meta}</p> : meta}
        </div>
      )}
    </div>
  );

  // LRM-447 — three-slot desktop shell (left meta tile · center title · right
  // rail). Mobile stays on the flex row so back + ⋯ keep their tap targets.
  if (layout === "slots3" && !isMobile) {
    return (
      <header
        className="grid min-h-14 shrink-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 border-b border-border/25 bg-background/95 px-5 py-1.5"
        data-testid="conversation-header-slots3"
      >
        <div className="flex items-center justify-start pl-2">{leading}</div>
        {titleBlock}
        {actions ? (
          <div className="flex shrink-0 items-center text-muted-foreground">{actions}</div>
        ) : null}
      </header>
    );
  }

  return (
    <header
      className={cn(
        "flex min-h-14 shrink-0 items-center gap-3 border-b border-border/25 bg-background/95 py-1.5",
        isMobile ? "px-2" : "px-5",
      )}
    >
      {/* Desktop `pl-2` aligns the header avatar + title with the message
          column (message rows sit at px-5 + the bubble's px-2 = a matching
          left edge), so the header avatar and every message avatar share one
          vertical line. Mobile keeps its tighter gutter.
          LRM-279 — title column flex:1 min-w-0 so the name eats space before
          the shrink-0 action cluster; no justify-between gutter. */}
      <div
        className={cn(
          "flex min-w-0 flex-1 items-center",
          isMobile ? "gap-2" : "gap-2.5 pl-2",
        )}
      >
        {leading}
        {titleBlock}
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

