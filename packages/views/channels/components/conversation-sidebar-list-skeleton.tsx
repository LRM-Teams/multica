"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/**
 * LRM-459 — Messages sidebar list skeletons (DMs + CHANNELS).
 *
 * Mirrors real row chrome so cold load / workspace switch never flash blank or
 * an empty-state. Use `isPending` (not `isLoading`) at call sites: a disabled
 * or not-yet-fetching query has `isPending && !isFetching` → `isLoading` false,
 * which previously painted the empty CTA before data arrived.
 */

function DmRowSkeleton() {
  return (
    <div className="mb-0.5 flex items-center gap-2.5 rounded-lg px-2 py-2">
      <Skeleton className="size-10 shrink-0 rounded-full" />
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <Skeleton className="h-3.5 w-24" />
          <Skeleton className="h-2.5 w-8 shrink-0" />
        </div>
        <Skeleton className="h-3 w-4/5 max-w-[11rem]" />
      </div>
    </div>
  );
}

function ChannelRowSkeleton() {
  return (
    <div className="mb-0.5 flex items-center gap-2.5 rounded-lg px-2 py-2">
      <div className="flex min-w-0 flex-1 items-center gap-1">
        <Skeleton className="size-3.5 shrink-0 rounded-sm" />
        <Skeleton className="h-3.5 w-28" />
      </div>
    </div>
  );
}

/** DIRECT MESSAGES region body while `dmListOptions` is pending. */
export function DmListSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div
      className="px-0 py-0.5"
      data-testid="dm-list-skeleton"
      aria-busy="true"
      aria-label="Loading direct messages"
    >
      {Array.from({ length: rows }, (_, i) => (
        <DmRowSkeleton key={i} />
      ))}
    </div>
  );
}

/** CHANNELS region body while `channelsOptions` is pending. */
export function ChannelListSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div
      className="px-0 py-0.5"
      data-testid="channel-list-skeleton"
      aria-busy="true"
      aria-label="Loading channels"
    >
      {Array.from({ length: rows }, (_, i) => (
        <ChannelRowSkeleton key={i} />
      ))}
    </div>
  );
}

/**
 * Cold-paint shell before `viewportReady` — list chrome first (mobile list-first
 * + desktop sidebar), detail skeleton only from md up.
 */
export function InitialChannelsShellSkeleton() {
  return (
    <div
      className="flex h-full min-h-0 bg-background"
      data-testid="channels-initial-shell-skeleton"
      aria-busy="true"
    >
      <aside className="flex w-full min-h-0 flex-col bg-sidebar md:w-72 md:shrink-0 md:border-r">
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
        <InitialDetailPaneSkeleton />
      </div>
    </div>
  );
}

function InitialDetailPaneSkeleton() {
  return (
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
  );
}
