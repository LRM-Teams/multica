"use client";

// NOTE: this file intentionally keeps the whole actor-profile cluster together —
// the trigger, its mobile-navigation variant, and the shared profile-content
// components (identity card + recent-activity) are tightly coupled around one
// profile query and are consumed as a unit (tests import ActorProfileContent /
// ActorProfileContentLoaded from here). Splitting them into six files would
// scatter tightly-coupled pieces for no benefit, so each secondary component
// carries a react-doctor-disable-next-line for react-doctor/no-multi-comp.

import { useQuery } from "@tanstack/react-query";
import { Sparkles } from "lucide-react";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import {
  HonorBadgeCrest,
  HonorBadgeIcon,
} from "@multica/ui/components/honor/honor-badge";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@multica/ui/components/ui/hover-card";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { avatarGlyph } from "@multica/ui/lib/avatar-fallback";
import {
  agentHonorOptions,
  memberProfileOptions,
} from "@multica/core/agents";
import { api } from "@multica/core/api";
import type { MemberProfile } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { honorNameDisplayProps } from "@multica/ui/lib/honor-name-display";
import {
  formatActorHandleLabel,
  resolveActorHandle,
  resolveActorIdentityPresentation,
  shouldShowActorHandleLabel,
} from "@multica/core/identity";
import { MemoryGrowthField } from "../agents/components/memory-growth-field";
import { AgentPresenceOverlay } from "./actor-avatar";
import { ActivityTimeline } from "../agents/components/tabs/activity-timeline";
import { useAgentActivityEvents } from "../agents/components/tabs/use-agent-activity-events";
import { useNavigation } from "../navigation";
import { useT } from "../i18n/use-t";

type ChannelsT = ReturnType<typeof useT<"channels">>["t"];

type ProfileMemberType = "agent" | "user";
type ProfilePopoverSide = "top" | "right" | "bottom" | "left" | "inline-start" | "inline-end";

interface ActorProfileTriggerProps {
  memberType: ProfileMemberType;
  memberId: string | null | undefined;
  children: React.ReactNode;
  align?: "start" | "center" | "end";
  side?: ProfilePopoverSide;
  sideOffset?: number;
  triggerElement?: "button" | "span";
  className?: string;
  onClickCapture?: React.MouseEventHandler;
}

export function ActorProfileTrigger({
  memberType,
  memberId,
  children,
  align = "start",
  side = "bottom",
  sideOffset = 4,
  triggerElement = "button",
  className,
  onClickCapture,
}: ActorProfileTriggerProps) {
  const isMobile = useIsMobile();
  if (!memberId) return <>{children}</>;

  const content = <ActorProfileContent memberType={memberType} memberId={memberId} />;
  // Message rows are CSS grid with default align-items:stretch. Without hug-
  // content sizing the trigger becomes as tall as the whole bubble (incl.
  // images) and Floating UI anchors the profile card mid-row.
  const triggerClassName = cn(
    "inline-flex h-fit w-fit shrink-0 self-start cursor-pointer rounded-md border-0 bg-transparent p-0 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
    className,
  );
  const triggerRender = triggerElement === "span"
    ? <span />
    : <button type="button" />;

  // Mobile: a Drawer caps at 80dvh, so a long Recent-activity list gets cut off
  // and can't be reached (#586). Navigate to a real full-page profile route
  // instead — same peek content, but the whole page scrolls with a back button.
  // Hooks (useWorkspacePaths/useNavigation) live in the child so the desktop
  // HoverCard branch never calls them.
  if (isMobile) {
    return (
      <MobileActorProfileTrigger
        memberType={memberType}
        memberId={memberId}
        triggerElement={triggerElement}
        triggerClassName={triggerClassName}
        onClickCapture={onClickCapture}
      >
        {children}
      </MobileActorProfileTrigger>
    );
  }

  return (
    <HoverCard>
      <HoverCardTrigger
        render={triggerRender}
        className={triggerClassName}
        onClickCapture={onClickCapture}
      >
        {children}
      </HoverCardTrigger>
      <HoverCardContent
        align={align}
        side={side}
        sideOffset={sideOffset}
        // IM-density profile peek: one shared size for author + @mention.
        // ~300px is closer to Slack/Discord hover cards than a 360 panel.
        className="w-[300px] p-0"
      >
        {content}
      </HoverCardContent>
    </HoverCard>
  );
}

