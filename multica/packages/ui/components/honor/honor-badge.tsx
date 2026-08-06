import { HONOR_BADGE_ICONS, GenesisNebulaIcon } from "./honor-badge-icons";
import { ActorBadgeFrame } from "../common/actor-badge-frame";
import { honorBadgeTone } from "../../lib/honor-badge-tone";
import { cn } from "../../lib/utils";

export function HonorBadgeIcon({
  svgKey,
  title,
  className,
  medal = false,
}: {
  svgKey: string;
  title?: string;
  className?: string;
  /** QQ-style pedestal for inline chat surfaces. */
  medal?: boolean;
}) {
  const Icon = HONOR_BADGE_ICONS[svgKey] ?? GenesisNebulaIcon;
  const icon = (
    <Icon
      title={title}
      className={cn(medal ? "size-4" : (className ?? "size-4 shrink-0"))}
    />
  );

  if (!medal) return icon;

  return (
    <ActorBadgeFrame
      tone={honorBadgeTone(svgKey)}
      className={className ?? "size-6"}
      animated
    >
      {icon}
    </ActorBadgeFrame>
  );
}

const crestToneClass = {
  gold: "from-amber-200/70 via-amber-500/35 to-orange-950/80 text-amber-100",
  cyan: "from-cyan-200/70 via-cyan-500/35 to-slate-950/80 text-cyan-100",
  violet: "from-violet-200/70 via-violet-500/35 to-indigo-950/80 text-violet-100",
  amber: "from-orange-200/70 via-orange-500/35 to-red-950/80 text-orange-100",
  emerald: "from-emerald-200/70 via-emerald-500/35 to-teal-950/80 text-emerald-100",
  neutral: "from-slate-200/60 via-slate-500/30 to-slate-950/80 text-slate-100",
} as const;

/** Large achievement crest for collection cards and unlock ceremonies. */
export function HonorBadgeCrest({
  svgKey,
  title,
  className,
  locked = false,
  rare = false,
  animated = false,
}: {
  svgKey: string;
  title?: string;
  className?: string;
  locked?: boolean;
  rare?: boolean;
  /** Slow breathing treatment for equipped or showcased badges. */
  animated?: boolean;
}) {
  const tone = honorBadgeTone(svgKey);

  return (
    <span
      className={cn(
        "relative isolate grid size-16 shrink-0 place-items-center",
        "[clip-path:polygon(50%_0%,90%_18%,96%_65%,50%_100%,4%_65%,10%_18%)]",
        "bg-gradient-to-br p-px shadow-[0_14px_30px_-16px_currentColor]",
        crestToneClass[tone],
        rare && "size-[4.5rem] drop-shadow-[0_0_16px_rgba(251,191,36,0.3)]",
        animated && !locked && "honor-badge-crest--animated",
        locked && "grayscale",
        className,
      )}
      data-honor-rare={rare ? "true" : undefined}
    >
      <span
        className={cn(
          "grid size-full place-items-center bg-slate-950/90",
          "[clip-path:polygon(50%_2%,89%_20%,94%_64%,50%_97%,6%_64%,11%_20%)]",
          locked && "opacity-55",
        )}
      >
        <HonorBadgeIcon
          svgKey={svgKey}
          title={title}
          className={cn("size-10", rare && "size-11")}
        />
      </span>
      {rare ? (
        <span
          aria-hidden="true"
          className="absolute -right-0.5 top-1 size-2 rotate-45 border border-amber-100/80 bg-amber-300 shadow-[0_0_12px_rgba(251,191,36,0.8)]"
        />
      ) : null}
    </span>
  );
}
