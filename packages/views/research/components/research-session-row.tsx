"use client";

import { cn } from "@multica/ui/lib/utils";
import type { ResearchSession } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { Time } from "../../i18n/time";
import { AppLink } from "../../navigation/app-link";
import { AgentAvatarStack } from "../../agents/components/agent-avatar-stack";
import { sessionDisplayTitle } from "../lib/session-list-filter";
import { ResearchSessionRowActions } from "./research-session-row-actions";

type StatusTone = { text: string; dot: string };

// Same semantic tones as the session chrome (design lock LRM-792): running =
// brand, awaiting = warning, completed = success, everything else muted.
const STATUS_TONES: Record<string, StatusTone> = {
  running: { text: "text-brand", dot: "bg-brand" },
  awaiting_user_confirm: { text: "text-warning", dot: "bg-warning" },
  completed: { text: "text-success", dot: "bg-success" },
};
const DEFAULT_TONE: StatusTone = {
  text: "text-muted-foreground",
  dot: "bg-muted-foreground",
};

interface ResearchSessionRowProps {
  session: ResearchSession;
  href: string;
  /** Persist list filter state before navigating to detail (D-IX). */
  onNavigate?: () => void;
}

function leadName(session: ResearchSession): string | null {
  const preview = session.fleet_preview ?? [];
  const lead = preview.find((m) => m.is_lead) ?? preview[0];
  if (!lead) return null;
  const name = (lead.display_name || lead.name || "").trim();
  return name || null;
}

/**
 * LRM-1106 row — status · title+meta · stage · time · people · ⋯
 * No inline goal chip (D2 → ⋯「查看目标」). Time uses `<Time kind="list">` (D3).
 * Breakpoint: md / 768 only.
 */
export function ResearchSessionRow({
  session,
  href,
  onNavigate,
}: ResearchSessionRowProps) {
  const { t } = useT("research");

  const status = session.status;
  const tone = STATUS_TONES[status] ?? DEFAULT_TONE;
  const statusLabel = t(($) => $.status[status as keyof typeof $.status] ?? status);
  const stageLabel = t(
    ($) => $.stage[session.current_stage as keyof typeof $.stage] ?? session.current_stage,
  );
  const fleetIds = (session.fleet_preview ?? []).map((m) => m.agent_id);
  const title = sessionDisplayTitle(session);
  const who = leadName(session);
  const archived = status === "archived";
  const awaiting = status === "awaiting_user_confirm";

  return (
    <div
      data-testid="research-session-row"
      data-session-id={session.id}
      className={cn(
        "group relative flex min-h-[58px] items-center gap-3 rounded-[10px] px-3 py-1.5 transition-colors",
        "hover:bg-accent/70 focus-within:bg-accent/70",
        "has-[a:focus-visible]:ring-2 has-[a:focus-visible]:ring-ring has-[a:focus-visible]:ring-offset-1",
        archived && "opacity-55",
      )}
    >
      <span
        aria-hidden
        className={cn(
          "size-2 shrink-0 rounded-full",
          tone.dot,
          status === "running" && "motion-safe:animate-pulse",
        )}
      />
      <span className="sr-only">{statusLabel}</span>

      <div className="min-w-0 flex-1">
        {/* Single primary tab stop — stretched hit target for the whole row. */}
        <AppLink
          href={href}
          onClick={onNavigate}
          className={cn(
            "relative block min-w-0 rounded-sm outline-none",
            "after:absolute after:inset-y-[-6px] after:inset-x-[-8px] after:-z-0 after:content-['']",
          )}
        >
          <div
            className={cn(
              "relative z-[1] text-sm font-medium tracking-tight text-foreground",
              "line-clamp-2 md:truncate md:whitespace-nowrap",
            )}
          >
            {title}
          </div>
          <div className="relative z-[1] mt-0.5 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
            {awaiting ? (
              <span className="shrink-0 font-medium text-warning">{statusLabel}</span>
            ) : null}
            {awaiting && who ? (
              <span aria-hidden className="text-muted-foreground/50">
                ·
              </span>
            ) : null}
            {who ? (
              <span className="min-w-0 truncate">
                {t(($) => $.list.who_working, { name: who })}
              </span>
            ) : null}
            {/* Narrow: fold stage · time into the secondary line. */}
            <span className="inline-flex min-w-0 items-center gap-1.5 md:hidden">
              {(awaiting || who) && stageLabel ? (
                <span aria-hidden className="text-muted-foreground/50">
                  ·
                </span>
              ) : null}
              <span className="shrink-0 font-medium text-foreground/80">{stageLabel}</span>
              <span aria-hidden className="text-muted-foreground/50">
                ·
              </span>
              <Time kind="list" value={session.updated_at} className="shrink-0 tabular-nums" />
            </span>
          </div>
        </AppLink>
      </div>

      {/* Desktop columns: stage | time | people */}
      <span
        className="hidden shrink-0 text-xs font-medium text-foreground/80 md:inline"
        aria-hidden
      >
        {stageLabel}
      </span>
      <span className="hidden shrink-0 text-xs tabular-nums text-muted-foreground md:inline" aria-hidden>
        <Time kind="list" value={session.updated_at} />
      </span>

      <AgentAvatarStack
        agentIds={fleetIds}
        size={22}
        max={3}
        className="hidden shrink-0 md:flex"
      />

      {/* Visible on hover AND focus-within (keyboard). */}
      <div
        className={cn(
          "relative z-[1] shrink-0",
          "opacity-100 md:opacity-0 md:transition-opacity",
          "md:group-hover:opacity-100 md:group-focus-within:opacity-100",
        )}
      >
        <ResearchSessionRowActions session={session} href={href} />
      </div>
    </div>
  );
}
