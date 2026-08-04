"use client";

import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { channelMessageThreadOptions } from "@multica/core/channels";
import type { ChannelMessage } from "@multica/core/types";

export const THREAD_PREVIEW_PREFETCH_COUNT = 3;
export const THREAD_PREVIEW_QUERY_LIMIT = 100;
export const THREAD_PREVIEW_STALE_TIME_MS = 30_000;

/**
 * Warm the reply previews most likely to be visible when a conversation lands
 * at the bottom of its latest page. Prefetch starts only after the mainline has
 * rendered, so slow or failed thread requests never gate conversation content.
 */
export function usePrefetchThreadPreviews(messages: readonly ChannelMessage[]): void {
  const queryClient = useQueryClient();

  useEffect(() => {
    const roots = [...messages]
      .reverse()
      .filter(
        (message) =>
          !message.thread_root_message_id &&
          !message.deleted_at &&
          (message.thread_reply_count ?? 0) > 0,
      )
      .slice(0, THREAD_PREVIEW_PREFETCH_COUNT);

    for (const root of roots) {
      void queryClient.prefetchQuery({
        ...channelMessageThreadOptions(root.channel_id, root.id, {
          limit: THREAD_PREVIEW_QUERY_LIMIT,
        }),
        staleTime: THREAD_PREVIEW_STALE_TIME_MS,
      });
    }
  }, [messages, queryClient]);
}
