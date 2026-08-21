"use client";

import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, MessageSquare, Pencil, Trash2 } from "lucide-react";
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
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { honorNameDisplayProps } from "@multica/ui/lib/honor-name-display";
import { cn } from "@multica/ui/lib/utils";
import type { AgentFleetRank } from "@multica/core/types/agent-fleet";
import { InlineFieldEditor } from "../agents/components/inline-field-editor";
import { AgentHonorLevelIcon } from "../agents/components/agent-honor-level-icon";
import { MemberSelfAvatarEditor } from "./member-self-avatar-editor";
import { useOpenAgentPanel } from "../common/agent-panel-context";
import { ActorAvatar } from "../common/actor-avatar";
import { ActorStyledName } from "../common/actor-styled-name";
import { ProfileField, ProfileSectionHeading } from "../common/profile-field";
import { ConversationSidePanelShell } from "../common/conversation-side-panel-shell";
import { useOpenDM } from "../common/use-open-dm";
import { AppLink } from "../navigation";
import { HonorWall } from "../honor/honor-wall";
import { UserHonorLevelIcon } from "../honor/user-honor-level-icon";
import { RolesDialog } from "../settings/components/roles-dialog";
import { useT } from "../i18n/use-t";
import { Time } from "../i18n";

const MAX_PROFILE_DESCRIPTION_LEN = 2000;

/** Stable shell leading slot — avoid jsx-as-prop redraw (react-doctor). */
const MEMBER_PANEL_LOADING_LEADING = <Skeleton className="h-5 w-28" />;

/** Directory-only manage surface (role pencil + Remove in Actions). */
export type MemberDirectoryManage = {
  member: MemberWithUser;
  canEditRole: boolean;
  canRemove: boolean;
  roleOptions: MemberRole[];
  busy: boolean;
  onRoleChange: (role: MemberRole) => Promise<void>;
  onRemove: () => Promise<void>;
};

interface MemberSidePanelProps {
  userId: string;
  onClose: () => void;
  onMessage?: (userId: string) => void;
  variant?: "panel" | "page";
  /** LRM-877 — conversation Sheet trailing control (「回消息」). */
  doneLabel?: string;
  /** Members Directory: hide dock Close/Done; identity is floating header. */
  hideDismiss?: boolean;
  /** Members Directory manage affordances (no bolted footer). */
  directoryManage?: MemberDirectoryManage | null;
}

/**
 * LRM-619 / LRM-614 lock A — human member Profile dock.
 * UI skeleton aligned with agent profile (identity → fields → honor → info →
 * actions). No agent tabs (Activity/…). Never Agent-pill Role; never silent fake email.
 */
