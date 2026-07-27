"use client";

import {
  resolveActorIdentityPresentation,
  type ActorIdentityPresentation,
} from "@multica/core/identity";
import type { ChannelMember } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { MessageSquare } from "lucide-react";
import { useMemo } from "react";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useT } from "../../i18n";
import { AgentCompactActivity } from "./agent-compact-activity";

export type MemberRoleLabel = "owner" | "admin" | "member" | "agent";

/** LRM-650 / Frank: section headers stay EN SoT (HUMANS / AGENTS), not i18n. */
function SectionHeader({ label, count }: { label: "HUMANS" | "AGENTS"; count: number }) {
  return (
    <div
      className="px-4 pb-1 pt-2 text-[11px] font-bold uppercase tracking-wide text-muted-foreground"
      data-testid={`channel-members-section-${label.toLowerCase()}`}
    >
      {label} · {count}
    </div>
  );
}

function MemberRow({
  m,
  roleForMember,
  canRemove,
  isMobile,
  currentUserId,
  onOpenDm,
  onOpenAgent,
  onOpenMember,
  onRemove,
  dmPending,
}: {
  m: ChannelMember;
  roleForMember: (member: ChannelMember) => MemberRoleLabel;
  canRemove: boolean;
  isMobile: boolean;
  currentUserId: string;
  onOpenDm?: (member: ChannelMember) => void;
  onOpenAgent?: OpenAgentPanelFn;
  onOpenMember?: (userId: string) => void;
  onRemove?: (member: ChannelMember) => void;
  dmPending?: boolean;
}) {
  const { t } = useT("channels");
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
          onOpenMember(m.member_id);
        }
      : undefined;

  return (
    <div
      className="group flex min-h-[52px] items-center gap-2.5 rounded-lg px-2.5 py-2 hover:bg-hover"
      data-testid="channel-members-row"
      data-member-type={m.member_type}
    >
      <ActorProfileTrigger
        memberType={profileMemberType}
        memberId={m.member_id}
        side="left"
        sideOffset={8}
        className="min-w-0 flex-1 items-center gap-2.5"
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
            {mutedRoleLabel ? (
              <span
                data-testid="member-role-label"
                className="shrink-0 text-[11px] font-normal leading-none text-muted-foreground"
              >
                {mutedRoleLabel}
              </span>
            ) : null}
          </div>
          {isAgent ? (
            <AgentCompactActivity agentId={m.member_id} />
          ) : presentation.showHandleLabel && presentation.handleLabel ? (
            <div className="mt-0.5 truncate text-xs text-muted-foreground">
              {presentation.handleLabel}
            </div>
          ) : null}
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
}

/**
 * Shared Members list (LRM-211 / LRM-650) — dialog + Channel details 「成员」Tab.
 * LRM-650: HUMANS / AGENTS sections, no row hairlines, agent Compact Activity.
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
  onOpenMember?: (userId: string) => void;
  onRemove?: (member: ChannelMember) => void;
  dmPending?: boolean;
  className?: string;
}) {
  const { humans, agents } = useMemo(() => {
    const h: ChannelMember[] = [];
    const a: ChannelMember[] = [];
    for (const m of members) {
      if (m.member_type === "agent") a.push(m);
      else h.push(m);
    }
    return { humans: h, agents: a };
  }, [members]);

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
        "min-h-0 overflow-y-auto overscroll-contain px-2 pb-2 [-webkit-overflow-scrolling:touch]",
        className,
      )}
      data-testid="channel-members-list"
    >
      {humans.length > 0 ? (
        <>
          <SectionHeader label="HUMANS" count={humans.length} />
          <div className="px-1 pb-1">
            {humans.map((m) => (
              <MemberRow
                key={`${m.member_type}:${m.member_id}`}
                m={m}
                roleForMember={roleForMember}
                canRemove={canRemove}
                isMobile={isMobile}
                currentUserId={currentUserId}
                onOpenDm={onOpenDm}
                onOpenAgent={onOpenAgent}
                onOpenMember={onOpenMember}
                onRemove={onRemove}
                dmPending={dmPending}
              />
            ))}
          </div>
        </>
      ) : null}
      {agents.length > 0 ? (
        <>
          <SectionHeader label="AGENTS" count={agents.length} />
          <div className="px-1 pb-1">
            {agents.map((m) => (
              <MemberRow
                key={`${m.member_type}:${m.member_id}`}
                m={m}
                roleForMember={roleForMember}
                canRemove={canRemove}
                isMobile={isMobile}
                currentUserId={currentUserId}
                onOpenDm={onOpenDm}
                onOpenAgent={onOpenAgent}
                onOpenMember={onOpenMember}
                onRemove={onRemove}
                dmPending={dmPending}
              />
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}
