import type { HonorBadgeCatalogItem } from "@multica/core/types/honor";
import { HonorBadgeCrest } from "@multica/ui/components/honor/honor-badge";
import { Progress } from "@multica/ui/components/ui/progress";
import { ArrowUpRight } from "lucide-react";
import { getNextHonorBadges, honorProgressPercent, isRareHonorBadge } from "./honor-progress";

export function HonorNextTargets({
  items,
  emptyLabel,
  progressLabel,
  rarityLabel,
}: {
  items: HonorBadgeCatalogItem[];
  emptyLabel: string;
  progressLabel: (current: number, target: number) => string;
  rarityLabel: (pct: number) => string;
}) {
  const targets = getNextHonorBadges(items);

  if (targets.length === 0) {
    return (
      <div className="rounded-2xl border border-dashed border-cyan-500/25 bg-cyan-500/5 px-5 py-8 text-center text-sm text-muted-foreground">
        {emptyLabel}
      </div>
    );
  }

  return (
    <div className="grid gap-3 lg:grid-cols-3">
      {targets.map((item) => {
        const progressPct = honorProgressPercent(item);
        const rare = isRareHonorBadge(item);

        return (
          <article
            key={item.id}
            className="relative overflow-hidden rounded-2xl border border-white/10 bg-slate-950 p-4 text-white shadow-[0_20px_50px_-38px_rgba(34,211,238,0.85)]"
          >
            <div
              aria-hidden="true"
              className="absolute inset-0 bg-[radial-gradient(circle_at_100%_0%,rgba(139,92,246,0.2),transparent_45%),radial-gradient(circle_at_0%_100%,rgba(6,182,212,0.14),transparent_42%)]"
            />
            <div className="relative flex items-start gap-3">
              <HonorBadgeCrest
                svgKey={item.svg_key}
                title={item.title}
                rare={rare}
              />
              <div className="min-w-0 flex-1">
                <div className="flex items-start justify-between gap-2">
                  <h4 className="line-clamp-1 text-sm font-semibold">{item.title}</h4>
                  <ArrowUpRight
                    aria-hidden="true"
                    className="mt-0.5 size-4 shrink-0 text-cyan-300"
                  />
                </div>
                <p className="mt-1 line-clamp-2 min-h-10 text-xs leading-5 text-slate-400">
                  {item.unlock_rule || item.description}
                </p>
                {rare && item.unlock_pct != null ? (
                  <p className="mt-1 font-mono text-[10px] text-amber-300">
                    {rarityLabel(item.unlock_pct)}
                  </p>
                ) : null}
              </div>
            </div>
            <div className="relative mt-4">
              <div className="mb-1.5 flex items-center justify-between gap-2 text-[11px] text-slate-400">
                <span>{item.progress?.label}</span>
                <span className="font-mono text-slate-200">
                  {progressLabel(
                    item.progress?.current ?? 0,
                    item.progress?.target ?? 0,
                  )}
                </span>
              </div>
              <Progress
                aria-label={item.progress?.label}
                value={progressPct}
                className="[&_[data-slot=progress-indicator]]:bg-gradient-to-r [&_[data-slot=progress-indicator]]:from-cyan-400 [&_[data-slot=progress-indicator]]:to-violet-400 [&_[data-slot=progress-track]]:h-1.5 [&_[data-slot=progress-track]]:bg-white/10"
              />
            </div>
          </article>
        );
      })}
    </div>
  );
}
