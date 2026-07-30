"use client";

import { MessageSquare, CircleDot } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { UserActivityItem } from "@multica/core/types";
import { toDirectoryActorType } from "@multica/core/workspace/resolved-actor-name";
import { useT, Time } from "../../i18n";
import { ActorAvatar } from "../../common/actor-avatar";

export function ActivityListRow({
  item,
  isSelected,
  onClick,
}: {
  item: UserActivityItem;
  isSelected: boolean;
  onClick: () => void;
}) {
  const { t } = useT("inbox");
  const isUnread = item.unread_count > 0;
  const isThread = item.kind === "thread";
  const channelLabel =
    item.channel_kind === "dm"
      ? item.channel_name
      : item.channel_name
        ? `#${item.channel_name}`
        : null;

  // LRM-809: row avatar — top-level actor fields first (thread dm peer / root
  // author / inbox actor), falling back to the embedded inbox payload for
  // older backends. Clicking the avatar opens the actor profile (same panel
  // entries as chat); clicking the row still opens the activity item.
  const rawActorType =
    item.actor_type ?? item.inbox?.actor_type ?? item.inbox?.recipient_type ?? null;
  const rawActorId =
    item.actor_id ?? item.inbox?.actor_id ?? item.inbox?.recipient_id ?? null;
  const directoryType = toDirectoryActorType(rawActorType ?? undefined);
  const avatarType = directoryType ?? (rawActorType === "system" ? "system" : null);

  return (
    <button
      type="button"
      disabled={item.access_denied}
      onClick={onClick}
      data-testid={`activity-row-${item.kind}-${item.id}`}
      {...(avatarType && rawActorId ? { "data-avatar-profile-entry": "true" } : {})}
      className={cn(
        "grid w-full grid-cols-[2px_28px_1fr_auto] items-center gap-x-2.5 border-b border-border text-left min-h-[52px] pr-4 transition-colors",
        isUnread && "bg-brand/5 dark:bg-brand/10",
        item.access_denied && "cursor-not-allowed opacity-60",
        isSelected ? "bg-accent" : !item.access_denied && "hover:bg-accent/50",
      )}
    >
      <span
        className={cn(
          "my-2 self-stretch rounded-r-sm",
          isUnread ? "bg-brand" : "bg-transparent",
        )}
        aria-hidden
      />
      {avatarType && rawActorId ? (
        <ActorAvatar
          actorType={avatarType}
          actorId={rawActorId}
          size={28}
          enableHoverCard
        />
      ) : (
        <span className="text-center text-muted-foreground">
          {isThread ? (
            <MessageSquare className="mx-auto h-4 w-4" aria-hidden />
          ) : (
            <CircleDot className="mx-auto h-4 w-4" aria-hidden />
          )}
        </span>
      )}
      <div className="min-w-0 py-2">
        {isThread && channelLabel && (
          <div
            className={cn(
              "truncate text-[11.5px] font-semibold",
              item.channel_kind === "dm" ? "text-brand" : "text-muted-foreground",
            )}
          >
            {channelLabel}
          </div>
        )}
        <div className="truncate text-sm font-semibold">{item.title}</div>
        {item.preview_text ? (
          <div className="truncate text-xs text-muted-foreground">{item.preview_text}</div>
        ) : null}
        {item.access_denied ? (
          <div className="text-xs text-destructive">
            {t(($) => $.activity.access_denied)}
          </div>
        ) : null}
      </div>
      <div className="flex flex-col items-end gap-1 py-2">
        {isUnread ? (
          <span className="whitespace-nowrap rounded-full bg-brand px-2 py-0.5 text-[11px] font-bold text-brand-foreground">
            {t(($) => $.activity.new_count, { count: item.unread_count })}
          </span>
        ) : null}
        {isThread && item.reply_count != null && item.reply_count > 0 ? (
          <span className="text-[11px] text-muted-foreground">
            {t(($) => $.activity.replies, { count: item.reply_count })}
          </span>
        ) : null}
        <span className="text-[11px] text-muted-foreground">
          <Time kind="relative" value={item.updated_at} />
        </span>
      </div>
    </button>
  );
}
