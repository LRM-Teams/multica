"use client";

import type {
  ResearchExecutionAgent,
  ResearchExecutionStatus,
} from "../lib/research-execution-panel-fixture";
import { ExecutionOverlayPanel, type ExecutionRow } from "../execution-overlay/index";
import { EXECUTION_STATUS_ACTION_KEY, type ExecutionStatus } from "../execution-overlay/execution-status";

/**
 * LRM-1479 — research execution panel upgraded to the 8-state execution
 * overlay. This file remains the public entry point so existing imports,
 * exports and the legacy 6-state tests keep working, while the actual
 * rendering is delegated to `ExecutionOverlayPanel`.
 *
 * The legacy adapter maps the pre-overlay 6-state agent rows onto the new
 * overlay model; live session wiring uses `ExecutionOverlayPanel` directly
 * with `buildExecutionOverlayRows` (times + recent output + bidirectional
 * locate).
 */

const LEGACY_TO_OVERLAY: Record<ResearchExecutionStatus, ExecutionStatus> = {
  queued: "waiting",
  running: "running",
  done: "done",
  failed: "failed",
  stale: "stale",
  idle: "waiting",
};

function toOverlayRow(agent: ResearchExecutionAgent): ExecutionRow {
  return {
    id: agent.id,
    name: agent.name,
    role: agent.role,
    initials: agent.initials,
    avatarUrl: agent.avatarUrl,
    status: LEGACY_TO_OVERLAY[agent.status],
    action: agent.action,
    actionKey: EXECUTION_STATUS_ACTION_KEY[LEGACY_TO_OVERLAY[agent.status]],
    actionDetail: agent.actionDetail,
    failureReasonKey: agent.failureReasonKey,
    updatedAt: Date.now(),
    currentNodeId: agent.currentNodeId,
    locationLabel: agent.locationLabel,
  };
}

export function ResearchExecutionPanel({
  agents,
  title,
  className,
  onLocate,
  error,
  onRetry,
  isRetrying = false,
}: {
  agents: readonly ResearchExecutionAgent[];
  title?: string;
  className?: string;
  onLocate?: (agent: ResearchExecutionAgent) => void;
  error?: string | null;
  onRetry?: () => void;
  isRetrying?: boolean;
}) {
  const rows = agents.map(toOverlayRow);
  return (
    <ExecutionOverlayPanel
      rows={rows}
      title={title}
      className={className}
      onLocate={onLocate ? (row) => {
        const original = agents.find((a) => a.id === row.id);
        if (original) onLocate(original);
      } : undefined}
      sync={{
        disconnected: Boolean(error),
        onRetry,
        isRetrying,
      }}
    />
  );
}

export type { ExecutionStatus };
