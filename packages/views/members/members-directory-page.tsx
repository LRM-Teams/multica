"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Monitor, Plus, Users } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { parseMembersSelectionFromSearch } from "@multica/core/paths";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  agentListOptions,
  memberListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { runtimeListOptions } from "@multica/core/runtimes";
import { resolveActorDisplayName } from "@multica/core/identity";
import type {
  Agent,
  CreateAgentRequest,
  MemberRole,
  MemberWithUser,
} from "@multica/core/types";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../common/actor-avatar";
import { ResolvedAgentSidePanel } from "../common/resolved-agent-side-panel";
import { CreateAgentDialog } from "../agents/components/create-agent-dialog";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";
import { MemberSidePanel } from "./member-side-panel";
import { InviteHumanDialog } from "./invite-human-dialog";
import {
  buildMembersDirectoryRoster,
  isMembersDirectoryRosterReady,
  resolveMembersSelection,
} from "./members-directory-model";
import { MemberDirectoryManageFooter } from "./member-directory-manage-footer";

export interface MembersDirectoryPageProps {
  localDaemonId?: string | null;
  localMachineName?: string | null;
  hasLocalMachine?: boolean;
}

export function MembersDirectoryPage({
  localDaemonId = null,
  localMachineName = null,
  hasLocalMachine = false,
}: MembersDirectoryPageProps = {}) {
  const { t } = useT("members");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const qc = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);

  const { data: agents = [], isLoading: agentsLoading } = useQuery(
    agentListOptions(wsId, { includeArchived: true }),
  );
  const { data: members = [], isLoading: membersLoading } = useQuery(
    memberListOptions(wsId),
  );
  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(
    runtimeListOptions(wsId),
  );

  const myRole = useMemo(() => {
    if (!currentUser) return null;
    return members.find((m) => m.user_id === currentUser.id)?.role ?? null;
  }, [members, currentUser]);
  const canManageWorkspace =
    myRole === "owner" || myRole === "admin";
  const isOwner = myRole === "owner";

  const roster = useMemo(
    () =>
      buildMembersDirectoryRoster(agents, members, runtimes, {
        localDaemonId,
        localMachineName,
        currentUserId: currentUser?.id ?? null,
        hasLocalMachine,
      }),
    [
      agents,
      members,
      runtimes,
      localDaemonId,
      localMachineName,
      currentUser?.id,
      hasLocalMachine,
    ],
  );

  const urlSelection = parseMembersSelectionFromSearch(
    navigation.searchParams as { get(name: string): string | null },
  );

  // Wait for runtimes too — otherwise agents with runtime_ids map to zero
  // groups while runtimes=[], default becomes first human, and that wrong
  // selection gets stamped into the URL before computers load (AC1 race).
  const rosterReady = isMembersDirectoryRosterReady({
    agentsLoading,
    membersLoading,
    runtimesLoading,
  });

  const selection = useMemo(() => {
    if (!rosterReady && !urlSelection) return null;
    return resolveMembersSelection(roster, urlSelection);
  }, [roster, urlSelection, rosterReady]);

  const setSelection = useCallback(
    (kind: "agent" | "user", id: string) => {
      navigation.replace(paths.members({ kind, id }));
    },
    [navigation, paths],
  );

  // Keep URL in sync when roster resolves a default and URL is empty.
  useEffect(() => {
    if (!rosterReady || !selection || urlSelection) return;
    navigation.replace(
      paths.members({ kind: selection.kind, id: selection.id }),
    );
  }, [rosterReady, selection, urlSelection, navigation, paths]);

  const [showCreate, setShowCreate] = useState(false);
  const [showInvite, setShowInvite] = useState(false);

  const handleCreate = async (data: CreateAgentRequest): Promise<Agent> => {
    if (!data.runtime_id) {
      throw new Error(t(($) => $.directory.computer_required));
    }
    const agent = await api.createAgent(data);
    qc.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (current = []) => {
      const exists = current.some((a) => a.id === agent.id);
      return exists
        ? current.map((a) => (a.id === agent.id ? agent : a))
        : [...current, agent];
    });
    setShowCreate(false);
    navigation.replace(paths.members({ kind: "agent", id: agent.id }));
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    return agent;
  };

  const roleMutation = useMutation({
    mutationFn: ({
      memberId,
      role,
    }: {
      memberId: string;
      role: MemberRole;
    }) => api.updateMember(wsId, memberId, { role }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
      toast.success(t(($) => $.directory.role_updated));
    },
    onError: (e: Error) => {
      showErrorToast(e.message || t(($) => $.directory.role_update_failed));
    },
  });

  const removeMutation = useMutation({
    mutationFn: (memberId: string) => api.deleteMember(wsId, memberId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
      toast.success(t(($) => $.directory.member_removed));
      const next = resolveMembersSelection(roster, null);
      if (next) {
        navigation.replace(paths.members({ kind: next.kind, id: next.id }));
      } else {
        navigation.replace(paths.members());
      }
    },
    onError: (e: Error) => {
      showErrorToast(e.message || t(($) => $.directory.member_remove_failed));
    },
  });

  const selectedHuman: MemberWithUser | null =
    selection?.kind === "user"
      ? (roster.humans.find((h) => h.user_id === selection.id) ?? null)
      : null;

  const loading = !rosterReady;

  return (
    <div
      className="flex min-h-0 flex-1"
      data-testid="members-directory-page"
    >
      {/* Left rail */}
      <div className="flex w-[300px] shrink-0 flex-col border-r bg-background">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <Users className="size-4 text-muted-foreground" />
          <h1 className="text-sm font-semibold">
            {t(($) => $.directory.title)}
          </h1>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto py-2">
          {loading ? (
            <p className="px-4 py-6 text-sm text-muted-foreground">
              {t(($) => $.directory.loading)}
            </p>
          ) : (
            <>
              <SectionHeader
                label={t(($) => $.directory.agents_section)}
                count={roster.listedAgents.length}
                onAdd={() => setShowCreate(true)}
                addAria={t(($) => $.directory.create_agent_aria)}
                testId="members-agents-add"
              />
              {roster.computerGroups.length === 0 ? (
                <p className="px-4 py-2 text-xs text-muted-foreground">
                  {t(($) => $.directory.no_agents)}
                </p>
              ) : (
                roster.computerGroups.map((group) => (
                  <div key={group.machineId} className="mb-1">
                    <div
                      className="flex items-center gap-1.5 px-4 py-1 text-[11px] font-medium text-muted-foreground"
                      data-testid="members-computer-label"
                    >
                      <Monitor className="size-3 shrink-0" />
                      <span className="truncate">{group.title}</span>
                    </div>
                    {group.agents.map((a) => (
                      <RailRow
                        key={a.id}
                        selected={
                          selection?.kind === "agent" && selection.id === a.id
                        }
                        onClick={() => setSelection("agent", a.id)}
                        avatar={
                          <ActorAvatar
                            actorType="agent"
                            actorId={a.id}
                            size={28}
                            avatarUrlHint={a.avatar_url}
                            profileLink={false}
                          />
                        }
                        title={resolveActorDisplayName(a, a.name)}
                        subtitle={a.description?.trim() || null}
                        testId={`members-agent-row-${a.id}`}
                      />
                    ))}
                  </div>
                ))
              )}

              <SectionHeader
                label={t(($) => $.directory.humans_section)}
                count={roster.humans.length}
                onAdd={
                  canManageWorkspace ? () => setShowInvite(true) : undefined
                }
                addAria={t(($) => $.directory.invite_human_aria)}
                testId="members-humans-add"
              />
              {roster.humans.length === 0 ? (
                <p className="px-4 py-2 text-xs text-muted-foreground">
                  {t(($) => $.directory.no_humans)}
                </p>
              ) : (
                roster.humans.map((h) => {
                  const isYou = currentUser?.id === h.user_id;
                  return (
                    <RailRow
                      key={h.user_id}
                      selected={
                        selection?.kind === "user" &&
                        selection.id === h.user_id
                      }
                      onClick={() => setSelection("user", h.user_id)}
                      avatar={
                        <ActorAvatar
                          actorType="member"
                          actorId={h.user_id}
                          size={28}
                          avatarUrlHint={h.avatar_url}
                          profileLink={false}
                          className="rounded-full"
                        />
                      }
                      title={
                        isYou
                          ? `${resolveActorDisplayName(h, h.user_id)} ${t(($) => $.directory.you_suffix)}`
                          : resolveActorDisplayName(h, h.user_id)
                      }
                      subtitle={null}
                      testId={`members-human-row-${h.user_id}`}
                    />
                  );
                })
              )}
            </>
          )}
        </div>
      </div>

      {/* Detail */}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {!selection ? (
          <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
            {t(($) => $.directory.empty_select)}
          </div>
        ) : selection.kind === "agent" ? (
          <ResolvedAgentSidePanel
            agentId={selection.id}
            currentUserId={currentUser?.id ?? null}
            members={members}
            onClose={() => {}}
            variant="page"
            hideDismiss
          />
        ) : (
          <div className="flex min-h-0 flex-1 flex-col">
            <div className="min-h-0 flex-1 overflow-hidden">
              <MemberSidePanel
                userId={selection.id}
                onClose={() => {}}
                variant="page"
                hideDismiss
              />
            </div>
            {selectedHuman && canManageWorkspace ? (
              <MemberDirectoryManageFooter
                member={selectedHuman}
                currentUserId={currentUser?.id ?? null}
                isOwner={isOwner}
                ownerCount={members.filter((m) => m.role === "owner").length}
                busy={roleMutation.isPending || removeMutation.isPending}
                onRoleChange={async (role: MemberRole) => {
                  await roleMutation.mutateAsync({
                    memberId: selectedHuman.id,
                    role,
                  });
                }}
                onRemove={async () => {
                  await removeMutation.mutateAsync(selectedHuman.id);
                }}
              />
            ) : null}
          </div>
        )}
      </div>

      {showCreate ? (
        <CreateAgentDialog
          runtimes={runtimes}
          runtimesLoading={runtimesLoading}
          members={members}
          currentUserId={currentUser?.id ?? null}
          onClose={() => setShowCreate(false)}
          onCreate={handleCreate}
        />
      ) : null}

      <InviteHumanDialog
        open={showInvite}
        onOpenChange={setShowInvite}
        workspaceId={wsId}
        canInviteOwner={isOwner}
      />
    </div>
  );
}