// Mobile trigger: navigate to the full-page profile route instead of opening a
// height-capped Drawer. Kept as its own component so `useWorkspacePaths`/
// `useNavigation` are only called on mobile — the desktop HoverCard branch stays
// hook-for-hook identical. Preserves the trigger's className/onClickCapture/
// children and the button-vs-span choice (span is used inside already-interactive
// mentions where a nested <button> would be invalid).
// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
function MobileActorProfileTrigger({
  memberType,
  memberId,
  triggerElement,
  triggerClassName,
  onClickCapture,
  children,
}: {
  memberType: ProfileMemberType;
  memberId: string;
  triggerElement: "button" | "span";
  triggerClassName: string;
  onClickCapture?: React.MouseEventHandler;
  children: React.ReactNode;
}) {
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const openProfile = () => {
    navigation.push(paths.actorProfile(memberType, memberId));
  };

  if (triggerElement === "span") {
    return (
      // Deliberately a span-with-role, not a <button>: this variant renders
      // inside already-interactive content (rendered @mentions), where a nested
      // <button> would be invalid interactive DOM. Same pattern as
      // ActorAvatarProfileLink / ActorAvatarPanelTrigger in actor-avatar.tsx.
      // react-doctor-disable-next-line react-doctor/prefer-tag-over-role -- span+role avoids invalid nested-interactive DOM (see comment)
      <span
        role="button"
        tabIndex={0}
        className={triggerClassName}
        onClickCapture={onClickCapture}
        onClick={openProfile}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            openProfile();
          }
        }}
      >
        {children}
      </span>
    );
  }

  return (
    <button
      type="button"
      className={triggerClassName}
      onClickCapture={onClickCapture}
      onClick={openProfile}
    >
      {children}
    </button>
  );
}

// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
export function ActorProfileContent({
  memberType,
  memberId,
}: {
  memberType: ProfileMemberType;
  memberId: string;
}) {
  const wsId = useWorkspaceId();
  const { t } = useT("channels");
  const { data: profile, isPending, isError } = useQuery(
    memberProfileOptions(wsId, memberType, memberId),
  );

  if (isPending) {
    return <UnavailableProfile message={t(($) => $.profile_popover.loading)} />;
  }

  if (isError || !profile) {
    return (
      <UnavailableProfile
        message={
          memberType === "agent"
            ? t(($) => $.profile_popover.agent_unavailable)
            : t(($) => $.profile_popover.member_unavailable)
        }
      />
    );
  }

  return (
    <ActorProfileContentLoaded profile={profile} />
  );
}

// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
export function ActorProfileContentLoaded({ profile }: { profile: MemberProfile }) {
  const { t } = useT("channels");
  // #2 identity-only card: when the server returns just basic identity
  // (`profile_access=identity_only` — a private agent surfaced via a message you
  // can read, or a removed/deactivated one), render name/handle/avatar/description
  // but HIDE the live-status mark + Recent activity — those are the protected
  // panels the BE still gates (`canAccessPrivateAgent`). `full` keeps everything.
  // Never a blank "Agent unavailable" card again.
  const isIdentityOnly = profile.profile_access === "identity_only";
  const identity = {
    name: profile.name,
    display_name: profile.display_name,
  };
  const presentation = resolveActorIdentityPresentation(identity, "");
  const displayName = presentation.displayName || presentation.handle || t(($) => $.profile_popover.unknown);
  const description = profile.description?.trim() || "";
  // Members: role text on the name row. Agents: avatar badge only (LRM-248).
  const memberRole =
    profile.member_type === "user"
      ? roleLabel(profile.role, t)
      : null;
  const handle = resolveActorHandle(identity);
  const handleLabel = formatActorHandleLabel(handle);
  const showHandle =
    handleLabel !== null && shouldShowActorHandleLabel(displayName, handle);
  const isAgent = profile.member_type === "agent";
  const avatar = (
    <ActorAvatarBase
      name={displayName}
      initials={avatarGlyph(displayName)}
      avatarUrl={resolvePublicFileUrl(profile.avatar_url)}
      isAgent={isAgent}
      size={48}
      toneSeed={`${profile.member_type}:${profile.member_id}`}
    />
  );

  return (
    <div className="text-left">
      <div
        className={cn(
          "flex items-start gap-3 p-3",
          (description || isAgent) && "border-b",
        )}
      >
        {isAgent && !isIdentityOnly ? (
          <AgentPresenceOverlay agentId={profile.member_id} size={48}>
            {avatar}
          </AgentPresenceOverlay>
        ) : (
          avatar
        )}
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-1.5">
            {/* No flex-1 on the name — status must sit right after the name
                (Slack/IM style), not get pushed to the far edge of the card. */}
            <span className="min-w-0 truncate text-sm font-semibold text-foreground">
              {displayName}
            </span>
            {memberRole ? (
              <span className="shrink-0 text-xs text-muted-foreground">
                {memberRole}
              </span>
            ) : null}
          </div>
          {showHandle && handleLabel ? (
            <span className="mt-0.5 block truncate text-xs text-muted-foreground">
              {handleLabel}
            </span>
          ) : null}
        </div>
      </div>

      {/* Only render when there is real copy — empty "No description yet"
          pads the card and is the main reason member peeks felt oversized. */}
      {description ? (
        <section className={cn("border-b p-3 last:border-b-0")}>
          <p className="line-clamp-2 text-xs leading-5 text-foreground/85">
            {description}
          </p>
        </section>
      ) : null}
      {profile.member_type === "user" ? (
        <MemberHonorShowcase
          userId={profile.member_id}
          displayName={displayName}
        />
      ) : null}
      {isAgent && !isIdentityOnly ? (
        <AgentHonorShowcase agentId={profile.member_id} />
      ) : null}
      {/* LRM-304: agent member card — growth only on full-access profiles. */}
      {isAgent && !isIdentityOnly && profile.memory_growth ? (
        <section className="border-b p-3 last:border-b-0">
          <MemoryGrowthField growth={profile.memory_growth} />
        </section>
      ) : null}
      {profile.member_type === "agent" ? (
        isIdentityOnly ? (
          <RestrictedProfileBlocks />
        ) : (
          <ProfileSection title={t(($) => $.profile_popover.recent_activity)}>
            <AgentRecentActivity agentId={profile.member_id} />
          </ProfileSection>
        )
      ) : null}
    </div>
  );
}

// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
function AgentHonorShowcase({ agentId }: { agentId: string }) {
  const { t } = useT("channels");
  const workspaceId = useWorkspaceId();
  const { data: honor, isPending, isError } = useQuery(
    agentHonorOptions(workspaceId, agentId),
  );

  if (isError) return null;
  if (isPending || !honor) {
    return (
      <section
        className="honor-dark-surface border-b bg-slate-950 px-3 py-3 text-slate-100"
        data-testid="agent-honor-showcase-loading"
      >
        <div className="h-3 w-24 animate-pulse rounded-full bg-violet-200/20" />
        <div className="mt-2 h-8 animate-pulse rounded-lg bg-white/10" />
      </section>
    );
  }

  const equipped = honor.achievements.find(
    (item) => item.id === honor.equipped_achievement_id && item.unlocked,
  );
  const showcase = honor.showcase_achievement_ids
    .map((id) => honor.achievements.find((item) => item.id === id && item.unlocked))
    .filter((item): item is NonNullable<typeof item> => Boolean(item))
    .slice(0, 3);
  const unlocked = honor.achievements.filter((item) => item.unlocked).length;

  return (
    <section
      className="honor-dark-surface relative isolate overflow-hidden border-b bg-[radial-gradient(circle_at_12%_12%,rgba(139,92,246,0.24),transparent_38%),radial-gradient(circle_at_88%_88%,rgba(34,211,238,0.18),transparent_42%),linear-gradient(135deg,#020617,#172554)] px-3 py-3 text-slate-100"
      data-testid="agent-honor-showcase"
    >
      <div className="relative flex items-center justify-between gap-2">
        <span className="inline-flex items-center gap-1 text-[10px] font-medium uppercase tracking-[0.16em] text-violet-100/85">
          <Sparkles className="size-3" aria-hidden />
          {t(($) => $.profile_popover.honor.agent_title)}
        </span>
        <span className="rounded-full border border-cyan-200/20 bg-cyan-300/10 px-2 py-0.5 text-[10px] font-semibold tabular-nums text-cyan-100">
          {t(($) => $.profile_popover.honor.level_value, { level: honor.level })}
        </span>
      </div>

      <div className="relative mt-2.5 flex items-center gap-3">
        {equipped ? (
          <HonorBadgeCrest
            svgKey={equipped.svg_key}
            title={equipped.title}
            className="size-12"
            rare={equipped.rarity >= 70}
            animated
          />
        ) : (
          <span className="grid size-12 shrink-0 place-items-center rounded-2xl border border-white/15 bg-white/5 text-violet-100/75">
            <Sparkles className="size-5" aria-hidden />
          </span>
        )}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-semibold text-white">
            {equipped?.title ?? honor.fleet.class_label}
          </p>
          <p className="mt-0.5 flex items-center gap-1.5 text-[10px] tabular-nums text-slate-300">
            <span>{honor.total_xp} XP</span>
            <span aria-hidden>·</span>
            <span>{honor.fleet.class_label}</span>
            {honor.fleet.fleet_rank > 0 ? (
              <>
                <span aria-hidden>·</span>
                <span>#{honor.fleet.fleet_rank}</span>
              </>
            ) : null}
          </p>
        </div>
      </div>

      <div className="relative mt-2.5 flex items-center justify-between gap-3 border-t border-white/10 pt-2">
        <div className="flex min-w-0 items-center gap-1.5">
          {showcase.length > 0 ? (
            showcase.map((achievement) => (
              <span
                key={achievement.id}
                className="grid size-7 place-items-center rounded-lg border border-white/10 bg-white/[0.07]"
                title={achievement.title}
              >
                <HonorBadgeIcon
                  svgKey={achievement.svg_key}
                  title={achievement.title}
                  className="size-[18px]"
                />
              </span>
            ))
          ) : (
            <span className="text-[10px] text-slate-400">
              {t(($) => $.profile_popover.honor.keep_building)}
            </span>
          )}
        </div>
        <span className="shrink-0 text-[10px] tabular-nums text-slate-300">
          {t(($) => $.profile_popover.honor.agent_collection, { unlocked })}
        </span>
      </div>
    </section>
  );
}

