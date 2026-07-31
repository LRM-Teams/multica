import { HONOR_BADGE_ICONS, GenesisNebulaIcon } from "./honor-badge-icons";
import { ActorBadgeFrame } from "../common/actor-badge-frame";
import { honorBadgeTone } from "../../lib/honor-badge-tone";
import { cn } from "../../lib/utils";

export function HonorBadgeIcon({
  svgKey,
  title,
  className = "size-4 shrink-0",
  medal = false,
}: {
  svgKey: string;
  title?: string;
  className?: string;
  /** QQ-style pedestal for inline chat surfaces. */
  medal?: boolean;
}) {
  const Icon = HONOR_BADGE_ICONS[svgKey] ?? GenesisNebulaIcon;
  const icon = <Icon title={title} className={cn(medal ? "size-4" : className)} />;

  if (!medal) return icon;

  return (
    <ActorBadgeFrame tone={honorBadgeTone(svgKey)} className={className}>
      {icon}
    </ActorBadgeFrame>
  );
}
