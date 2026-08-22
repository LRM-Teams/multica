"use client";

import type { ResearchSession } from "@multica/core/types/research";
import { Radio } from "lucide-react";
import { useT } from "../../i18n/use-t";

export function ResearchHomeHeader({ sessions }: { sessions: ResearchSession[] }) {
  const { t } = useT("research");
  const workingAgents = new Set(
    sessions.flatMap((session) =>
      (session.active_assignments ?? [])
        .filter((assignment) => assignment.state === "running")
        .map((assignment) => assignment.agent_id),
    ),
  ).size;

  return (
    <header className="relative z-[1] flex min-h-12 items-center justify-between gap-4">
      <div className="flex min-w-0 items-center gap-3">
        <span className="px-diamond grid size-10 shrink-0 place-items-center border-2 border-border bg-card text-base text-brand shadow-[3px_3px_0_rgb(0_0_0/45%)]" aria-hidden />
        <div className="min-w-0">
          <h1 className="truncate text-base font-medium text-foreground">{t(($) => $.home_header.title)}</h1>
          <p className="truncate text-xs text-muted-foreground">{t(($) => $.home_header.subtitle)}</p>
        </div>
      </div>
      <span className="research-home-header-status hidden shrink-0 items-center gap-2 text-xs text-muted-foreground sm:inline-flex">
        <Radio className="size-3.5 text-success" aria-hidden />
        {workingAgents > 0
          ? t(($) => $.home_header.working, { count: workingAgents })
          : t(($) => $.home_header.standby)}
      </span>
    </header>
  );
}
