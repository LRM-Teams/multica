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
}: {
  realUnread: number;
  isManualDot: boolean;
  isMuted: boolean;
}) {
  if (realUnread > 0) {
    return (
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
    );
  }
  if (isManualDot) {
    return (
      <span
        className={cn(
          "size-2 shrink-0 rounded-full",
          isMuted ? "bg-muted-foreground/50" : "bg-primary",
        )}
      />
    );
  }
  return null;
}
