"use client";

import { useMemo } from "react";
import { useT } from "../../i18n";
import { useAgentAchievementCopy } from "./use-agent-achievement-copy";

type AgentHonorEventCopySource = {
  event_type: string;
  source_ref: string;
  reason: string;
};

export function useAgentHonorCopy() {
  const { t } = useT("agents");
  const achievementCopy = useAgentAchievementCopy();

  return useMemo(
    () => ({
      metricName(metric: string): string {
        switch (metric) {
          case "completed":
            return t(($) => $.honor_agent.metric_names.completed);
          case "success_streak":
            return t(($) => $.honor_agent.metric_names.success_streak);
          case "memory_writes":
            return t(($) => $.honor_agent.metric_names.memory_writes);
          case "evolution_promotions":
            return t(($) => $.honor_agent.metric_names.evolution_promotions);
          case "distinct_projects":
            return t(($) => $.honor_agent.metric_names.distinct_projects);
          case "recoveries":
            return t(($) => $.honor_agent.metric_names.recoveries);
          case "fleet_class":
            return t(($) => $.honor_agent.metric_names.fleet_class);
          default:
            return metric;
        }
      },
      auditActionName(action: string): string {
        switch (action) {
          case "rules.update":
            return t(($) => $.honor_agent.audit_actions.rules_update);
          case "xp.grant":
            return t(($) => $.honor_agent.audit_actions.xp_grant);
          case "achievement.grant":
            return t(($) => $.honor_agent.audit_actions.achievement_grant);
          case "achievement.revoke":
            return t(($) => $.honor_agent.audit_actions.achievement_revoke);
          default:
            return action;
        }
      },
      eventReason(event: AgentHonorEventCopySource): string {
        if (event.event_type === "delivery") {
          return t(($) => $.honor_agent.accepted_delivery);
        }
        if (event.event_type === "achievement") {
          return achievementCopy({
            id: event.source_ref,
            title: event.reason,
            unlocked: true,
          }).title;
        }
        return event.reason;
      },
    }),
    [achievementCopy, t],
  );
}
