"use client";

import { Filter } from "lucide-react";
import type { ResearchCanvasFilter } from "@multica/core/research";
import {
  isBlankFilter,
  useResearchCanvasStore,
} from "@multica/core/research";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { D5FilterOptions } from "../lib/research-d5-filter-options";

function FilterField({
  label,
  value,
  onChange,
  options,
  allLabel,
  testId,
}: {
  label: string;
  value: string | null | undefined;
  onChange: (next: string | null) => void;
  options: readonly string[];
  allLabel: string;
  testId: string;
}) {
  if (options.length === 0) return null;

  return (
    <label className="grid gap-1">
      <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <select
        data-testid={testId}
        value={value ?? ""}
        onChange={(event) => onChange(event.target.value || null)}
        className="h-8 rounded-md border border-border bg-background px-2 text-[11px] text-foreground"
      >
        <option value="">{allLabel}</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    </label>
  );
}

export function ResearchD5CanvasFilter({
  options,
  className,
}: {
  options: D5FilterOptions;
  className?: string;
}) {
  const { t } = useT("research");
  const filter = useResearchCanvasStore((s) => s.filter);
  const setFilter = useResearchCanvasStore((s) => s.setFilter);
  const clearFilter = useResearchCanvasStore((s) => s.clearFilter);
  const active = !isBlankFilter(filter);

  const patch = (partial: Partial<ResearchCanvasFilter>) => setFilter(partial);

  return (
    <Popover>
      <PopoverTrigger
        data-testid="research-d5-filter-trigger"
        className={cn(
          "d5-filter-trigger d5-lens-btn inline-flex items-center gap-1.5",
          active && "d5-filter-trigger-active",
          className,
        )}
        aria-pressed={active}
      >
        <Filter className="size-3.5" aria-hidden />
        {active ? t(($) => $.d5.filter.active) : t(($) => $.d5.filter.trigger)}
      </PopoverTrigger>
      <PopoverContent
        align="end"
        className="w-[min(18rem,calc(100vw-1.5rem))] gap-3 p-3"
        data-testid="research-d5-filter-popover"
      >
        <div className="grid gap-2.5">
          <FilterField
            label={t(($) => $.d5.filter.status)}
            value={filter.status}
            onChange={(status) => patch({ status })}
            options={options.statuses}
            allLabel={t(($) => $.d5.filter.all)}
            testId="research-d5-filter-status"
          />
          <FilterField
            label={t(($) => $.d5.filter.tier)}
            value={filter.tier}
            onChange={(tier) => patch({ tier })}
            options={options.tiers}
            allLabel={t(($) => $.d5.filter.all)}
            testId="research-d5-filter-tier"
          />
          <FilterField
            label={t(($) => $.d5.filter.round)}
            value={filter.round}
            onChange={(round) => patch({ round })}
            options={options.rounds}
            allLabel={t(($) => $.d5.filter.all)}
            testId="research-d5-filter-round"
          />
          <FilterField
            label={t(($) => $.d5.filter.cluster)}
            value={filter.cluster}
            onChange={(cluster) => patch({ cluster })}
            options={options.clusters}
            allLabel={t(($) => $.d5.filter.all)}
            testId="research-d5-filter-cluster"
          />
          <label className="grid gap-1">
            <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
              {t(($) => $.d5.filter.query)}
            </span>
            <Input
              data-testid="research-d5-filter-query"
              value={filter.query ?? ""}
              onChange={(event) => patch({ query: event.target.value })}
              placeholder={t(($) => $.d5.filter.query_placeholder)}
              className="h-8 text-[11px]"
            />
          </label>
        </div>
        <div className="flex justify-end">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            data-testid="research-d5-filter-clear"
            disabled={!active}
            onClick={() => clearFilter()}
          >
            {t(($) => $.d5.filter.clear)}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
