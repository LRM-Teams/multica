"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { ChannelMessage } from "@multica/core/types";
import { channelMessageThreadOptions } from "@multica/core/channels";
import { resolveActorDisplayName } from "@multica/core/identity";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { Time } from "../../i18n/time";
import {
  filterSpokenChannelMessages,
  spokenMessagePreviewText,
} from "./spoken-channel-message";

const PREVIEW_LIMIT = 3;

/**
 * LRM-873 — Thread reply preview under a mainline parent (v1.1c).
 * Thin border + wash, no left accent. Spoken replies only (user|agent).
 * Top: reply count. Bottom (when >3): brand「查看全部 N 条 →」.
 */
export function ThreadReplyPreview({
  message,
  onOpenThread,
}: {
  message: ChannelMessage;
  onOpenThread: (message: ChannelMessage) => void;
}) {
  const { t } = useT("channels");
  const rootId = message.id;
  const channelId = message.channel_id;
  const hintCount = message.thread_reply_count ?? 0;
  const unreadCount = message.thread_unread_count ?? 0;
  const enabled = !!channelId && !!rootId && hintCount > 0 && !message.thread_root_message_id;

  const { data, isLoading, isError, refetch } = useQuery({
    ...channelMessageThreadOptions(channelId, rootId, { limit: 100 }),
    enabled,
    staleTime: 30_000,
  });

  const spoken = useMemo(() => {
    const rows = data?.messages ?? [];
    // API returns [root, …replies chronological]. Drop the root.
    const replies = rows.filter((m) => m.id !== rootId && !m.deleted_at);
    return filterSpokenChannelMessages(replies);
  }, [data?.messages, rootId]);

  const previewRows = spoken.slice(0, PREVIEW_LIMIT);
  // When we loaded the full window (no next cursor), trust the filtered count.
  // Otherwise keep the server hint as a floor so the header count stays useful.
  const hasMorePages = !!data?.next_cursor;
  const spokenCount = hasMorePages
    ? Math.max(hintCount, spoken.length)
    : spoken.length;

  if (!enabled) return null;
  if (isLoading && spoken.length === 0) {
    return (
      <div
        data-testid="thread-reply-preview-loading"
        className="mt-2 rounded-lg border border-border/80 bg-muted/40 px-2.5 py-2"
      >
        <div className="h-3 w-16 animate-pulse rounded bg-muted" />
        <div className="mt-2 space-y-1">
          <div className="h-6 animate-pulse rounded bg-muted/80" />
          <div className="h-6 animate-pulse rounded bg-muted/80" />
        </div>
      </div>
    );
  }
  if (isError && spoken.length === 0) {
    return (
      <div
        data-testid="thread-reply-preview-error"
        className="mt-2 flex items-center justify-between gap-2 rounded-lg border border-border/80 bg-muted/40 px-2.5 py-2"
      >
        <span className="text-[11px] text-muted-foreground">
          {t(($) => $.thread.load_failed)}
        </span>
        <button
          type="button"
          data-testid="thread-reply-preview-retry"
          onClick={() => void refetch()}
          className="shrink-0 text-[11px] font-medium text-primary hover:underline"
        >
          {t(($) => $.composer.retry)}
        </button>
      </div>
    );
  }
  if (spokenCount === 0) return null;

  const open = () => onOpenThread(message);
  const showViewAll = spokenCount > PREVIEW_LIMIT;
  const countLabel =
    unreadCount > 0
      ? t(($) => $.thread.preview_count_with_new, {
          count: spokenCount,
          newCount: unreadCount,
        })
      : t(($) => $.thread.preview_count, { count: spokenCount });
  const viewAllLabel = t(($) => $.thread.preview_view_all, { count: spokenCount });

  return (
    <button
      type="button"
      data-testid="thread-reply-preview"
      aria-label={showViewAll ? viewAllLabel : t(($) => $.thread.preview_open)}
      onClick={open}
      className={cn(
        "mt-2 w-full rounded-lg border border-border/80 bg-muted/40 px-2.5 py-2 text-left",
        "transition-colors hover:bg-muted/55 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
      )}
    >
      <div
        className="mb-1 text-[11px] text-muted-foreground"
        data-testid="thread-reply-preview-count"
      >
        {countLabel}
      </div>
      <ul className="flex flex-col gap-1">
        {previewRows.map((reply) => {
          const name =
            reply.author_name?.trim() ||
            resolveActorDisplayName(null, reply.author_id ?? reply.id);
          const summary = spokenMessagePreviewText(reply, {
            image: t(($) => $.thread.preview_image),
            sticker: t(($) => $.thread.preview_sticker),
            attachment: t(($) => $.thread.preview_attachment),
          });
          const actorType =
            reply.type === "agent" ? "agent" : reply.type === "user" ? "member" : null;
          return (
            <li key={reply.id} className="flex h-6 min-w-0 items-center gap-2">
              {actorType && reply.author_id ? (
                <ActorAvatar
                  actorType={actorType}
                  actorId={reply.author_id}
                  size={18}
                  className="size-[18px] shrink-0 rounded-[4px]"
                  avatarUrlHint={reply.author_avatar_url}
                  showStatusDot={false}
                  profileLink={false}
                />
              ) : (
                <span className="size-[18px] shrink-0 rounded-[4px] bg-muted" />
              )}
              <span className="min-w-0 flex-1 truncate text-xs leading-6">
                <span className="font-semibold text-foreground">{name}</span>
                {summary ? (
                  <span className="font-normal text-muted-foreground">
                    {" "}
                    {summary}
                  </span>
                ) : null}
              </span>
              {reply.created_at ? (
                <span
                  data-testid="thread-reply-preview-time"
                  className="shrink-0 text-[10px] tabular-nums text-muted-foreground/70"
                >
                  <Time kind="clock" value={reply.created_at} title={false} />
                </span>
              ) : null}
            </li>
          );
        })}
      </ul>
      {showViewAll ? (
        <div
          className="mt-1.5 text-[11px] font-medium text-primary"
          data-testid="thread-reply-preview-view-all"
        >
          {viewAllLabel}
        </div>
      ) : (
        <div
          className="mt-1.5 text-[11px] font-medium text-primary"
          data-testid="thread-reply-preview-open"
        >
          {t(($) => $.thread.preview_open)}
        </div>
      )}
    </button>
  );
}
