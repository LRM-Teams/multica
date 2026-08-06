"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/**
 * LRM-783 / LRM-781 — list-row skeleton matching dense `ResearchSessionRow`
 * (~58px · status dot · title · meta · avatars).
 */
export function ResearchSessionRowSkeleton() {
  return (
    <div
      className="flex min-h-[58px] items-center gap-3 rounded-[10px] px-3 py-1.5"
      data-testid="research-session-row-skeleton"
    >
      <Skeleton className="size-2 shrink-0 rounded-full" />
      <div className="min-w-0 flex-1 space-y-1.5">
        <Skeleton className="h-3.5 w-[55%] max-w-[16rem]" />
        <Skeleton className="h-2.5 w-[34%] max-w-[10rem] opacity-70" />
      </div>
      <span className="hidden items-center sm:flex">
        <Skeleton className="size-[22px] rounded-full" />
        <Skeleton className="-ml-1.5 size-[22px] rounded-full" />
      </span>
      <Skeleton className="hidden h-3 w-10 shrink-0 sm:block" />
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
      className="space-y-0"
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
