"use client";

import { ChevronRight } from "lucide-react";
import type { AgentPresenceDetail } from "@multica/core/agents";
import { useAgentPresenceDetail, useRunnerActivity } from "@multica/core/agents";
import { useCurrentWorkspace } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { ProviderLogo } from "../../runtimes/components/provider-logo";
import { useAgentLiveStatus } from "../use-agent-live-status";

const activityToneDotClass: Record<string, string> = {
  neutral: "bg-muted-foreground/40",
  info: "bg-blue-500",
  warning: "bg-warning",
  error: "bg-destructive",
  success: "bg-success",
};

/**
 * Shared list Activity mark. Labels and tones are supplied by the server-owned
 * Workspace Runner projection; presence and task state are never interpreted.
 */
export function AgentActivityStatus({
  agentId,
  className,
  alignEnd = false,
  unknownLabel,
  testId = "agent-activity-status",
}: {
  agentId: string;
  className?: string;
  /** Raft desktop list: push mark to the trailing edge. */
  alignEnd?: boolean;
  /** When presence is missing — callers that need a localized unknown string. */
  unknownLabel?: string;
  testId?: string;
}) {
  const workspaceId = useCurrentWorkspace()?.id;
  const { data } = useRunnerActivity(workspaceId, agentId);
  const summary = data?.summary;
  const presenceDetail = useAgentPresenceDetail(workspaceId, agentId);
  const presence = useAgentLiveStatus(workspaceId, agentId);
  const isOnline =
    presenceDetail !== "loading" && presenceDetail.availability === "online";
  const hasDynamicActivity =
    isOnline &&
    summary?.visibility === "visible" &&
    summary.tone !== "success" &&
    summary.tone !== "neutral";
  if (!hasDynamicActivity) {
    if (presence) {
      return (
        <span
          className={cn(
            "inline-flex min-w-0 max-w-[50%] items-center gap-1.5 text-muted-foreground",
            alignEnd && "ml-auto shrink-0",
            className,
          )}
          data-testid={testId}
          data-activity-tone="success"
        >
          <span className={cn("size-1.5 shrink-0 rounded-full", presence.dotClass)} aria-hidden />
          <span className="truncate text-[13px]">{presence.label}</span>
        </span>
      );
    }
    return (
      <span
        className={cn(
          "inline-flex min-w-0 items-center gap-1.5 text-muted-foreground/60",
          alignEnd && "ml-auto shrink-0",
          className,
        )}
        data-testid={testId}
      >
        {unknownLabel ?? "—"}
      </span>
    );
  }
  const isWorkingTone =
    summary.tone === "warning" || summary.tone === "info" || summary.tone === "active";
  const dotClass = isWorkingTone
    ? "bg-warning"
    : activityToneDotClass[summary.tone] ?? "bg-muted-foreground";
  const pulses = isWorkingTone;
  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-[50%] items-center gap-1.5 text-muted-foreground",
        alignEnd && "ml-auto shrink-0",
        className,
      )}
      data-testid={testId}
      data-activity-tone={summary.tone}
    >
      <span className="relative size-1.5 shrink-0" aria-hidden>
        {pulses ? (
          <span className={cn("absolute inset-0 animate-ping rounded-full opacity-60 motion-reduce:hidden", dotClass)} />
        ) : null}
        <span className={cn("absolute inset-0 rounded-full", dotClass)} />
      </span>
      <span className="truncate text-[13px]">{summary.label}</span>
    </span>
  );
}

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
  trailingLabel,
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
  /** Optional trailing text (e.g. "View agent" in delete dialogs). */
  trailingLabel?: string;
}) {
  void presence;
  const size = avatarSize ?? (layout === "stacked" ? 28 : 22);
  const trailing = trailingLabel ? (
    <span className="shrink-0 text-primary">{trailingLabel}</span>
  ) : null;

  const activity = (
    <AgentActivityStatus
      agentId={agentId}
      alignEnd={layout === "inline"}
      className={layout === "inline" ? undefined : "max-w-none"}
      testId="agent-activity-list-item-activity"
    />
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
        {selectionMode ? <SelectionCheck selected={selected} /> : null}
        <ActorAvatar
          actorType="agent"
          actorId={agentId}
          size={size}
          showStatusDot
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
        {trailing}
        {showChevron && !selectionMode ? (
          <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground/45" />
        ) : null}
      </button>
    );
  }

  // Raft desktop: [avatar] [name · runtime] ………… [activity] [trailing?]
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
        showStatusDot
        profileLink={false}
      />
      <span className="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden">
        <span className="shrink-0 truncate font-medium">{displayName}</span>
        {(provider || runtimeLabel) && runtime}
      </span>
      {activity}
      {trailing}
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
