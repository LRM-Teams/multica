"use client";

import { Search, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { SessionStatusFilter } from "../lib/session-list-filter";

const STATUS_OPTIONS: SessionStatusFilter[] = ["in_progress", "completed", "failed"];

type ResearchSessionFilterBarProps = {
  query: string;
  status: SessionStatusFilter | null;
  active: boolean;
  onQueryChange: (value: string) => void;
  onStatusChange: (value: SessionStatusFilter | null) => void;
  onClear: () => void;
};

export function ResearchSessionFilterBar({
  query,
  status,
  active,
  onQueryChange,
  onStatusChange,
  onClear,
}: ResearchSessionFilterBarProps) {
  const { t } = useT("research");

  const statusLabel = (id: SessionStatusFilter) => {
    switch (id) {
      case "in_progress":
        return t(($) => $.filter.status_in_progress);
      case "completed":
        return t(($) => $.filter.status_completed);
      case "failed":
        return t(($) => $.filter.status_failed);
    }
  };

  return (
    <div className="flex w-full max-w-3xl flex-col gap-2 sm:flex-row sm:items-center">
      <div className="relative min-w-0 flex-1">
        <Search
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden
        />
        <Input
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          placeholder={t(($) => $.filter.search_placeholder)}
          aria-label={t(($) => $.filter.search_label)}
          className="pl-9"
        />
      </div>
      <div
        role="radiogroup"
        aria-label={t(($) => $.filter.status_label)}
        className="flex flex-wrap items-center gap-1.5"
      >
        {STATUS_OPTIONS.map((id) => {
          const selected = status === id;
          return (
            <button
              key={id}
              type="button"
              role="radio"
              aria-checked={selected}
              onClick={() => onStatusChange(selected ? null : id)}
              className={cn(
                "h-8 rounded-md border px-2.5 text-xs font-medium transition-colors",
                selected
                  ? "border-brand/40 bg-brand/10 text-brand"
                  : "bg-card text-muted-foreground hover:bg-accent/50 hover:text-foreground",
              )}
            >
              {statusLabel(id)}
            </button>
          );
        })}
        {active ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 gap-1 px-2 text-xs"
            onClick={onClear}
            aria-label={t(($) => $.filter.clear)}
          >
            <X className="size-3.5" aria-hidden />
            {t(($) => $.filter.clear)}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
