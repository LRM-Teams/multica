import { cn } from "@multica/ui/lib/utils";
import level01 from "./assets/honor-levels/agent-honor-level-01.webp";
import level02 from "./assets/honor-levels/agent-honor-level-02.webp";
import level03 from "./assets/honor-levels/agent-honor-level-03.webp";
import level04 from "./assets/honor-levels/agent-honor-level-04.webp";
import level05 from "./assets/honor-levels/agent-honor-level-05.webp";
import level06 from "./assets/honor-levels/agent-honor-level-06.webp";
import level07 from "./assets/honor-levels/agent-honor-level-07.webp";
import level08 from "./assets/honor-levels/agent-honor-level-08.webp";
import level09 from "./assets/honor-levels/agent-honor-level-09.webp";
import level10 from "./assets/honor-levels/agent-honor-level-10.webp";
import level11 from "./assets/honor-levels/agent-honor-level-11.webp";
import level12 from "./assets/honor-levels/agent-honor-level-12.webp";
import level13 from "./assets/honor-levels/agent-honor-level-13.webp";
import level14 from "./assets/honor-levels/agent-honor-level-14.webp";
import level15 from "./assets/honor-levels/agent-honor-level-15.webp";
import level16 from "./assets/honor-levels/agent-honor-level-16.webp";
import level17 from "./assets/honor-levels/agent-honor-level-17.webp";
import level18 from "./assets/honor-levels/agent-honor-level-18.webp";
import level19 from "./assets/honor-levels/agent-honor-level-19.webp";
import level20 from "./assets/honor-levels/agent-honor-level-20.webp";
import level21 from "./assets/honor-levels/agent-honor-level-21.webp";
import level22 from "./assets/honor-levels/agent-honor-level-22.webp";
import level23 from "./assets/honor-levels/agent-honor-level-23.webp";
import level24 from "./assets/honor-levels/agent-honor-level-24.webp";
import level25 from "./assets/honor-levels/agent-honor-level-25.webp";
import level26 from "./assets/honor-levels/agent-honor-level-26.webp";
import level27 from "./assets/honor-levels/agent-honor-level-27.webp";
import level28 from "./assets/honor-levels/agent-honor-level-28.webp";
import level29 from "./assets/honor-levels/agent-honor-level-29.webp";
import level30 from "./assets/honor-levels/agent-honor-level-30.webp";

type BundledImageAsset = string | { src: string };

const levelAssets = [
  level01,
  level02,
  level03,
  level04,
  level05,
  level06,
  level07,
  level08,
  level09,
  level10,
  level11,
  level12,
  level13,
  level14,
  level15,
  level16,
  level17,
  level18,
  level19,
  level20,
  level21,
  level22,
  level23,
  level24,
  level25,
  level26,
  level27,
  level28,
  level29,
  level30,
] as const satisfies readonly BundledImageAsset[];

export const MAX_AGENT_HONOR_LEVEL = levelAssets.length;

export function normalizeAgentHonorLevel(level: number): number {
  if (!Number.isFinite(level)) return 1;
  return Math.min(MAX_AGENT_HONOR_LEVEL, Math.max(1, Math.floor(level)));
}

function assetURL(asset: BundledImageAsset): string {
  return typeof asset === "string" ? asset : asset.src;
}

export function agentHonorLevelIconURL(level: number): string {
  const normalizedLevel = normalizeAgentHonorLevel(level);
  return assetURL(levelAssets[normalizedLevel - 1]!);
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
      className={cn("shrink-0 object-contain", className)}
      data-agent-honor-level={normalizedLevel}
    />
  );
}
