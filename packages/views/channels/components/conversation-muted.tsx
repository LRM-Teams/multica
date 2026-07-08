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
  hasMention = false,
  mentionLabel,
}: {
  realUnread: number;
  isManualDot: boolean;
  isMuted: boolean;
  /** An unread message in this conversation @-mentions the viewer (#303). */
  hasMention?: boolean;
  /** Accessible label for the @-mention dot. */
  mentionLabel?: string;
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

  // An @-mention outranks the plain count and shows a distinct red dot alongside
  // it (both coexist — Parker's spec). Suppressed when muted: mute stays silent
  // (no attention-grabbing red), while the count still renders so the
  // conversation isn't lost.
  if (hasMention && !isMuted) {
    return (
      <span className="flex shrink-0 items-center gap-1">
        <span className="size-2 shrink-0 rounded-full bg-destructive" aria-hidden="true" />
        {mentionLabel ? <span className="sr-only">{mentionLabel}</span> : null}
        {countBadge}
      </span>
    );
  }
  return countBadge;
}
