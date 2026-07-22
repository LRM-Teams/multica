"use client";

import {
  resolveActorIdentityPresentation,
  type ActorIdentityPresentation,
} from "@multica/core/identity";
import type { ChannelMember } from "@multica/core/types";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { MessageSquare } from "lucide-react";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { avatarGlyph, avatarToneClass } from "../../common/initials";
import { useT } from "../../i18n";

export type MemberRoleLabel = "owner" | "admin" | "member" | "agent";

/**
 * Shared Members list (LRM-211) — used by the Members dialog and the
 * Channel details 「成员」Tab so there is one list IA, not two.
 * LRM-225 — scroll via flex-1 min-h-0 (dialog) or parent overflow (details);
 * drop the old fixed max-h that clipped the roster on mobile.
 */
export function ChannelMembersList({
  members,
  loading,
  emptyLabel,
  noResultsLabel,
  roleForMember,
  canRemove,
  isMobile,
  currentUserId,
  onOpenDm,
  onRemove,
  dmPending,
  className,
}: {
  members: ChannelMember[];
  loading?: boolean;
  emptyLabel: string;
  noResultsLabel: string;
  roleForMember: (member: ChannelMember) => MemberRoleLabel;
  canRemove: boolean;
  isMobile: boolean;
  currentUserId: string;
  onOpenDm?: (member: ChannelMember) => void;
  onRemove?: (member: ChannelMember) => void;
  dmPending?: boolean;
  className?: string;
}) {
  const { t } = useT("channels");

  if (loading) {
    return (
      <div
        className={cn(
          "min-h-0 space-y-2 overflow-y-auto overscroll-contain px-5 py-3 [-webkit-overflow-scrolling:touch]",
          className,
        )}
        aria-busy="true"
      >
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="flex items-center gap-3">
            <Skeleton className="size-9 shrink-0 rounded-full" />
            <div className="min-w-0 flex-1 space-y-1.5">
              <Skeleton className="h-3.5 w-28" />
              <Skeleton className="h-3 w-20" />
            </div>
            <Skeleton className="h-5 w-14 rounded-full" />
          </div>
        ))}
      </div>
    );
  }

  if (members.length === 0) {
    return (
      <p
        className={cn(
          "min-h-0 px-5 py-10 text-center text-sm text-muted-foreground",
          className,
        )}
      >
        {emptyLabel || noResultsLabel}
      </p>
    );
  }

  return (
    <div
      className={cn(
        "min-h-0 overflow-y-auto overscroll-contain pb-2 [-webkit-overflow-scrolling:touch]",
        className,
      )}
      data-testid="channel-members-list"
    >
      {members.map((m) => {
        const isAgent = m.member_type === "agent";
        const presentation: ActorIdentityPresentation = resolveActorIdentityPresentation(
          m,
          isAgent ? t(($) => $.message.agent_badge) : t(($) => $.members.title),
        );
        const roleKey = roleForMember(m);
        const roleLabel = t(($) => $.profile_popover.role[roleKey]);
        const canDm = Boolean(onOpenDm) && (isAgent || m.member_id !== currentUserId);

        return (
          <div
            key={`${m.member_type}:${m.member_id}`}
            className="group flex min-h-[52px] items-center gap-3 border-b border-border/40 px-5 py-2.5 last:border-b-0 hover:bg-accent/60"
          >
            <ActorAvatar
              name={presentation.displayName}
              initials={avatarGlyph(presentation.displayName || "?")}
              avatarUrl={resolvePublicFileUrl(m.avatar_url)}
              isAgent={isAgent}
              size={36}
              className={avatarToneClass(`${m.member_type}:${m.member_id}`)}
            />
            <ActorIdentityRow
              displayName={presentation.displayName}
              handle={presentation.handle}
              showHandle
              className="min-w-0 flex-1"
              primaryClassName="truncate text-sm font-semibold text-foreground"
            />
            <span className="shrink-0 rounded-full border border-border bg-background px-2 py-0.5 text-[11px] text-muted-foreground">
              {roleLabel}
            </span>
            {canDm && (
              <button
                type="button"
                onClick={() => onOpenDm?.(m)}
                disabled={dmPending}
                aria-label={t(($) => $.dm.send_message)}
                title={t(($) => $.dm.send_message)}
                className={cn(
                  "rounded p-1.5 text-muted-foreground transition hover:text-foreground disabled:opacity-50",
                  isMobile ? "opacity-100" : "opacity-0 group-hover:opacity-100",
                )}
              >
                <MessageSquare className="size-3.5" />
              </button>
            )}
            {canRemove && onRemove && (
              <button
                type="button"
                onClick={() => onRemove(m)}
                aria-label={t(($) => $.members.remove_aria)}
                className={cn(
                  "shrink-0 font-semibold text-destructive transition",
                  isMobile
                    ? "min-h-11 px-2 py-2.5 text-sm opacity-100"
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
