"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  Clock,
  Cloud,
  Folder,
  Loader2,
  Monitor,
  MoreHorizontal,
  Plus,
  Server,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  agentTaskSnapshotOptions,
  deriveWorkloadDetail,
} from "@multica/core/agents";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { runtimeListOptions, runtimeKeys } from "@multica/core/runtimes/queries";
import {
  useDeleteRuntimeAgentWorkspace,
  useRuntimeAgentWorkspaces,
} from "@multica/core/runtimes/mutations";
import { useWSEvent } from "@multica/core/realtime";
import { agentListOptions } from "@multica/core/workspace/queries";
import { useActorName } from "@multica/core/workspace/hooks";
import type { Agent, AgentRuntime, AgentTask } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Collapsible,
  CollapsibleTrigger,
  CollapsibleContent,
} from "@multica/ui/components/ui/collapsible";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { PageHeader } from "../../layout/page-header";
import { ActorAvatar } from "../../common/actor-avatar";
import { ACTIVITY_LABEL_EN } from "../../agents/components/tabs/activity-event";
import { ConnectRemoteDialog } from "./connect-remote-dialog";
import { CloudRuntimeDialog } from "./cloud-runtime-dialog";
import { formatRuntimeUpdateError } from "./update-error";
import { buildWorkloadIndex } from "./runtime-list";
import {
  DeleteComputerDialog,
  MachineDeleteControl,
} from "./delete-computer-dialog";
import {
  buildRuntimeMachines,
  headerRuntimeHealthBadge,
  isMineMachine,
  machineHostname,
  machinePrimaryRuntimeId,
  splitRuntimeName,
  type RuntimeMachine,
} from "./runtime-machines";
import { MachineNameEditor } from "./machine-name-editor";
import {
  HealthDot,
  RuntimeConnectivityStatus,
  RuntimeHealthStateInline,
  useHealthLabel,
} from "./shared";
import { ProviderLogo } from "./provider-logo";
import { MachineCodeAgentsSection } from "./machine-code-agents-section";
import { formatLastSeen } from "../utils";
import { useT } from "../../i18n/use-t";

interface RuntimesPageProps {
  localDaemonId?: string | null;
  localMachineName?: string | null;
  localMachineActions?: React.ReactNode;
  hasLocalMachine?: boolean;
  bootstrapping?: boolean;
  cloudRuntimeEnabled?: boolean;
}

