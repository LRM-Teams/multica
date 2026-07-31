import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/** QQ-style service-icon pedestal — makes inline honor/fleet badges read at chat scale. */
export function ActorBadgeFrame({
  children,
  className,
  tone = "neutral",
  rank,
}: {
  children: ReactNode;
  className?: string;
  tone?: "neutral" | "gold" | "cyan" | "violet" | "amber" | "emerald" | "sky" | "orange";
  /** Top-3 fleet rank ribbon (1–3). */
  rank?: number;
}) {
  const toneRing =
    tone === "gold"
      ? "from-amber-200/90 via-amber-400/80 to-amber-600/70"
      : tone === "cyan"
        ? "from-cyan-200/90 via-sky-400/75 to-indigo-500/70"
        : tone === "violet"
          ? "from-violet-200/90 via-purple-400/75 to-indigo-600/70"
          : tone === "amber"
            ? "from-yellow-200/90 via-amber-300/80 to-orange-500/75"
            : tone === "emerald"
              ? "from-emerald-200/90 via-emerald-400/75 to-teal-600/70"
              : tone === "sky"
                ? "from-sky-200/90 via-sky-400/75 to-blue-600/70"
                : tone === "orange"
                  ? "from-orange-200/90 via-orange-400/75 to-red-600/70"
                  : "from-slate-200/80 via-slate-300/70 to-slate-500/60";

  return (
    <span
      className={cn(
        "relative inline-flex size-6 shrink-0 items-center justify-center rounded-md",
        "bg-gradient-to-br shadow-sm ring-1 ring-black/10 dark:ring-white/15",
        toneRing,
        className,
      )}
    >
      <span className="flex size-[1.375rem] items-center justify-center rounded-[0.3rem] bg-background/85 shadow-inner ring-1 ring-black/5 dark:bg-background/70">
        {children}
      </span>
      {rank && rank > 0 && rank <= 3 ? (
        <span
          className={cn(
            "pointer-events-none absolute -bottom-0.5 -right-0.5 flex min-w-[0.85rem] items-center justify-center rounded-full px-0.5 text-[8px] font-bold leading-none tabular-nums shadow",
            rank === 1 && "bg-amber-400 text-amber-950",
            rank === 2 && "bg-slate-300 text-slate-900",
            rank === 3 && "bg-orange-400 text-orange-950",
          )}
        >
          {rank}
        </span>
      ) : null}
    </span>
  );
}
