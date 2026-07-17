"use client";

// NOTE: this file intentionally keeps the whole actor-profile cluster together —
// the trigger, its mobile-navigation variant, and the shared profile-content
// components (identity card + recent-activity) are tightly coupled around one
// profile query and are consumed as a unit (tests import ActorProfileContent /
// ActorProfileContentLoaded from here). Splitting them into six files would
// scatter tightly-coupled pieces for no benefit, so each secondary component
// carries a react-doctor-disable-next-line for react-doctor/no-multi-comp.

import { useQuery } from "@tanstack/react-query";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@multica/ui/components/ui/hover-card";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { memberProfileOptions } from "@multica/core/agents";
import type { MemberProfile } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import {
  formatActorHandleLabel,
  resolveActorHandle,
  resolveActorIdentityPresentation,
  shouldShowActorHandleLabel,
} from "@multica/core/identity";
import { AgentLiveStatusMark } from "../agents/components/agent-live-status-mark";
import { useAgentLiveStatus } from "../agents/use-agent-live-status";
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
  const wsId = useWorkspaceId();
  // #2 identity-only card: when the server returns just basic identity
  // (`profile_access=identity_only` — a private agent surfaced via a message you
  // can read, or a removed/deactivated one), render name/handle/avatar/description
  // but HIDE the live-status mark + Recent activity — those are the protected
  // panels the BE still gates (`canAccessPrivateAgent`). `full` keeps everything.
  // Never a blank "Agent unavailable" card again.
  const isIdentityOnly = profile.profile_access === "identity_only";
  // Agents (full access only): stage-detail live status (Thinking / Running
  // command… / …) when a task is active; coarse presence word (Idle / Offline)
  // when idle. Same snapshot + activity caches as the chat status pill / avatar
  // presence dot, so the three surfaces stay in lockstep via WS. Passing
  // `undefined` for identity_only skips the fetch entirely (no access anyway).
  const liveStatus = useAgentLiveStatus(
    wsId,
    profile.member_type === "agent" && !isIdentityOnly ? profile.member_id : undefined,
  );
  const identity = {
    name: profile.name,
    display_name: profile.display_name,
  };
  const presentation = resolveActorIdentityPresentation(identity, "");
  const displayName = presentation.displayName || presentation.handle || t(($) => $.profile_popover.unknown);
  const initials = displayName
    .split(" ")
    .map((part) => part[0])
    .join("")
    .toUpperCase()
    .slice(0, 2);
  const description = profile.description?.trim() || "";
  // Members: role text on the name row. Agents: live status immediately
  // after the name (dot + word) — not a far-right filler and not a pill.
  const memberRole =
    profile.member_type === "user"
      ? roleLabel(profile.role, t)
      : null;
  const handle = resolveActorHandle(identity);
  const handleLabel = formatActorHandleLabel(handle);
  const showHandle =
    handleLabel !== null && shouldShowActorHandleLabel(displayName, handle);

  return (
    <div className="text-left">
      <div
        className={cn(
          "flex items-start gap-3 p-3",
          (description || profile.member_type === "agent") && "border-b",
        )}
      >
        <ActorAvatarBase
          name={displayName}
          initials={initials}
          avatarUrl={resolvePublicFileUrl(profile.avatar_url)}
          isAgent={profile.member_type === "agent"}
          size={48}
        />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-1.5">
            {/* No flex-1 on the name — status must sit right after the name
                (Slack/IM style), not get pushed to the far edge of the card. */}
            <span className="min-w-0 truncate text-sm font-semibold text-foreground">
              {displayName}
            </span>
            {isIdentityOnly ? null : (
              <AgentLiveStatusMark status={liveStatus} className="shrink-0" />
            )}
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
      {profile.member_type === "agent" && !isIdentityOnly ? (
        <ProfileSection title={t(($) => $.profile_popover.recent_activity)}>
          <AgentRecentActivity agentId={profile.member_id} />
        </ProfileSection>
      ) : null}
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