function useNowTick(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

function agentsOnMachine(
  machine: RuntimeMachine,
  agents: Agent[],
): Agent[] {
  const runtimeIds = new Set(machine.runtimes.map((r) => r.id));
  return agents.filter(
    (a) => a.runtime_id && runtimeIds.has(a.runtime_id) && !a.archived_at,
  );
}

function agentCountOnMachine(machine: RuntimeMachine, agents: Agent[]): number {
  return agentsOnMachine(machine, agents).length;
}

function runtimeForAgent(
  agent: Agent,
  machine: RuntimeMachine,
): AgentRuntime | null {
  return machine.runtimes.find((r) => r.id === agent.runtime_id) ?? null;
}

function providerLabel(runtime: AgentRuntime | null): string {
  if (!runtime) return "—";
  switch (runtime.provider) {
    case "claude":
    case "claude-code":
      return "Claude Code";
    case "codex":
      return "Codex";
    case "cursor":
      return "Cursor";
    case "opencode":
      return "OpenCode";
    case "openclaw":
      return "OpenClaw";
    case "hermes":
      return "Hermes";
    case "pi":
      return "Pi";
    default: {
      const { base } = splitRuntimeName(runtime.name);
      return base || runtime.provider;
    }
  }
}

function formatMachineLastSeen(
  machine: RuntimeMachine,
  now: number,
  activeNowLabel: string,
): string {
  if (!machine.lastSeenAt) return "—";
  if (machine.health === "online") {
    const diffMs = now - new Date(machine.lastSeenAt).getTime();
    if (diffMs < 60_000) {
      return activeNowLabel;
    }
  }
  return formatLastSeen(machine.lastSeenAt);
}

// task #7 (2026-07-31): was tAgents($.workload[...]) (Working/Queued/Idle) —
// aligned to the same Activity vocabulary as the rest of the product
// (ACTIVITY_LABEL_EN). "Queued" folds into "Working" — this function has no
// presence/availability data, only task counts, so it can't distinguish
// online/offline; it only ever needed the idle-vs-active distinction anyway.
function formatAgentActivity(agentId: string, snapshot: AgentTask[]): string {
  const tasks = snapshot.filter((task) => task.agent_id === agentId);
  const detail = deriveWorkloadDetail(tasks);
  const label =
    detail.workload === "idle" ? ACTIVITY_LABEL_EN.idle : ACTIVITY_LABEL_EN.working;

  if (detail.workload === "working" || detail.workload === "queued") {
    const active = tasks.find(
      (task) =>
        task.status === "running" ||
        task.status === "queued" ||
        task.status === "dispatched" ||
        task.status === "waiting_local_directory",
    );
    if (active?.issue_id) {
      const hint =
        active.issue_id.length > 12
          ? `${active.issue_id.slice(0, 8)}…`
          : active.issue_id;
      return `${label} · ${hint}`;
    }
    return label;
  }

  const terminal = tasks
    .filter(
      (task) =>
        task.status === "completed" ||
        task.status === "failed" ||
        task.status === "cancelled",
    )
    .toSorted((a, b) => {
      const at = a.completed_at ?? a.created_at;
      const bt = b.completed_at ?? b.created_at;
      return new Date(bt).getTime() - new Date(at).getTime();
    });
  if (terminal[0]) {
    const at = terminal[0].completed_at ?? terminal[0].created_at;
    return `${label} · ${formatLastSeen(at)}`;
  }
  return label;
}

/**
 * LRM-863 — Runtimes per **v8c** freeze (Frank 2026-07-31):
 * Desktop = left machine list (~300px) + right detail on the same page
 * (row click only swaps the right pane). Mobile = list → detail drill-in.
 * Agent → Profile dock/sheet; Machine name ✎; Workspaces via ⋯ (LRM-810).
 * Not v9 flat→full-page, not v10.
 */
export function RuntimesPage({
  localDaemonId,
  localMachineName,
  localMachineActions,
  hasLocalMachine,
  bootstrapping,
  cloudRuntimeEnabled = false,
}: RuntimesPageProps = {}) {
  const { t } = useT("runtimes");
  const isLoading = useAuthStore((s) => s.isLoading);
  const currentUserId = useAuthStore((s) => s.user?.id);
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const isMobile = useIsMobile();
  const [userPickId, setUserPickId] = useState<string | null>(null);
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const [showConnectDialog, setShowConnectDialog] = useState(false);
  const [showCloudRuntimeDialog, setShowCloudRuntimeDialog] = useState(false);

  const { data: runtimes = [], isLoading: fetching } = useQuery(
    runtimeListOptions(wsId),
  );
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const handleDaemonEvent = useCallback(() => {
    qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
  }, [qc, wsId]);
  useWSEvent("daemon:register", handleDaemonEvent);
  useWSEvent("daemon:runtime_updated", handleDaemonEvent);

  const now = useNowTick();

  const workloadIndex = useMemo(
    () => buildWorkloadIndex(agents, snapshot),
    [agents, snapshot],
  );

  const machines = useMemo(
    () =>
      buildRuntimeMachines(runtimes, {
        now,
        localDaemonId,
        localMachineName,
        currentUserId,
        workloadByRuntimeId: workloadIndex,
        ensureLocalMachine: hasLocalMachine,
      }),
    [
      runtimes,
      now,
      localDaemonId,
      localMachineName,
      currentUserId,
      workloadIndex,
      hasLocalMachine,
    ],
  );

  const userPickValid =
    !!userPickId && machines.some((m) => m.id === userPickId);

  const selectedMachineId = isMobile
    ? userPickId
    : userPickValid
      ? userPickId
      : (machines.find((m) => m.isCurrent)?.id ?? machines[0]?.id ?? null);

  const selectedMachine =
    machines.find((m) => m.id === selectedMachineId) ?? null;

  const handleSelectMachine = useCallback((id: string) => {
    setUserPickId(id);
    setMobileDetailOpen(true);
  }, []);

  const handleMobileBack = useCallback(() => setMobileDetailOpen(false), []);

  const handleComputerDeleted = useCallback(() => {
    setUserPickId(null);
    setMobileDetailOpen(false);
  }, []);

  if (isLoading || fetching) return <RuntimesPageSkeleton isMobile={isMobile} />;

  const totalCount = runtimes.length;
  const showEmpty = totalCount === 0 && !bootstrapping && !hasLocalMachine;

  if (showEmpty) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <PageHeader className="justify-between px-5">
          <div className="flex items-center gap-2">
            <Server className="h-4 w-4 text-muted-foreground" />
            <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
          </div>
        </PageHeader>
        <div className="flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-6">
          <EmptyState onConnectRemote={() => setShowConnectDialog(true)} />
        </div>
        {showConnectDialog && (
          <ConnectRemoteDialog onClose={() => setShowConnectDialog(false)} />
        )}
      </div>
    );
  }

  const headerActions = (
    <MachineListActions
      onAdd={() => setShowConnectDialog(true)}
      cloudRuntimeEnabled={cloudRuntimeEnabled}
      onOpenCloudRuntime={() => setShowCloudRuntimeDialog(true)}
    />
  );

  const listView = (
    <MachineListView
      machines={machines}
      agents={agents}
      now={now}
      wsId={wsId}
      currentUserId={currentUserId ?? null}
      layout={isMobile ? "full" : "sidebar"}
      selectedMachineId={isMobile ? null : selectedMachineId}
      headerActions={headerActions}
      onSelect={isMobile ? handleSelectMachine : setUserPickId}
    />
  );

  const detailView = selectedMachine ? (
    <ComputersMachineDetail
      machine={selectedMachine}
      agents={agents}
      snapshot={snapshot}
      now={now}
      wsId={wsId}
      isMobile={isMobile}
      actions={selectedMachine.isCurrent ? localMachineActions : undefined}
      bootstrapping={bootstrapping}
      onBack={handleMobileBack}
      onComputerDeleted={handleComputerDeleted}
      showBack={isMobile}
      showListActions={isMobile}
      headerActions={headerActions}
    />
  ) : (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col items-center justify-center px-6 text-center">
      {bootstrapping ? (
        <>
          <Server className="h-8 w-8 animate-pulse text-muted-foreground/40" />
          <p className="mt-3 text-sm text-muted-foreground">
            {t(($) => $.page.bootstrapping.title)}
          </p>
        </>
      ) : (
        <>
          <Monitor className="h-8 w-8 text-muted-foreground/40" />
          <p className="mt-3 text-sm text-muted-foreground">
            {t(($) => $.machine.select_machine)}
          </p>
        </>
      )}
    </main>
  );

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      {isMobile ? (
        mobileDetailOpen && selectedMachine ? (
          detailView
        ) : (
          <div className="flex min-h-0 flex-1 flex-col">{listView}</div>
        )
      ) : (
        <div className="flex min-h-0 flex-1">
          <aside className="flex min-h-0 w-[300px] shrink-0 flex-col border-r">
            {listView}
          </aside>
          {detailView}
        </div>
      )}

      {showConnectDialog && (
        <ConnectRemoteDialog onClose={() => setShowConnectDialog(false)} />
      )}
      {cloudRuntimeEnabled && showCloudRuntimeDialog && (
        <CloudRuntimeDialog onClose={() => setShowCloudRuntimeDialog(false)} />
      )}
    </div>
  );
}

