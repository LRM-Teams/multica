import { BellOff } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";

export interface MuteableConversation {
  muted_at?: string | null;
  muted?: boolean;
}

export function isConversationMuted(item: MuteableConversation): boolean {
  return item.muted === true || !!item.muted_at;
}

export function sumUnmutedUnreadCounts<T>(
  items: readonly T[],
  getCount: (item: T) => number,
  getMuted: (item: T) => boolean,
): number {
  return items.reduce((sum, item) => (getMuted(item) ? sum : sum + getCount(item)), 0);
}

export function MutedIndicator({ label }: { label: string }) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            role="img"
            aria-label={label}
            className="inline-flex shrink-0"
          />
        }
      >
        <BellOff className="size-3.5 text-muted-foreground/70" />
      </TooltipTrigger>
      <TooltipContent side="top">{label}</TooltipContent>
    </Tooltip>
  );
}

export function ConversationUnreadAffordance({
  realUnread,
  isManualDot,
  isMuted,
  mentionCount = 0,
  mentionLabel,
  mentionTooltip,
}: {
  realUnread: number;
  isManualDot: boolean;
  isMuted: boolean;
  /** How many unread messages in this conversation @-mention the viewer.
   *  Server-cursor driven (#557) — cleared to 0 only when the viewer reads the
   *  mentioning message; the FE never zeroes it on click/render. */
  mentionCount?: number;
  /** Accessible (sr-only) label for the @-mention pill. */
  mentionLabel?: string;
  /** Visible tooltip text, e.g. "共 N 未读 · M 条 @ 你". */
  mentionTooltip?: string;
}) {
  const countBadge =
    realUnread > 0 ? (
      <span
        className={cn(
          "flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full px-1 text-[10px] font-medium",
          isMuted
            ? "bg-muted-foreground/25 text-muted-foreground"
            : "bg-primary text-primary-foreground",
        )}
      >
        {realUnread > 99 ? "99+" : realUnread}
      </span>
    ) : isManualDot ? (
      <span
        className={cn(
          "size-2 shrink-0 rounded-full",
          isMuted ? "bg-muted-foreground/50" : "bg-primary",
        )}
      />
    ) : null;

  // An @-mention outranks the plain count: the row shows a single `@N` pill
  // instead of stacking a mention marker on top of the unread badge (#556, Iris).
  // The literal `@` glyph is the primary cue so it stays distinguishable without
  // relying on color (A6 — colour-blind safe); the emphasis colour is secondary.
  // The plain-unread `countBadge` path below is deliberately untouched — only the
  // @-present slot changes. Suppressed when muted: mute stays silent, so the row
  // falls back to the dimmed count (muted @-pierce is a separate follow-up).
  if (mentionCount > 0 && !isMuted) {
    const mentionText = `@${mentionCount > 99 ? "99+" : mentionCount}`;
    return (
      <Tooltip>
        <TooltipTrigger
          render={
            <span
              aria-label={mentionLabel}
              className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold text-destructive-foreground"
            />
          }
        >
          {mentionText}
        </TooltipTrigger>
        {mentionTooltip ? (
          <TooltipContent side="top">{mentionTooltip}</TooltipContent>
        ) : null}
      </Tooltip>
    );
  }
  return countBadge;
}
