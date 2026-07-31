import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/** Compact achievement crest — keeps honor/fleet badges legible at chat scale. */
export function ActorBadgeFrame({
  children,
  className,
  tone = "neutral",
  rank,
  animated = false,
}: {
  children: ReactNode;
  className?: string;
  tone?: "neutral" | "gold" | "cyan" | "violet" | "amber" | "emerald" | "sky" | "orange";
  /** Top-3 fleet rank ribbon (1–3). */
  rank?: number;
  /** Slow spectrum and halo treatment for equipped honor badges. */
  animated?: boolean;
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
        "actor-badge-frame relative isolate inline-flex size-6 shrink-0 items-center justify-center",
        animated && "actor-badge-frame--animated",
        className,
      )}
      data-honor-badge-tone={tone}
    >
      <span
        className={cn(
          "relative z-[1] flex size-full items-center justify-center p-px shadow-[0_6px_14px_-8px_currentColor]",
          "[clip-path:polygon(50%_0%,91%_18%,96%_66%,50%_100%,4%_66%,9%_18%)]",
          animated
            ? "bg-[conic-gradient(from_210deg,#22d3ee,#818cf8,#f0abfc,#fde68a,#34d399,#22d3ee)]"
            : "bg-gradient-to-br",
          !animated && toneRing,
        )}
      >
        <span className="flex size-full items-center justify-center bg-background/90 [clip-path:polygon(50%_3%,89%_20%,93%_64%,50%_96%,7%_64%,11%_20%)] dark:bg-slate-950/85">
          {children}
        </span>
      </span>
      {rank && rank > 0 && rank <= 3 ? (
        <span
          className={cn(
            "pointer-events-none absolute -bottom-0.5 -right-0.5 z-[2] flex min-w-[0.85rem] items-center justify-center rounded-full px-0.5 text-[8px] font-bold leading-none tabular-nums shadow ring-1 ring-background",
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
