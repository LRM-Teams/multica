"use client";

import { useState } from "react";
import { dedupeResearchFleetMembers } from "@multica/core/research";
import type { ResearchFleetMember } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n/use-t";

/**
 * LRM-797 narrow: collapsible fleet avatar stack (not a permanent canvas float roster).
 */
export function ResearchFleetAvatarStack({
  members,
  className,
}: {
  members: ResearchFleetMember[];
  className?: string;
}) {
  const { t } = useT("research");
  const [open, setOpen] = useState(false);
  const active = dedupeResearchFleetMembers(members).filter((m) => m.status !== "archived");
  if (active.length === 0) return null;

  const visible = open ? active : active.slice(0, 3);
  const extra = Math.max(0, active.length - 3);

  return (
    <div
      className={cn(
        "pointer-events-auto flex flex-col items-end gap-1",
        className,
      )}
      data-testid="research-fleet-avatar-stack"
    >
      {open ? (
        <div className="max-h-48 w-[200px] overflow-y-auto rounded-xl border bg-card/95 p-2 shadow-lg backdrop-blur">
          <div className="mb-1.5 px-1 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
            {t(($) => $.panel.fleet)}
          </div>
          <ul className="space-y-1.5">
            {active.map((m) => (
              <li key={m.id} className="flex items-center gap-2 px-0.5">
                <ActorAvatar
                  actorType="agent"
                  actorId={m.agent_id}
                  size={24}
                  enableHoverCard
                  showStatusDot
                  profileLink
                />
                <span className="truncate text-[11px] font-medium">
                  {m.display_name || m.name || m.role}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center rounded-full border bg-card/95 py-1 pr-2 pl-1 shadow-md backdrop-blur"
        aria-expanded={open}
        aria-label={open ? t(($) => $.overlay.fleet_collapse) : t(($) => $.overlay.fleet_expand)}
        data-testid="research-fleet-avatar-stack-toggle"
      >
        <span className="flex -space-x-2">
          {visible.map((m) => (
            <span key={m.id} className="ring-2 ring-card rounded-full">
              <ActorAvatar
                actorType="agent"
                actorId={m.agent_id}
                size={28}
                showStatusDot
              />
            </span>
          ))}
        </span>
        {!open && extra > 0 ? (
          <span className="ml-1.5 text-[11px] font-semibold text-muted-foreground">+{extra}</span>
        ) : null}
      </button>
    </div>
  );
}
