"use client";

import { useState } from "react";
import { ChevronDown, ChevronUp, Loader2 } from "lucide-react";
import { dedupeResearchFleetMembers } from "@multica/core/research";
import type { ResearchFleetMember } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n/use-t";
import { resolveFleetStripMode } from "../lib/fleet-strip-mode";

/**
 * LRM-797 / LRM-980 narrow: collapsible fleet avatar stack with four-state chrome.
 */
export function ResearchFleetAvatarStack({
  members,
  sessionStatus,
  className,
}: {
  members: ResearchFleetMember[];
  sessionStatus?: string | null;
  className?: string;
}) {
  const { t } = useT("research");
  const [open, setOpen] = useState(false);
  const active = dedupeResearchFleetMembers(members).filter((m) => m.status !== "archived");
  const mode = resolveFleetStripMode(active.length, sessionStatus);

  if (mode === "empty") {
    return (
      <div
        className={cn("pointer-events-auto", className)}
        data-testid="research-fleet-avatar-stack"
        data-fleet-mode="empty"
      >
        <div className="rounded-xl border border-border/55 bg-card/95 px-2.5 py-1.5 text-[11px] text-muted-foreground shadow-md backdrop-blur-sm">
          {t(($) => $.panel.fleet_mode.empty)}
        </div>
      </div>
    );
  }

  if (mode === "loading") {
    return (
      <div
        className={cn("pointer-events-auto", className)}
        data-testid="research-fleet-avatar-stack"
        data-fleet-mode="loading"
        aria-busy
      >
        <div className="flex items-center gap-1.5 rounded-xl border border-brand/30 bg-card/95 px-2.5 py-1.5 text-[11px] text-brand shadow-md backdrop-blur-sm">
          <Loader2 className="size-3.5 animate-spin" aria-hidden />
          {t(($) => $.panel.fleet_mode.loading)}
        </div>
      </div>
    );
  }

  const visible = active.slice(0, 3);
  const extra = Math.max(0, active.length - 3);

  return (
    <div
      className={cn(
        "pointer-events-auto flex flex-col items-end gap-1",
        className,
      )}
      data-testid="research-fleet-avatar-stack"
      data-fleet-mode={mode}
    >
      {open ? (
        <div className="max-h-48 w-[220px] overflow-y-auto rounded-xl border border-border/55 bg-card/95 p-2 shadow-lg backdrop-blur-sm">
          <div className="mb-1.5 flex items-center gap-1.5 px-1">
            <span className="text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.panel.fleet)}
            </span>
            <span
              className={cn(
                "rounded-md border px-1.5 py-0.5 text-[10px] font-medium",
                mode === "done"
                  ? "border-success/35 bg-success/10 text-success-strong"
                  : "border-brand/35 bg-brand/10 text-brand",
              )}
            >
              {mode === "done"
                ? t(($) => $.panel.fleet_mode.done)
                : t(($) => $.panel.fleet_mode.running)}
            </span>
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
      <div
        className={cn(
          "flex items-center rounded-full border bg-card/95 py-1 pr-2 pl-1 shadow-md backdrop-blur-sm",
          mode === "done" ? "border-success/35" : "border-border/55",
        )}
      >
        <span className="flex -space-x-2">
          {visible.map((m) => (
            <span key={m.id} className="ring-2 ring-card rounded-full">
              <ActorAvatar
                actorType="agent"
                actorId={m.agent_id}
                size={28}
                showStatusDot
                enableHoverCard
                profileLink
              />
            </span>
          ))}
        </span>
        {!open && extra > 0 ? (
          <span className="ml-1.5 text-[11px] font-semibold text-muted-foreground">+{extra}</span>
        ) : null}
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="ml-1.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={open}
          aria-label={open ? t(($) => $.overlay.fleet_collapse) : t(($) => $.overlay.fleet_expand)}
          data-testid="research-fleet-avatar-stack-toggle"
        >
          {open ? (
            <ChevronUp className="size-3.5" aria-hidden />
          ) : (
            <ChevronDown className="size-3.5" aria-hidden />
          )}
        </button>
      </div>
    </div>
  );
}
