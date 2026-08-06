"use client";

import { Loader2, Users } from "lucide-react";
import { dedupeResearchFleetMembers } from "@multica/core/research";
import type { ResearchFleetMember } from "@multica/core/types";
import { ActorAvatar } from "../../common/actor-avatar";
import { AgentCompactActivity } from "../../channels/components/agent-compact-activity";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { resolveFleetStripMode } from "../lib/fleet-strip-mode";

const modeChip: Record<
  ReturnType<typeof resolveFleetStripMode>,
  string
> = {
  empty: "border-border/70 bg-muted/40 text-muted-foreground",
  loading: "border-brand/35 bg-brand/10 text-brand",
  running: "border-brand/35 bg-brand/10 text-brand",
  done: "border-success/35 bg-success/10 text-success",
};

export function ResearchFleetStrip({
  members,
  sessionStatus,
  loading,
  embedded = false,
}: {
  members: ResearchFleetMember[];
  sessionStatus?: string | null;
  loading?: boolean;
  /** When true, render as panel content (LRM-919) instead of a canvas float. */
  embedded?: boolean;
}) {
  const { t } = useT("research");
  const active = dedupeResearchFleetMembers(members).filter(
    (m) => m.status !== "archived",
  );
  const mode = resolveFleetStripMode(active.length, sessionStatus, loading);

  const shell = cn(
    "flex flex-col gap-2",
    !embedded &&
      "pointer-events-auto absolute left-4 top-4 z-10 max-w-[min(100%,420px)] rounded-xl border border-border/55 bg-card/95 p-2.5 shadow-lg backdrop-blur-sm",
    embedded && "gap-2.5",
  );

  const modeLabel =
    mode === "empty"
      ? t(($) => $.panel.fleet_mode.empty)
      : mode === "loading"
        ? t(($) => $.panel.fleet_mode.loading)
        : mode === "done"
          ? t(($) => $.panel.fleet_mode.done)
          : t(($) => $.panel.fleet_mode.running);

  return (
    <div data-testid="research-fleet-strip" data-fleet-mode={mode} className={shell}>
      <div className="flex flex-wrap items-center gap-1.5 px-0.5">
        {!embedded ? (
          <span className="text-[10px] font-semibold tracking-wide text-foreground uppercase">
            {t(($) => $.panel.fleet)}
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 text-[10px] font-semibold tracking-wide text-foreground uppercase">
            <Users className="size-3 text-muted-foreground" aria-hidden />
            {t(($) => $.panel.fleet)}
          </span>
        )}
        <span
          data-testid="research-fleet-strip-mode"
          className={cn(
            "rounded-md border px-1.5 py-0.5 text-[10px] font-medium",
            modeChip[mode],
          )}
        >
          {modeLabel}
        </span>
        {active.length > 0 ? (
          <span className="text-[10px] text-muted-foreground">
            {t(($) => $.panel.fleet_count, { count: active.length })}
          </span>
        ) : null}
      </div>

      {mode === "loading" ? (
        <div
          data-testid="research-fleet-strip-loading"
          className="space-y-2"
          aria-busy
          aria-live="polite"
        >
          <div className="flex items-center gap-2 px-0.5 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden />
            <span>{t(($) => $.panel.fleet_loading_body)}</span>
          </div>
          {[0, 1].map((i) => (
            <div
              key={i}
              className="animate-pulse rounded-xl border border-border/50 bg-card/70 p-2.5"
              style={{ animationDelay: `${i * 80}ms` }}
            >
              <div className="flex items-center gap-2">
                <div className="size-7 rounded-full bg-muted/70" />
                <div className="min-w-0 flex-1 space-y-1.5">
                  <div className="h-2.5 w-[45%] rounded bg-muted/70" />
                  <div className="h-2 w-[70%] rounded bg-muted/45" />
                </div>
              </div>
            </div>
          ))}
        </div>
      ) : null}

      {mode === "empty" ? (
        <div
          data-testid="research-fleet-strip-empty"
          className="rounded-xl border border-border/55 bg-card/80 px-3 py-3"
        >
          <p className="text-sm font-medium text-foreground">
            {t(($) => $.panel.fleet_empty_title)}
          </p>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.panel.fleet_empty_body)}
          </p>
        </div>
      ) : null}

      {mode === "running" || mode === "done" ? (
        <ul
          data-testid="research-fleet-strip-cards"
          className="flex flex-col gap-1.5"
        >
          {active.map((m) => (
            <li
              key={m.id}
              data-testid="research-fleet-member-card"
              className={cn(
                "flex min-w-0 items-start gap-2.5 rounded-xl border bg-card/90 px-2.5 py-2 shadow-sm backdrop-blur-sm",
                mode === "done"
                  ? "border-success/30"
                  : "border-border/55 hover:border-border",
              )}
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
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="truncate text-xs font-semibold text-foreground">
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
                  {mode === "done" ? (
                    <Badge
                      variant="outline"
                      className="h-4 border-success/40 px-1 text-[9px] text-success"
                    >
                      {t(($) => $.panel.fleet_badge.done)}
                    </Badge>
                  ) : null}
                </div>
                {mode === "running" ? (
                  <AgentCompactActivity agentId={m.agent_id} />
                ) : (
                  <p className="mt-0.5 text-[10px] text-muted-foreground">
                    {t(($) => $.panel.fleet_done_hint)}
                  </p>
                )}
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
