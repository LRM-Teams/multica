"use client";

import type { ResearchFleetMember, ResearchMessage, ResearchRunTask } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n/use-t";

type Props = {
  mode?: "forming" | "stalled";
  stage?: string;
  members?: ResearchFleetMember[];
  tasks?: ResearchRunTask[];
  messages?: ResearchMessage[];
};

const QUEUED_STATUSES = new Set(["pending", "ready", "queued", "dispatched"]);

const EMPTY_MEMBERS: ResearchFleetMember[] = [];
const EMPTY_TASKS: ResearchRunTask[] = [];
const EMPTY_MESSAGES: ResearchMessage[] = [];

export function ResearchCanvasForming({
  mode = "forming",
  stage,
  members = EMPTY_MEMBERS,
  tasks = EMPTY_TASKS,
  messages = EMPTY_MESSAGES,
}: Props) {
  const { t } = useT("research");
  const running = tasks.filter((task) => task.status === "running");
  const queued = tasks.filter((task) => QUEUED_STATUSES.has(task.status)).length;
  const failed = tasks.filter((task) => task.status === "failed").length;
  const workingIds = new Set(running.flatMap((task) => task.assigned_agent_id ? [task.assigned_agent_id] : []));
  const working = members.filter((member) => workingIds.has(member.agent_id));
  const latest = messages.reduce<ResearchMessage | undefined>((best, msg) => {
    if (!msg.body?.trim()) return best;
    if (!best || msg.created_at.localeCompare(best.created_at) > 0) return msg;
    return best;
  }, undefined);
  const stageLabel = stage
    ? t(($) => $.stage[stage as keyof typeof $.stage] ?? stage)
    : t(($) => $.session_page.canvas_forming_title);

  return (
    <div
      data-testid="research-session-canvas-forming"
      data-forming-mode={mode}
      className="absolute inset-0 z-[5] flex flex-col items-center justify-center gap-4 bg-canvas-bg/88 px-6 py-8 transition-opacity duration-300 motion-reduce:transition-none"
      aria-busy={mode === "forming"}
      aria-live="polite"
    >
      <div className="relative w-full max-w-lg rounded-xl border border-border/50 p-5">
        <div className="flex flex-wrap gap-3" aria-hidden>
          <Skeleton className="h-[72px] w-[160px] rounded-xl" />
          <Skeleton className="mt-6 h-[72px] w-[160px] rounded-xl opacity-80" />
          <Skeleton className="h-[72px] w-[140px] rounded-xl opacity-60" />
        </div>
        <dl className="mt-5 grid grid-cols-3 gap-2 text-center text-xs">
          <div><dt className="text-muted-foreground">{t(($) => $.step_card.status.waiting)}</dt><dd className="font-semibold">{queued}</dd></div>
          <div><dt className="text-muted-foreground">{t(($) => $.step_card.status.running)}</dt><dd className="font-semibold">{running.length}</dd></div>
          <div><dt className="text-muted-foreground">{t(($) => $.step_card.status.failed)}</dt><dd className="font-semibold">{failed}</dd></div>
        </dl>
      </div>
      <div className="max-w-lg text-center">
        <p className="text-sm font-medium text-foreground">
          {mode === "stalled" ? t(($) => $.status.paused) : t(($) => $.session_page.canvas_forming_title)}
          {" · "}{stageLabel}
        </p>
        <p className="mt-1.5 text-xs text-muted-foreground">
          {working.length > 0
            ? working.map((member) => member.display_name || member.name || member.role).join(" · ")
            : t(($) => $.step_card.standby)}
        </p>
        <p className="mt-1 truncate text-xs text-muted-foreground">
          {latest?.body || t(($) => $.session_page.canvas_forming_hint)}
        </p>
      </div>
    </div>
  );
}
