"use client";

import { useQuery } from "@tanstack/react-query";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import {
  Drawer,
  DrawerContent,
  DrawerTrigger,
} from "@multica/ui/components/ui/drawer";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@multica/ui/components/ui/hover-card";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import {
  memberProfileOptions,
  useAgentPresenceDetail,
} from "@multica/core/agents";
import type {
  MemberProfile,
  MemberProfileActivityItem,
} from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import {
  formatActorHandleLabel,
  resolveActorHandle,
  resolveActorIdentityPresentation,
  shouldShowActorHandleLabel,
} from "@multica/core/identity";
import { formatPresenceStatus } from "../agents/presence";
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

  if (isMobile) {
    if (triggerElement === "span") {
      return (
        <Drawer>
          <DrawerTrigger asChild>
            <span className={triggerClassName} onClickCapture={onClickCapture}>
              {children}
            </span>
          </DrawerTrigger>
          <DrawerContent className="max-h-[80dvh] overflow-y-auto p-0">
            {content}
          </DrawerContent>
        </Drawer>
      );
    }

    return (
      <Drawer>
        <DrawerTrigger
          type="button"
          className={triggerClassName}
          onClickCapture={onClickCapture}
        >
          {children}
        </DrawerTrigger>
        <DrawerContent className="max-h-[80dvh] overflow-y-auto p-0">
          {content}
        </DrawerContent>
      </Drawer>
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

function ActorProfileContent({
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

export function ActorProfileContentLoaded({ profile }: { profile: MemberProfile }) {
  const { t } = useT("channels");
  const { t: tAgents } = useT("agents");
  const wsId = useWorkspaceId();
  // #288: the name-row status reads the same availability/health source as
  // the presence dot (via useAgentPresenceDetail), so the two can never disagree.
  const presence = useAgentPresenceDetail(
    wsId,
    profile.member_type === "agent" ? profile.member_id : undefined,
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
  // Members: role text on the name row. Agents: live presence status as plain
  // muted text on the name row so the header fills width — no pill/chip.
  const memberRole =
    profile.member_type === "user"
      ? roleLabel(profile.role, t)
      : null;
  // Plain text on the name row — shared #288 rule + agents i18n.
  const agentStatus =
    profile.member_type === "agent" ? formatPresenceStatus(presence, tAgents) : null;
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
          className={profile.member_type === "agent" ? "rounded-md" : "rounded-full"}
        />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-1.5">
            <span className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">
              {displayName}
            </span>
            {agentStatus ? (
              <span className="shrink-0 text-xs text-muted-foreground">
                {agentStatus}
              </span>
            ) : null}
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
      {profile.member_type === "agent" ? (
        <ProfileSection title={t(($) => $.profile_popover.recent_activity)}>
          {(profile.recent_activity ?? []).length > 0 ? (
            <div className="flex flex-col">
              {(profile.recent_activity ?? []).map((activity) => (
                <ActivityRow key={activity.id} activity={activity} />
              ))}
            </div>
          ) : (
            <div className="rounded-md bg-muted/45 px-2.5 py-1.5 text-xs text-muted-foreground">
              {t(($) => $.profile_popover.no_recent_activity)}
            </div>
          )}
        </ProfileSection>
      ) : null}
    </div>
  );
}

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

function ActivityRow({ activity }: { activity: MemberProfileActivityItem }) {
  const { t } = useT("channels");
  const label = activity.label?.trim() || activityFallbackLabel(activity.kind, t);
  const clock = formatActivityClock(activity.occurred_at);

  return (
    <div className="flex min-w-0 items-center gap-2 py-1 text-xs first:pt-0 last:pb-0">
      <span
        className="w-[4.75rem] shrink-0 tabular-nums text-muted-foreground"
        title={formatAbsoluteTime(activity.occurred_at)}
      >
        {clock}
      </span>
      <span
        className={cn(
          "size-1.5 shrink-0 rounded-full",
          activityStatusDotClass(activity.status),
        )}
        aria-hidden
      />
      <span className="min-w-0 truncate text-foreground">{label}</span>
    </div>
  );
}

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

function activityFallbackLabel(
  kind: MemberProfileActivityItem["kind"],
  t: ChannelsT,
): string {
  switch (kind) {
    case "queued":
      return t(($) => $.profile_popover.activity.queued);
    case "failed":
      return t(($) => $.profile_popover.activity.failed);
    case "cancelled":
      return t(($) => $.profile_popover.activity.cancelled);
    case "task":
      return t(($) => $.profile_popover.activity.task);
    case "working":
    default:
      return t(($) => $.profile_popover.activity.working);
  }
}

// Compact status dot for the timeline row — mirrors the IM-style activity log
// (time · colored dot · label) rather than icon-in-circle + relative "just now".
function activityStatusDotClass(
  status: MemberProfileActivityItem["status"],
): string {
  switch (status) {
    case "running":
    case "dispatched":
      return "bg-success";
    case "queued":
    case "waiting_local_directory":
      return "bg-warning";
    case "failed":
      return "bg-destructive";
    case "completed":
    case "cancelled":
    default:
      return "bg-muted-foreground/40";
  }
}

// Clock for the activity column: "16:05:14" (local, 24h, fixed width).
function formatActivityClock(value: string): string {
  try {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    const hh = String(date.getHours()).padStart(2, "0");
    const mm = String(date.getMinutes()).padStart(2, "0");
    const ss = String(date.getSeconds()).padStart(2, "0");
    return `${hh}:${mm}:${ss}`;
  } catch {
    return value;
  }
}

function formatAbsoluteTime(value: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: "medium",
      timeStyle: "short",
    }).format(new Date(value));
  } catch {
    return value;
  }
}
