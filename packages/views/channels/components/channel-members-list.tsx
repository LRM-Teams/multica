"use client";

import {
  resolveActorIdentityPresentation,
  type ActorIdentityPresentation,
} from "@multica/core/identity";
import type { ChannelMember } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import type { OpenMemberPanelFn } from "@multica/core/members";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { MessageSquare } from "lucide-react";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useT } from "../../i18n";

export type MemberRoleLabel = "owner" | "admin" | "member" | "agent";

/**
 * Shared Members list (LRM-211) — used by the Members dialog and the
 * Channel details 「成员」Tab so there is one list IA, not two.
 * LRM-225 — scroll via flex-1 min-h-0 (dialog) or parent overflow (details);
 * drop the old fixed max-h that clipped the roster on mobile.
 *
 * Avatars are identity-first (LRM-224 Option B): actor id → shared Avatar;
 * agents show the presence status dot on this directory surface.
 * LRM-288 — row identity is clickable: agents open the agent panel (including
 * channel-only / group-manager agents absent from ListAgents); humans open
 * the LRM-619 human member profile panel (Lock A).
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
  onOpenAgent,
  onOpenMember,
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
  onOpenAgent?: OpenAgentPanelFn;
  onOpenMember?: OpenMemberPanelFn;
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
          </div>
        ))}
      </div>
    );
  }

  if (members.length === 0) {
    return (
      <p
        className={cn(
          "min-h-0 px-5 py-10 text-center text-sm text-foreground",
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
        const showMutedRole = !isAgent && (roleKey === "owner" || roleKey === "admin");
        const mutedRoleLabel = showMutedRole
          ? t(($) => $.profile_popover.role[roleKey])
          : null;
        const canDm = Boolean(onOpenDm) && (isAgent || m.member_id !== currentUserId);
        const actorType = isAgent ? "agent" : "member";
        const profileMemberType = isAgent ? "agent" : "user";
        const openAgentCapture =
          isAgent && onOpenAgent
            ? () => {
                onOpenAgent(m.member_id, {
                  name: m.name,
                  display_name: m.display_name,
                  avatar_url: m.avatar_url ?? null,
                });
              }
            : undefined;
        const openMemberCapture =
          !isAgent && onOpenMember
            ? () => {
                onOpenMember(m.member_id, {
                  name: m.name,
                  display_name: m.display_name,
                  avatar_url: m.avatar_url ?? null,
                });
              }
            : undefined;

        return (
          <div
            key={`${m.member_type}:${m.member_id}`}
            className="group flex min-h-[52px] items-center gap-3 border-b border-border px-5 py-2.5 last:border-b-0 hover:bg-hover"
          >
            <ActorProfileTrigger
              memberType={profileMemberType}
              memberId={m.member_id}
              side="left"
              sideOffset={8}
              className="min-w-0 flex-1 items-center gap-3"
              onClickCapture={openAgentCapture ?? openMemberCapture}
            >
              <ActorAvatar
                actorType={actorType}
                actorId={m.member_id}
                avatarUrlHint={m.avatar_url}
                size={36}
                showStatusDot
                profileLink={false}
              />
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-baseline gap-1.5">
                  <span className="truncate text-sm font-semibold text-ink">
                    {presentation.displayName}
                  </span>
                  {presentation.showHandleLabel && presentation.handleLabel ? (
                    <span className="truncate text-xs font-normal text-ink-2">
                      {presentation.handleLabel}
                    </span>
                  ) : null}
                  {mutedRoleLabel ? (
                    <span
                      data-testid="member-role-label"
                      className="shrink-0 text-[11px] font-normal leading-none text-muted-foreground"
                    >
                      {mutedRoleLabel}
                    </span>
                  ) : null}
                </div>
              </div>
            </ActorProfileTrigger>
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
