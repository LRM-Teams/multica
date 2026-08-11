"use client";

import { useCallback, useEffect, useMemo, useReducer } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Monitor, Plus, Users } from "lucide-react";
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
import { Input } from "@multica/ui/components/ui/input";
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
  filterMembersDirectoryRoster,
  isMembersDirectoryRosterReady,
  resolveMembersSelection,
} from "./members-directory-model";

export interface MembersDirectoryPageProps {
  localDaemonId?: string | null;
  localMachineName?: string | null;
  hasLocalMachine?: boolean;
}

/** Local rail/dialog UI — one reducer so related updates don't fan out. */
type DirectoryUiState = {
  query: string;
  agentsOpen: boolean;
  humansOpen: boolean;
  showCreate: boolean;
  showInvite: boolean;
  /** Selection key we last auto-expanded for (deep-link / URL). */
  expandedForKey: string | null;
};

type DirectoryUiAction =
  | { type: "set_query"; query: string }
  | { type: "toggle_agents" }
  | { type: "toggle_humans" }
  | { type: "select_kind"; kind: "agent" | "user" }
  | { type: "open_create"; open: boolean }
  | { type: "open_invite"; open: boolean }
  | {
      type: "sync_selection";
      key: string | null;
      kind: "agent" | "user" | null;
    };

const initialDirectoryUi: DirectoryUiState = {
  query: "",
  // Agents collapsed by default; humans expanded.
  agentsOpen: false,
  humansOpen: true,
  showCreate: false,
  showInvite: false,
  expandedForKey: null,
};

