"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/**
 * LRM-781 — session-page skeleton matching chrome + stage strip + tabs +
 * canvas / chat drawer shell so load → ready does not jump the layout.
 */
export function ResearchSessionPageSkeleton() {
  return (
    <div
      className="relative flex h-full min-h-0 flex-col"
      aria-busy="true"
      data-testid="research-session-page-skeleton"
    >
      {/* Header ≈ ResearchSessionChrome */}
      <header className="shrink-0 border-b bg-background/85">
        <div className="flex items-center gap-2.5 px-4 pt-2.5 pb-1">
          <Skeleton className="h-5 w-40 max-w-[45%]" />
          <Skeleton className="h-3.5 w-14" />
          <Skeleton className="hidden h-5 w-16 rounded-md sm:block" />
          <div className="ml-auto flex items-center gap-2">
            <Skeleton className="h-8 w-8 rounded-md" />
            <Skeleton className="hidden h-8 w-20 rounded-md sm:block" />
          </div>
        </div>
        <div className="flex items-center gap-2 px-4 pb-2.5">
          <Skeleton className="h-3 w-[70%] max-w-md" />
        </div>
      </header>

      {/* Stage timeline strip */}
      <nav className="shrink-0 border-b bg-background/70 px-3 py-2 sm:px-4">
        <ol className="flex gap-2">
          {Array.from({ length: 4 }, (_, i) => (
            <li key={i} className="flex min-w-[7.5rem] flex-1 items-center gap-2 sm:min-w-0">
              <Skeleton className="size-5 shrink-0 rounded-full" />
              <Skeleton className="h-3 w-16" />
            </li>
          ))}
        </ol>
      </nav>

      {/* Visibility tabs */}
      <div className="flex shrink-0 gap-2 border-b px-3 py-2 sm:px-4">
        <Skeleton className="h-7 w-16 rounded-md" />
        <Skeleton className="h-7 w-16 rounded-md" />
        <Skeleton className="h-7 w-16 rounded-md" />
      </div>

      {/* Source strip (desktop) */}
      <div className="hidden border-b px-4 py-2 sm:block">
        <div className="flex gap-2">
          <Skeleton className="h-6 w-20 rounded-full" />
          <Skeleton className="h-6 w-24 rounded-full" />
          <Skeleton className="h-6 w-16 rounded-full" />
        </div>
      </div>

      {/* Body: rail + canvas + optional chat */}
      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-[200px] shrink-0 flex-col gap-2 border-r p-3 sm:flex">
          <Skeleton className="h-3 w-16" />
          <Skeleton className="h-8 w-full rounded-md" />
          <Skeleton className="h-8 w-full rounded-md" />
          <Skeleton className="h-8 w-4/5 rounded-md" />
        </aside>

        <section className="relative min-h-0 min-w-0 flex-1 bg-muted/20 p-4">
          <div className="flex flex-wrap gap-3">
            <Skeleton className="h-[88px] w-[200px] rounded-xl" />
            <Skeleton className="h-[88px] w-[200px] rounded-xl" />
            <Skeleton className="mt-8 h-[88px] w-[200px] rounded-xl" />
          </div>
        </section>

        {/* Chat drawer shell — mirrors default open desktop chat width */}
        <aside className="hidden w-[min(100%,380px)] shrink-0 flex-col border-l bg-background sm:flex">
          <div className="flex items-center justify-between border-b px-3 py-2.5">
            <Skeleton className="h-4 w-12" />
            <Skeleton className="h-7 w-14 rounded-md" />
          </div>
          <div className="flex-1 space-y-2.5 overflow-hidden p-3">
            <Skeleton className="h-16 w-full rounded-xl" />
            <Skeleton className="h-16 w-[90%] rounded-xl" />
            <Skeleton className="h-16 w-full rounded-xl" />
          </div>
          <div className="border-t p-3">
            <Skeleton className="h-[88px] w-full rounded-xl" />
          </div>
        </aside>
      </div>
    </div>
  );
}
