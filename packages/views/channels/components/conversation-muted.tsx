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
  unreadLabel,
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
  /** Accessible label for the numeric unread badge, e.g. "7 unread messages". */
  unreadLabel?: string;
}) {
  // An @-mention outranks the plain count: the row shows a single `@N` pill
  // instead of stacking a mention marker on top of the unread badge (#556, Iris).
  // The literal `@` glyph is the primary cue so it stays distinguishable without
  // relying on color (A6 — colour-blind safe); the emphasis colour is secondary.
  // @-mentions PIERCE mute (Parker): a muted row silences its ambient unread,
  // but a direct @ still surfaces the `@N` pill at full salience — matching
  // Slack (mute suppresses ambient noise, not direct mentions). The mention
  // count is server-cursor driven (#557) and wired for muted rows too.
  if (mentionCount > 0) {
    const mentionText = `@${mentionCount > 99 ? "99+" : mentionCount}`;
    return (
      <Tooltip>
        <TooltipTrigger
          render={
            <span
              aria-label={mentionLabel}
              className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-brand-solid px-1 text-[10px] font-semibold text-brand-solid-foreground"
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

  // LRM-767 (design gate locked, Slack-aligned): a plain unread on an ACTIVE
  // conversation shows the real message count in a neutral pill — it answers
  // "how many did I miss", which the bold name alone can't. Supersedes the
  // #3-era neutral dot for this slot. A MUTED conversation shows nothing here
  // at all (bold name is its only unread signal) — a silenced row must never
  // be as salient as an active one.
  if (realUnread > 0) {
    if (isMuted) return null;
    return (
      <span
        aria-label={unreadLabel}
        className="flex h-5 min-w-5 shrink-0 items-center justify-center rounded-md border border-border/70 bg-background px-1.5 text-[11px] font-medium tabular-nums text-muted-foreground"
      >
        {realUnread > 99 ? "99+" : realUnread}
      </span>
    );
  }

  // Manual "mark as unread": the server bumps `unread` but `real_unread` stays
  // 0, so there is no honest count — the marker stays a subtle neutral dot
  // (never a fabricated "1"), dimmed further when the row is muted.
  if (isManualDot) {
    return (
      <span
        className={cn(
          "size-2 shrink-0 rounded-full",
          isMuted ? "bg-muted-foreground/50" : "bg-muted-foreground",
        )}
      />
    );
  }
  return null;
}
