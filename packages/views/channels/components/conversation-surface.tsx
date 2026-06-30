"use client";

import type { ReactNode } from "react";
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
        "flex min-h-14 items-center justify-between gap-3 border-b border-border/25 bg-background/95 py-1.5",
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

export function ComposerShell({ children }: { children: ReactNode }) {
  return (
    <div className="px-5 pb-4">
      <div className="rounded-lg border border-border/35 bg-background shadow-none">
        {children}
      </div>
    </div>
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