// User honor is deliberately shown only inside the roomy profile surface. Dense
// message/member lists keep the readable styled name without another badge.
// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
function MemberHonorShowcase({
  userId,
  displayName,
}: {
  userId: string;
  displayName: string;
}) {
  const { t } = useT("channels");
  const { data: honor, isPending, isError } = useQuery({
    queryKey: ["honor", "wall", userId],
    queryFn: () => api.getUserHonor(userId),
    staleTime: 60_000,
  });

  if (isError) return null;

  if (isPending || !honor) {
    return (
      <section
        className="honor-dark-surface relative overflow-hidden border-b bg-slate-950 px-3 py-3 text-slate-100 last:border-b-0"
        data-testid="member-honor-showcase-loading"
      >
        <div className="h-3 w-24 animate-pulse rounded-full bg-cyan-200/20" />
        <div className="mt-2 h-8 animate-pulse rounded-lg bg-white/10" />
      </section>
    );
  }

  const equipped = honor.equipped_badge;
  const showcase = (honor.showcase_badges ?? honor.unlocked_badges)
    .filter((badge) => badge.id !== equipped?.id)
    .slice(0, 3);
  const nameDisplay = honorNameDisplayProps({
    nameStyle: honor.name_style,
    level: honor.level,
    surface: "profile",
  });
  const unlocked = honor.badges_unlocked ?? honor.unlocked_badges.length;
  const total = honor.badges_total ?? unlocked;

  return (
    <section
      className="honor-dark-surface relative isolate overflow-hidden border-b bg-[radial-gradient(circle_at_86%_18%,rgba(34,211,238,0.22),transparent_32%),radial-gradient(circle_at_10%_90%,rgba(139,92,246,0.2),transparent_42%),linear-gradient(135deg,#020617,#111827_52%,#172554)] px-3 py-3 text-slate-100 last:border-b-0"
      data-testid="member-honor-showcase"
    >
      <span
        aria-hidden
        className="absolute -right-6 -top-7 size-24 rounded-full border border-cyan-200/15 shadow-[0_0_40px_rgba(34,211,238,0.12)]"
      />
      <div className="relative flex items-center justify-between gap-2">
        <span className="inline-flex items-center gap-1 text-[10px] font-medium uppercase tracking-[0.16em] text-cyan-100/80">
          <Sparkles className="size-3" aria-hidden />
          {t(($) => $.profile_popover.honor.title)}
        </span>
        <span className="rounded-full border border-violet-200/20 bg-violet-300/10 px-2 py-0.5 text-[10px] font-semibold tabular-nums text-violet-100">
          {t(($) => $.profile_popover.honor.level_value, { level: honor.level })}
        </span>
      </div>

      <div className="relative mt-2.5 flex items-center gap-3">
        {equipped ? (
          <HonorBadgeCrest
            svgKey={equipped.svg_key}
            title={equipped.title}
            className="size-12"
            rare={honor.level >= 42}
            animated
          />
        ) : (
          <span className="grid size-12 shrink-0 place-items-center rounded-2xl border border-white/15 bg-white/5 text-cyan-100/70">
            <Sparkles className="size-5" aria-hidden />
          </span>
        )}
        <div className="min-w-0 flex-1">
          <span
            className={cn("block truncate text-sm font-bold", nameDisplay.className)}
            data-honor-glow-tier={nameDisplay["data-honor-glow-tier"]}
            data-honor-surface={nameDisplay["data-honor-surface"]}
            style={nameDisplay.style}
          >
            {displayName}
          </span>
          <p className="mt-0.5 truncate text-[11px] text-slate-300">
            {equipped?.title ?? t(($) => $.profile_popover.honor.no_badge)}
          </p>
        </div>
      </div>

      <div className="relative mt-2.5 flex items-center justify-between gap-3 border-t border-white/10 pt-2">
        <div className="flex min-w-0 items-center gap-1.5">
          {showcase.length > 0 ? (
            showcase.map((badge) => (
              <span
                key={badge.id}
                className="grid size-7 place-items-center rounded-lg border border-white/10 bg-white/[0.07] shadow-[0_0_14px_rgba(56,189,248,0.08)]"
                title={badge.title}
              >
                <HonorBadgeIcon
                  svgKey={badge.svg_key}
                  title={badge.title}
                  className="size-[18px]"
                />
              </span>
            ))
          ) : (
            <span className="text-[10px] text-slate-400">
              {t(($) => $.profile_popover.honor.keep_building)}
            </span>
          )}
        </div>
        <span className="shrink-0 text-[10px] tabular-nums text-slate-300">
          {t(($) => $.profile_popover.honor.collection, {
            unlocked,
            total,
          })}
        </span>
      </div>
    </section>
  );
}

