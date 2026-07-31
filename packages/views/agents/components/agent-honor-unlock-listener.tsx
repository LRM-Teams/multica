"use client";

import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type {
  AgentFleetClassChangedPayload,
  AgentHonorUnlockedPayload,
} from "@multica/core/types/events";
import { agentHonorKeys } from "@multica/core/agents";
import { getCurrentWsId } from "@multica/core/platform";
import { useWSEvent } from "@multica/core/realtime";
import { workspaceKeys } from "@multica/core/workspace/queries";
import {
  HonorUnlockToast,
  honorUnlockToastOptions,
} from "../../honor/honor-unlock-toast";
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
        <HonorUnlockToast
          eyebrow={t(($) => $.honor_agent.unlock_toast_title)}
          title={event.achievement.title}
          meta={`+${event.achievement.xp_reward} XP`}
          svgKey={event.achievement.svg_key}
          rare={event.achievement.rarity >= 75}
          dismissLabel={t(($) => $.honor_agent.dismiss_unlock)}
          onDismiss={() => toast.dismiss(toastId)}
        />
      ),
      honorUnlockToastOptions,
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
