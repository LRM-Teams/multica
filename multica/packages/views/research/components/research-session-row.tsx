"use client";

import { useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import type { ResearchSession } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n/use-t";
import { useTimeAgo } from "../../i18n/use-time-ago";
import { AppLink } from "../../navigation/app-link";
import { AgentAvatarStack } from "../../agents/components/agent-avatar-stack";
import {
  sessionGoalSummary,
  sessionShortTitle,
} from "../lib/session-list-filter";
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

function leadName(session: ResearchSession): string | null {
  const preview = session.fleet_preview ?? [];
  const lead = preview.find((m) => m.is_lead) ?? preview[0];
  if (!lead) return null;
  const name = (lead.display_name || lead.name || "").trim();
  return name || null;
}

/**
 * LRM-783 dense row + LRM-790 narrow/dark:
 * status · title · stage·time (avatars/time column yield <sm; stage kept).
 * Goal chip uses brand tokens (no hard-coded violet).
 */
export function ResearchSessionRow({ session, href }: ResearchSessionRowProps) {
  const { t } = useT("research");
  const timeAgo = useTimeAgo();
  const [goalOpen, setGoalOpen] = useState(false);

  const status = session.status;
  const tone = STATUS_TONES[status] ?? DEFAULT_TONE;
  const statusLabel = t(($) => $.status[status as keyof typeof $.status] ?? status);
  const stageLabel = t(
    ($) => $.stage[session.current_stage as keyof typeof $.stage] ?? session.current_stage,
  );
  const fleetIds = (session.fleet_preview ?? []).map((m) => m.agent_id);
  const title = sessionShortTitle(session);
  const goalSummary = sessionGoalSummary(session);
  const who = leadName(session);
  const archived = status === "archived";
  const awaiting = status === "awaiting_user_confirm";
  const relative = timeAgo(session.updated_at);

  return (
    <>
      <div
        data-testid="research-session-row"
        className={cn(
          "group flex min-h-[58px] items-center gap-3 rounded-[10px] px-3 py-1.5 transition-colors hover:bg-accent/70",
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
          <AppLink href={href} className="block min-w-0">
            <div className="truncate text-[13.5px] font-medium tracking-tight">
              {title}
            </div>
          </AppLink>

          <div className="mt-0.5 flex min-w-0 items-center gap-1.5">
            {/* Desktop-only goal chip — yields on narrow so stage·time stay scannable. */}
            <button
              type="button"
              data-testid="research-session-goal-chip"
              className="hidden max-w-[min(100%,14rem)] items-center gap-1 truncate rounded-md border border-brand/25 bg-brand/10 px-1.5 py-px text-[11px] font-semibold text-brand hover:bg-brand/15 sm:inline-flex"
              onClick={() => setGoalOpen(true)}
            >
              <span
                aria-hidden
                className="size-1.5 shrink-0 rounded-full bg-brand"
              />
              <span className="truncate">
                {t(($) => $.list.goal_chip, { summary: goalSummary })}
              </span>
            </button>

            <AppLink
              href={href}
              className="flex min-w-0 items-center gap-1.5 truncate text-[11.5px] text-muted-foreground"
            >
              {awaiting ? (
                <span className="shrink-0 font-semibold text-warning">{statusLabel}</span>
              ) : null}
              {awaiting ? (
                <span aria-hidden className="text-muted-foreground/50">
                  ·
                </span>
              ) : null}
              <span className="shrink-0 font-medium tracking-wide text-foreground/80">
                {stageLabel}
              </span>
              <span aria-hidden className="text-muted-foreground/50">
                ·
              </span>
              <span className="shrink-0 tabular-nums">{relative}</span>
              {who ? (
                <span className="hidden min-w-0 truncate sm:inline">
                  <span aria-hidden className="text-muted-foreground/50">
                    {" "}
                    ·{" "}
                  </span>
                  {t(($) => $.list.who_working, { name: who })}
                </span>
              ) : null}
            </AppLink>
          </div>
        </div>

        {/* LRM-790: avatar pile yields below sm. */}
        <AgentAvatarStack
          agentIds={fleetIds}
          size={22}
          max={3}
          className="hidden shrink-0 sm:flex"
        />

        <div className="shrink-0 opacity-100 sm:opacity-0 sm:transition-opacity sm:group-hover:opacity-100">
          <ResearchSessionRowActions session={session} />
        </div>
      </div>

      <Dialog open={goalOpen} onOpenChange={setGoalOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.list.goal_dialog_title)}</DialogTitle>
          </DialogHeader>
          <p className="max-h-[50vh] overflow-y-auto whitespace-pre-wrap text-sm leading-relaxed text-muted-foreground">
            {session.goal}
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setGoalOpen(false)}>
              {t(($) => $.list.goal_dialog_close)}
            </Button>
            <AppLink
              href={href}
              className="inline-flex h-8 items-center justify-center rounded-lg bg-brand px-2.5 text-sm font-medium text-brand-foreground"
              onClick={() => setGoalOpen(false)}
            >
              {t(($) => $.list.goal_dialog_open)}
            </AppLink>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
