import type { ReactNode } from "react";

/**
 * The footer shown in place of the composer when a conversation is read-only
 * (archived channel, closed DM). Rendered by {@link Composer} in its read-only
 * branch and mounted directly by surfaces that gate the whole composer.
 */
export function ReadOnlyConversationBanner({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center gap-2 border-t border-border/25 px-5 py-3 text-sm text-muted-foreground">
      {children}
    </div>
  );
}