function directoryUiReducer(
  state: DirectoryUiState,
  action: DirectoryUiAction,
): DirectoryUiState {
  switch (action.type) {
    case "set_query":
      return { ...state, query: action.query };
    case "toggle_agents":
      return { ...state, agentsOpen: !state.agentsOpen };
    case "toggle_humans":
      return { ...state, humansOpen: !state.humansOpen };
    case "select_kind":
      return {
        ...state,
        agentsOpen: action.kind === "agent" ? true : state.agentsOpen,
        humansOpen: action.kind === "user" ? true : state.humansOpen,
      };
    case "open_create":
      return { ...state, showCreate: action.open };
    case "open_invite":
      return { ...state, showInvite: action.open };
    case "sync_selection": {
      if (action.key === state.expandedForKey) return state;
      if (action.kind === "agent") {
        return {
          ...state,
          expandedForKey: action.key,
          agentsOpen: true,
        };
      }
      if (action.kind === "user") {
        return {
          ...state,
          expandedForKey: action.key,
          humansOpen: true,
        };
      }
      return { ...state, expandedForKey: action.key };
    }
    default:
      return state;
  }
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
  const [ui, dispatch] = useReducer(directoryUiReducer, initialDirectoryUi);

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
  const canManageWorkspace = myRole === "owner" || myRole === "admin";
  const isOwner = myRole === "owner";
  const ownerCount = useMemo(
    () => members.filter((m) => m.role === "owner").length,
    [members],
  );

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

  const filtered = useMemo(
    () => filterMembersDirectoryRoster(roster, ui.query),
    [roster, ui.query],
  );

  const urlSelection = parseMembersSelectionFromSearch(
    navigation.searchParams as { get(name: string): string | null },
  );

  const rosterReady = isMembersDirectoryRosterReady({
    agentsLoading,
    membersLoading,
    runtimesLoading,
  });

  const selection = useMemo(() => {
    if (!rosterReady && !urlSelection) return null;
    // Resolve against full roster so URL selection survives search filter.
    return resolveMembersSelection(roster, urlSelection);
  }, [roster, urlSelection, rosterReady]);

  // Deep-link / default selection: expand the section that holds it during
  // render (not in an effect — avoids a one-frame collapsed flash).
  const selectionKey = selection
    ? `${selection.kind}:${selection.id}`
    : null;
  if (selectionKey !== ui.expandedForKey) {
    dispatch({
      type: "sync_selection",
      key: selectionKey,
      kind: selection?.kind ?? null,
    });
  }

  const setSelection = useCallback(
    (kind: "agent" | "user", id: string) => {
      dispatch({ type: "select_kind", kind });
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
    dispatch({ type: "open_create", open: false });
    dispatch({ type: "select_kind", kind: "agent" });
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

  const manage =
    selectedHuman && canManageWorkspace
      ? directoryManageFor(selectedHuman, {
          currentUserId: currentUser?.id ?? null,
          isOwner,
          ownerCount,
        })
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
          <Users className="size-4 text-muted-foreground" aria-hidden />
          <h1 className="text-sm font-semibold">
            {t(($) => $.directory.title)}
          </h1>
        </div>

        <div className="shrink-0 border-b px-3 py-2.5">
          <Input
            value={ui.query}
            onChange={(e) =>
              dispatch({ type: "set_query", query: e.target.value })
            }
            placeholder={t(($) => $.directory.search_placeholder)}
            className="h-8"
            data-testid="members-directory-search"
          />
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
                count={filtered.listedAgents.length}
                open={ui.agentsOpen}
                onToggle={() => dispatch({ type: "toggle_agents" })}
                onAdd={() => dispatch({ type: "open_create", open: true })}
                addAria={t(($) => $.directory.create_agent_aria)}
                testId="members-agents-add"
                toggleTestId="members-agents-toggle"
              />
              {ui.agentsOpen ? (
                filtered.computerGroups.length === 0 ? (
                  <p className="px-4 py-2 text-xs text-muted-foreground">
                    {t(($) => $.directory.no_agents)}
                  </p>
                ) : (
                  filtered.computerGroups.map((group) => (
                    <div key={group.machineId} className="mb-1">
                      <div
                        className="flex items-center gap-1.5 px-4 py-1 text-[11px] font-medium text-muted-foreground"
                        data-testid="members-computer-label"
                      >
                        <Monitor className="size-3 shrink-0" aria-hidden />
                        <span className="truncate">{group.title}</span>
                      </div>
                      {group.agents.map((a) => {
                        const isMine =
                          !!currentUser?.id && a.owner_id === currentUser.id;
                        return (
                          <RailRow
                            key={a.id}
                            selected={
                              selection?.kind === "agent" &&
                              selection.id === a.id
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
                            badge={
                              isMine ? (
                                <span
                                  className="shrink-0 rounded-full bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-700 dark:text-emerald-300"
                                  data-testid="members-mine-badge"
                                >
                                  {t(($) => $.directory.mine_badge)}
                                </span>
                              ) : null
                            }
                            testId={`members-agent-row-${a.id}`}
                          />
                        );
                      })}
                    </div>
                  ))
                )
              ) : null}

              <SectionHeader
                label={t(($) => $.directory.humans_section)}
                count={filtered.humans.length}
                open={ui.humansOpen}
                onToggle={() => dispatch({ type: "toggle_humans" })}
                onAdd={
                  canManageWorkspace
                    ? () => dispatch({ type: "open_invite", open: true })
                    : undefined
                }
                addAria={t(($) => $.directory.invite_human_aria)}
                testId="members-humans-add"
                toggleTestId="members-humans-toggle"
              />
              {ui.humansOpen ? (
                filtered.humans.length === 0 ? (
                  <p className="px-4 py-2 text-xs text-muted-foreground">
                    {t(($) => $.directory.no_humans)}
                  </p>
                ) : (
                  filtered.humans.map((h) => {
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
                )
              ) : null}
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
          <MemberSidePanel
            userId={selection.id}
            onClose={() => {}}
            variant="page"
            hideDismiss
            directoryManage={
              manage && selectedHuman
                ? {
                    member: selectedHuman,
                    canEditRole: manage.canEditRole,
                    canRemove: manage.canRemove,
                    roleOptions: manage.roleOptions,
                    busy:
                      roleMutation.isPending || removeMutation.isPending,
                    onRoleChange: async (role: MemberRole) => {
                      await roleMutation.mutateAsync({
                        memberId: selectedHuman.id,
                        role,
                      });
                    },
                    onRemove: async () => {
                      await removeMutation.mutateAsync(selectedHuman.id);
                    },
                  }
                : null
            }
          />
        )}
      </div>

      {ui.showCreate ? (
        <CreateAgentDialog
          runtimes={runtimes}
          runtimesLoading={runtimesLoading}
          members={members}
          currentUserId={currentUser?.id ?? null}
          onClose={() => dispatch({ type: "open_create", open: false })}
          onCreate={handleCreate}
        />
      ) : null}

      <InviteHumanDialog
        open={ui.showInvite}
        onOpenChange={(open) => dispatch({ type: "open_invite", open })}
        workspaceId={wsId}
        canInviteOwner={isOwner}
      />
    </div>
  );
}

function directoryManageFor(
  member: MemberWithUser,
  opts: {
    currentUserId: string | null;
    isOwner: boolean;
    ownerCount: number;
  },
): {
  canEditRole: boolean;
  canRemove: boolean;
  roleOptions: MemberRole[];
} {
  const isSelf = opts.currentUserId === member.user_id;
  const isLastOwner = member.role === "owner" && opts.ownerCount <= 1;
  const blocked =
    isSelf || (member.role === "owner" && !opts.isOwner) || isLastOwner;
  return {
    canEditRole: !blocked,
    canRemove: !blocked,
    roleOptions: opts.isOwner
      ? ["owner", "admin", "member"]
      : ["admin", "member"],
  };
}

function SectionHeader({
  label,
  count,
  open,
  onToggle,
  onAdd,
  addAria,
  testId,
  toggleTestId,
}: {
  label: string;
  count: number;
  open: boolean;
  onToggle: () => void;
  onAdd?: () => void;
  addAria: string;
  testId: string;
  toggleTestId: string;
}) {
  return (
    <div className="mt-2 flex items-center gap-1 px-2 py-1">
      <button
        type="button"
        onClick={onToggle}
        className="flex min-w-0 flex-1 items-center gap-1 rounded-md px-1 py-1 text-left hover:bg-muted/60"
        aria-expanded={open}
        data-testid={toggleTestId}
      >
        <ChevronRight
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
          aria-hidden
        />
        <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {label}
        </span>
        <span className="font-mono text-[10px] tabular-nums text-muted-foreground/70">
          {count}
        </span>
      </button>
      {onAdd ? (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={onAdd}
          aria-label={addAria}
          data-testid={testId}
        >
          <Plus className="size-3.5" aria-hidden />
        </Button>
      ) : null}
    </div>
  );
}

function RailRow({
  selected,
  onClick,
  avatar,
  title,
  subtitle,
  badge,
  testId,
}: {
  selected: boolean;
  onClick: () => void;
  avatar: React.ReactNode;
  title: string;
  subtitle: string | null;
  badge?: React.ReactNode;
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
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-sm font-medium">{title}</span>
          {badge}
        </span>
        {subtitle ? (
          <span className="block truncate text-xs text-muted-foreground">
            {subtitle}
          </span>
        ) : null}
      </span>
    </button>
  );
}
