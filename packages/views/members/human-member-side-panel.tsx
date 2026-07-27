"use client";

import { useMemo } from "react";
import { MessageSquare, X } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
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
import { memberProfileOptions } from "@multica/core/agents";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import {
  agentListOptions,
  memberListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import type { Agent, MemberRole } from "@multica/core/types";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { avatarGlyph } from "@multica/ui/lib/avatar-fallback";
import { cn } from "@multica/ui/lib/utils";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { ActorAvatar } from "../common/actor-avatar";
import { useOpenAgentPanel } from "../common/agent-panel-context";
import { useOpenDM } from "../common/use-open-dm";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { InlineFieldEditor } from "../agents/components/inline-field-editor";
import { useT } from "../i18n/use-t";

/** Mirror server MaxProfileDescriptionLen (auth.go). */
const MAX_PROFILE_DESCRIPTION_LEN = 2000;

const PROVIDER_SUBTITLE: Record<string, string> = {
  claude: "Claude Code",
  codex: "Codex CLI",
  opencode: "OpenCode",
  openclaw: "OpenClaw",
  cursor: "Cursor",
  gemini: "Gemini",
  hermes: "Hermes",
  kimi: "Kimi",
  pi: "Pi",
  copilot: "Copilot",
};

interface HumanMemberSidePanelProps {
  userId: string;
  onClose: () => void;
  /** Mobile full-page host reuses the same IA without dock chrome close-X. */
  variant?: "panel" | "page";
}

/**
 * LRM-619 / LRM-614 Lock A — human member Profile dock.
 * Five sections: topbar · identity · DESCRIPTION · INFO · CREATED AGENTS.
 * Never Agent diagnostic chrome (Restart/Reset). Email only when the
 * workspace member list exposes it (no silent fake).
 */
export function HumanMemberSidePanel({
  userId,
  onClose,
  variant = "panel",
}: HumanMemberSidePanelProps) {
  const { t } = useT("members");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const setAuthUser = useAuthStore((s) => s.setUser);
  const { openDM, isPending: dmPending } = useOpenDM();
  const openAgentFromContext = useOpenAgentPanel();
  const openAgentFromStore = useAgentPanelStore((s) => s.open);
  const openAgent = openAgentFromContext ?? openAgentFromStore;

  const { data: profile, isPending: profilePending, isError: profileError } = useQuery(
    memberProfileOptions(wsId, "user", userId),
  );
  const { data: members = [], isPending: membersPending } = useQuery(
    memberListOptions(wsId),
  );
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: runCounts = [] } = useQuery(agentRunCounts30dOptions(wsId));
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));

  const member = members.find((m) => m.user_id === userId) ?? null;
  const isSelf = !!currentUserId && currentUserId === userId;
  const isLoading = (profilePending || membersPending) && !profile && !member;

  const ownedAgents = useMemo(() => {
    const runCountById = new Map(runCounts.map((r) => [r.agent_id, r.run_count]));
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
  }, [agents, runCounts, userId]);

  if (isLoading) {
    return (
      <aside
        className={cn(
          "flex h-full min-h-0 w-full flex-col bg-background",
          variant === "panel" && "border-l",
        )}
        data-testid="human-member-side-panel"
      >
        <div className="flex items-center gap-2 border-b px-3 py-2.5">
          <Skeleton className="size-[22px] rounded-md" />
          <Skeleton className="h-4 w-28" />
        </div>
        <div className="flex gap-3 p-4">
          <Skeleton className="size-16 rounded-[10px]" />
          <div className="flex-1 space-y-2 pt-1">
            <Skeleton className="h-5 w-32" />
            <Skeleton className="h-3 w-24" />
          </div>
        </div>
      </aside>
    );
  }

  if (profileError || (!profile && !member)) {
    return (
      <aside
        className={cn(
          "flex h-full min-h-0 w-full flex-col bg-background",
          variant === "panel" && "border-l",
        )}
        data-testid="human-member-side-panel"
      >
        <PanelTopBar
          variant={variant}
          displayName={t(($) => $.side_panel.unavailable)}
          handleLabel={null}
          avatar={null}
          showMessage={false}
          onClose={onClose}
          onMessage={() => {}}
          dmPending={false}
          messageAria={t(($) => $.side_panel.message_aria)}
          closeAria={t(($) => $.side_panel.close_aria)}
        />
        <div className="p-4 text-xs text-muted-foreground">
          {t(($) => $.side_panel.unavailable)}
        </div>
      </aside>
    );
  }

  const identity = {
    name: profile?.name ?? member?.name ?? "",
    display_name: profile?.display_name ?? member?.display_name ?? "",
  };
  const presentation = resolveActorIdentityPresentation(identity, "");
  const displayName =
    presentation.displayName ||
    presentation.handle ||
    t(($) => $.side_panel.unknown);
  const handle = resolveActorHandle(identity);
  const handleLabel = formatActorHandleLabel(handle);
  const showHandle =
    handleLabel !== null && shouldShowActorHandleLabel(displayName, handle);
  const description =
    (profile?.description ?? member?.profile_description ?? "").trim();
  const role = (profile?.role ?? member?.role ?? "member") as MemberRole | string;
  const roleKey =
    role === "owner" || role === "admin" || role === "member" ? role : "member";
  const roleLabel = t(($) => $.role[roleKey]);
  // Email: only from workspace member list (permission-gated by ListMembers).
  // GetMemberProfile deliberately omits email — never invent from elsewhere.
  const email = member?.email?.trim() || null;
  const joinedAt = member?.created_at ?? null;
  const joinedLabel = joinedAt ? formatJoinedDate(joinedAt) : null;
  const avatarUrl = resolvePublicFileUrl(
    profile?.avatar_url ?? member?.avatar_url ?? null,
  );

  const saveDescription = async (next: string) => {
    const updated = await api.updateMe({ profile_description: next });
    setAuthUser(updated);
    void qc.invalidateQueries({
      queryKey: workspaceKeys.memberProfile(wsId, "user", userId),
    });
    void qc.invalidateQueries({
      predicate: (q) => q.queryKey[0] === "workspaces" && q.queryKey[2] === "members",
    });
    // agents memberProfileKeys uses a different key shape — invalidate broadly.
    void qc.invalidateQueries({
      predicate: (q) =>
        Array.isArray(q.queryKey) &&
        q.queryKey.includes("member-profiles") &&
        q.queryKey.includes(userId),
    });
  };

  const handleMessage = () => {
    void openDM({ peer_type: "user", peer_id: userId }).then(() => {
      onClose();
    });
  };

  const miniAvatar = (
    <ActorAvatarBase
      name={displayName}
      initials={avatarGlyph(displayName)}
      avatarUrl={avatarUrl}
      isAgent={false}
      size={22}
      toneSeed={`member:${userId}`}
      className="rounded-md"
    />
  );
  const bigAvatar = (
    <ActorAvatarBase
      name={displayName}
      initials={avatarGlyph(displayName)}
      avatarUrl={avatarUrl}
      isAgent={false}
      size={64}
      toneSeed={`member:${userId}`}
      className="rounded-[10px]"
    />
  );

  return (
    <aside
      className={cn(
        "flex h-full min-h-0 w-full flex-col bg-background",
        variant === "panel" && "border-l",
      )}
      data-testid="human-member-side-panel"
    >
      <PanelTopBar
        variant={variant}
        displayName={displayName}
        handleLabel={showHandle ? handleLabel : null}
        avatar={miniAvatar}
        showMessage={!isSelf}
        onClose={onClose}
        onMessage={handleMessage}
        dmPending={dmPending}
        messageAria={t(($) => $.side_panel.message_aria)}
        closeAria={t(($) => $.side_panel.close_aria)}
      />

      <div className="min-h-0 flex-1 overflow-y-auto px-3.5 pb-5 pt-3.5">
        {/* Identity */}
        <div
          className="mb-1 flex items-start gap-3"
          data-testid="human-member-identity"
        >
          {bigAvatar}
          <div className="min-w-0 flex-1 pt-0.5">
            <p className="truncate text-lg font-bold leading-tight">{displayName}</p>
            {showHandle && handleLabel ? (
              <p className="mt-1 truncate font-mono text-xs text-muted-foreground">
                {handleLabel}
                {isSelf ? (
                  <span className="ml-1 font-sans">{t(($) => $.side_panel.you_suffix)}</span>
                ) : null}
              </p>
            ) : isSelf ? (
              <p className="mt-1 text-xs text-muted-foreground">
                {t(($) => $.side_panel.you_suffix).replace(/[()]/g, "")}
              </p>
            ) : null}
          </div>
        </div>

        {/* DESCRIPTION */}
        <section
          className="mt-3.5 border-t border-border pt-3"
          data-testid="human-member-description"
        >
          <div className="mb-2 flex items-center gap-1.5">
            <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
              {t(($) => $.side_panel.description)}
            </h3>
          </div>
          {isSelf ? (
            <InlineFieldEditor
              value={description}
              onSave={saveDescription}
              kind="textarea"
              label={t(($) => $.side_panel.description)}
              placeholder={t(($) => $.side_panel.description_placeholder)}
              emptyLabel={t(($) => $.side_panel.no_description)}
              maxLength={MAX_PROFILE_DESCRIPTION_LEN}
              displayClassName={cn(
                !description && "italic text-muted-foreground",
              )}
              testId="human-member-description-editor"
            />
          ) : description ? (
            <p className="text-[12.5px] leading-5 text-foreground/90">{description}</p>
          ) : (
            <p className="text-[12.5px] italic text-muted-foreground">
              {t(($) => $.side_panel.no_description)}
            </p>
          )}
        </section>

        {/* INFO */}
        <section
          className="mt-3.5 border-t border-border pt-3"
          data-testid="human-member-info"
        >
          <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.side_panel.info)}
          </h3>
          <div className="grid gap-2.5">
            <InfoRow label={t(($) => $.side_panel.role)}>
              <span
                className="inline-flex items-center rounded-full border border-amber-500/30 bg-amber-500/10 px-2.5 py-0.5 text-xs font-semibold text-amber-700 dark:text-amber-300"
                data-testid="human-member-role-pill"
              >
                {roleLabel}
              </span>
            </InfoRow>
            {email ? (
              <InfoRow label={t(($) => $.side_panel.email)}>
                <span className="font-mono text-[12.5px]">{email}</span>
              </InfoRow>
            ) : null}
            {joinedLabel ? (
              <InfoRow label={t(($) => $.side_panel.joined)}>
                <span className="font-mono text-[12.5px]">{joinedLabel}</span>
              </InfoRow>
            ) : null}
          </div>
        </section>

        {/* CREATED AGENTS */}
        <section
          className="mt-3.5 border-t border-border pt-3"
          data-testid="human-member-created-agents"
        >
          <div className="mb-2 flex items-center gap-1.5">
            <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
              {t(($) => $.side_panel.created_agents)}
            </h3>
            <span className="ml-auto font-mono text-[11px] font-semibold text-muted-foreground/80">
              {ownedAgents.length}
            </span>
          </div>
          {ownedAgents.length === 0 ? (
            <p className="text-[12.5px] italic text-muted-foreground">
              {t(($) => $.side_panel.no_agents)}
            </p>
          ) : (
            <div className="flex flex-col gap-1.5">
              {ownedAgents.map((agent) => (
                <CreatedAgentRow
                  key={agent.id}
                  agent={agent}
                  runtimeSubtitle={runtimeSubtitle(agent, runtimes)}
                  onOpen={() =>
                    openAgent(agent.id, {
                      name: agent.name,
                      display_name: agent.display_name,
                      avatar_url: agent.avatar_url ?? null,
                    })
                  }
                />
              ))}
            </div>
          )}
        </section>
      </div>
    </aside>
  );
}

