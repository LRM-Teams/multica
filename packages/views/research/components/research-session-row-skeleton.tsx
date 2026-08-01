"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/**
 * LRM-781 — list-row skeleton matching `ResearchSessionRow` structure
 * (status dot · title · goal chip · stage/who/avatars meta · time).
 */
export function ResearchSessionRowSkeleton() {
  return (
    <div
      className="flex items-start gap-2 rounded-xl border px-3 py-2.5"
      data-testid="research-session-row-skeleton"
    >
      <Skeleton className="mt-1.5 size-2 shrink-0 rounded-full" />
      <div className="min-w-0 flex-1 space-y-1.5">
        <Skeleton className="h-4 w-[55%] max-w-[16rem]" />
        <Skeleton className="h-5 w-[70%] max-w-[20rem] rounded-md" />
        <div className="flex flex-wrap items-center gap-1.5">
          <Skeleton className="h-5 w-14 rounded-md" />
          <Skeleton className="h-3 w-20" />
          <span className="flex -space-x-1.5">
            <Skeleton className="size-5 rounded-full" />
            <Skeleton className="size-5 rounded-full" />
            <Skeleton className="size-5 rounded-full" />
          </span>
          <Skeleton className="h-3 w-28 max-w-[40%]" />
        </div>
      </div>
      <Skeleton className="mt-0.5 hidden h-3 w-10 shrink-0 sm:block" />
    </div>
  );
}

export function ResearchSessionListSkeleton({
  rows = 4,
  label,
}: {
  rows?: number;
  label?: string;
}) {
  return (
    <div
      className="space-y-2"
      aria-busy="true"
      aria-label={label}
      data-testid="research-session-list-skeleton"
    >
      {Array.from({ length: rows }, (_, i) => (
        <ResearchSessionRowSkeleton key={i} />
      ))}
    </div>
  );
}
