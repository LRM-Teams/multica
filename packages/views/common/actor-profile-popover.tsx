"use client";

import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  AlertCircle,
  Ban,
  Clock,
  ListTodo,
  type LucideIcon,
} from "lucide-react";
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
  type AgentPresenceDetail,
} from "@multica/core/agents";
import type {
  MemberProfile,
  MemberProfileActivityItem,
} from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { resolveActorIdentityPresentation } from "@multica/core/identity";
import { ActorIdentityRow } from "./actor-identity-row";
import { presenceStatusToken } from "../agents/presence";
import { useT } from "../i18n/use-t";
import { useTimeAgo } from "../i18n/use-time-ago";

type ChannelsT = ReturnType<typeof useT<"channels">>["t"];
type AgentsT = ReturnType<typeof useT<"agents">>["t"];

// #288: the status pill must agree with the presence dot — both read the
// availability/health source. A workload word ("空闲/处理中") shows only while
// online; offline/unstable/archived show the availability word ("离线" etc.).
// Never a bare raw status (that was the "gray dot vs idle" contradiction).
function presenceStatusLabel(
  presence: AgentPresenceDetail | "loading",
  t: AgentsT,
): string | null {
  const token = presenceStatusToken(presence);
  if (!token) return null;
  return token.kind === "workload"
    ? t(($) => $.workload[token.value])
    : t(($) => $.availability[token.value]);
}

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
  const triggerClassName = cn(
    "inline-flex cursor-pointer rounded-md border-0 bg-transparent p-0 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
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
        className="w-[360px] p-0"
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
  // #288: the status pill reads the same availability/health source as the
  // presence dot (via useAgentPresenceDetail), so the two can never disagree.
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
  const safeDescription = profile.description?.trim() || t(($) => $.profile_popover.no_description);
  const role = roleLabel(profile.role ?? (profile.member_type === "agent" ? "agent" : null), t);
  const agentStatus =
    profile.member_type === "agent" ? presenceStatusLabel(presence, tAgents) : null;
  const metadata = [role, agentStatus].filter(Boolean).join(" · ");

  return (
    <div className="text-left">
      <div className="grid grid-cols-[48px_minmax(0,1fr)] gap-3 border-b p-4">
        <ActorAvatarBase
          name={displayName}
          initials={initials}
          avatarUrl={resolvePublicFileUrl(profile.avatar_url)}
          isAgent={profile.member_type === "agent"}
          size={48}
          className={profile.member_type === "agent" ? "rounded-md" : "rounded-full"}
        />
        <div className="min-w-0">
          <ActorIdentityRow
            identity={identity}
            displayName={displayName}
            primaryClassName="truncate text-base font-semibold text-foreground"
            secondaryClassName="mt-0.5 truncate text-xs text-muted-foreground"
            className="block min-w-0"
          />
          {metadata ? (
            <div className="mt-2">
              <span className="inline-flex max-w-full items-center rounded-full border bg-muted/35 px-2 py-0.5 text-xs text-muted-foreground">
                {metadata}
              </span>
            </div>
          ) : null}
        </div>
      </div>

      <section className="border-b p-4 last:border-b-0">
        <p className={cn(
          "line-clamp-3 text-sm leading-6",
          profile.description?.trim() ? "text-foreground/85" : "text-muted-foreground",
        )}>
          {safeDescription}
        </p>
      </section>
      {profile.member_type === "agent" ? (
        <ProfileSection title={t(($) => $.profile_popover.recent_activity)}>
          {(profile.recent_activity ?? []).length > 0 ? (
            <div className="flex flex-col">
              {(profile.recent_activity ?? []).map((activity) => (
                <ActivityRow key={activity.id} activity={activity} />
              ))}
            </div>
          ) : (
            <div className="rounded-md bg-muted/45 px-3 py-2 text-xs text-muted-foreground">
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
    <section className="border-b p-4 last:border-b-0">
      <div className="mb-2 text-[11px] font-medium uppercase tracking-normal text-muted-foreground">
        {title}
      </div>
      {children}
    </section>
  );
}

function ActivityRow({ activity }: { activity: MemberProfileActivityItem }) {
  const { t } = useT("channels");
  const timeAgo = useTimeAgo();
  const meta = activityMeta(activity.kind, t);
  const Icon = meta.icon;
  const label = activity.label?.trim() || meta.label;

  return (
    <div className="grid grid-cols-[22px_minmax(0,1fr)] gap-2.5 border-t py-2.5 first:border-t-0 first:pt-0 last:pb-0">
      <span className="flex size-[22px] items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Icon className="size-3" />
      </span>
      <div className="min-w-0">
        <div className="flex min-w-0 items-baseline gap-2 text-xs">
          <span className="font-medium text-foreground">{label}</span>
          <span className="shrink-0 text-muted-foreground" title={formatAbsoluteTime(activity.occurred_at)}>
            {timeAgo(activity.occurred_at)}
          </span>
        </div>
      </div>
    </div>
  );
}

function UnavailableProfile({ message }: { message: string }) {
  return <div className="p-4 text-xs text-muted-foreground">{message}</div>;
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

function activityMeta(
  kind: MemberProfileActivityItem["kind"],
  t: ChannelsT,
): { label: string; icon: LucideIcon } {
  switch (kind) {
    case "queued":
      return { label: t(($) => $.profile_popover.activity.queued), icon: Clock };
    case "failed":
      return { label: t(($) => $.profile_popover.activity.failed), icon: AlertCircle };
    case "cancelled":
      return { label: t(($) => $.profile_popover.activity.cancelled), icon: Ban };
    case "task":
      return { label: t(($) => $.profile_popover.activity.task), icon: ListTodo };
    case "working":
    default:
      return { label: t(($) => $.profile_popover.activity.working), icon: Activity };
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