function PanelTopBar({
  variant,
  displayName,
  handleLabel,
  avatar,
  showMessage,
  onClose,
  onMessage,
  dmPending,
  messageAria,
  closeAria,
}: {
  variant: "panel" | "page";
  displayName: string;
  handleLabel: string | null;
  avatar: React.ReactNode;
  showMessage: boolean;
  onClose: () => void;
  onMessage: () => void;
  dmPending: boolean;
  messageAria: string;
  closeAria: string;
}) {
  return (
    <div
      className="flex min-h-11 shrink-0 items-center gap-2 border-b px-2.5 py-2"
      data-testid="human-member-topbar"
    >
      {avatar}
      <div className="min-w-0 flex-1 leading-tight">
        <div className="truncate text-[12.5px] font-semibold">{displayName}</div>
        {handleLabel ? (
          <div className="truncate font-mono text-[11px] text-muted-foreground">
            {handleLabel}
          </div>
        ) : null}
      </div>
      {showMessage ? (
        <Button
          type="button"
          variant="outline"
          size="icon"
          className="size-[30px] shrink-0"
          onClick={onMessage}
          disabled={dmPending}
          aria-label={messageAria}
          data-testid="human-member-message"
        >
          <MessageSquare className="size-3.5" />
        </Button>
      ) : null}
      {variant === "panel" ? (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-[30px] shrink-0"
          onClick={onClose}
          aria-label={closeAria}
          data-testid="human-member-close"
        >
          <X className="size-4" />
        </Button>
      ) : null}
    </div>
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
    <div>
      <div className="mb-0.5 text-xs text-muted-foreground">{label}</div>
      <div className="text-[13px] text-foreground">{children}</div>
    </div>
  );
}

