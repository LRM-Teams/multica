"use client";

import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Agent } from "@multica/core/types";
import type {
  AgentFleetClassChangedPayload,
  AgentHonorLevelChangedPayload,
  AgentHonorUnlockedPayload,
} from "@multica/core/types/events";
import { agentDetailOptions, agentHonorKeys } from "@multica/core/agents";
import { getCurrentWsId } from "@multica/core/platform";
import { useWSEvent } from "@multica/core/realtime";
import { workspaceKeys } from "@multica/core/workspace/queries";
import {
  HonorUnlockToast,
  honorUnlockToastOptions,
} from "../../honor/honor-unlock-toast";
import { useT } from "../../i18n";
import { resolveActorDisplayName } from "@multica/core/identity";
import { AgentHonorAchievementIcon } from "./agent-honor-achievement-icon";
import { useAgentAchievementCopy } from "../hooks/use-agent-achievement-copy";
import { useAgentFleetClassName } from "../hooks/use-agent-fleet-class-name";

export function AgentHonorUnlockListener() {
  const queryClient = useQueryClient();
  const { t } = useT("agents");
  const achievementCopy = useAgentAchievementCopy();
  const fleetClassName = useAgentFleetClassName();

  const withAgentName = (
    workspaceId: string,
    agentId: string,
    eventName: string | undefined,
    callback: (agentName: string) => void,
  ) => {
    const cachedAgent = queryClient
      .getQueryData<Agent[]>(workspaceKeys.agents(workspaceId))
      ?.find((agent) => agent.id === agentId);
    const agentName = eventName?.trim() || resolveAgentName(cachedAgent);
    if (agentName) {
      callback(agentName);
      return;
    }
    void queryClient
      .ensureQueryData(agentDetailOptions(workspaceId, agentId))
      .then((agent) => {
        const resolvedName = resolveAgentName(agent);
        if (resolvedName) callback(resolvedName);
      })
      .catch(() => undefined);
  };

  useWSEvent("agent_honor:achievement_unlocked", (payload: unknown) => {
    const event = payload as AgentHonorUnlockedPayload;
    const achievement = achievementCopy(event.achievement);
    const workspaceId = getCurrentWsId();
    if (workspaceId) {
      void queryClient.invalidateQueries({
        queryKey: agentHonorKeys.dashboard(workspaceId, event.agent_id),
      });
    }
    if (!workspaceId) return;
    withAgentName(workspaceId, event.agent_id, event.agent_name, (agentName) => {
      toast.custom(
        (toastId) => (
          <HonorUnlockToast
            eyebrow={t(($) => $.honor_agent.unlock_toast_title)}
            title={t(($) => $.honor_agent.achievement_unlocked, {
              agentName,
              achievementName: achievement.title,
            })}
            meta={t(($) => $.honor_agent.xp_value, {
              value: `+${event.achievement.xp_reward}`,
            })}
            svgKey={event.achievement.svg_key}
            icon={
              <AgentHonorAchievementIcon
                rarity={event.achievement.rarity}
                title={achievement.title}
                featured
                className="size-9"
              />
            }
            rare={event.achievement.rarity >= 75}
            dismissLabel={t(($) => $.honor_agent.dismiss_unlock)}
            onDismiss={() => toast.dismiss(toastId)}
          />
        ),
        honorUnlockToastOptions,
      );
    });
  });

  useWSEvent("agent_honor:level_changed", (payload: unknown) => {
    const event = payload as AgentHonorLevelChangedPayload;
    const workspaceId = getCurrentWsId();
    if (!workspaceId) return;

    void queryClient.invalidateQueries({
      queryKey: agentHonorKeys.dashboard(workspaceId, event.agent_id),
    });
    void queryClient.invalidateQueries({
      queryKey: workspaceKeys.agents(workspaceId),
    });
    void queryClient.invalidateQueries({
      queryKey: agentDetailOptions(workspaceId, event.agent_id).queryKey,
    });

    if (event.level <= event.previous_level) return;
    withAgentName(workspaceId, event.agent_id, event.agent_name, (agentName) => {
      toast.success(
        t(($) => $.honor_agent.level_promoted, {
          agentName,
          level: event.level,
        }),
      );
    });
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
    withAgentName(workspaceId, event.agent_id, event.agent_name, (agentName) => {
      toast.success(
        t(($) => $.honor_agent.fleet_upgraded, {
          agentName,
          className: fleetClassName(event.class_id),
        }),
      );
    });
  });

  return null;
}

function resolveAgentName(agent?: Agent): string {
  return agent ? resolveActorDisplayName(agent, agent.name).trim() : "";
}
