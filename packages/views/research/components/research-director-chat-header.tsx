"use client";

import type { ReactNode } from "react";
import type { ResearchFleetMember } from "@multica/core/types";
import { ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n/use-t";

export function ResearchDirectorChatHeader({
  director,
  activity,
  fallbackName,
  modeChip,
  mode,
}: {
  director: ResearchFleetMember | null;
  activity?: string | null;
  fallbackName?: string;
  modeChip: ReactNode;
  mode?: string;
}) {
  const { t } = useT("research");
  const name = director?.display_name || director?.name || director?.role || null;
  const resolvedName =
    name ?? fallbackName ?? t(($) => $.d5.rail.director_fallback);
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
        {/* One site-wide actor face. No status dot: the green pip below is
            live *activity*, a different vocabulary from Agent Presence. */}
        {director?.agent_id ? (
          <ActorAvatar
            actorType="agent"
            actorId={director.agent_id}
            name={resolvedName}
            avatarUrlHint={director.avatar_url}
            size={36}
            profileLink={false}
          />
        ) : (
          <ActorAvatarBase
            name={resolvedName}
            initials={resolvedName}
            isAgent
            size={36}
            toneSeed={`agent:${resolvedName}`}
          />
        )}
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
            {resolvedName}
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
