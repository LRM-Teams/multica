"use client";

import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  resolveAgentLifecycleStatus,
  type AgentLifecycleStatusVisual,
} from "../resolve-agent-lifecycle-status";

/**
 * Denser-surface lifecycle line for agent side panel / profile / inspector.
 * Reads `runtime_display_status` — never presence.ts (LRM-248 avatar Online/
 * Offline stays untouched). idle/working render nothing.
 */
export function AgentLifecycleStatusLine({
  status,
  className,
}: {
  status: string | null | undefined;
  className?: string;
}) {
  const { t } = useT("agents");
  const visual = resolveAgentLifecycleStatus(status, t);
  if (!visual) return null;
  return <AgentLifecycleStatusMark visual={visual} className={className} />;
}

export function AgentLifecycleStatusMark({
  visual,
  className,
}: {
  visual: AgentLifecycleStatusVisual;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex min-w-0 items-center gap-1 text-xs",
        visual.toneClass,
        className,
      )}
      data-testid="agent-lifecycle-status"
      data-shape={visual.shape}
    >
      <span
        className={cn(
          "size-1.5 shrink-0",
          visual.shape === "square" ? "rounded-[1px]" : "rounded-full",
          visual.dotClass,
        )}
        aria-hidden
      />
      <span className="truncate">{visual.label}</span>
    </span>
  );
}
