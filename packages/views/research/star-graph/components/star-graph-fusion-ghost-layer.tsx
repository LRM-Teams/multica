import { cn } from "@multica/ui/lib/utils";
import type { StarGraphFusionGhost } from "../lib/star-graph-fusion-transition";

const MAX_STAGGER_INDEX = 6;

export function StarGraphFusionGhostLayer({
  ghosts,
  lowPerformance = false,
}: {
  ghosts: readonly StarGraphFusionGhost[];
  lowPerformance?: boolean;
}) {
  if (ghosts.length === 0) return null;
  return (
    <div
      data-testid="star-graph-fusion-ghost-layer"
      className="sg-fusion-ghost-layer pointer-events-none absolute inset-0 z-[2]"
      aria-hidden="true"
    >
      {ghosts.map((ghost, index) => (
        <span
          key={ghost.id}
          data-testid="star-graph-fusion-ghost"
          data-source-node-id={ghost.id}
          data-tier={ghost.tier}
          data-state={ghost.state}
          className={cn(
            "sg-fusion-ghost",
            `sg-fusion-ghost-${ghost.tier}`,
            lowPerformance && "sg-fusion-ghost-low-performance",
          )}
          style={{
            left: ghost.x - ghost.radius,
            top: ghost.y - ghost.radius,
            width: ghost.radius * 2,
            height: ghost.radius * 2,
            "--fusion-target-x": `${ghost.translateX}px`,
            "--fusion-target-y": `${ghost.translateY}px`,
            animationDelay: `${Math.min(index, MAX_STAGGER_INDEX) * 34}ms`,
          } as React.CSSProperties}
        />
      ))}
    </div>
  );
}
