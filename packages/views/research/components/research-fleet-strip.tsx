"use client";

import type { ResearchFleetMember } from "@multica/core/types";
import { ActorAvatar } from "../../common/actor-avatar";
import { AgentCompactActivity } from "../../channels/components/agent-compact-activity";
import { Badge } from "@multica/ui/components/ui/badge";
import { useT } from "../../i18n/use-t";

export function ResearchFleetStrip({ members }: { members: ResearchFleetMember[] }) {
  const { t } = useT("research");
  const active = members.filter((m) => m.status !== "archived");
  if (active.length === 0) return null;

  return (
    <div className="pointer-events-auto absolute left-4 top-4 z-10 flex max-w-[min(100%,420px)] flex-col gap-2 rounded-xl border bg-card/95 p-2 shadow-lg backdrop-blur">
      <div className="px-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
        {t(($) => $.panel.fleet)}
      </div>
      <div className="flex flex-col gap-1.5">
        {active.map((m) => (
          <div key={m.id} className="flex min-w-0 items-start gap-2 rounded-lg px-1 py-0.5 hover:bg-muted/50">
            <ActorAvatar
              actorType="agent"
              actorId={m.agent_id}
              size={28}
              enableHoverCard
              showStatusDot
              profileLink
            />
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-1.5">
                <span className="truncate text-xs font-medium">
                  {m.display_name || m.name || m.role}
                </span>
                {m.is_lead ? (
                  <Badge variant="secondary" className="h-4 px-1 text-[9px]">
                    {t(($) => $.panel.fleet_badge.lead)}
                  </Badge>
                ) : null}
                {m.status === "pending_prompt_review" ? (
                  <Badge variant="outline" className="h-4 px-1 text-[9px]">
                    {t(($) => $.panel.fleet_badge.pending)}
                  </Badge>
                ) : null}
              </div>
              <AgentCompactActivity agentId={m.agent_id} />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
