"use client";

import { ChevronRight, Clock, Loader2 } from "lucide-react";
import type { AgentPresenceDetail } from "@multica/core/agents";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { ProviderLogo } from "../../runtimes/components/provider-logo";
import {
  presentAgentActivityBand,
  resolveAgentActivityBand,
} from "../resolve-agent-live-status";

/**
 * Canonical agent list row (Frank / Parker 2026-08-03 task #30):
 * Raft-aligned: avatar · name · runtime ………… Activity (right).
 *
 * All surfaces that list agent entries should use this — do not hand-roll
 * another row with a private activity label path.
 */
export function AgentActivityListItem({
  agentId,
  displayName,
  provider,
  runtimeLabel,
  presence,
  onClick,
  layout = "inline",
  showChevron = false,
  showBorder = false,
  className,
  avatarSize,
  selectionMode = false,
  selected = false,
}: {
  agentId: string;
  displayName: string;
  provider?: string | null;
  runtimeLabel?: string | null;
  presence?: AgentPresenceDetail | null;
  onClick?: () => void;
  /** inline = single line (desktop); stacked = name + meta (mobile). */
  layout?: "inline" | "stacked";
  showChevron?: boolean;
  showBorder?: boolean;
  className?: string;
  avatarSize?: number;
  /** Multi-select mode (Computer Agents section Select). */
  selectionMode?: boolean;
  selected?: boolean;
}) {
  const band = resolveAgentActivityBand(presence ?? null);
  const view = band
    ? presentAgentActivityBand(band, true)
    : { label: "—", dotClass: "bg-muted-foreground/40" };
  const isWorking = band === "working" && presence?.workload !== "queued";
  const isQueued = presence?.workload === "queued";
  const size = avatarSize ?? (layout === "stacked" ? 28 : 22);

  const activity = (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-[50%] items-center gap-1.5 text-muted-foreground",
        layout === "inline" && "ml-auto shrink-0",
      )}
      data-testid="agent-activity-list-item-activity"
    >
      {isWorking ? (
        <Loader2 className="h-3 w-3 shrink-0 animate-spin text-running" />
      ) : null}
      {isQueued ? (
        <Clock className="h-3 w-3 shrink-0 text-muted-foreground" />
      ) : null}
      {!isWorking && !isQueued ? (
        <span
          className={cn("size-1.5 shrink-0 rounded-full", view.dotClass)}
          aria-hidden
        />
      ) : null}
      <span className="truncate text-[13px]">{view.label}</span>
    </span>
  );

  const runtime = (
    <span className="inline-flex min-w-0 items-center gap-1 text-muted-foreground">
      {provider ? (
        <ProviderLogo provider={provider} className="h-3.5 w-3.5 shrink-0" />
      ) : null}
      {runtimeLabel ? (
        <span
          className="truncate text-[13px]"
          data-testid="agent-activity-list-item-runtime"
        >
          {runtimeLabel}
        </span>
      ) : null}
    </span>
  );

  if (layout === "stacked") {
    return (
      <button
        type="button"
        onClick={onClick}
        data-testid="agent-activity-list-item"
        data-agent-id={agentId}
        data-selected={selected || undefined}
        aria-pressed={selectionMode ? selected : undefined}
        className={cn(
          "flex w-full items-center gap-3 px-4 py-3 text-left text-sm transition-colors hover:bg-accent/50",
          showBorder && "border-b",
          selected && "bg-accent/60",
          className,
        )}
      >
        {selectionMode ? (
          <SelectionCheck selected={selected} />
        ) : null}
        <ActorAvatar
          actorType="agent"
          actorId={agentId}
          size={size}
          profileLink={false}
        />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">
            {displayName}
          </span>
          <span className="mt-0.5 flex min-w-0 items-center justify-between gap-2 text-xs">
            {runtime}
            {activity}
          </span>
        </span>
        {showChevron && !selectionMode ? (
          <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground/45" />
        ) : null}
      </button>
    );
  }

  // Raft desktop: [avatar] [name · runtime] ………… [activity]
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid="agent-activity-list-item"
      data-agent-id={agentId}
      data-selected={selected || undefined}
      aria-pressed={selectionMode ? selected : undefined}
      className={cn(
        "flex w-full items-center gap-2.5 px-4 py-3 text-left text-sm transition-colors hover:bg-accent/50",
        showBorder && "border-b",
        selected && "bg-accent/60",
        className,
      )}
    >
      {selectionMode ? <SelectionCheck selected={selected} /> : null}
      <ActorAvatar
        actorType="agent"
        actorId={agentId}
        size={size}
        profileLink={false}
      />
      <span className="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden">
        <span className="shrink-0 truncate font-medium">{displayName}</span>
        {(provider || runtimeLabel) && runtime}
      </span>
      {activity}
      {showChevron && !selectionMode ? (
        <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground/45" />
      ) : null}
    </button>
  );
}

function SelectionCheck({ selected }: { selected: boolean }) {
  return (
    <span
      className={cn(
        "flex h-4 w-4 shrink-0 items-center justify-center rounded border text-[10px]",
        selected
          ? "border-primary bg-primary text-primary-foreground"
          : "border-muted-foreground/40",
      )}
      aria-hidden
      data-testid="agent-activity-list-item-check"
    >
      {selected ? "✓" : ""}
    </span>
  );
}