/** LRM-288: sensitive panels are explicit, never silently omitted (LRM-238). */
// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
function RestrictedProfileBlocks() {
  const { t } = useT("channels");
  const labels = [
    t(($) => $.profile_popover.restricted.runtime),
    t(($) => $.profile_popover.restricted.usage),
    t(($) => $.profile_popover.restricted.activity),
  ];
  return (
    <div className="border-t">
      {labels.map((label) => (
        <div
          key={label}
          className="flex items-center justify-between gap-3 border-b px-3 py-2.5 last:border-b-0"
        >
          <span className="text-xs text-muted-foreground/70">{label}</span>
          <span className="shrink-0 text-[11px] text-muted-foreground/60">
            {t(($) => $.profile_popover.restricted.channel_only)}
          </span>
        </div>
      ))}
    </div>
  );
}

// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
function ProfileSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="border-b p-3 last:border-b-0">
      <div className="mb-1.5 text-[11px] font-medium uppercase tracking-normal text-muted-foreground">
        {title}
      </div>
      {children}
    </section>
  );
}

// Agent "Recent activity" is the SAME shared ActivityTimeline the Activity tab
// renders, in compact mode (#383): last N narrative rows, dense, single-line
// subtext, no expand. It consumes the live #302 ActivityEvent stream (one shared
// read-model) instead of the legacy server-projected `recent_activity` labels,
// so this hover surface stays in lockstep with the tab/header and there is a
// single Activity renderer — no second local presentation to drift.
// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
function AgentRecentActivity({ agentId }: { agentId: string }) {
  const { t } = useT("channels");
  const { events, isLoading } = useAgentActivityEvents(agentId);
  // Guard only the first paint so the section doesn't flash the empty state
  // before the REST first-paint lands; ActivityTimeline owns empty + populated.
  if (isLoading && events.length === 0) {
    return (
      <div className="rounded-md bg-muted/45 px-2.5 py-1.5 text-xs text-muted-foreground">
        {t(($) => $.profile_popover.loading)}
      </div>
    );
  }
  return <ActivityTimeline events={events} compact />;
}

// react-doctor-disable-next-line react-doctor/no-multi-comp -- cohesive actor-profile cluster (see file header)
function UnavailableProfile({ message }: { message: string }) {
  return <div className="p-3 text-xs text-muted-foreground">{message}</div>;
}

function roleLabel(
  role: MemberProfile["role"],
  t: ChannelsT,
): string | null {
  if (role === "owner") return t(($) => $.profile_popover.role.owner);
  if (role === "admin") return t(($) => $.profile_popover.role.admin);
  if (role === "member") return t(($) => $.profile_popover.role.member);
  if (role === "agent") return t(($) => $.profile_popover.role.agent);
  return null;
}
