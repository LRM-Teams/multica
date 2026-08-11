"use client";

import { useQuery } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { agentHonorOptions } from "@multica/core/agents";
import { appendQueryParams, useWorkspacePaths } from "@multica/core/paths";
import type { AgentHonorDashboard } from "@multica/core/types";
import { FleetRankBadge } from "@multica/ui/components/fleet/fleet-class-badge";
import { Progress } from "@multica/ui/components/ui/progress";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation/app-link";
import {
  AgentHonorLevelIcon,
  MAX_AGENT_HONOR_LEVEL,
} from "./agent-honor-level-icon";
import { useAgentFleetClassName } from "../hooks/use-agent-fleet-class-name";

function progressPercent(current: number, target: number) {
  if (target <= 0) return 100;
  return Math.max(0, Math.min(100, (current / target) * 100));
}

function agentLevelProgress(dashboard: AgentHonorDashboard) {
  if (dashboard.level >= MAX_AGENT_HONOR_LEVEL) return 100;

  const levelStart = dashboard.level <= 1 ? 0 : 25 * (dashboard.level - 1) ** 2;
  const levelEnd = 25 * dashboard.level ** 2;
  return progressPercent(dashboard.total_xp - levelStart, levelEnd - levelStart);
}

export function AgentHonorPanelSection({
  agentId,
  workspaceId,
  className,
}: {
  agentId: string;
  workspaceId: string;
  className?: string;
}) {
  const { t } = useT("agents");
  const fleetClassName = useAgentFleetClassName();
  const paths = useWorkspacePaths();
  const { data: honor, isPending, isError } = useQuery(
    agentHonorOptions(workspaceId, agentId),
  );
  const unlockedCount = honor?.achievements.filter((item) => item.unlocked).length ?? 0;
  const totalAchievements = honor?.achievements.length ?? 0;

  if (isError) return null;

  return (
    <section
      className={cn("border-t border-border pt-3", className)}
      data-testid="agent-honor-panel-section"
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {t(($) => $.side_panel.honor_title)}
        </h3>
        {!isPending && honor ? (
          <AppLink
            href={appendQueryParams(paths.agentDetail(agentId), { tab: "honor" })}
            className="inline-flex items-center gap-0.5 text-[11px] font-medium text-primary hover:underline"
            data-testid="agent-honor-view-all"
          >
            {t(($) => $.side_panel.honor_view_all)}
            <ChevronRight className="size-3" aria-hidden />
          </AppLink>
        ) : null}
      </div>

      {isPending || !honor ? (
        <div className="flex items-center gap-3 rounded-xl border border-border/70 bg-muted/20 p-3">
          <Skeleton className="size-16 shrink-0 rounded-xl" />
          <div className="min-w-0 flex-1 space-y-2">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-3 w-36" />
            <Skeleton className="h-1.5 w-full" />
          </div>
        </div>
      ) : (
        <div className="flex items-center gap-3 rounded-xl border border-border/70 bg-muted/20 p-3">
          <AgentHonorLevelIcon
            level={honor.level}
            title={t(($) => $.honor_agent.level_value, { level: honor.level })}
            className="size-16 shrink-0"
          />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-sm font-semibold tabular-nums text-foreground">
                {t(($) => $.honor_agent.level_value, { level: honor.level })}
              </span>
              <FleetRankBadge
                classLabel={fleetClassName(
                  honor.fleet.class_id,
                  honor.fleet.class_label,
                )}
                fleetRank={honor.fleet.fleet_rank}
                frozen={honor.fleet.frozen}
              />
            </div>
            <p className="mt-0.5 text-[11px] text-muted-foreground">
              {t(($) => $.side_panel.honor_stats, {
                xp: honor.total_xp,
                unlocked: unlockedCount,
                total: totalAchievements,
              })}
            </p>
            <div className="mt-2">
              <div className="mb-1 flex items-center justify-between gap-2 text-[10px] text-muted-foreground">
                <span>{t(($) => $.side_panel.honor_level_progress)}</span>
                <span className="shrink-0 font-mono tabular-nums">
                  {honor.xp_to_next_level > 0
                    ? t(($) => $.side_panel.honor_xp_to_next, {
                        xp: honor.xp_to_next_level,
                      })
                    : t(($) => $.honor_agent.max_level)}
                </span>
              </div>
              <Progress
                aria-label={t(($) => $.side_panel.honor_level_progress)}
                value={agentLevelProgress(honor)}
                className="[&_[data-slot=progress-indicator]]:bg-gradient-to-r [&_[data-slot=progress-indicator]]:from-primary [&_[data-slot=progress-indicator]]:to-chart-2 [&_[data-slot=progress-track]]:h-1.5"
              />
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
