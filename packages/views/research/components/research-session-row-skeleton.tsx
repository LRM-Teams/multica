"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";

/**
 * LRM-785 / LRM-781 — list-row skeleton matching compact `ResearchSessionRow`
 * (status dot · title · stage·time meta · avatars).
 */
export function ResearchSessionRowSkeleton() {
  return (
    <div
      className="flex h-[58px] items-center gap-3 rounded-[10px] px-3"
      data-testid="research-session-row-skeleton"
    >
      <Skeleton className="size-2 shrink-0 rounded-full" />
      <div className="min-w-0 flex-1 space-y-1.5">
        <Skeleton className="h-3.5 w-[55%] max-w-[16rem]" />
        <Skeleton className="h-3 w-[40%] max-w-[12rem]" />
      </div>
      <span className="hidden shrink-0 -space-x-1.5 sm:flex">
        <Skeleton className="size-[22px] rounded-full" />
        <Skeleton className="size-[22px] rounded-full" />
        <Skeleton className="size-[22px] rounded-full" />
      </span>
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
      className="flex flex-col"
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
