"use client";

import { useMemo } from "react";
import {
  resolveActorIdentityPresentation,
  type ActorIdentityPresentation,
} from "@multica/core/identity";
import type { ChannelMember } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { MessageSquare } from "lucide-react";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useT } from "../../i18n";
import { AgentActivityCompact } from "./agent-activity-compact";

export type MemberRoleLabel = "owner" | "admin" | "member" | "agent";

/**
 * Shared Members list (LRM-211 / LRM-650) — used by the Members dialog and the
 * Channel details 「成员」Tab so there is one list IA, not two.
 * LRM-647: People / Agents sections; no row dividers; agent rows hang Compact
 * Activity (EN state type only). Humans: name + handle; presence = avatar dot.
 */
export function ChannelMembersList({
  members,
  loading,
  emptyLabel,
  noResultsLabel,
  roleForMember: _roleForMember,
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
  const { t } = useT("channels");
  const { humans, agents } = useMemo(() => {
    const nextHumans: ChannelMember[] = [];
    const nextAgents: ChannelMember[] = [];
    for (const m of members) {
      if (m.member_type === "agent") nextAgents.push(m);
      else nextHumans.push(m);
    }
    return { humans: nextHumans, agents: nextAgents };
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
        "min-h-0 overflow-y-auto overscroll-contain pb-2 [-webkit-overflow-scrolling:touch]",
        className,
      )}
      data-testid="channel-members-list"
    >
      {humans.length > 0 ? (
        <MemberSection
          label={t(($) => $.members.people_section, { count: humans.length })}
          testId="channel-members-people"
        >
          {humans.map((m) => (
            <MemberRow
              key={`${m.member_type}:${m.member_id}`}
              member={m}
              isAgent={false}
              isMobile={isMobile}
              currentUserId={currentUserId}
              canRemove={canRemove}
              dmPending={dmPending}
              onOpenDm={onOpenDm}
              onOpenAgent={onOpenAgent}
              onOpenMember={onOpenMember}
              onRemove={onRemove}
            />
          ))}
        </MemberSection>
      ) : null}
      {agents.length > 0 ? (
        <MemberSection
          label={t(($) => $.members.agents_section, { count: agents.length })}
          testId="channel-members-agents"
        >
          {agents.map((m) => (
            <MemberRow
              key={`${m.member_type}:${m.member_id}`}
              member={m}
              isAgent
              isMobile={isMobile}
              currentUserId={currentUserId}
              canRemove={canRemove}
              dmPending={dmPending}
              onOpenDm={onOpenDm}
              onOpenAgent={onOpenAgent}
              onOpenMember={onOpenMember}
              onRemove={onRemove}
            />
          ))}
        </MemberSection>
      ) : null}
    </div>
  );
}

function MemberSection({
  label,
  testId,
  children,
}: {
  label: string;
  testId: string;
  children: React.ReactNode;
}) {
  return (
    <section data-testid={testId}>
      <h3 className="px-5 pb-1 pt-2 text-[11px] font-bold uppercase tracking-wide text-muted-foreground">
        {label}
      </h3>
      <div className="px-2 pb-1">{children}</div>
    </section>
  );
}

function MemberRow({
  member: m,
  isAgent,
  isMobile,
  currentUserId,
  canRemove,
  dmPending,
  onOpenDm,
  onOpenAgent,
  onOpenMember,
  onRemove,
}: {
  member: ChannelMember;
  isAgent: boolean;
  isMobile: boolean;
  currentUserId: string;
  canRemove: boolean;
  dmPending?: boolean;
  onOpenDm?: (member: ChannelMember) => void;
  onOpenAgent?: OpenAgentPanelFn;
  onOpenMember?: (userId: string) => void;
  onRemove?: (member: ChannelMember) => void;
}) {
  const { t } = useT("channels");
  const presentation: ActorIdentityPresentation = resolveActorIdentityPresentation(
    m,
    isAgent ? t(($) => $.message.agent_badge) : t(($) => $.members.title),
  );
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
      data-testid="channel-member-row"
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
          <div className="truncate text-sm font-semibold text-ink">
            {presentation.displayName}
          </div>
          {isAgent ? (
            <AgentActivityCompact agentId={m.member_id} className="mt-0.5" />
          ) : presentation.showHandleLabel && presentation.handleLabel ? (
            <div className="mt-0.5 truncate text-xs text-ink-2">
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
