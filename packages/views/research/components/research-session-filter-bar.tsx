"use client";

import { Search } from "lucide-react";
import { useRef } from "react";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { SessionStatusFilter } from "../lib/session-list-filter";
import type { SessionListStatusCounts } from "../lib/session-list-counts";

type StatusOption = SessionStatusFilter | null;

type ResearchSessionFilterBarProps = {
  query: string;
  status: SessionStatusFilter | null;
  counts: SessionListStatusCounts;
  onQueryChange: (value: string) => void;
  onStatusChange: (value: SessionStatusFilter | null) => void;
  onClear: () => void;
  searchInputRef?: React.RefObject<HTMLInputElement | null>;
};

/**
 * LRM-1106 / LRM-1115 — search + status radiogroup.
 * Clear lives outside this tree (page scope row / S4). 「全部」 is the null option;
 * re-clicking a selected segment does not deselect.
 */
export function ResearchSessionFilterBar({
  query,
  status,
  counts,
  onQueryChange,
  onStatusChange,
  onClear,
  searchInputRef,
}: ResearchSessionFilterBarProps) {
  const { t } = useT("research");
  const groupRef = useRef<HTMLDivElement>(null);

  const options: Array<{
    id: StatusOption;
    label: string;
    count: number;
    show: boolean;
  }> = (
    [
      {
        id: null,
        label: t(($) => $.filter.status_all),
        count: counts.all,
        show: true,
      },
      {
        id: "in_progress" as const,
        label: t(($) => $.filter.status_in_progress),
        count: counts.in_progress,
        show: true,
      },
      {
        id: "completed" as const,
        label: t(($) => $.filter.status_completed),
        count: counts.completed,
        show: true,
      },
      {
        id: "failed" as const,
        label: t(($) => $.filter.status_failed),
        count: counts.failed,
        // D1=A: only render Failed when count > 0
        show: counts.failed > 0,
      },
    ] satisfies Array<{
      id: StatusOption;
      label: string;
      count: number;
      show: boolean;
    }>
  ).filter((o) => o.show);

  const selectedIndex = Math.max(
    0,
    options.findIndex((o) => o.id === status),
  );

  const focusOption = (index: number) => {
    const buttons = groupRef.current?.querySelectorAll<HTMLButtonElement>(
      '[role="radio"]',
    );
    buttons?.[index]?.focus();
  };

  const selectByIndex = (index: number) => {
    const opt = options[index];
    if (!opt) return;
    onStatusChange(opt.id);
    focusOption(index);
  };

  const onGroupKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      e.preventDefault();
      onClear();
      return;
    }
    if (options.length === 0) return;
    let next = selectedIndex;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") {
      e.preventDefault();
      next = (selectedIndex + 1) % options.length;
      selectByIndex(next);
    } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
      e.preventDefault();
      next = (selectedIndex - 1 + options.length) % options.length;
      selectByIndex(next);
    } else if (e.key === "Home") {
      e.preventDefault();
      selectByIndex(0);
    } else if (e.key === "End") {
      e.preventDefault();
      selectByIndex(options.length - 1);
    }
  };

  return (
    <div className="flex w-full flex-col gap-2 md:flex-row md:items-center">
      <div className="relative min-w-0 flex-1">
        <Search
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden
        />
        <Input
          ref={searchInputRef}
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          placeholder={t(($) => $.filter.search_placeholder)}
          aria-label={t(($) => $.filter.search_label)}
          className="pl-9"
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              if (query.trim() || status != null) {
                e.preventDefault();
                onClear();
              } else {
                (e.target as HTMLInputElement).blur();
              }
            }
          }}
        />
      </div>
      <div
        ref={groupRef}
        role="radiogroup"
        tabIndex={-1}
        aria-label={t(($) => $.filter.status_label)}
        className="flex flex-wrap items-center gap-1.5"
        onKeyDown={onGroupKeyDown}
      >
        {options.map((opt) => {
          const selected = status === opt.id;
          const ariaName = t(($) => $.filter.count_aria, {
            label: opt.label,
            count: opt.count,
          });
          return (
            <button
              key={opt.id ?? "all"}
              type="button"
              role="radio"
              aria-checked={selected}
              aria-label={ariaName}
              tabIndex={selected || (status == null && opt.id == null) ? 0 : -1}
              data-testid={`research-filter-status-${opt.id ?? "all"}`}
              onClick={() => {
                // LRM-1115: clicking the selected segment does not deselect.
                onStatusChange(opt.id);
              }}
              className={cn(
                "h-8 rounded-md border px-2.5 text-xs font-medium transition-colors",
                selected
                  ? "border-brand/40 bg-brand/10 text-brand"
                  : "bg-card text-muted-foreground hover:bg-accent/50 hover:text-foreground",
              )}
            >
              <span aria-hidden>
                {opt.label}
                <span className="ml-1 tabular-nums text-xs">
                  {opt.count}
                </span>
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
