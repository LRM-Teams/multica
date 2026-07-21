"use client";

import type { ChannelMember } from "@multica/core/types";
import {
  resolveActorIdentityPresentation,
} from "@multica/core/identity";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { MessageSquare } from "lucide-react";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { avatarGlyph, avatarToneClass } from "../../common/initials";
import { useT } from "../../i18n";

/**
 * Shared channel member rows for LRM-211 Members modal and LRM-210 channel
 * details 「成员」Tab — one list component, two entry points.
 */
export function ChannelMembersList({
  members,
  loading = false,
  emptyLabel,
  canManage,
  isMobile,
  currentUserId,
  roleForMember,
  agentFallbackLabel,
  onRemove,
  onMessage,
  className,
}: {
  members: ChannelMember[];
  loading?: boolean;
  emptyLabel: string;
  canManage: boolean;
  isMobile: boolean;
  currentUserId?: string | null;
  roleForMember: (member: ChannelMember) => string | null;
  agentFallbackLabel: string;
  onRemove?: (member: ChannelMember) => void;
  onMessage?: (member: ChannelMember) => void;
  className?: string;
}) {
  const { t } = useT("channels");

  if (loading) {
    return (
      <div className={cn("space-y-1 px-1.5 pb-2", className)} aria-busy="true">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="flex items-center gap-3 px-2 py-2.5">
            <Skeleton className="size-9 shrink-0 rounded-full" />
            <div className="min-w-0 flex-1 space-y-1.5">
              <Skeleton className="h-3.5 w-28" />
              <Skeleton className="h-3 w-20" />
            </div>
            <Skeleton className="h-5 w-12 rounded-full" />
          </div>
        ))}
      </div>
    );
  }

  if (members.length === 0) {
    return (
      <p className={cn("px-4 py-8 text-center text-sm text-muted-foreground", className)}>
        {emptyLabel}
      </p>
    );
  }

  return (
    <div className={cn("overflow-y-auto px-1.5 pb-2", className)}>
      {members.map((m) => {
        const isAgent = m.member_type === "agent";
        const presentation = resolveActorIdentityPresentation(
          m,
          isAgent ? agentFallbackLabel : t(($) => $.members.title),
        );
        const role = roleForMember(m);
        const seed = `${m.member_type}:${m.member_id}`;
        const showMessage =
          !!onMessage && (isAgent || (currentUserId != null && m.member_id !== currentUserId));
        const showRemove = canManage && !!onRemove;

        return (
          <div
            key={seed}
            className="group flex min-h-[52px] items-center gap-3 rounded-md px-2.5 py-2 hover:bg-accent"
          >
            <ActorAvatar
              name={presentation.displayName}
              initials={avatarGlyph(presentation.displayName || "?")}
              avatarUrl={resolvePublicFileUrl(m.avatar_url)}
              isAgent={isAgent}
              size={36}
              className={avatarToneClass(seed)}
            />
            <ActorIdentityRow
              displayName={presentation.displayName}
              handle={presentation.handle}
              showHandle
              primaryClassName="truncate text-sm font-semibold"
            />
            {role ? (
              <span className="shrink-0 rounded-full border border-border bg-background px-2 py-0.5 text-[11px] text-muted-foreground">
                {role}
              </span>
            ) : null}
            {showMessage && (
              <button
                type="button"
                onClick={() => onMessage(m)}
                aria-label={t(($) => $.dm.send_message)}
                title={t(($) => $.dm.send_message)}
                className={cn(
                  "rounded p-1.5 text-muted-foreground transition hover:text-foreground",
                  isMobile ? "opacity-100" : "opacity-0 group-hover:opacity-100",
                )}
              >
                <MessageSquare className="size-3.5" />
              </button>
            )}
            {showRemove && (
              <button
                type="button"
                onClick={() => onRemove(m)}
                aria-label={t(($) => $.members.remove_aria)}
                className={cn(
                  "shrink-0 font-semibold text-destructive transition",
                  isMobile
                    ? "min-h-11 px-2 py-2.5 text-sm"
                    : "px-1.5 py-1 text-sm opacity-0 group-hover:opacity-100",
                )}
              >
                {t(($) => $.members.remove)}
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}
