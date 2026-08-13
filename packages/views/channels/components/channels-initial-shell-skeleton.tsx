"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { ChannelListSkeleton } from "./channel-list-skeleton";
import { CONVERSATION_LIST_PANE_BG } from "./conversation-sidebar-styles";
import { DmListSkeleton } from "./dm-list-skeleton";

/**
 * LRM-459 — cold-paint shell before `viewportReady`: list chrome first
 * (mobile list-first + desktop sidebar); detail skeleton only from md up.
 */
export function InitialChannelsShellSkeleton() {
  return (
    <div
      className="flex h-full min-h-0 bg-background"
      data-testid="channels-initial-shell-skeleton"
      aria-busy="true"
    >
      {/* Match live listPane: surface plane + column border. */}
      <aside
        className={cn(
          "flex w-full min-h-0 flex-col md:w-72 md:shrink-0 md:border-r md:border-border",
          CONVERSATION_LIST_PANE_BG,
        )}
      >
        <div className="flex items-center gap-2 px-4 pb-1 pt-4">
          <Skeleton className="h-6 w-28" />
        </div>
        <div className="px-3 pb-2">
          <Skeleton className="h-9 w-full rounded-md" />
        </div>
        <div className="min-h-0 flex-1 space-y-3 overflow-hidden px-2 pb-2">
          <div className="space-y-1">
            <Skeleton className="mx-2 h-3 w-16" />
            <DmListSkeleton rows={2} />
          </div>
          <div className="space-y-1">
            <Skeleton className="mx-2 h-3 w-20" />
            <ChannelListSkeleton rows={3} />
          </div>
        </div>
      </aside>
      <div className="hidden min-h-0 min-w-0 flex-1 flex-col md:flex">
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="flex items-center gap-3 border-b px-4 py-3">
            <Skeleton className="size-10 shrink-0 rounded-full" />
            <div className="min-w-0 flex-1 space-y-1.5">
              <Skeleton className="h-4 w-36" />
              <Skeleton className="h-3 w-48 max-w-[50%]" />
            </div>
            <Skeleton className="size-8 rounded-md" />
            <Skeleton className="size-8 rounded-md" />
          </div>
          <div className="min-h-0 flex-1 space-y-4 p-4">
            <div className="flex gap-2">
              <Skeleton className="size-8 shrink-0 rounded-full" />
              <div className="space-y-2">
                <Skeleton className="h-3 w-24" />
                <Skeleton className="h-16 w-64 max-w-[80vw] rounded-lg" />
              </div>
            </div>
            <div className="flex flex-col items-end gap-2">
              <Skeleton className="h-3 w-20" />
              <Skeleton className="h-14 w-52 max-w-[70vw] rounded-lg" />
            </div>
          </div>
          <div className="border-t p-3">
            <Skeleton className="h-12 w-full rounded-md" />
          </div>
        </div>
      </div>
    </div>
  );
}
