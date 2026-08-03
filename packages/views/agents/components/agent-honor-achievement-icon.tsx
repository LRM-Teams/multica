import { cn } from "@multica/ui/lib/utils";
import {
  AgentHonorLevelIcon,
  MAX_AGENT_HONOR_LEVEL,
} from "./agent-honor-level-icon";

const ENTRY_ACHIEVEMENT_RARITY = 10;
const TOP_ACHIEVEMENT_RARITY = 95;

export function agentAchievementIconLevel(rarity: number): number {
  if (!Number.isFinite(rarity) || rarity <= ENTRY_ACHIEVEMENT_RARITY) return 1;
  if (rarity >= TOP_ACHIEVEMENT_RARITY) return MAX_AGENT_HONOR_LEVEL;

  const progress =
    (rarity - ENTRY_ACHIEVEMENT_RARITY) /
    (TOP_ACHIEVEMENT_RARITY - ENTRY_ACHIEVEMENT_RARITY);

  return Math.round(progress * (MAX_AGENT_HONOR_LEVEL - 2)) + 1;
}

export function AgentHonorAchievementIcon({
  rarity,
  title,
  locked = false,
  featured = false,
  className,
}: {
  rarity: number;
  title: string;
  locked?: boolean;
  featured?: boolean;
  className?: string;
}) {
  const level = agentAchievementIconLevel(rarity);

  return (
    <span
      className={cn(
        "relative block size-14 shrink-0 transition duration-200",
        featured && "drop-shadow-[0_0_14px_rgba(99,102,241,0.4)]",
        locked && "opacity-45 grayscale saturate-0",
        className,
      )}
      data-agent-achievement-icon="warship"
      data-agent-achievement-level={level}
      data-agent-achievement-locked={locked ? "true" : undefined}
    >
      <AgentHonorLevelIcon level={level} title={title} className="size-full" />
    </span>
  );
}
