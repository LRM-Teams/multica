"use client";

import { useState } from "react";
import { ChevronRight } from "lucide-react";
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
 * LRM-783 / LRM-784: ~58px borderless dense row — status · title · stage·who·time · avatars.
 * LRM-906: colored goal chip → dialog stays outside the primary link.
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
  const output = session.handoff_summary?.trim()
    ? t(($) => $.list.output_line, { summary: session.handoff_summary.trim() })
    : t(($) => $.list.no_output);

  return (
    <>
      <div
        data-testid="research-session-row"
        className="group flex min-h-[58px] items-center gap-3 rounded-[10px] px-3 py-1.5 transition-colors hover:bg-accent/70"
      >
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
          <AppLink href={href} className="block min-w-0">
            <div className="truncate text-[13.5px] font-medium tracking-tight">
              {title}
            </div>
          </AppLink>

          <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5">
            <button
              type="button"
              className="inline-flex max-w-[min(100%,14rem)] items-center gap-1 truncate rounded-md border border-violet-500/25 bg-violet-500/10 px-1.5 py-px text-[11px] font-semibold text-violet-700 hover:bg-violet-500/15 dark:text-violet-300"
              onClick={() => setGoalOpen(true)}
            >
              <span
                aria-hidden
                className="size-1.5 shrink-0 rounded-full bg-violet-500"
              />
              <span className="truncate">
                {t(($) => $.list.goal_chip, { summary: goalSummary })}
              </span>
            </button>

            <AppLink
              href={href}
              className="flex min-w-0 flex-wrap items-center gap-1.5 text-[11.5px] text-muted-foreground"
            >
              <span className="font-medium tracking-wide text-foreground/80">
                {stageLabel}
              </span>
              {who ? (
                <span className="truncate">
                  <span aria-hidden className="text-muted-foreground/70">
                    ·{" "}
                  </span>
                  {t(($) => $.list.who_working, { name: who })}
                </span>
              ) : null}
              <span className="min-w-0 truncate">
                <span aria-hidden className="text-muted-foreground/70">
                  ·{" "}
                </span>
                {output}
              </span>
            </AppLink>
          </div>
        </div>

        <AgentAvatarStack
          agentIds={fleetIds}
          size={22}
          max={3}
          className="hidden shrink-0 sm:flex"
        />

        <div className="flex shrink-0 items-center gap-0.5">
          <span className="hidden text-xs whitespace-nowrap text-muted-foreground tabular-nums sm:inline">
            {timeAgo(session.updated_at)}
          </span>
          <ChevronRight className="size-4 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
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
              className="inline-flex h-8 items-center justify-center rounded-lg bg-primary px-2.5 text-sm font-medium text-primary-foreground"
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
