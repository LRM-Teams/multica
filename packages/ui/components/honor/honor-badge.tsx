import { HONOR_BADGE_ICONS, GenesisNebulaIcon } from "./honor-badge-icons";

export function HonorBadgeIcon({
  svgKey,
  title,
  className = "size-4 shrink-0",
}: {
  svgKey: string;
  title?: string;
  className?: string;
}) {
  const Icon = HONOR_BADGE_ICONS[svgKey] ?? GenesisNebulaIcon;
  return <Icon title={title} className={className} />;
}
