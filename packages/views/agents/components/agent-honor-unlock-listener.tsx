"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Award, Sparkles, X } from "lucide-react";
import { toast } from "sonner";
import type {
  AgentFleetClassChangedPayload,
  AgentHonorUnlockedPayload,
} from "@multica/core/types/events";
import { agentHonorKeys } from "@multica/core/agents";
import { getCurrentWsId } from "@multica/core/platform";
import { useWSEvent } from "@multica/core/realtime";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { HonorBadgeCrest } from "@multica/ui/components/honor/honor-badge";
import { useT } from "../../i18n";

export function AgentHonorUnlockListener() {
  const queryClient = useQueryClient();
  const { t } = useT("agents");

  useWSEvent("agent_honor:achievement_unlocked", (payload: unknown) => {
    const event = payload as AgentHonorUnlockedPayload;
    const workspaceId = getCurrentWsId();
    if (workspaceId) {
      void queryClient.invalidateQueries({
        queryKey: agentHonorKeys.dashboard(workspaceId, event.agent_id),
      });
    }
    toast.custom(
      (toastId) => (
        <output className="relative w-[min(420px,calc(100vw-2rem))] overflow-hidden rounded-2xl border border-violet-300/25 bg-[#030817] p-4 text-white shadow-[0_24px_80px_-26px_rgba(99,102,241,0.8)]">
          <div
            aria-hidden="true"
            className="absolute inset-0 bg-[radial-gradient(circle_at_10%_0%,rgba(34,211,238,0.2),transparent_42%),radial-gradient(circle_at_100%_100%,rgba(168,85,247,0.24),transparent_50%)]"
          />
          <button
            type="button"
            aria-label={t(($) => $.honor_agent.dismiss_unlock)}
            onClick={() => toast.dismiss(toastId)}
            className="absolute right-3 top-3 z-10 grid size-7 place-items-center rounded-full text-slate-400 hover:bg-white/10 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300"
          >
            <X className="size-3.5" />
          </button>
          <div className="relative flex items-center gap-4 pr-7">
            <HonorBadgeCrest
              svgKey={event.achievement.svg_key}
              title={event.achievement.title}
              rare={event.achievement.rarity >= 75}
              animated
              className="size-[4.5rem] motion-safe:animate-[honor-unlock-in_700ms_cubic-bezier(0.16,1,0.3,1)]"
            />
            <div className="min-w-0 flex-1">
              <p className="flex items-center gap-1.5 text-[10px] uppercase tracking-[0.18em] text-cyan-300">
                <Sparkles className="size-3" />
                {t(($) => $.honor_agent.unlock_toast_title)}
              </p>
              <p className="mt-1 truncate text-base font-semibold">
                {event.achievement.title}
              </p>
              <p className="mt-1 flex items-center gap-1 text-xs text-slate-300">
                <Award className="size-3" />+{event.achievement.xp_reward} XP
              </p>
            </div>
          </div>
        </output>
      ),
      { duration: 8000 },
    );
  });

  useWSEvent("agent_honor:fleet_class_changed", (payload: unknown) => {
    const event = payload as AgentFleetClassChangedPayload;
    const workspaceId = getCurrentWsId();
    if (!workspaceId) return;
    void queryClient.invalidateQueries({
      queryKey: agentHonorKeys.dashboard(workspaceId, event.agent_id),
    });
    void queryClient.invalidateQueries({
      queryKey: [...workspaceKeys.agents(workspaceId), "fleet-rankings"],
    });
    toast.success(
      t(($) => $.honor_agent.fleet_upgraded, {
        className: event.class_id,
      }),
    );
  });

  return null;
}