export function MemberSidePanel({
  userId,
  onClose,
  onMessage,
  variant = "panel",
  doneLabel,
  hideDismiss = false,
  directoryManage = null,
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
  // Directory embeds use floating chrome (identity is the header), matching agent.
  const shellHeader = hideDismiss ? "floating" : "bar";

  if (isPending) {
    return (
      <ConversationSidePanelShell
        variant={variant}
        hideDismiss={hideDismiss}
        header={shellHeader}
        onClose={onClose}
        closeAriaLabel={closeAriaLabel}
        leading={hideDismiss ? undefined : MEMBER_PANEL_LOADING_LEADING}
      >
        <div className="space-y-3 p-4" data-testid="member-side-panel-loading">
          <Skeleton className="h-16 w-16 rounded-full" />
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
        hideDismiss={hideDismiss}
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
      hideDismiss={hideDismiss}
      closeAriaLabel={closeAriaLabel}
      directoryManage={directoryManage}
    />
  );
}

function MemberUnavailablePanel({
  variant,
  onClose,
  closeAriaLabel,
  label,
  hideDismiss = false,
}: {
  variant: "panel" | "page";
  onClose: () => void;
  closeAriaLabel: string;
  label: string;
  hideDismiss?: boolean;
}) {
  const leading = useMemo(
    () => <span className="text-sm text-muted-foreground">{label}</span>,
    [label],
  );
  return (
    <ConversationSidePanelShell
      variant={variant}
      hideDismiss={hideDismiss}
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
  hideDismiss = false,
  closeAriaLabel,
  directoryManage = null,
}: {
  userId: string;
  member: MemberWithUser | null;
  profile: MemberProfile | null | undefined;
  isSelf: boolean;
  onClose: () => void;
  onMessage?: (userId: string) => void;
  hideDismiss?: boolean;
  variant: "panel" | "page";
  doneLabel?: string;
  closeAriaLabel: string;
  directoryManage?: MemberDirectoryManage | null;
}) {
  const { t } = useT("members");
  const { t: tChannels } = useT("channels");
  const { t: tSettings } = useT("settings");
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const paths = useWorkspacePaths();
  const qc = useQueryClient();
  const setUser = useAuthStore((s) => s.setUser);
  const openAgentFromContext = useOpenAgentPanel();
  const { openDM, isPending: openingDM } = useOpenDM();
  const [roleDialogOpen, setRoleDialogOpen] = useState(false);
  const [roleSaving, setRoleSaving] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
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
  const shellHeader = hideDismiss ? "floating" : "bar";

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

  // Dock chrome (conversation): name + Message. Directory: floating, no bar actions.
  const identityLeading = useMemo(
    () => (
      <p className="min-w-0 truncate text-sm font-semibold">{displayName}</p>
    ),
    [displayName],
  );

  // Conversation dock keeps Message in chrome; directory moves Message into Actions.
  const chromeActions = useMemo(() => {
    if (hideDismiss) return null;
    if (canMessage) {
      return (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onClick={() => onMessage?.(userId)}
          aria-label={messageAriaLabel}
          data-testid="member-side-panel-message"
        >
          <MessageSquare className="size-4" aria-hidden />
        </Button>
      );
    }
    if (isSelf) {
      return (
        <AppLink href={`${paths.settings()}?tab=profile`}>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            data-testid="member-side-panel-edit-profile"
          >
            <Pencil className="size-3" aria-hidden />
            {t(($) => $.panel.edit_profile)}
          </Button>
        </AppLink>
      );
    }
    return null;
  }, [
    hideDismiss,
    canMessage,
    isSelf,
    messageAriaLabel,
    onMessage,
    paths,
    t,
    userId,
  ]);

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

  const roleLabel =
    role === "owner"
      ? t(($) => $.role.owner)
      : role === "admin"
        ? t(($) => $.role.admin)
        : t(($) => $.role.member);

  const handleMessage = () => {
    if (onMessage) {
      onMessage(userId);
      return;
    }
    void openDM({ peer_type: "user", peer_id: userId });
  };

  // Directory embed: Message (+ Remove) in Actions, matching agent profile.
  // Conversation dock: Message stays in chrome only — do not double-render.
  const canEditRole = directoryManage?.canEditRole === true;
  const canRemove = directoryManage?.canRemove === true;
  const showActionMessage = !isSelf && hideDismiss;
  const showActions = showActionMessage || canRemove;

  return (
    <ConversationSidePanelShell
      variant={variant}
      hideDismiss={hideDismiss}
      header={shellHeader}
      onClose={onClose}
      closeAriaLabel={closeAriaLabel}
      doneLabel={doneLabel}
      leading={hideDismiss ? undefined : identityLeading}
      actions={chromeActions}
    >
      <div
        className={cn(
          "min-h-0 flex-1 overflow-y-auto px-3 pb-5 pt-3 md:px-4",
          variant === "page" && "px-4",
        )}
        data-testid="member-side-panel"
      >
        {/* Identity — matches agent floating header row */}
        <div className="mb-1 flex items-start gap-3">
          {isSelf ? (
            <MemberSelfAvatarEditor userId={userId}>
              <ActorAvatar
                actorType="member"
                actorId={userId}
                size={56}
                avatarUrlHint={member?.avatar_url ?? profile?.avatar_url}
                showStatusDot
                profileLink={false}
                className="rounded-full"
              />
            </MemberSelfAvatarEditor>
          ) : (
            <ActorAvatar
              actorType="member"
              actorId={userId}
              size={56}
              avatarUrlHint={member?.avatar_url ?? profile?.avatar_url}
              showStatusDot
              profileLink={false}
              className="rounded-full"
            />
          )}
          <div className="min-w-0 flex-1 pt-0.5">
            <div className="flex min-w-0 items-center gap-1.5">
              {isSelf ? (
                <span
                  className={cn(
                    "truncate text-[17px] font-bold leading-tight",
                    selfNameDisplay?.className,
                  )}
                  data-honor-glow-tier={
                    selfNameDisplay?.["data-honor-glow-tier"]
                  }
                  style={selfNameDisplay?.style}
                >
                  {displayName}
                  {youSuffix}
                </span>
              ) : (
                <ActorStyledName
                  displayName={displayName}
                  honor={member?.honor}
                  honorSurface="profile"
                  className="text-[17px] font-bold leading-tight"
                />
              )}
              {selfHonor ? (
                <UserHonorLevelIcon
                  level={selfHonor.level}
                  title={tChannels(($) => $.profile_popover.honor.level_value, {
                    level: selfHonor.level,
                  })}
                  className="size-6 shrink-0 drop-shadow-sm"
                />
              ) : null}
            </div>
            {showHandle && handle ? (
              <p className="mt-0.5 truncate text-[13px] text-muted-foreground">
                {handle}
              </p>
            ) : null}
          </div>
        </div>

        <div className="mt-4 space-y-4">
          {/* Display name + Description — agent ProfileField rhythm */}
          <ProfileField label={t(($) => $.panel.display_name_label)}>
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
                displayClassName="text-[13px] leading-5"
                testId="member-profile-name"
              />
            ) : (
              <p className="text-[13px] leading-5">{displayName}</p>
            )}
          </ProfileField>

          <ProfileField label={t(($) => $.panel.description)}>
            {isSelf ? (
              <InlineFieldEditor
                value={description}
                onSave={saveDescription}
                kind="textarea"
                label={t(($) => $.panel.description)}
                emptyLabel={t(($) => $.panel.no_description)}
                placeholder={t(($) => $.panel.description_placeholder)}
                maxLength={MAX_PROFILE_DESCRIPTION_LEN}
                displayClassName="text-[13px] leading-5 text-foreground/85"
                testId="member-profile-description"
              />
            ) : (
              <p className="text-[13px] leading-5 text-foreground/85">
                {description || t(($) => $.panel.no_description)}
              </p>
            )}
          </ProfileField>

          {honorWall ? (
            <section className="border-t border-border pt-3">
              <div className="mb-2">
              <ProfileSectionHeading label={t(($) => $.panel.honor_title)} />
            </div>
              <HonorWall
                wall={honorWall}
                completionLabel={t(($) => $.panel.honor_completion, {
                  unlocked:
                    honorWall.badges_unlocked ??
                    honorWall.unlocked_badges.length,
                  total:
                    honorWall.badges_total ?? honorWall.unlocked_badges.length,
                })}
                statsLabel={t(($) => $.panel.honor_stats, {
                  unlocked:
                    honorWall.badges_unlocked ??
                    honorWall.unlocked_badges.length,
                  total:
                    honorWall.badges_total ?? honorWall.unlocked_badges.length,
                  level: honorWall.level,
                })}
                showcaseTitle={t(($) => $.panel.honor_showcase)}
                recentTitle={t(($) => $.panel.honor_recent)}
                compare={honorCompare ?? null}
                compareTitle={
                  !isSelf ? t(($) => $.panel.honor_compare) : undefined
                }
                sharedTitle={t(($) => $.panel.honor_shared)}
                youOnlyTitle={t(($) => $.panel.honor_you_only)}
                themOnlyTitle={t(($) => $.panel.honor_them_only)}
              />
            </section>
          ) : null}

          <section className="border-t border-border pt-3">
            <div className="mb-2">
              <ProfileSectionHeading label={t(($) => $.panel.info)} />
            </div>
            <div className="grid grid-cols-[100px_minmax(0,1fr)] gap-x-3 gap-y-2 text-[13px]">
              <span className="pt-0.5 text-muted-foreground">
                {t(($) => $.panel.role)}
              </span>
              <div className="flex min-w-0 items-center gap-1">
                <span
                  className="truncate"
                  data-testid="member-role-value"
                >
                  {roleLabel}
                </span>
                {canEditRole ? (
                  <button
                    type="button"
                    className="inline-flex shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    onClick={() => setRoleDialogOpen(true)}
                    aria-label={t(($) => $.panel.change_role_aria)}
                    data-testid="member-role-edit"
                  >
                    <Pencil className="size-3.5" aria-hidden />
                  </button>
                ) : null}
              </div>
              {email ? (
                <>
                  <span className="text-muted-foreground">
                    {t(($) => $.panel.email)}
                  </span>
                  <span className="min-w-0 break-all">{email}</span>
                </>
              ) : null}
              {joinedAt ? (
                <>
                  <span className="text-muted-foreground">
                    {t(($) => $.panel.joined)}
                  </span>
                  <Time kind="date" value={joinedAt} className="truncate" />
                </>
              ) : null}
            </div>
          </section>

          <section className="border-t border-border pt-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <ProfileSectionHeading label={t(($) => $.panel.created_agents)} />
              <span className="text-[11px] font-medium tabular-nums text-muted-foreground/80">
                {ownedAgents.length}
              </span>
            </div>
            {ownedAgents.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">
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

          {showActions ? (
            <section
              className="border-t border-border pt-3"
              aria-label={t(($) => $.panel.actions_section)}
              data-testid="member-profile-actions"
            >
              <div className="mb-2">
              <ProfileSectionHeading label={t(($) => $.panel.actions_section)} />
            </div>
              <div className="flex flex-col gap-2">
                {showActionMessage ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="lg"
                    className="w-full gap-2"
                    data-testid="member-profile-action-message"
                    disabled={openingDM}
                    onClick={handleMessage}
                  >
                    {openingDM ? (
                      <Loader2
                        className="size-4 shrink-0 animate-spin"
                        aria-hidden
                      />
                    ) : (
                      <MessageSquare className="size-4 shrink-0" aria-hidden />
                    )}
                    {t(($) => $.panel.message_button)}
                  </Button>
                ) : null}
                {canRemove ? (
                  <div
                    className={
                      showActionMessage
                        ? "mt-1 border-t border-border pt-3"
                        : undefined
                    }
                  >
                    <Button
                      type="button"
                      variant="destructive"
                      size="lg"
                      className="w-full gap-2 bg-destructive text-white hover:bg-destructive/90"
                      data-testid="member-directory-remove"
                      disabled={directoryManage?.busy}
                      onClick={() => setConfirmRemove(true)}
                    >
                      <Trash2 className="size-4 shrink-0" aria-hidden />
                      {t(($) => $.directory.remove_member)}
                    </Button>
                  </div>
                ) : null}
              </div>
            </section>
          ) : null}
        </div>
      </div>

      {directoryManage && canEditRole ? (
        <RolesDialog
          open={roleDialogOpen}
          onOpenChange={setRoleDialogOpen}
          mode="select"
          value={(directoryManage.member.role ?? "member") as MemberRole}
          allowedRoles={directoryManage.roleOptions}
          saving={roleSaving || directoryManage.busy}
          onSave={async (next) => {
            setRoleSaving(true);
            try {
              await directoryManage.onRoleChange(next);
              setRoleDialogOpen(false);
            } finally {
              setRoleSaving(false);
            }
          }}
          title={tSettings(($) => $.members.change_role)}
          subtitle={t(($) => $.panel.role_dialog_subtitle)}
        />
      ) : null}

      {directoryManage && canRemove ? (
        <AlertDialog open={confirmRemove} onOpenChange={setConfirmRemove}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t(($) => $.directory.remove_confirm_title, {
                  name: displayName,
                })}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.directory.remove_confirm_description)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>
                {t(($) => $.directory.cancel)}
              </AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                onClick={() => {
                  void directoryManage
                    .onRemove()
                    .finally(() => setConfirmRemove(false));
                }}
              >
                {t(($) => $.directory.remove_member)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}
    </ConversationSidePanelShell>
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
  const { t } = useT("channels");
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
          {agent.honor_level ? (
            <AgentHonorLevelIcon
              level={agent.honor_level}
              title={t(($) => $.profile_popover.honor.level_value, {
                level: agent.honor_level,
              })}
              className="size-6 drop-shadow-sm"
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

