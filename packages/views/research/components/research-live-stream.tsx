"use client";

import { useMemo } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import {
  chatSessionsOptions,
  pendingChatTaskOptions,
} from "@multica/core/chat/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { StreamingMarkdown } from "@multica/ui/markdown";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { researchWakeChatTitle } from "../lib/research-stream";

/**
 * Live agent output for research fleet wakes. Subscribes to the wake chat
 * session's pending task transcript (task:message → cache) so the drawer
 * streams without waiting for MirrorResearchChatReply on complete (LRM-820).
 */
export function ResearchLiveStream({ sessionId }: { sessionId: string }) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const wakeTitle = researchWakeChatTitle(sessionId);

  const { data: sessions = [] } = useQuery({
    ...chatSessionsOptions(wsId),
    // Only needed while the drawer is open / generating — parent gates mount.
    staleTime: 30_000,
  });

  const wakeSessionIds = useMemo(() => {
    const ids: string[] = [];
    for (const s of sessions) {
      if (s.title === wakeTitle) ids.push(s.id);
    }
    return ids;
  }, [sessions, wakeTitle]);

  const pendingQueries = useQueries({
    queries: wakeSessionIds.map((id) => ({
      ...pendingChatTaskOptions(id),
      refetchInterval: 4_000,
    })),
  });

  const live = useMemo(() => {
    for (let i = 0; i < wakeSessionIds.length; i++) {
      const pending = pendingQueries[i]?.data;
      if (pending?.pending === true) {
        return { chatSessionId: wakeSessionIds[i]! };
      }
    }
    return null;
  }, [pendingQueries, wakeSessionIds]);

  const streamText = "";
  const isGenerating = !!live;

  if (!isGenerating && !streamText) return null;

  return (
    <article
      data-testid="research-live-stream"
      // LRM-1341: busy flag only — drawer already has one persistent live region
      // (research-chat-mode-live / LRM-1225). Do not re-announce stream tokens here.
      aria-busy={isGenerating || undefined}
      className={cn(
        "mr-1 rounded-xl border border-primary/20 bg-card px-3 py-2.5 text-sm shadow-sm",
        isGenerating && "ring-1 ring-primary/15",
      )}
    >
      <header className="mb-1.5 flex items-center gap-2">
        <span className="flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full bg-primary/15 text-[9px] font-semibold uppercase text-primary">
          S
        </span>
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium text-foreground">
            {t(($) => $.chat.streaming_from)}
          </div>
          <div className="truncate text-[10px] text-muted-foreground">
            {/* Shimmer on plain status text only — never wrap StreamingMarkdown (SC 1.4.1). */}
            <span className={cn(isGenerating && "animate-chat-text-shimmer")}>
              {isGenerating
                ? t(($) => $.chat.streaming)
                : t(($) => $.chat.stream_settled)}
            </span>
          </div>
        </div>
        {isGenerating ? (
          <span
            className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-primary"
            aria-hidden
          />
        ) : null}
      </header>
      {streamText ? (
        <div className="text-[13px] leading-relaxed text-foreground/90">
          <StreamingMarkdown content={streamText} isStreaming={isGenerating} />
        </div>
      ) : (
        <p className="text-[13px] text-muted-foreground">{t(($) => $.chat.streaming_wait)}</p>
      )}
    </article>
  );
}
