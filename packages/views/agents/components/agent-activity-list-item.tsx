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
 * avatar · name · runtime · Activity (Idle / Working / Disconnected…).
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
      className="inline-flex min-w-0 items-center gap-1 text-muted-foreground"
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
      <span className="truncate">{view.label}</span>
    </span>
  );

  const runtime = (
    <span className="inline-flex min-w-0 items-center gap-1 text-muted-foreground">
      {provider ? (
        <ProviderLogo provider={provider} className="h-3.5 w-3.5 shrink-0" />
      ) : null}
      {runtimeLabel ? (
        <span className="truncate" data-testid="agent-activity-list-item-runtime">
          {runtimeLabel}
        </span>
      ) : null}
    </span>
  );

  const body =
    layout === "stacked" ? (
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium underline decoration-muted-foreground/40 underline-offset-2">
          {displayName}
        </span>
        <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs">
          {runtime}
          <span aria-hidden>·</span>
          {activity}
        </span>
      </span>
    ) : (
      <>
        <span className="shrink-0 truncate font-medium underline decoration-muted-foreground/40 underline-offset-2">
          {displayName}
        </span>
        {(provider || runtimeLabel) && (
          <>
            <span className="shrink-0 text-muted-foreground" aria-hidden>
              ·
            </span>
            {runtime}
          </>
        )}
        <span className="shrink-0 text-muted-foreground" aria-hidden>
          ·
        </span>
        {activity}
      </>
    );

  return (
    <button
      type="button"
      onClick={onClick}
      data-testid="agent-activity-list-item"
      data-agent-id={agentId}
      className={cn(
        "flex w-full items-center gap-1.5 px-4 py-3 text-left text-sm transition-colors hover:bg-accent/50",
        layout === "stacked" && "gap-3",
        showBorder && "border-b",
        className,
      )}
    >
      <ActorAvatar
        actorType="agent"
        actorId={agentId}
        size={size}
        profileLink={false}
      />
      {body}
      {showChevron ? (
        <ChevronRight className="ml-auto h-4 w-4 shrink-0 text-muted-foreground/45" />
      ) : null}
    </button>
  );
}
