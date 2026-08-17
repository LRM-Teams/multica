"use client";

import type { ReactNode } from "react";
import type { ResearchFleetMember } from "@multica/core/types";
import {
  Avatar,
  AvatarFallback,
  AvatarImage,
} from "@multica/ui/components/ui/avatar";
import { useT } from "../../i18n/use-t";

export function ResearchDirectorChatHeader({
  director,
  activity,
  modeChip,
  mode,
}: {
  director: ResearchFleetMember | null;
  activity?: string | null;
  modeChip: ReactNode;
  mode?: string;
}) {
  const { t } = useT("research");
  const name = director?.display_name || director?.name || director?.role || null;
  const fallback = name?.trim().charAt(0).toUpperCase() || "D";
  const status = activity?.trim()
    ? activity
    : director?.status === "active"
      ? t(($) => $.d5.rail.director_active)
      : t(($) => $.d5.rail.director_standby);

  return (
    <div
      className="flex min-h-16 items-center gap-3 border-b px-3 py-2.5"
      data-testid="research-chat-header"
      data-chat-mode={mode}
    >
      <div className="relative shrink-0">
        <Avatar className="size-9 border border-primary/35 bg-primary/10">
          {director?.avatar_url ? (
            <AvatarImage src={director.avatar_url} alt="" />
          ) : null}
          <AvatarFallback className="bg-primary/10 text-xs font-semibold text-primary">
            {fallback}
          </AvatarFallback>
        </Avatar>
        {activity?.trim() ? (
          <span
            className="absolute right-0 bottom-0 size-2.5 rounded-full border-2 border-card bg-emerald-400"
            aria-hidden
          />
        ) : null}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-semibold text-foreground">
            {name ?? t(($) => $.d5.rail.director_fallback)}
          </span>
          <span className="shrink-0 text-[10px] font-medium tracking-wide text-primary uppercase">
            {t(($) => $.d5.rail.director_role)}
          </span>
        </div>
        <p className="truncate text-[11px] text-muted-foreground">{status}</p>
      </div>
      {modeChip}
    </div>
  );
}
