"use client";

import { useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { MessageSquare } from "lucide-react";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { agentRunCounts30dOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
  resolveActorIdentityPresentation,
  shouldShowActorHandleLabel,
} from "@multica/core/identity";
import { useWorkspacePaths } from "@multica/core/paths";
import type { Agent, MemberRole } from "@multica/core/types";
import {
  agentListOptions,
  memberListOptions,
  memberProfileOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { avatarGlyph } from "@multica/ui/lib/avatar-fallback";
import { cn } from "@multica/ui/lib/utils";
import { InlineFieldEditor } from "../agents/components/inline-field-editor";
import { useOpenAgentPanel } from "../common/agent-panel-context";
import { ActorAvatar } from "../common/actor-avatar";
import { ConversationSidePanelShell } from "../common/conversation-side-panel-shell";
import { AppLink } from "../navigation";
import { useT } from "../i18n/use-t";

const MAX_PROFILE_DESCRIPTION_LEN = 2000;

interface MemberSidePanelProps {
  userId: string;
  onClose: () => void;
  onMessage?: (userId: string) => void;
  variant?: "panel" | "page";
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
}: MemberSidePanelProps) {
  const { t } = useT("members");
  const { t: tChannels } = useT("channels");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const qc = useQueryClient();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const setUser = useAuthStore((s) => s.setUser);
  const openAgentFromContext = useOpenAgentPanel();
  const { data: members = [], isPending: membersPending } = useQuery(
    memberListOptions(wsId),
  );
  const { data: profile, isPending: profilePending } = useQuery(
    memberProfileOptions(wsId, "user", userId),
  );
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: runCounts = [] } = useQuery(agentRunCounts30dOptions(wsId));

  const member = members.find((m) => m.user_id === userId) ?? null;
  const isSelf = !!currentUserId && currentUserId === userId;
  const isPending = (membersPending && !member) || (profilePending && !profile);

  const displayName = useMemo(() => {
    if (member) return resolveActorDisplayName(member, member.name);
    if (profile) {
      return (
        resolveActorIdentityPresentation(
          { name: profile.name, display_name: profile.display_name },
          "",
        ).displayName || profile.display_name || profile.name
      );
    }
    return "";
  }, [member, profile]);

  const handle = useMemo(() => {
    const identity = member ?? (profile
      ? { name: profile.name, display_name: profile.display_name }
      : null);
    if (!identity) return null;
    return formatActorHandleLabel(resolveActorHandle(identity));
  }, [member, profile]);

  const handleRaw = member
    ? resolveActorHandle(member)
    : profile
      ? resolveActorHandle({ name: profile.name, display_name: profile.display_name })
      : null;
  const showHandle =
    !!handle &&
    handleRaw !== null &&
    shouldShowActorHandleLabel(displayName, handleRaw);

  const description =
    (profile?.description ?? member?.profile_description ?? "").trim();
  const role = (member?.role ?? profile?.role ?? "") as MemberRole | string;
  const email = member?.email?.trim() || "";
  const joinedAt = member?.created_at ?? null;

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

  const saveDescription = async (next: string) => {
    const updated = await api.updateMe({ profile_description: next });
    setUser(updated);
    void qc.invalidateQueries({
      predicate: (q) =>
        q.queryKey[0] === "workspaces" && q.queryKey[2] === "members",
    });
    void qc.invalidateQueries({
      queryKey: workspaceKeys.memberProfile(wsId, "user", userId),
    });
  };

  if (isPending) {
    return (
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={tChannels(($) => $.profile_popover.close_aria)}
        leading={<Skeleton className="h-5 w-28" />}
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
      <ConversationSidePanelShell
        variant={variant}
        onClose={onClose}
        closeAriaLabel={tChannels(($) => $.profile_popover.close_aria)}
        leading={
          <span className="text-sm text-muted-foreground">
            {t(($) => $.card.unavailable)}
          </span>
        }
      >
        <div className="p-4 text-sm text-muted-foreground">
          {t(($) => $.card.unavailable)}
        </div>
      </ConversationSidePanelShell>
    );
  }

  const avatarUrl = resolvePublicFileUrl(
    member?.avatar_url ?? profile?.avatar_url ?? null,
  );
  const canMessage = !!onMessage && !isSelf;
  const youSuffix = isSelf ? ` ${t(($) => $.panel.you_suffix)}` : "";

  const leading = (
    <>
      <ActorAvatarBase
        name={displayName || "?"}
        initials={avatarGlyph(displayName || "?")}
        avatarUrl={avatarUrl}
        size={22}
        toneSeed={`member:${userId}`}
        className="rounded-[5px]"
      />
      <div className="min-w-0 flex-1 leading-tight">
        <div className="truncate text-[12.5px] font-semibold">{displayName}</div>
        {showHandle && handle ? (
          <div className="truncate font-mono text-[11px] text-muted-foreground">
            {handle}
          </div>
        ) : null}
      </div>
    </>
  );

  const actions = canMessage ? (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      onClick={() => onMessage(userId)}
      aria-label={t(($) => $.panel.message_aria)}
      data-testid="member-side-panel-message"
    >
      <MessageSquare className="size-4" />
    </Button>
  ) : null;

  return (
    <ConversationSidePanelShell
      variant={variant}
      onClose={onClose}
      closeAriaLabel={tChannels(($) => $.profile_popover.close_aria)}
      leading={leading}
      actions={actions}
    >
      <div
        className={cn(
          "min-h-0 flex-1 overflow-y-auto px-4 pb-5 pt-3",
          variant === "page" && "px-0",
        )}
        data-testid="member-side-panel"
      >
        <div className="mb-1 flex items-start gap-3">
          <ActorAvatar
            actorType="member"
            actorId={userId}
            size={64}
            avatarUrlHint={member?.avatar_url ?? profile?.avatar_url}
            showStatusDot
            profileLink={false}
            className="rounded-[10px]"
          />
          <div className="min-w-0 flex-1 pt-0.5">
            <p className="truncate text-[15px] font-bold leading-tight">
              {displayName}
            </p>
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
                  href={paths.agentDetail(agent.id)}
                  onOpenPanel={
                    openAgentFromContext
                      ? () =>
                          openAgentFromContext(agent.id, {
                            name: agent.name,
                            display_name: agent.display_name,
                            avatar_url: agent.avatar_url,
                          })
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
  href,
  onOpenPanel,
}: {
  agent: Agent;
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
        profileLink={false}
        className="rounded-md"
      />
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-semibold text-foreground">
          {title}
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