function SectionHeader({
  label,
  count,
  onAdd,
  addAria,
  testId,
}: {
  label: string;
  count: number;
  onAdd?: () => void;
  addAria: string;
  testId: string;
}) {
  return (
    <div className="mt-2 flex items-center gap-1 px-3 py-1.5">
      <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span className="font-mono text-[10px] tabular-nums text-muted-foreground/70">
        {count}
      </span>
      {onAdd ? (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="ml-auto size-6"
          onClick={onAdd}
          aria-label={addAria}
          data-testid={testId}
        >
          <Plus className="size-3.5" />
        </Button>
      ) : (
        <span className="ml-auto" />
      )}
    </div>
  );
}

function RailRow({
  selected,
  onClick,
  avatar,
  title,
  subtitle,
  testId,
}: {
  selected: boolean;
  onClick: () => void;
  avatar: React.ReactNode;
  title: string;
  subtitle: string | null;
  testId: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      className={cn(
        "flex w-full items-center gap-2.5 px-3 py-1.5 text-left transition-colors",
        selected
          ? "bg-brand/15 text-foreground"
          : "hover:bg-muted/60 text-foreground",
      )}
    >
      <span className="shrink-0">{avatar}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{title}</span>
        {subtitle ? (
          <span className="block truncate text-xs text-muted-foreground">
            {subtitle}
          </span>
        ) : null}
      </span>
    </button>
  );
}
