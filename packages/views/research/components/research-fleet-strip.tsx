"use client";

import { dedupeResearchFleetMembers } from "@multica/core/research";
import type { ResearchFleetMember } from "@multica/core/types";
import { ActorAvatar } from "../../common/actor-avatar";
import { AgentCompactActivity } from "../../channels/components/agent-compact-activity";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

export function ResearchFleetStrip({
  members,
  embedded = false,
}: {
  members: ResearchFleetMember[];
  /** When true, render as panel content (LRM-919) instead of a canvas float. */
  embedded?: boolean;
}) {
  const { t } = useT("research");
  const active = dedupeResearchFleetMembers(members);
  if (active.length === 0) {
    return embedded ? (
      <p className="px-1 text-xs text-muted-foreground">{t(($) => $.panel.fleet_empty)}</p>
    ) : null;
  }

  return (
    <div
      data-testid="research-fleet-strip"
      className={cn(
        "flex flex-col gap-2",
        !embedded &&
          "pointer-events-auto absolute left-4 top-4 z-10 max-w-[min(100%,420px)] rounded-xl border bg-card/95 p-2 shadow-lg backdrop-blur",
      )}
    >
      {!embedded ? (
        <div className="px-1 text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          {t(($) => $.panel.fleet)}
        </div>
      ) : null}
      <div className="flex flex-col gap-1.5">
        {active.map((m) => (
          <div
            key={m.id}
            className="flex min-w-0 items-start gap-2 rounded-lg px-1 py-0.5 hover:bg-muted/50"
          >
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
