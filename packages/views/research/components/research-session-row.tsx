"use client";

import { cn } from "@multica/ui/lib/utils";
import type { ResearchSession } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { useTimeAgo } from "../../i18n/use-time-ago";
import { AppLink } from "../../navigation/app-link";
import { AgentAvatarStack } from "../../agents/components/agent-avatar-stack";
import { sessionShortTitle } from "../lib/session-list-filter";
import { ResearchSessionRowActions } from "./research-session-row-actions";

type StatusTone = { text: string; dot: string };

// Same semantic tones as the session chrome (design lock LRM-792 / LRM-784):
// running = brand, awaiting = warning, completed = success, else muted.
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
 * LRM-785 / LRM-784 — dense ~58px session row:
 * status dot · title · stage·time meta · fleet avatars · kebab.
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
  const title = sessionShortTitle(session);
  const archived = status === "archived";
  const awaiting = status === "awaiting_user_confirm";

  return (
    <div
      className={cn(
        "group flex h-[58px] items-center gap-3 rounded-[10px] px-3 transition-colors hover:bg-accent/40",
        archived && "opacity-55",
      )}
      data-testid="research-session-row"
    >
      <AppLink
        href={href}
        className="flex min-w-0 flex-1 items-center gap-3 self-stretch"
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
          <div className="truncate text-[13.5px] font-medium tracking-tight">
            {title}
          </div>
          <div className="mt-0.5 flex min-w-0 items-center gap-1.5 truncate text-xs text-muted-foreground">
            {awaiting ? (
              <span className="shrink-0 font-semibold text-warning">{statusLabel}</span>
            ) : null}
            {awaiting ? <span className="text-muted-foreground/50">·</span> : null}
            <span className="truncate">{stageLabel}</span>
            <span className="text-muted-foreground/50">·</span>
            <span className="shrink-0 tabular-nums">{timeAgo(session.updated_at)}</span>
          </div>
        </div>

        <AgentAvatarStack
          agentIds={fleetIds}
          size={22}
          max={3}
          className="hidden shrink-0 sm:flex"
        />
      </AppLink>

      <div className="shrink-0 opacity-100 sm:opacity-0 sm:transition-opacity sm:group-hover:opacity-100">
        <ResearchSessionRowActions session={session} />
      </div>
    </div>
  );
}
