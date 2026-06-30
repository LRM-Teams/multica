"use client";

import { useQuery } from "@tanstack/react-query";
import {
  Brain,
  MessageSquare,
  Reply,
  Terminal,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import {
  Drawer,
  DrawerContent,
  DrawerTrigger,
} from "@multica/ui/components/ui/drawer";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { memberProfileOptions } from "@multica/core/agents";
import type {
  MemberProfile,
  MemberProfileActivityItem,
} from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { resolveActorIdentityPresentation } from "@multica/core/identity";
import { ActorIdentityRow } from "./actor-identity-row";
import { useT, useTimeAgo } from "../i18n";

type ChannelsT = ReturnType<typeof useT<"channels">>["t"];

type ProfileMemberType = "agent" | "user";

interface ActorProfileTriggerProps {
  memberType: ProfileMemberType;
  memberId: string | null | undefined;
  children: React.ReactNode;
  align?: "start" | "center" | "end";
}

export function ActorProfileTrigger({
  memberType,
  memberId,
  children,
  align = "start",
}: ActorProfileTriggerProps) {
  const isMobile = useIsMobile();
  if (!memberId) return <>{children}</>;

  const content = <ActorProfileContent memberType={memberType} memberId={memberId} />;

  if (isMobile) {
    return (
      <Drawer>
        <DrawerTrigger
          type="button"
          className="inline-flex cursor-pointer rounded-md border-0 bg-transparent p-0 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
    <Popover>
      <PopoverTrigger
        render={<span />}
        className="inline-flex cursor-pointer rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {children}
      </PopoverTrigger>
      <PopoverContent align={align} side="bottom" className="w-[360px] p-0">
        {content}
      </PopoverContent>
    </Popover>
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

function ActorProfileContentLoaded({ profile }: { profile: MemberProfile }) {
  const { t } = useT("channels");
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
  const metadata = profile.status ?? roleLabel(profile.role, t);

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
            <div className="mt-2 inline-flex items-center gap-1.5 rounded-full border bg-muted/35 px-2 py-0.5 text-xs text-muted-foreground">
              {profile.member_type === "agent" ? (
                <span className="h-1.5 w-1.5 rounded-full bg-success" />
              ) : null}
              <span>{metadata}</span>
            </div>
          ) : null}
        </div>
      </div>

      <ProfileSection title={t(($) => $.profile_popover.description)}>
        <p className="line-clamp-3 text-sm leading-6 text-foreground/85">
          {safeDescription}
        </p>
      </ProfileSection>
      {profile.member_type === "agent" ? (
        <ProfileSection title={t(($) => $.profile_popover.recent_activity)}>
          {(profile.recent_activity ?? []).length > 0 ? (
            <div className="flex flex-col">
              {(profile.recent_activity ?? []).slice(0, 3).map((activity) => (
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
        {activity.summary ? (
          <div className="mt-0.5 truncate text-xs text-muted-foreground">
            {activity.summary}
          </div>
        ) : null}
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
  return null;
}

function activityMeta(
  kind: MemberProfileActivityItem["kind"],
  t: ChannelsT,
): { label: string; icon: LucideIcon } {
  switch (kind) {
    case "queued":
      return { label: t(($) => $.profile_popover.activity.queued), icon: Reply };
    case "failed":
      return { label: t(($) => $.profile_popover.activity.failed), icon: Wrench };
    case "cancelled":
      return { label: t(($) => $.profile_popover.activity.cancelled), icon: MessageSquare };
    case "task":
      return { label: t(($) => $.profile_popover.activity.task), icon: Terminal };
    case "working":
    default:
      return { label: t(($) => $.profile_popover.activity.working), icon: Brain };
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
