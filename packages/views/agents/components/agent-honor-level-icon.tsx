import { cn } from "@multica/ui/lib/utils";
import {
  honorAssetFallbackURL,
  honorAssetURL,
  recoverHonorAsset,
} from "../../honor/honor-assets";

export const MAX_AGENT_HONOR_LEVEL = 30;

export function normalizeAgentHonorLevel(level: number): number {
  if (!Number.isFinite(level)) return 1;
  return Math.min(MAX_AGENT_HONOR_LEVEL, Math.max(1, Math.floor(level)));
}

export function agentHonorLevelIconURL(level: number): string {
  return honorAssetURL(agentHonorLevelIconPath(level));
}

export function agentHonorLevelIconFallbackURL(level: number): string {
  return honorAssetFallbackURL(agentHonorLevelIconPath(level));
}

function agentHonorLevelIconPath(level: number): string {
  const normalizedLevel = normalizeAgentHonorLevel(level);
  return `agents/agent-honor-level-${String(normalizedLevel).padStart(2, "0")}.webp`;
}

export function AgentHonorLevelIcon({
  level,
  title,
  className,
  priority = false,
}: {
  level: number;
  title?: string;
  className?: string;
  priority?: boolean;
}) {
  const normalizedLevel = normalizeAgentHonorLevel(level);

  return (
    <img
      src={agentHonorLevelIconURL(normalizedLevel)}
      alt={title ?? ""}
      aria-hidden={title ? undefined : true}
      width={256}
      height={256}
      loading={priority ? "eager" : "lazy"}
      fetchPriority={priority ? "high" : "auto"}
      decoding="async"
      draggable={false}
      onError={(event) =>
        recoverHonorAsset(
          event.currentTarget,
          agentHonorLevelIconPath(normalizedLevel),
        )
      }
      className={cn("shrink-0 object-contain", className)}
      data-agent-honor-level={normalizedLevel}
    />
  );
}