function MachineListActions({
  onAdd,
  cloudRuntimeEnabled,
  onOpenCloudRuntime,
}: {
  onAdd: () => void;
  cloudRuntimeEnabled: boolean;
  onOpenCloudRuntime: () => void;
}) {
  const { t } = useT("runtimes");
  return (
    <div className="flex shrink-0 items-center gap-1">
      {cloudRuntimeEnabled && (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={onOpenCloudRuntime}
          aria-label={t(($) => $.cloud_runtime.action)}
          title={t(($) => $.cloud_runtime.action)}
        >
          <Cloud className="h-4 w-4" />
        </Button>
      )}
      <Button
        type="button"
        size="icon-sm"
        onClick={onAdd}
        aria-label={t(($) => $.page.connect_remote)}
        title={t(($) => $.page.connect_remote)}
        className="rounded-lg bg-brand text-brand-foreground hover:bg-brand/90"
      >
        <Plus className="h-4 w-4" />
      </Button>
    </div>
  );
}

export function MachineListView({
  machines,
  agents,
  now,
  wsId,
  currentUserId,
  layout,
  selectedMachineId,
  headerActions,
  onSelect,
}: {
  machines: RuntimeMachine[];
  agents: Agent[];
  now: number;
  wsId: string;
  currentUserId: string | null;
  /** `sidebar` = v8c left rail; `full` = mobile full-width list. */
  layout: "sidebar" | "full";
  selectedMachineId: string | null;
  headerActions: React.ReactNode;
  onSelect: (id: string) => void;
}) {
  const { t } = useT("runtimes");
  const labelOf = useHealthLabel();
  const { getActorName } = useActorName();
  const showChevron = layout === "full";

  const mineMachines = machines.filter((m) => isMineMachine(m, currentUserId));
  const teamMachines = machines.filter((m) => !isMineMachine(m, currentUserId));
  // Frank 07-31: "分不清哪个是我的" — only worth splitting into two labeled
  // groups once there's actually a second owner to distinguish from. A
  // workspace where the viewer owns everything they can see stays a flat
  // list, same as before.
  const showGroups = teamMachines.length > 0;

  const renderRow = (machine: RuntimeMachine, showOwnerBadge: boolean) => {
    const count = agentCountOnMachine(machine, agents);
    const lastSeen = formatMachineLastSeen(
      machine,
      now,
      t(($) => $.machine.last_seen_active_now),
    );
    const selected = selectedMachineId === machine.id;
    const ownerId = showOwnerBadge ? (machine.runtimes[0]?.owner_id ?? null) : null;
    return (
      <div
        key={machine.id}
        className={cn(
          "relative flex items-center gap-3",
          layout === "full"
            ? "border-b px-4 py-3.5"
            : cn(
                "rounded-lg px-2.5 py-2.5",
                selected ? "bg-accent" : "hover:bg-accent/50",
              ),
        )}
      >
        <button
          type="button"
          aria-pressed={selected}
          aria-label={machine.title}
          onClick={() => onSelect(machine.id)}
          className="absolute inset-0 z-0 rounded-[inherit]"
        />
        <span className="relative z-10 shrink-0 pointer-events-none" aria-hidden>
          <HealthDot health={machine.health} />
        </span>
        <div className="relative z-10 min-w-0 flex-1 pointer-events-none">
          <div className="flex items-center gap-1.5">
            <MachineNameEditor
              machine={machine}
              wsId={wsId}
              variant="list"
            />
            {ownerId && (
              // Scoped to just the avatar+name, not the whole row content —
              // it's the only thing here with its own click handler
              // (ActorAvatar opens a profile popover). The name text next to
              // it has none, so it must stay pointer-events-none and let the
              // click fall through to the row's select button underneath
              // (LRM-923 / #23: this div used to carry pointer-events-auto
              // for a pencil-rename button that list rows no longer render,
              // silently eating clicks meant to select the row).
              <span className="pointer-events-auto inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                <ActorAvatar actorType="member" actorId={ownerId} size={18} />
                <span className="max-w-20 truncate">
                  {getActorName("member", ownerId)}
                </span>
              </span>
            )}
          </div>
          <div className="mt-0.5 truncate text-xs text-muted-foreground">
            {labelOf(machine.health)}
            {count > 0 && (
              <>
                {" · "}
                {t(($) => $.machine.table_agents_count, {
                  count,
                })}
              </>
            )}
            {machine.lastSeenAt && (
              <>
                {" · "}
                {lastSeen}
              </>
            )}
          </div>
        </div>
        {showChevron ? (
          <ChevronRight
            className="relative z-10 h-4 w-4 shrink-0 pointer-events-none text-muted-foreground/45"
            aria-hidden
          />
        ) : null}
      </div>
    );
  };

  return (
    <>
      <PageHeader className="justify-between gap-2 px-4">
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-semibold">{t(($) => $.page.title)}</h1>
        </div>
        {headerActions}
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className={layout === "sidebar" ? "p-2" : undefined}>
          {showGroups ? (
            <>
              <SectionTitle>{t(($) => $.machine.section_mine)}</SectionTitle>
              {mineMachines.map((machine) => renderRow(machine, false))}
              <Collapsible defaultOpen={false}>
                <CollapsibleTrigger
                  render={
                    <button
                      type="button"
                      aria-label={t(($) => $.machine.section_team_public, {
                        count: teamMachines.length,
                      })}
                      className="group/team-toggle mt-2 flex w-full items-center gap-1 rounded-md px-2.5 py-1.5 text-left text-[11px] font-semibold uppercase tracking-wide text-muted-foreground hover:bg-accent/50"
                    />
                  }
                >
                  <ChevronRight className="h-3 w-3 stroke-[2.5] transition-transform duration-200 group-data-[panel-open]/team-toggle:rotate-90" />
                  {t(($) => $.machine.section_team_public, {
                    count: teamMachines.length,
                  })}
                </CollapsibleTrigger>
                <CollapsibleContent>
                  {teamMachines.map((machine) => renderRow(machine, true))}
                </CollapsibleContent>
              </Collapsible>
            </>
          ) : (
            machines.map((machine) => renderRow(machine, false))
          )}
        </div>
      </div>
    </>
  );
}

