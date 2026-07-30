"use client";

import { ChevronRight } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { ResearchSession } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { useTimeAgo } from "../../i18n/use-time-ago";
import { AppLink } from "../../navigation/app-link";
import { AgentAvatarStack } from "../../agents/components/agent-avatar-stack";
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
}

/**
 * LRM-788 slice B: dense session row for the research homepage — semantic
 * status dot (running pulses), truncated title/goal, stage chip, fleet avatar
 * stack, relative time, and a hover chevron. The whole row is one link; the
 * ⋯ menu stays a sibling so its clicks never navigate.
 */
export function ResearchSessionRow({ session, href }: ResearchSessionRowProps) {
  const { t } = useT("research");
  const timeAgo = useTimeAgo();

  const status = session.status;
  const tone = STATUS_TONES[status] ?? DEFAULT_TONE;
  const statusLabel = t(($) => $.status[status as keyof typeof $.status] ?? status);
  const stageLabel = t(
    ($) => $.stage[session.current_stage as keyof typeof $.stage] ?? session.current_stage,
  );
  const fleetIds = (session.fleet_preview ?? []).map((m) => m.agent_id);

  return (
    <div className="group flex items-center gap-2 rounded-md border px-4 py-3 transition-colors hover:bg-accent/40">
      <AppLink href={href} className="flex min-w-0 flex-1 items-center gap-3">
        <span
          aria-hidden
          className={cn(
            "size-2 shrink-0 rounded-full",
            tone.dot,
            status === "running" && "animate-pulse",
          )}
        />
        <span className="sr-only">{statusLabel}</span>
        <div className="min-w-0 flex-1">
          <div className="truncate font-medium">{session.title || session.goal}</div>
          <div className="truncate text-xs text-muted-foreground">{session.goal}</div>
        </div>
        <span className="shrink-0 rounded-md border px-1.5 py-0.5 font-mono text-[10px] tracking-wider text-muted-foreground uppercase">
          {stageLabel}
        </span>
        <AgentAvatarStack agentIds={fleetIds} size={20} max={3} className="shrink-0" />
        <span className="shrink-0 text-xs whitespace-nowrap text-muted-foreground tabular-nums">
          {timeAgo(session.updated_at)}
        </span>
        <ChevronRight className="size-4 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
      </AppLink>
      <ResearchSessionRowActions session={session} />
    </div>
  );
}