function CreatedAgentRow({
  agent,
  runtimeSubtitle,
  onOpen,
}: {
  agent: Agent;
  runtimeSubtitle: string;
  onOpen: () => void;
}) {
  const displayName = resolveActorDisplayName(agent, agent.id);
  const roleText = agent.description?.trim() || null;
  const line1 = roleText ? `${displayName} - ${roleText}` : displayName;

  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex w-full items-center gap-2.5 rounded-lg border border-border bg-background px-2.5 py-2 text-left transition-colors hover:border-border/80 hover:bg-accent/40"
      data-testid="human-member-agent-row"
    >
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
        <div className="truncate text-[12.5px] font-semibold">{line1}</div>
        {runtimeSubtitle ? (
          <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">
            {runtimeSubtitle}
          </div>
        ) : null}
      </div>
    </button>
  );
}

function runtimeSubtitle(
  agent: Agent,
  runtimes: { id: string; name: string; provider: string }[],
): string {
  const runtime = runtimes.find((r) => r.id === agent.runtime_id);
  if (runtime?.provider) {
    const mapped = PROVIDER_SUBTITLE[runtime.provider.toLowerCase()];
    if (mapped) return mapped;
  }
  if (runtime?.name) return runtime.name;
  if (agent.runtime_mode === "cloud") return "Cloud";
  return "";
}

function formatJoinedDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(d);
}