type MachineDetailViewProps = {
  machine: RuntimeMachine;
  agents: Agent[];
  snapshot: AgentTask[];
  now: number;
  wsId: string;
  isMobile: boolean;
  actions?: React.ReactNode;
  bootstrapping?: boolean;
  onBack: () => void;
  onComputerDeleted?: () => void;
  headerActions: React.ReactNode;
  showBack: boolean;
  showListActions: boolean;
};

/**
 * List-page machine detail. Remounts when `machine.id` changes so
 * destructive dialog state (`deleteOpen`, workspace scan, etc.) cannot
 * leak across machines — e.g. delete confirm opened on A must not stay
 * open after the user selects B.
 */
export function ComputersMachineDetail(props: MachineDetailViewProps) {
  return <MachineDetailView key={props.machine.id} {...props} />;
}

function MachineDetailView({
  machine,
  agents,
  snapshot,
  now,
  wsId,
  isMobile,
  actions,
  bootstrapping,
  onBack,
  onComputerDeleted,
  headerActions,
  showBack,
  showListActions,
}: MachineDetailViewProps) {
  const { t } = useT("runtimes");
  const { getActorName } = useActorName();
  const openAgentPanel = useAgentPanelStore((s) => s.open);
  const user = useAuthStore((s) => s.user);
  const [workspacesEnabled, setWorkspacesEnabled] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const primaryRuntimeId = machinePrimaryRuntimeId(machine, now);
  const { data: workspacesData, isFetching: workspacesLoading } =
    useRuntimeAgentWorkspaces(primaryRuntimeId, workspacesEnabled);
  const deleteWorkspace = useDeleteRuntimeAgentWorkspace(primaryRuntimeId ?? "");

  const machineAgents = useMemo(
    () => agentsOnMachine(machine, agents),
    [machine, agents],
  );

  const canDelete =
    machine.runtimes.length > 0 &&
    !!user &&
    machine.runtimes.every((r) => r.owner_id === user.id);

  const headerBadge = headerRuntimeHealthBadge(
    machine.runtimeHealth,
    machine.health,
  );
  const updateIssue = machine.updateError
    ? formatRuntimeUpdateError({
        rawError: machine.updateError,
        currentVersion: machine.cliVersion,
        targetVersion: machine.updateTargetVersion,
        t,
      })
    : "";
  const hostname = machineHostname(machine);
  // Prefer real device_info; never fall back to "daemon …" subtitle filler.
  const osLabel =
    machine.deviceInfo && !/^daemon\b/i.test(machine.deviceInfo)
      ? machine.deviceInfo
      : null;

  const scanWorkspaces = () => {
    if (!primaryRuntimeId) {
      showErrorToast(t(($) => $.machine.scan_workspaces_offline));
      return;
    }
    if (machine.health !== "online") {
      showErrorToast(t(($) => $.machine.scan_workspaces_offline));
      return;
    }
    setWorkspacesEnabled(true);
  };

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <PageHeader className="justify-between gap-2 px-4 md:px-5">
        <div className="flex min-w-0 items-center gap-1">
          {showBack ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={onBack}
              aria-label={t(($) => $.machine.back_to_list)}
            >
              <ChevronLeft className="h-5 w-5" />
            </Button>
          ) : null}
          {showBack ? (
            <span className="truncate text-sm font-medium">{machine.title}</span>
          ) : (
            <span className="truncate text-sm font-medium text-muted-foreground">
              {t(($) => $.page.title)}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1">
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t(($) => $.list.row_actions_aria)}
                />
              }
            >
              <MoreHorizontal className="h-4 w-4" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              <DropdownMenuItem onClick={scanWorkspaces}>
                {t(($) => $.machine.scan_workspaces)}
              </DropdownMenuItem>
              {canDelete && (
                <DropdownMenuItem
                  variant="destructive"
                  onClick={() => setDeleteOpen(true)}
                >
                  {t(($) => $.machine.delete_danger_row)}
                </DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
          {showListActions ? headerActions : null}
        </div>
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[760px] flex-col gap-5 px-4 py-4 md:px-6 md:py-5">
          <div className="flex items-start gap-3">
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <MachineNameEditor
                  machine={machine}
                  wsId={wsId}
                  variant="title"
                />
                {machine.isCurrent && (
                  <span className="shrink-0 rounded bg-foreground px-1.5 py-0.5 text-[10px] font-medium text-background">
                    {t(($) => $.machine.this_machine)}
                  </span>
                )}
              </div>
              <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                <RuntimeConnectivityStatus health={machine.health} />
                {machine.lastSeenAt && (
                  <>
                    <span className="text-muted-foreground/40">·</span>
                    <span>
                      {formatMachineLastSeen(
                        machine,
                        now,
                        t(($) => $.machine.last_seen_active_now),
                      )}
                    </span>
                  </>
                )}
                {machine.cliVersion && (
                  <>
                    <span className="text-muted-foreground/40">·</span>
                    <span className="font-mono text-[11px]">
                      {t(($) => $.machine.daemon_version_chip, {
                        version: machine.cliVersion,
                      })}
                    </span>
                  </>
                )}
                {headerBadge && (
                  <>
                    <span className="text-muted-foreground/40">·</span>
                    <RuntimeHealthStateInline health={headerBadge} />
                  </>
                )}
              </div>
              {updateIssue && (
                <p className="mt-2 max-w-2xl break-words text-xs text-destructive">
                  {updateIssue}
                </p>
              )}
            </div>
            {actions && <div className="shrink-0">{actions}</div>}
          </div>

          <section>
            <SectionTitle>{t(($) => $.machine.basics_section)}</SectionTitle>
            <div className="overflow-hidden rounded-xl border bg-card">
              <InfoRow label={t(($) => $.machine.basics_display_name)}>
                <MachineNameEditor
                  machine={machine}
                  wsId={wsId}
                  variant="basics"
                />
              </InfoRow>
              {hostname && (
                <InfoRow label={t(($) => $.machine.basics_hostname)}>
                  <span className="truncate font-mono text-sm">{hostname}</span>
                </InfoRow>
              )}
              {osLabel && (
                <InfoRow label={t(($) => $.machine.basics_os)}>
                  <span className="truncate text-sm">{osLabel}</span>
                </InfoRow>
              )}
              {machine.cliVersion && (
                <InfoRow label={t(($) => $.machine.basics_daemon)}>
                  <span className="truncate font-mono text-sm">
                    {t(($) => $.machine.version_prefix, {
                      version: machine.cliVersion,
                    })}
                  </span>
                </InfoRow>
              )}
            </div>
          </section>

          <MachineCodeAgentsSection machine={machine} />

          <section>
            <SectionTitle>{t(($) => $.machine.agents_section)}</SectionTitle>
            {machineAgents.length === 0 ? (
              <p className="px-1 text-sm text-muted-foreground">
                {t(($) => $.detail.no_agents)}
              </p>
            ) : isMobile ? (
              <div className="overflow-hidden rounded-xl border bg-card">
                {machineAgents.map((agent, idx) => {
                  const runtime = runtimeForAgent(agent, machine);
                  const activity = formatAgentActivity(agent.id, snapshot);
                  return (
                    <button
                      key={agent.id}
                      type="button"
                      onClick={() => openAgentPanel(agent.id)}
                      className={cn(
                        "flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-accent/50",
                        idx < machineAgents.length - 1 && "border-b",
                      )}
                    >
                      <ActorAvatar
                        actorType="agent"
                        actorId={agent.id}
                        size={28}
                        profileLink={false}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium underline decoration-muted-foreground/40 underline-offset-2">
                          {getActorName("agent", agent.id)}
                        </span>
                        <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                          {providerLabel(runtime)} · {activity}
                        </span>
                      </span>
                      <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground/45" />
                    </button>
                  );
                })}
              </div>
            ) : (
              <div className="overflow-hidden rounded-xl border bg-card">
                <table className="w-full border-collapse">
                  <thead>
                    <tr className="border-b text-left text-[11px] font-medium text-muted-foreground">
                      <th className="px-4 py-2.5">
                        {t(($) => $.machine.agents_section)}
                      </th>
                      <th className="px-4 py-2.5">
                        {t(($) => $.list.col_runtime)}
                      </th>
                      <th className="px-4 py-2.5">
                        {t(($) => $.list.col_workload)}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {machineAgents.map((agent) => {
                      const runtime = runtimeForAgent(agent, machine);
                      const activity = formatAgentActivity(agent.id, snapshot);
                      const tasks = snapshot.filter(
                        (task) => task.agent_id === agent.id,
                      );
                      const wl = deriveWorkloadDetail(tasks).workload;
                      return (
                        <tr
                          key={agent.id}
                          className="border-b transition-colors last:border-b-0 hover:bg-accent/40"
                        >
                          <td className="px-4 py-3">
                            <span className="inline-flex min-w-0 items-center gap-2">
                              <ActorAvatar
                                actorType="agent"
                                actorId={agent.id}
                                size={22}
                                profileLink={false}
                              />
                              <button
                                type="button"
                                onClick={() => openAgentPanel(agent.id)}
                                className="truncate text-sm font-medium underline decoration-muted-foreground/40 underline-offset-2 hover:text-foreground"
                              >
                                {getActorName("agent", agent.id)}
                              </button>
                            </span>
                          </td>
                          <td className="px-4 py-3 text-sm text-muted-foreground">
                            <span className="inline-flex items-center gap-1.5">
                              {runtime && (
                                <ProviderLogo
                                  provider={runtime.provider}
                                  className="h-3.5 w-3.5"
                                />
                              )}
                              {providerLabel(runtime)}
                            </span>
                          </td>
                          <td className="px-4 py-3 text-sm text-muted-foreground">
                            <span className="inline-flex items-center gap-1">
                              {/* task #7: genuinely running keeps the spinning
                                  loader; purely-queued keeps the static clock
                                  (folded into the same "Working" label in
                                  `activity`, but nothing's actually moving
                                  yet). */}
                              {wl === "working" && (
                                <Loader2 className="h-3 w-3 animate-spin text-running" />
                              )}
                              {wl === "queued" && (
                                <Clock className="h-3 w-3 text-muted-foreground" />
                              )}
                              {activity}
                            </span>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {workspacesEnabled && (
            <section>
              <SectionTitle>
                {t(($) => $.machine.workspaces_section)}
              </SectionTitle>
              {workspacesLoading ? (
                <div className="flex items-center gap-2 px-1 py-3 text-sm text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t(($) => $.machine.scan_workspaces)}
                </div>
              ) : workspacesData?.status === "offline" ||
                workspacesData?.status === "error" ? (
                <p className="px-1 text-sm text-muted-foreground">
                  {t(($) => $.machine.scan_workspaces_error)}
                </p>
              ) : (workspacesData?.items.length ?? 0) === 0 ? (
                <p className="px-1 text-sm text-muted-foreground">
                  {t(($) => $.machine.scan_workspaces_empty)}
                </p>
              ) : (
                <div className="overflow-hidden rounded-xl border bg-card">
                  {workspacesData!.items.map((ws, idx) => (
                    <div
                      key={ws.dir_name}
                      className={cn(
                        "flex items-center gap-3 px-4 py-3",
                        idx < workspacesData!.items.length - 1 && "border-b",
                      )}
                    >
                      <Folder className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-mono text-xs">
                          {ws.rel_path}
                        </span>
                        {ws.orphan && (
                          <span className="mt-0.5 block text-[11px] text-muted-foreground">
                            {t(($) => $.machine.workspace_orphan)}
                          </span>
                        )}
                      </span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="shrink-0 text-destructive hover:text-destructive"
                        disabled={deleteWorkspace.isPending || !primaryRuntimeId}
                        onClick={() => {
                          if (!primaryRuntimeId) return;
                          deleteWorkspace.mutate(ws.dir_name);
                        }}
                      >
                        {t(($) => $.machine.workspace_delete)}
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </section>
          )}

          <MachineDeleteControl
            machine={machine}
            wsId={wsId}
            onDeleted={onComputerDeleted}
            layout="row"
          />
        </div>
      </div>

      {canDelete && (
        <DeleteComputerDialog
          open={deleteOpen}
          onOpenChange={setDeleteOpen}
          machine={machine}
          wsId={wsId}
          canDelete={canDelete}
          onDeleted={() => {
            setDeleteOpen(false);
            onComputerDeleted?.();
          }}
        />
      )}

      {!machine.runtimes.length && bootstrapping && (
        <div className="px-6 py-8 text-center text-sm text-muted-foreground">
          {t(($) => $.page.bootstrapping.hint)}
        </div>
      )}
    </div>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
      {children}
    </h3>
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
    <div className="flex items-center justify-between gap-3 border-b px-4 py-3 last:border-b-0">
      <span className="shrink-0 text-xs text-muted-foreground">{label}</span>
      <span className="flex min-w-0 items-center justify-end">{children}</span>
    </div>
  );
}

function EmptyState({ onConnectRemote }: { onConnectRemote: () => void }) {
  const { t } = useT("runtimes");
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted">
        <Server className="h-6 w-6 text-muted-foreground" />
      </div>
      <h2 className="mt-4 text-base font-semibold text-foreground">
        {t(($) => $.page.empty.title)}
      </h2>
      <p className="mt-1 max-w-md text-sm text-muted-foreground">
        {t(($) => $.page.empty.hint)}
      </p>
      <Button type="button" size="sm" onClick={onConnectRemote} className="mt-5">
        <Plus className="h-3 w-3" />
        {t(($) => $.page.connect_remote)}
      </Button>
    </div>
  );
}

function RuntimesPageSkeleton({ isMobile }: { isMobile: boolean }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center justify-between px-4 py-4 md:px-5">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-8 w-8 rounded-md" />
      </div>
      {isMobile ? (
        <div className="space-y-0">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3 border-b px-4 py-3.5">
              <Skeleton className="h-2 w-2 rounded-full" />
              <div className="flex-1">
                <Skeleton className="h-4 w-32" />
                <Skeleton className="mt-1.5 h-3 w-48" />
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="px-5 py-2">
          <Skeleton className="mb-2 h-3 w-full" />
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="mb-2 h-10 w-full" />
          ))}
        </div>
      )}
    </div>
  );
}

export default RuntimesPage;
export type { RuntimesPageProps };
