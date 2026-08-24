"use client";

import { ChevronRight } from "lucide-react";
import type { AgentPresence } from "@multica/core/agents";
import { useAgentPresence, useRunnerActivitySummary } from "@multica/core/agents";
import { useCurrentWorkspace } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { ProviderLogo } from "../../runtimes/components/provider-logo";
import { useT } from "../../i18n";
import { resolveAgentLiveStatus } from "../resolve-agent-live-status";
import { runnerActivityVisuals } from "../runner-activity-visuals";

/**
 * Shared list Activity mark. The daemon supplies lifecycle facts and a label;
 * this view owns visibility, color, and motion.
 */
export function AgentActivityStatus({
  agentId,
  className,
  alignEnd = false,
  unknownLabel,
  testId = "agent-activity-status",
  presence,
}: {
  agentId: string;
  className?: string;
  /** Raft desktop list: push mark to the trailing edge. */
  alignEnd?: boolean;
  /** When presence is missing — callers that need a localized unknown string. */
  unknownLabel?: string;
  testId?: string;
  /** Page-level snapshot avoids one Presence Query observer per list row. */
  presence?: AgentPresence | "loading";
}) {
  const workspaceId = useCurrentWorkspace()?.id;
  if (presence !== undefined) {
    return (
      <AgentActivityStatusView
        workspaceId={workspaceId}
        agentId={agentId}
        className={className}
        alignEnd={alignEnd}
        unknownLabel={unknownLabel}
        testId={testId}
        presence={presence}
      />
    );
  }
  return (
    <QueriedAgentActivityStatus
      workspaceId={workspaceId}
      agentId={agentId}
      className={className}
      alignEnd={alignEnd}
      unknownLabel={unknownLabel}
      testId={testId}
    />
  );
}

function QueriedAgentActivityStatus({
  workspaceId,
  agentId,
  ...props
}: {
  workspaceId: string | undefined;
  agentId: string;
  className?: string;
  alignEnd: boolean;
  unknownLabel?: string;
  testId: string;
}) {
  const presence = useAgentPresence(workspaceId, agentId);
  return (
    <AgentActivityStatusView
      {...props}
      workspaceId={workspaceId}
      agentId={agentId}
      presence={presence}
    />
  );
}

function AgentActivityStatusView({
  workspaceId,
  agentId,
  className,
  alignEnd,
  unknownLabel,
  testId,
  presence,
}: {
  workspaceId: string | undefined;
  agentId: string;
  className?: string;
  alignEnd: boolean;
  unknownLabel?: string;
  testId: string;
  presence: AgentPresence | "loading";
}) {
  const { t: tAgents } = useT("agents");
  const { data: summary } = useRunnerActivitySummary(workspaceId, agentId);
  const liveStatus = resolveAgentLiveStatus({ presence, tAgents });
  const isOnline = presence === "online";
  const isOffline = presence === "offline";
  const activityVisuals = summary
    ? runnerActivityVisuals({ activity_kind: summary.activityKind, detail_kind: summary.detailKind })
    : null;
  const hasDynamicActivity = Boolean(
    summary && activityVisuals?.show && (isOnline || (isOffline && summary.activityKind === "error")),
  );
  if (!liveStatus && !hasDynamicActivity) {
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
  const activityKind = hasDynamicActivity ? summary!.activityKind : "online";
  const dotClass = activityVisuals?.dotClass ?? "bg-emerald-500";
  const pulses = hasDynamicActivity && Boolean(activityVisuals?.pulse);
  const showLiveStatus = !!liveStatus && !hasDynamicActivity;
  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-[50%] items-center gap-1.5 text-muted-foreground",
        alignEnd && "ml-auto shrink-0",
        className,
      )}
      data-testid={testId}
      data-activity-kind={activityKind}
    >
      {showLiveStatus ? (
        <>
          <span
            className={cn("size-1.5 shrink-0 rounded-full", liveStatus.dotClass)}
            aria-hidden
          />
          <span className="shrink-0 text-[13px]">{liveStatus.label}</span>
        </>
      ) : null}
      {hasDynamicActivity ? (
        <>
          <span className="relative size-1.5 shrink-0" aria-hidden>
            {pulses ? (
              <span
                className={cn(
                  "absolute inset-0 animate-ping rounded-full opacity-60 motion-reduce:hidden",
                  dotClass,
                )}
              />
            ) : null}
            <span className={cn("absolute inset-0 rounded-full", dotClass)} />
          </span>
          <span className="truncate text-[13px]">{summary!.label}</span>
        </>
      ) : null}
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
  presence?: AgentPresence | null;
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
  const resolvedPresence = presence === null ? "loading" : presence;
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
      presence={resolvedPresence}
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
          agentPresence={resolvedPresence}
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
        agentPresence={resolvedPresence}
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
