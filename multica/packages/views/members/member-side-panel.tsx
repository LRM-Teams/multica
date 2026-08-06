"use client";

import { useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { MessageSquare, Pencil } from "lucide-react";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { agentRunCounts30dOptions, agentFleetRankingsOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
  resolveActorIdentityPresentation,
  shouldShowActorHandleLabel,
} from "@multica/core/identity";
import { useWorkspacePaths } from "@multica/core/paths";
import type {
  Agent,
  MemberProfile,
  MemberRole,
  MemberWithUser,
} from "@multica/core/types";
import {
  agentListOptions,
  memberListOptions,
  memberProfileOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { HonorBadgeIcon } from "@multica/ui/components/honor/honor-badge";
import { honorNameDisplayProps } from "@multica/ui/lib/honor-name-display";
import { cn } from "@multica/ui/lib/utils";
import { FleetRankBadge } from "@multica/ui/components/fleet/fleet-class-badge";
import type { AgentFleetRank } from "@multica/core/types/agent-fleet";
import { InlineFieldEditor } from "../agents/components/inline-field-editor";
import { MemberSelfAvatarEditor } from "./member-self-avatar-editor";
import { useOpenAgentPanel } from "../common/agent-panel-context";
import { ActorAvatar } from "../common/actor-avatar";
import { ActorStyledName } from "../common/actor-styled-name";
import { ConversationSidePanelShell } from "../common/conversation-side-panel-shell";
import { AppLink } from "../navigation";
import { HonorWall } from "../honor/honor-wall";
import { useT } from "../i18n/use-t";

const MAX_PROFILE_DESCRIPTION_LEN = 2000;

/** Stable shell leading slot — avoid jsx-as-prop redraw (react-doctor). */
const MEMBER_PANEL_LOADING_LEADING = <Skeleton className="h-5 w-28" />;

interface MemberSidePanelProps {
  userId: string;
  onClose: () => void;
  onMessage?: (userId: string) => void;
  variant?: "panel" | "page";
  /** LRM-877 — conversation Sheet trailing control (「回消息」). */
  doneLabel?: string;
}

/**
 * LRM-619 / LRM-614 lock A — human member Profile dock.
 * Five sections: chrome (Message+Close) → identity → DESCRIPTION → INFO →
 * CREATED AGENTS (full list). Never Agent-pill Role; never silent fake email.
 */
export function MemberSidePanel({
  userId,
  onClose,
  onMessage,
  variant = "panel",
  doneLabel,
}: MemberSidePanelProps) {
  const { t } = useT("members");
  const { t: tChannels } = useT("channels");
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const { data: members = [], isPending: membersPending } = useQuery(
    memberListOptions(wsId),
  );
  const { data: profile, isPending: profilePending } = useQuery(
    memberProfileOptions(wsId, "user", userId),
  );

  const member = members.find((m) => m.user_id === userId) ?? null;
  const isPending = (membersPending && !member) || (profilePending && !profile);
  const closeAriaLabel = tChannels(($) => $.profile_popover.close_aria);
  const unavailableLabel = t(($) => $.card.unavailable);

  if (isPending) {
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={closeAriaLabel}
        leading={MEMBER_PANEL_LOADING_LEADING}
      >
        <div className="space-y-3 p-4" data-testid="member-side-panel-loading">
          <Skeleton className="h-16 w-16 rounded-[10px]" />
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-3 w-24" />
        </div>
      </ConversationSidePanelShell>
    );
  }

  if (!member && !profile) {
    return (
      <MemberUnavailablePanel
        variant={variant}
        onClose={onClose}
        closeAriaLabel={closeAriaLabel}
        label={unavailableLabel}
      />
    );
  }

  return (
    <MemberSidePanelReady
      userId={userId}
      member={member}
      profile={profile}
      isSelf={!!currentUserId && currentUserId === userId}
      onClose={onClose}
      onMessage={onMessage}
      variant={variant}
      doneLabel={doneLabel}
      closeAriaLabel={closeAriaLabel}
    />
  );
}

function MemberUnavailablePanel({
  variant,
  onClose,
  closeAriaLabel,
  label,
}: {
  variant: "panel" | "page";
  onClose: () => void;
  closeAriaLabel: string;
  label: string;
}) {
  const leading = useMemo(
    () => <span className="text-sm text-muted-foreground">{label}</span>,
    [label],
  );
  return (
    <ConversationSidePanelShell
      variant={variant}
      onClose={onClose}
      closeAriaLabel={closeAriaLabel}
      leading={leading}
    >
      <div className="p-4 text-sm text-muted-foreground">{label}</div>
    </ConversationSidePanelShell>
  );
}

function MemberSidePanelReady({
  userId,
  member,
  profile,
  isSelf,
  onClose,
  onMessage,
  variant,
  doneLabel,
  closeAriaLabel,
}: {
  userId: string;
  member: MemberWithUser | null;
  profile: MemberProfile | null | undefined;
  isSelf: boolean;
  onClose: () => void;
  onMessage?: (userId: string) => void;
  variant: "panel" | "page";
  doneLabel?: string;
  closeAriaLabel: string;
}) {
  const { t } = useT("members");
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const paths = useWorkspacePaths();
  const qc = useQueryClient();
  const setUser = useAuthStore((s) => s.setUser);
  const openAgentFromContext = useOpenAgentPanel();
  const { data: honorWall } = useQuery({
    queryKey: ["honor", "wall", userId],
    queryFn: () => api.getUserHonor(userId),
  });
  const { data: honorCompare } = useQuery({
    queryKey: ["honor", "compare", currentUserId, userId],
    queryFn: () => api.compareHonor(userId),
    enabled: Boolean(currentUserId && !isSelf),
  });
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: runCounts = [] } = useQuery(agentRunCounts30dOptions(wsId));
  const { data: fleetRankings = [] } = useQuery(agentFleetRankingsOptions(wsId));
  const fleetByAgentId = useMemo(() => {
    const m = new Map<string, AgentFleetRank>();
    for (const row of fleetRankings) m.set(row.agent_id, row);
    return m;
  }, [fleetRankings]);
  const messageAriaLabel = t(($) => $.panel.message_aria);

  const displayName = useMemo(() => {
    if (member) return resolveActorDisplayName(member, member.name);
    if (profile) {
      return (
        resolveActorIdentityPresentation(
          { name: profile.name, display_name: profile.display_name },
          "",
        ).displayName ||
        profile.display_name ||
        profile.name
      );
    }
    return "";
  }, [member, profile]);

  const handle = useMemo(() => {
    const identity =
      member ??
      (profile
        ? { name: profile.name, display_name: profile.display_name }
        : null);
    if (!identity) return null;
    return formatActorHandleLabel(resolveActorHandle(identity));
  }, [member, profile]);

  const handleRaw = member
    ? resolveActorHandle(member)
    : profile
      ? resolveActorHandle({
          name: profile.name,
          display_name: profile.display_name,
        })
      : null;
  const showHandle =
    !!handle &&
    handleRaw !== null &&
    shouldShowActorHandleLabel(displayName, handleRaw);
  const selfHonor =
    member?.honor ??
    (honorWall
      ? {
          level: honorWall.level,
          name_style: honorWall.name_style,
          equipped_badge: honorWall.equipped_badge,
        }
      : null);
  const selfNameDisplay = selfHonor
    ? honorNameDisplayProps({
        nameStyle: selfHonor.name_style,
        level: selfHonor.level,
        surface: "profile",
      })
    : null;

  const description =
    (profile?.description ?? member?.profile_description ?? "").trim();
  const role = (member?.role ?? profile?.role ?? "") as MemberRole | string;
  const email = member?.email?.trim() || "";
  const joinedAt = member?.created_at ?? null;
  const canMessage = !!onMessage && !isSelf;
  const youSuffix = isSelf ? ` ${t(($) => $.panel.you_suffix)}` : "";

  const runCountById = useMemo(
    () => new Map(runCounts.map((r) => [r.agent_id, r.run_count])),
    [runCounts],
  );
  const ownedAgents = useMemo(() => {
    return agents
      .filter((a) => a.owner_id === userId && !a.archived_at)
      .sort((a, b) => {
        const ra = runCountById.get(a.id) ?? 0;
        const rb = runCountById.get(b.id) ?? 0;
        if (ra !== rb) return rb - ra;
        return resolveActorDisplayName(a, a.id).localeCompare(
          resolveActorDisplayName(b, b.id),
        );
      });
  }, [agents, runCountById, userId]);

  // LRM-812: top bar carries the name text only — no 22px avatar, no handle.
  // The single avatar lives in the body identity block (64px + presence +
  // name + handle), matching the Agent card chrome (resolved-agent-side-panel).
  const identityLeading = useMemo(
    () => (
      <p className="min-w-0 truncate text-sm font-semibold">{displayName}</p>
    ),
    [displayName],
  );

  const messageActions = useMemo(
    () =>
      canMessage ? (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onClick={() => onMessage?.(userId)}
          aria-label={messageAriaLabel}
          data-testid="member-side-panel-message"
        >
          <MessageSquare className="size-4" />
        </Button>
      ) : isSelf ? (
        // LRM-751 — own card keeps a settings-page escape hatch (design gate
        // topbar「编辑资料」outline) alongside the inline entries.
        <AppLink href={`${paths.settings()}?tab=profile`}>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            data-testid="member-side-panel-edit-profile"
          >
            <Pencil className="size-3" />
            {t(($) => $.panel.edit_profile)}
          </Button>
        </AppLink>
      ) : null,
    [canMessage, isSelf, messageAriaLabel, onMessage, paths, t, userId],
  );

  const invalidateProfileCaches = () => {
    void qc.invalidateQueries({
      predicate: (q) =>
        q.queryKey[0] === "workspaces" && q.queryKey[2] === "members",
    });
    void qc.invalidateQueries({
      queryKey: workspaceKeys.memberProfile(wsId, "user", userId),
    });
  };

  const saveDescription = async (next: string) => {
    const updated = await api.updateMe({ profile_description: next });
    setUser(updated);
    invalidateProfileCaches();
  };

  const saveName = async (next: string) => {
    const updated = await api.updateMe({ display_name: next.trim() });
    setUser(updated);
    invalidateProfileCaches();
  };

  return (
    <ConversationSidePanelShell
      variant={variant}
      onClose={onClose}
      closeAriaLabel={closeAriaLabel}
      doneLabel={doneLabel}
      leading={identityLeading}
      actions={messageActions}
    >
      <div
        className={cn(
          "min-h-0 flex-1 overflow-y-auto px-4 pb-5 pt-3",
          variant === "page" && "px-0",
        )}
        data-testid="member-side-panel"
      >
        <div className="mb-1 flex items-start gap-3">
          {isSelf ? (
            <MemberSelfAvatarEditor userId={userId}>
              <ActorAvatar
                actorType="member"
                actorId={userId}
                size={64}
                avatarUrlHint={member?.avatar_url ?? profile?.avatar_url}
                showStatusDot
                profileLink={false}
                className="rounded-[10px]"
              />
            </MemberSelfAvatarEditor>
          ) : (
            <ActorAvatar
              actorType="member"
              actorId={userId}
              size={64}
              avatarUrlHint={member?.avatar_url ?? profile?.avatar_url}
              showStatusDot
              profileLink={false}
              className="rounded-[10px]"
            />
          )}
          <div className="min-w-0 flex-1 pt-0.5">
            {isSelf ? (
              <InlineFieldEditor
                value={displayName}
                onSave={saveName}
                kind="input"
                label={t(($) => $.panel.name_label)}
                placeholder={t(($) => $.panel.name_placeholder)}
                maxLength={80}
                validate={(draft) =>
                  draft.trim() ? null : t(($) => $.panel.name_required)
                }
                displayContent={
                  <span className="inline-flex min-w-0 items-center gap-1.5">
                    <span
                      className={selfNameDisplay?.className}
                      data-honor-glow-tier={
                        selfNameDisplay?.["data-honor-glow-tier"]
                      }
                      style={selfNameDisplay?.style}
                    >
                      {displayName}
                    </span>
                    {selfHonor?.equipped_badge ? (
                      <HonorBadgeIcon
                        svgKey={selfHonor.equipped_badge.svg_key}
                        title={selfHonor.equipped_badge.title}
                        medal
                      />
                    ) : null}
                  </span>
                }
                displayClassName="truncate text-[15px] font-bold leading-tight"
                testId="member-profile-name"
              />
            ) : (
              <ActorStyledName
                displayName={displayName}
                honor={member?.honor}
                honorSurface="profile"
                className="text-[15px] font-bold leading-tight"
              />
            )}
            {(showHandle && handle) || youSuffix ? (
              <p className="mt-1 truncate text-xs text-muted-foreground">
                {showHandle && handle ? handle : null}
                {youSuffix}
              </p>
            ) : null}
          </div>
        </div>

        <section className="mt-3.5 border-t border-border pt-3">
          <h3 className="mb-2 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-[0.05em] text-muted-foreground">
            {t(($) => $.panel.description)}
          </h3>
          {isSelf ? (
            <InlineFieldEditor
              value={description}
              onSave={saveDescription}
              kind="textarea"
              label={t(($) => $.panel.description)}
              emptyLabel={t(($) => $.panel.no_description)}
              placeholder={t(($) => $.panel.description_placeholder)}
              maxLength={MAX_PROFILE_DESCRIPTION_LEN}
              displayClassName="text-sm leading-5 text-foreground/90"
              testId="member-profile-description"
            />
          ) : description ? (
            <p className="whitespace-pre-wrap break-words text-sm leading-5 text-foreground/90">
              {description}
            </p>
          ) : (
            <p className="text-sm text-muted-foreground">
              {t(($) => $.panel.no_description)}
            </p>
          )}
        </section>

        {honorWall ? (
          <section className="mt-3.5 border-t border-border pt-3">
            <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.05em] text-muted-foreground">
              {t(($) => $.panel.honor_title)}
            </h3>
            <HonorWall
              wall={honorWall}
              completionLabel={t(($) => $.panel.honor_completion, {
                unlocked: honorWall.badges_unlocked ?? honorWall.unlocked_badges.length,
                total: honorWall.badges_total ?? honorWall.unlocked_badges.length,
              })}
              statsLabel={t(($) => $.panel.honor_stats, {
                unlocked: honorWall.badges_unlocked ?? honorWall.unlocked_badges.length,
                total: honorWall.badges_total ?? honorWall.unlocked_badges.length,
                level: honorWall.level,
              })}
              showcaseTitle={t(($) => $.panel.honor_showcase)}
              recentTitle={t(($) => $.panel.honor_recent)}
              compare={honorCompare ?? null}
              compareTitle={!isSelf ? t(($) => $.panel.honor_compare) : undefined}
              sharedTitle={t(($) => $.panel.honor_shared)}
              youOnlyTitle={t(($) => $.panel.honor_you_only)}
              themOnlyTitle={t(($) => $.panel.honor_them_only)}
            />
          </section>
        ) : null}

        <section className="mt-3.5 border-t border-border pt-3">
          <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.05em] text-muted-foreground">
            {t(($) => $.panel.info)}
          </h3>
          <dl className="space-y-1.5 text-xs">
            <InfoRow label={t(($) => $.panel.role)}>
              <RoleSoftPill role={role} />
            </InfoRow>
            {email ? (
              <InfoRow label={t(($) => $.panel.email)}>
                <span className="break-all text-foreground">{email}</span>
              </InfoRow>
            ) : null}
            {joinedAt ? (
              <InfoRow label={t(($) => $.panel.joined)}>
                <span className="text-foreground">
                  {formatJoinedDate(joinedAt)}
                </span>
              </InfoRow>
            ) : null}
          </dl>
        </section>

        <section className="mt-3.5 border-t border-border pt-3">
          <h3 className="mb-2 flex items-center justify-between gap-2 text-[11px] font-semibold uppercase tracking-[0.05em] text-muted-foreground">
            <span>{t(($) => $.panel.created_agents)}</span>
            <span className="font-medium tabular-nums">{ownedAgents.length}</span>
          </h3>
          {ownedAgents.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              {t(($) => $.panel.no_agents)}
            </p>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {ownedAgents.map((agent) => (
                <CreatedAgentRow
                  key={agent.id}
                  agent={agent}
                  fleet={fleetByAgentId.get(agent.id)}
                  href={paths.agentDetail(agent.id)}
                  onOpenPanel={
                    openAgentFromContext
                      ? () =>
                          openAgentFromContext(
                            agent.id,
                            {
                              name: agent.name,
                              display_name: agent.display_name,
                              avatar_url: agent.avatar_url,
                            },
                            { returnToMemberId: userId },
                          )
                      : undefined
                  }
                />
              ))}
            </ul>
          )}
        </section>
      </div>
    </ConversationSidePanelShell>
  );
}

function InfoRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-2">
      <dt className="w-14 shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1">{children}</dd>
    </div>
  );
}

function RoleSoftPill({ role }: { role: string }) {
  const { t } = useT("members");
  const normalized = role.toLowerCase();
  const label =
    normalized === "owner"
      ? t(($) => $.role.owner)
      : normalized === "admin"
        ? t(($) => $.role.admin)
        : t(($) => $.role.member);
  const tone =
    normalized === "owner"
      ? "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300"
      : normalized === "admin"
        ? "border-amber-500/20 bg-amber-500/5 text-amber-800/80 dark:text-amber-200/80"
        : "border-border bg-muted/60 text-muted-foreground";
  return (
    <span
      data-testid="member-role-soft-pill"
      className={cn(
        "inline-flex rounded-full border px-2 py-0.5 text-[11px] font-semibold",
        tone,
      )}
    >
      {label}
    </span>
  );
}

function CreatedAgentRow({
  agent,
  fleet,
  href,
  onOpenPanel,
}: {
  agent: Agent;
  fleet?: AgentFleetRank;
  href: string;
  onOpenPanel?: () => void;
}) {
  const displayName = resolveActorDisplayName(agent, agent.id);
  const roleHint = agent.description?.trim().split("\n")[0]?.trim() ?? "";
  const title =
    roleHint && roleHint.length <= 40 && roleHint !== displayName
      ? `${displayName} - ${roleHint}`
      : displayName;
  const runtimeLabel =
    agent.model?.trim() ||
    agent.runtime_name?.trim() ||
    (agent.runtime_mode === "cloud" ? "Cloud" : agent.runtime_mode) ||
    "";

  const body = (
    <>
      <ActorAvatar
        actorType="agent"
        actorId={agent.id}
        size={28}
        avatarUrlHint={agent.avatar_url}
        showStatusDot
        fleetRank={fleet?.fleet_rank}
        profileLink={false}
        className="rounded-md"
      />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1">
          <div className="truncate text-xs font-semibold text-foreground">{title}</div>
          {fleet ? (
            <FleetRankBadge
              classId={fleet.class_id}
              classLabel={fleet.class_label}
              fleetRank={fleet.fleet_rank}
              frozen={fleet.frozen}
              medal
            />
          ) : null}
        </div>
        {runtimeLabel ? (
          <div className="truncate text-[11px] text-muted-foreground">
            {runtimeLabel}
          </div>
        ) : null}
      </div>
    </>
  );

  if (onOpenPanel) {
    return (
      <li data-testid="member-created-agent-row">
        <button
          type="button"
          onClick={onOpenPanel}
          className="flex w-full items-center gap-2.5 rounded-lg border border-border px-2.5 py-2 text-left transition-colors hover:bg-accent/60"
        >
          {body}
        </button>
      </li>
    );
  }

  return (
    <li data-testid="member-created-agent-row">
      <AppLink
        href={href}
        className="flex items-center gap-2.5 rounded-lg border border-border px-2.5 py-2 transition-colors hover:bg-accent/60"
      >
        {body}
      </AppLink>
    </li>
  );
}

function formatJoinedDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}
