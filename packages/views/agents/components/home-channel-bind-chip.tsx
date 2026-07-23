"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, Hash } from "lucide-react";
import { channelsOptions } from "@multica/core/channels";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Channel } from "@multica/core/types";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useT } from "../../i18n";

/**
 * 方案 A bind control — `#name ▾` chip inside the「仅本群」option.
 * Lists unarchived group channels only. Explicit empty / load-error states
 * (LRM-238: never silently invent a home or fall back to private).
 */
export function HomeChannelBindChip({
  value,
  onChange,
  invalid = false,
  disabled = false,
  className = "",
}: {
  value: string | null;
  onChange: (channelId: string) => void;
  /** Highlight when submit blocked for missing home. */
  invalid?: boolean;
  disabled?: boolean;
  className?: string;
}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const {
    data: channels = [],
    isLoading,
    isError,
    refetch,
    isFetching,
  } = useQuery(channelsOptions(wsId));

  const groups = useMemo(
    () =>
      channels.filter((c) => c.kind === "group" && !c.archived_at),
    [channels],
  );

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return groups;
    return groups.filter((c) => c.name.toLowerCase().includes(q));
  }, [groups, filter]);

  const selected = groups.find((c) => c.id === value) ?? null;
  // Bound id present but not in list (archived / left / API lag) — show
  // explicit missing state, never swap to another channel.
  const missingBound = !!value && !selected && !isLoading;

  const triggerLabel = missingBound
    ? t(($) => $.visibility_bind.channel_unavailable)
    : selected
      ? selected.name
      : t(($) => $.visibility_bind.pick_channel);

  const chipClass = [
    "inline-flex max-w-full items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium transition-colors",
    invalid || missingBound
      ? "border-destructive bg-destructive/5 text-destructive"
      : "border-border bg-muted text-foreground",
    disabled ? "opacity-50 cursor-not-allowed" : "hover:bg-accent cursor-pointer",
    className,
  ].join(" ");

  if (isError) {
    return (
      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-destructive">
        <span>{t(($) => $.visibility_bind.load_failed)}</span>
        <button
          type="button"
          className="underline underline-offset-2"
          onClick={(e) => {
            e.stopPropagation();
            void refetch();
          }}
          disabled={isFetching}
        >
          {t(($) => $.visibility_bind.retry)}
        </button>
      </div>
    );
  }

  if (!isLoading && groups.length === 0) {
    return (
      <p className="mt-2 text-xs text-muted-foreground">
        {t(($) => $.visibility_bind.no_groups)}
      </p>
    );
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setFilter("");
      }}
    >
      <PopoverTrigger
        render={
          <button
            type="button"
            disabled={disabled || isLoading}
            className={chipClass}
            aria-label={t(($) => $.visibility_bind.pick_channel)}
            onClick={(e) => e.stopPropagation()}
          />
        }
      >
        <Hash className="size-3 shrink-0 text-muted-foreground" />
        <span className="min-w-0 truncate">
          {isLoading ? t(($) => $.visibility_bind.loading) : triggerLabel}
        </span>
        <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-56 p-0"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="border-b p-1.5">
          <input
            type="text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t(($) => $.visibility_bind.search_placeholder)}
            className="w-full rounded-md border border-border bg-background px-2 py-1 text-xs outline-none focus:border-primary"
          />
        </div>
        <div className="max-h-48 overflow-y-auto p-1">
          {filtered.length === 0 ? (
            <p className="px-2 py-1.5 text-xs text-muted-foreground">
              {t(($) => $.visibility_bind.no_match)}
            </p>
          ) : (
            filtered.map((c: Channel) => (
              <button
                key={c.id}
                type="button"
                className={`flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent ${
                  c.id === value ? "bg-primary/5" : ""
                }`}
                onClick={() => {
                  onChange(c.id);
                  setOpen(false);
                  setFilter("");
                }}
              >
                <Hash className="size-3 shrink-0 text-muted-foreground" />
                <span className="min-w-0 truncate">{c.name}</span>
              </button>
            ))
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}

/** Resolve a home channel display name from the workspace channel list. */
export function useHomeChannelName(
  homeChannelId: string | null | undefined,
): {
  name: string | null;
  missing: boolean;
  loading: boolean;
} {
  const wsId = useWorkspaceId();
  const { data: channels = [], isLoading } = useQuery(channelsOptions(wsId));
  if (!homeChannelId) {
    return { name: null, missing: false, loading: false };
  }
  const hit = channels.find(
    (c) => c.id === homeChannelId && c.kind === "group",
  );
  return {
    name: hit?.name ?? null,
    missing: !isLoading && !hit,
    loading: isLoading,
  };
}
