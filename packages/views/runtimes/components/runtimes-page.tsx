"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Cloud,
  Copy,
  Loader2,
  Monitor,
  Plus,
  RotateCcw,
  Square,
  X,
} from "lucide-react";
import { copyText } from "@multica/ui/lib/clipboard";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  agentTaskSnapshotOptions,
  useWorkspaceAgentPresence,
} from "@multica/core/agents";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import {
  computerListOptions,
  runtimeListOptions,
  runtimeKeys,
} from "@multica/core/runtimes/queries";
import {
  RUNTIME_ATTENTION_RUNTIME_QUERY,
  runtimeHasHealthAttention,
} from "@multica/core/runtimes";
import {
  useDeleteRuntimeAgentWorkspace,
  useRuntimeAgentWorkspaces,
} from "@multica/core/runtimes/mutations";
import { sandboxListOptions, sandboxKeys } from "@multica/core/sandboxes/queries";
import { useWSEvent } from "@multica/core/realtime";
import {
  agentListOptions,
  memberListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { useActorName } from "@multica/core/workspace/hooks";
import { resolveActorDisplayName } from "@multica/core/identity";
import type {
  Agent,
  AgentRuntime,
  AgentTask,
  CreateAgentRequest,
  SandboxInstance,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
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
import { AgentActivityListItem } from "../../agents/components/agent-activity-list-item";
import { CreateAgentDialog } from "../../agents/components/create-agent-dialog";
import { api } from "@multica/core/api";
import { AddComputerDialog } from "./add-computer-dialog";
import { ConnectRemoteDialog } from "./connect-remote-dialog";
import { CreateCloudComputerDialog } from "./create-cloud-computer-dialog";
import { CloudRuntimeDialog } from "./cloud-runtime-dialog";
import { SandboxEndpointLinks } from "../../sandboxes/components/sandbox-endpoint-links";
import { buildWorkloadIndex } from "./runtime-list";
import {
  buildRuntimeMachines,
  defaultDesktopSelectedMachineId,
  isMineMachine,
  machineDaemonUpgradeRuntimeId,
  machineHostname,
  machinePrimaryRuntimeId,
  mergePendingCloudComputers,
  decorateCloudComputerMachines,
  pendingCloudComputerMachineId,
  resolveCloudComputerSelectionId,
  splitRuntimeName,
  type RuntimeMachine,
} from "./runtime-machines";
import { MachineNameEditor } from "./machine-name-editor";
import {
  ComputerIcon,
  HealthDot,
  MachineConnectedStatus,
} from "./shared";
import { MachineCodeAgentsSection } from "./machine-code-agents-section";
import { MachineDangerZone } from "./machine-danger-zone";
import { MachineDaemonUpgrade } from "./machine-daemon-upgrade";
import { MachineHeaderOps } from "./machine-header-ops";
import { MachineWorkspacesSection } from "./machine-workspaces-section";
import { useT } from "../../i18n/use-t";
import { useNavigation } from "../../navigation";
import { formatOperatingSystem } from "../utils";

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
      return "Codex CLI";
    case "cursor":
      return "Cursor";
    case "opencode":
      return "OpenCode";
    case "pi":
      return "Pi";
    case "grok":
      return "Grok Build";
    case "kiro":
      return "Kiro";
    default: {
      const { base } = splitRuntimeName(runtime.name);
      return base || runtime.provider;
    }
  }
}

export function attentionMachineIdFromRuntime(
  machines: RuntimeMachine[],
  runtimeId: string | null,
  currentUserId: string | null,
): string | null {
  if (!runtimeId || !currentUserId) return null;
  return (
    machines.find((machine) =>
      machine.runtimes.some(
        (runtime) =>
          runtime.id === runtimeId &&
          runtimeHasHealthAttention(runtime, currentUserId),
      ),
    )?.id ?? null
  );
}

/** Mutually exclusive Computers overlays — one discriminant (prefer-useReducer). */
type PageOverlay = null | "add-chooser" | "connect" | "cloud-computer" | "cloud";

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
  const { pathname, replace, searchParams } = useNavigation();
  const [userPickId, setUserPickId] = useState<string | null>(null);
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const [pageOverlay, setPageOverlay] = useState<PageOverlay>(null);

  const { data: runtimes = [], isLoading: fetching } = useQuery(
    runtimeListOptions(wsId),
  );
  const { data: computerConnections = [] } = useQuery(computerListOptions(wsId));
  const { data: sandboxInstances = [] } = useQuery(sandboxListOptions(wsId));
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
      decorateCloudComputerMachines(
        mergePendingCloudComputers(
          buildRuntimeMachines(runtimes, {
            now,
            localDaemonId,
            localMachineName,
            currentUserId,
            workloadByRuntimeId: workloadIndex,
            ensureLocalMachine: hasLocalMachine,
            connections: computerConnections,
          }),
          sandboxInstances,
        ),
        sandboxInstances,
      ),
    [
      runtimes,
      computerConnections,
      sandboxInstances,
      now,
      localDaemonId,
      localMachineName,
      currentUserId,
      workloadIndex,
      hasLocalMachine,
    ],
  );

  const attentionRuntimeId = searchParams.get(
    RUNTIME_ATTENTION_RUNTIME_QUERY,
  );
  const searchParamsString = searchParams.toString();
  // react-doctor-disable-next-line react-doctor/no-event-handler -- consume one-shot URL deep-link into selection; no user event owns this
  useEffect(() => {
    if (!attentionRuntimeId || fetching || isLoading) return;

    const machineId = attentionMachineIdFromRuntime(
      machines,
      attentionRuntimeId,
      currentUserId ?? null,
    );
    if (machineId) {
      setUserPickId(machineId);
      setMobileDetailOpen(true);
    }

    // Consume the one-shot selection parameter. Besides keeping the URL tidy,
    // this lets Mobile Back return to the list instead of reopening the same
    // detail forever. A forged/other-owner runtime id is simply discarded.
    const nextSearchParams = new URLSearchParams(searchParamsString);
    nextSearchParams.delete(RUNTIME_ATTENTION_RUNTIME_QUERY);
    const nextSearch = nextSearchParams.toString();
    replace(nextSearch ? `${pathname}?${nextSearch}` : pathname);
  }, [
    attentionRuntimeId,
    currentUserId,
    fetching,
    isLoading,
    machines,
    pathname,
    replace,
    searchParamsString,
  ]);

  // Pending cloud id → registered daemon id; keep using resolved value (no mirror effect).
  const resolvedUserPickId = resolveCloudComputerSelectionId(
    machines,
    userPickId,
  );

  const userPickValid =
    !!resolvedUserPickId && machines.some((m) => m.id === resolvedUserPickId);

  // LRM-1094: desktop default = isCurrent → Mine[0]; never Team machines[0].
  const selectedMachineId = isMobile
    ? resolvedUserPickId
    : userPickValid
      ? resolvedUserPickId
      : defaultDesktopSelectedMachineId(machines, currentUserId ?? null);

  const selectedMachine =
    machines.find((m) => m.id === selectedMachineId) ?? null;
  const selectedCloudSandbox = useMemo(() => {
    const sandboxId = selectedMachine?.sandboxInstanceId?.trim();
    if (!sandboxId) return null;
    return sandboxInstances.find((instance) => instance.id === sandboxId) ?? null;
  }, [sandboxInstances, selectedMachine?.sandboxInstanceId]);

  const handleSelectMachine = useCallback((id: string) => {
    setUserPickId(id);
    setMobileDetailOpen(true);
  }, []);

  const handleMobileBack = useCallback(() => setMobileDetailOpen(false), []);

  const handleComputerDeleted = useCallback(() => {
    setUserPickId(null);
    setMobileDetailOpen(false);
  }, []);

  const openAddChooser = useCallback(() => setPageOverlay("add-chooser"), []);
  const closeOverlay = useCallback(() => setPageOverlay(null), []);
  const openConnectFromChooser = useCallback(
    () => setPageOverlay("connect"),
    [],
  );
  const openCloudComputerFromChooser = useCallback(
    () => setPageOverlay("cloud-computer"),
    [],
  );
  const openCloudRuntime = useCallback(() => setPageOverlay("cloud"), []);

  const handleCloudComputerCreated = useCallback(
    (instance: SandboxInstance) => {
      // Seed the React Query cache so the pending sidebar row appears before refetch.
      qc.setQueryData<SandboxInstance[]>(sandboxKeys.list(wsId), (prev) => {
        const list = prev ?? [];
        if (list.some((item) => item.id === instance.id)) return list;
        return [instance, ...list];
      });
      void qc.invalidateQueries({ queryKey: sandboxKeys.all(wsId) });
      setUserPickId(pendingCloudComputerMachineId(instance.id));
      setMobileDetailOpen(true);
      setPageOverlay(null);
    },
    [qc, wsId],
  );

  if (isLoading || fetching) return <RuntimesPageSkeleton isMobile={isMobile} />;

  const showEmpty =
    machines.length === 0 && !bootstrapping && !hasLocalMachine;

  if (showEmpty) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <PageHeader className="justify-between px-5">
          <div className="flex items-center gap-2">
            <Monitor className="h-4 w-4 text-muted-foreground" />
            <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
          </div>
        </PageHeader>
        <div className="flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-6">
          <EmptyState onConnectRemote={openAddChooser} />
        </div>
        {pageOverlay === "add-chooser" && (
          <AddComputerDialog
            onClose={closeOverlay}
            onChooseYourComputer={openConnectFromChooser}
            onChooseCloud={openCloudComputerFromChooser}
          />
        )}
        {pageOverlay === "connect" && (
          <ConnectRemoteDialog onClose={closeOverlay} />
        )}
        {pageOverlay === "cloud-computer" && (
          <CreateCloudComputerDialog
            onClose={closeOverlay}
            onCreated={handleCloudComputerCreated}
          />
        )}
      </div>
    );
  }

  const headerActions = (
    <MachineListActions
      onAdd={openAddChooser}
      cloudRuntimeEnabled={cloudRuntimeEnabled}
      onOpenCloudRuntime={openCloudRuntime}
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
      cloudSandbox={selectedCloudSandbox}
    />
  ) : (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col items-center justify-center px-6 text-center">
      {bootstrapping ? (
        <>
          <Monitor className="h-8 w-8 animate-pulse text-muted-foreground/40" />
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

      {pageOverlay === "add-chooser" && (
        <AddComputerDialog
          onClose={closeOverlay}
          onChooseYourComputer={openConnectFromChooser}
          onChooseCloud={openCloudComputerFromChooser}
        />
      )}
      {pageOverlay === "connect" && (
        <ConnectRemoteDialog onClose={closeOverlay} />
      )}
      {pageOverlay === "cloud-computer" && (
        <CreateCloudComputerDialog
          onClose={closeOverlay}
          onCreated={handleCloudComputerCreated}
        />
      )}
      {cloudRuntimeEnabled && pageOverlay === "cloud" && (
        <CloudRuntimeDialog onClose={closeOverlay} />
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
  agents: _agents,
  now: _now,
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
  const { t: tAgents } = useT("agents");
  const { getActorName } = useActorName();
  const showChevron = layout === "full";

  const mineMachines = machines.filter((m) => isMineMachine(m, currentUserId));
  const teamMachines = machines.filter((m) => !isMineMachine(m, currentUserId));
  // Frank 07-31: "分不清哪个是我的" — only worth splitting into two labeled
  // groups once there's actually a second owner to distinguish from. A
  // workspace where the viewer owns everything they can see stays a flat
  // list, same as before.
  const showGroups = teamMachines.length > 0;

  const connectivityLabel = (machine: RuntimeMachine) =>
    machine.health === "online"
      ? tAgents(($) => $.inspector.computer_connected)
      : tAgents(($) => $.inspector.computer_disconnected);

  const renderRow = (machine: RuntimeMachine, showOwnerBadge: boolean) => {
    const selected = selectedMachineId === machine.id;
    const ownerId = showOwnerBadge ? (machine.runtimes[0]?.owner_id ?? null) : null;
    const versionLabel = machine.cliVersion?.trim()
      ? t(($) => $.machine.version_prefix, {
          version: machine.cliVersion.trim(),
        })
      : "—";
    const statusLabel = connectivityLabel(machine);
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
          aria-label={`${machine.title}, ${statusLabel}`}
          onClick={() => onSelect(machine.id)}
          className="absolute inset-0 z-0 rounded-[inherit]"
        />
        {/* Computer kind icon (Monitor vs Cloud) + corner connectivity dot.
            Kind is the primary local/cloud read; health stays as the small
            corner signal so LRM-1094's non-text connectivity cue remains
            without a second leading column. */}
        <span
          className="relative z-10 flex size-5 shrink-0 items-center justify-center text-muted-foreground pointer-events-none"
          aria-hidden
        >
          <ComputerIcon kind={machine.mode} className="size-4" />
          <HealthDot
            health={machine.health}
            className="absolute -bottom-0.5 -right-0.5"
          />
        </span>
        <div className="relative z-10 min-w-0 flex-1 pointer-events-none">
          <div className="flex min-w-0 items-center">
            <MachineNameEditor
              machine={machine}
              wsId={wsId}
              variant="list"
            />
          </div>
          <div className="mt-0.5 truncate text-xs tabular-nums text-muted-foreground">
            {versionLabel}
          </div>
        </div>
        {ownerId ? (
          // Scoped to just the avatar+name, not the whole row content —
          // it's the only thing here with its own click handler
          // (ActorAvatar opens a profile popover). The name text next to
          // it has none, so it must stay pointer-events-none and let the
          // click fall through to the row's select button underneath
          // (LRM-923 / #23: this div used to carry pointer-events-auto
          // for a pencil-rename button that list rows no longer render,
          // silently eating clicks meant to select the row).
          <span className="pointer-events-auto relative z-10 inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
            <ActorAvatar actorType="member" actorId={ownerId} size={18} />
            <span className="max-w-20 truncate">
              {getActorName("member", ownerId)}
            </span>
          </span>
        ) : null}
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
          <Monitor className="h-4 w-4 text-muted-foreground" />
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
                      aria-label={t(($) => $.machine.section_team, {
                        count: teamMachines.length,
                      })}
                      className="group/team-toggle mt-2 flex w-full items-center gap-1 rounded-md px-2.5 py-1.5 text-left text-[11px] font-semibold uppercase tracking-wide text-muted-foreground hover:bg-accent/50"
                    />
                  }
                >
                  <ChevronRight className="h-3 w-3 stroke-[2.5] transition-transform duration-200 group-data-[panel-open]/team-toggle:rotate-90" />
                  {t(($) => $.machine.section_team, {
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
  /** Docker cloud computer sandbox row — used for Terminal / Pi / noVNC links. */
  cloudSandbox?: SandboxInstance | null;
};

/**
 * List-page machine detail. Remounts when `machine.id` changes so
 * workspace-scan / ops dialog state cannot leak across machines.
 */
export function ComputersMachineDetail(props: MachineDetailViewProps) {
  return <MachineDetailView key={props.machine.id} {...props} />;
}

function MachineDetailView({
  machine,
  agents,
  snapshot: _snapshot,
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
  cloudSandbox = null,
}: MachineDetailViewProps) {
  void _snapshot;
  const { t } = useT("runtimes");
  const { getActorName } = useActorName();
  const openAgentPanel = useAgentPanelStore((s) => s.open);
  const user = useAuthStore((s) => s.user);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const [workspacesEnabled, setWorkspacesEnabled] = useState(false);

  const primaryRuntimeId = machinePrimaryRuntimeId(machine, now);
  const primaryRuntime =
    machine.runtimes.find((r) => r.id === primaryRuntimeId) ??
    machine.runtimes[0] ??
    null;
  const daemonUpgradeRuntimeId = machineDaemonUpgradeRuntimeId(machine, now);
  const daemonUpgradeRuntime =
    machine.runtimes.find((r) => r.id === daemonUpgradeRuntimeId) ??
    primaryRuntime;
  const ownerId = machine.runtimes[0]?.owner_id ?? null;
  const ownerMember = ownerId
    ? members.find((m) => m.user_id === ownerId) ?? null
    : null;
  const canUpdate =
    !!user && !!daemonUpgradeRuntime && daemonUpgradeRuntime.owner_id === user.id;
  const {
    data: workspacesData,
    isFetching: workspacesLoading,
    refetch: refetchWorkspaces,
  } = useRuntimeAgentWorkspaces(primaryRuntimeId, workspacesEnabled);
  const deleteWorkspace = useDeleteRuntimeAgentWorkspace(primaryRuntimeId ?? "");

  const machineAgents = useMemo(
    () => agentsOnMachine(machine, agents),
    [machine, agents],
  );
  const { byAgent: presenceMap } = useWorkspaceAgentPresence(wsId);
  // One object for Select mode (react-doctor: avoid many related useState).
  const [select, setSelect] = useState<{
    mode: boolean;
    ids: Set<string>;
    busy: "stop" | "restart" | null;
  }>(() => ({ mode: false, ids: new Set(), busy: null }));
  const selectMode = select.mode;
  const selectedAgentIds = select.ids;
  const bulkBusy = select.busy;
  const [showCreateAgent, setShowCreateAgent] = useState(false);
  const { data: allRuntimes = [], isLoading: runtimesLoading } = useQuery(
    runtimeListOptions(wsId),
  );
  const qc = useQueryClient();
  const selectedCount = selectedAgentIds.size;
  const allSelected =
    machineAgents.length > 0 && selectedCount === machineAgents.length;

  const exitSelectMode = useCallback(() => {
    setSelect({ mode: false, ids: new Set(), busy: null });
  }, []);

  const toggleAgentSelected = (agentId: string) => {
    setSelect((prev) => {
      const next = new Set(prev.ids);
      if (next.has(agentId)) next.delete(agentId);
      else next.add(agentId);
      return { ...prev, ids: next };
    });
  };

  const selectAllAgents = () => {
    setSelect((prev) => ({
      ...prev,
      ids: new Set(machineAgents.map((a) => a.id)),
    }));
  };

  const clearSelection = () => {
    setSelect((prev) => ({ ...prev, ids: new Set() }));
  };

  const handleCreateAgent = async (data: CreateAgentRequest) => {
    const agent = await api.createAgent(data);
    await qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    setShowCreateAgent(false);
    return agent;
  };

  /** Raft-aligned bulk bar: Stop = cancel active tasks; Restart = `restart` mode. */
  const handleBulkStop = async () => {
    if (selectedCount === 0 || bulkBusy) return;
    const ids = Array.from(selectedAgentIds);
    setSelect((prev) => ({ ...prev, busy: "stop" }));
    try {
      const results = await Promise.all(
        ids.map(async (id) => {
          try {
            const res = await api.cancelAgentTasks(id);
            return { ok: true as const, cancelled: res.cancelled ?? 0 };
          } catch {
            return { ok: false as const, cancelled: 0 };
          }
        }),
      );
      let cancelled = 0;
      let failed = 0;
      for (const r of results) {
        if (r.ok) cancelled += r.cancelled;
        else failed += 1;
      }
      await qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      if (failed > 0) {
        showErrorToast(
          t(($) => $.machine.agents_bulk_stop_partial, {
            cancelled,
            failed,
          }),
        );
      } else {
        toast.success(
          t(($) => $.machine.agents_bulk_stop_done, { count: cancelled }),
        );
      }
    } finally {
      setSelect((prev) => ({ ...prev, busy: null }));
    }
  };

  const handleBulkRestart = async () => {
    if (selectedCount === 0 || bulkBusy) return;
    const ids = Array.from(selectedAgentIds);
    setSelect((prev) => ({ ...prev, busy: "restart" }));
    try {
      const results = await Promise.all(
        ids.map(async (id) => {
          try {
            await api.resetAgent(
              id,
              "restart",
              crypto.randomUUID(),
            );
            return true;
          } catch {
            return false;
          }
        }),
      );
      let ok = 0;
      let failed = 0;
      for (const r of results) {
        if (r) ok += 1;
        else failed += 1;
      }
      await qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      if (failed > 0) {
        showErrorToast(
          t(($) => $.machine.agents_bulk_restart_partial, { ok, failed }),
        );
      } else {
        toast.success(
          t(($) => $.machine.agents_bulk_restart_done, { count: ok }),
        );
      }
    } finally {
      setSelect((prev) => ({ ...prev, busy: null }));
    }
  };



  const hostname = machineHostname(machine);
  // Only the daemon-reported GOOS is an OS value. The device name is usually
  // the hostname, so using it here would duplicate the Hostname row.
  const osLabel = formatOperatingSystem(machine.os) ?? "—";
  // Task #81 — the daemon's locally-recorded MULTICA_PINNED_VERSION intent,
  // if any. Purely informational (server doesn't enforce it yet against a
  // server-initiated update — that's #81's still-unmade (b) half). Only
  // renders when set; the other three cases (no pin / missing field / empty
  // value) add nothing, per Iris/Parker 08-02.
  const primaryPinnedVersion =
    machine.runtimes.find((r) => r.id === primaryRuntimeId)?.pinned_version ??
    null;

  const scanWorkspaces = () => {
    if (!primaryRuntimeId) {
      showErrorToast(t(($) => $.machine.scan_workspaces_offline));
      return;
    }
    if (machine.health !== "online") {
      showErrorToast(t(($) => $.machine.scan_workspaces_offline));
      return;
    }
    if (workspacesEnabled) {
      void refetchWorkspaces();
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
                {/*
                  Frank/Iris 2026-08-02: this line answers exactly one question —
                  is the computer connected. No last-seen, no secondary
                  runtimeHealth badge (those collided as Online · … · Offline).
                  LRM-1071 / v5: daemon version + Upgrade live on the Basics
                  Daemon row — not repeated on this subtitle.
                */}
                <MachineConnectedStatus health={machine.health} />
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <MachineHeaderOps machine={machine} now={now} />
              {actions}
            </div>
          </div>

          <section>
            <SectionTitle>{t(($) => $.machine.basics_section)}</SectionTitle>
            <div className="overflow-hidden rounded-xl border bg-card">
              {/*
                Rename lives only on the hero title (single pencil). Basics
                no longer repeats Display name — same field, one entry point.
              */}
              {ownerMember && (
                <InfoRow label={t(($) => $.machine.basics_owner)}>
                  <span className="truncate text-sm">
                    {resolveActorDisplayName(ownerMember, ownerMember.user_id)}
                  </span>
                </InfoRow>
              )}
              {hostname && (
                <InfoRow label={t(($) => $.machine.basics_hostname)}>
                  <span className="truncate font-mono text-sm">{hostname}</span>
                </InfoRow>
              )}
              <InfoRow label={t(($) => $.machine.basics_os)}>
                <span className="truncate text-sm">{osLabel}</span>
              </InfoRow>
              {daemonUpgradeRuntime ? (
                <InfoRow label={t(($) => $.machine.basics_daemon)}>
                  <MachineDaemonUpgrade
                    runtime={daemonUpgradeRuntime}
                    cliVersion={machine.cliVersion}
                    daemonTargetVersion={machine.daemonTargetVersion}
                    updateError={machine.updateError}
                    isOnline={machine.health === "online"}
                    canUpdate={canUpdate}
                  />
                </InfoRow>
              ) : null}
              {primaryPinnedVersion?.trim() && (
                <InfoRow label={t(($) => $.machine.basics_pinned_version)}>
                  <span
                    className="truncate font-mono text-sm"
                    data-testid="machine-basics-pinned-version"
                  >
                    {t(($) => $.machine.version_prefix, {
                      version: primaryPinnedVersion.trim(),
                    })}
                  </span>
                </InfoRow>
              )}
              {machine.daemonId ? (
                <InfoRow label={t(($) => $.machine.basics_daemon_id)}>
                  <ComputerIdValue
                    id={machine.daemonId}
                    copyAria={t(($) => $.machine.copy_computer_id_aria)}
                  />
                </InfoRow>
              ) : null}
            </div>
          </section>

          {cloudSandbox ? (
            <section data-testid="machine-cloud-endpoint-links">
              <SandboxEndpointLinks instance={cloudSandbox} />
            </section>
          ) : null}

          <MachineCodeAgentsSection machine={machine} />

          <section data-testid="machine-agents-section">
            <div className="mb-2 flex items-center justify-between gap-3">
              <h3 className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                {t(($) => $.machine.agents_section)}
                {machineAgents.length > 0 ? (
                  <span className="ml-1.5 font-mono tabular-nums text-muted-foreground/70">
                    {machineAgents.length}
                  </span>
                ) : null}
              </h3>
              <div className="flex shrink-0 items-center gap-1.5">
                {selectMode ? (
                  <>
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      className="h-7 gap-1 px-2.5 text-[11px]"
                      onClick={allSelected ? clearSelection : selectAllAgents}
                      data-testid="machine-agents-select-all"
                      disabled={machineAgents.length === 0 || !!bulkBusy}
                    >
                      {allSelected
                        ? t(($) => $.machine.agents_clear_all)
                        : t(($) => $.machine.agents_select_all)}
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      className="h-7 gap-1 px-2.5 text-[11px]"
                      onClick={exitSelectMode}
                      data-testid="machine-agents-select-cancel"
                      disabled={!!bulkBusy}
                    >
                      <X className="h-3 w-3" />
                      {t(($) => $.machine.agents_cancel)}
                    </Button>
                  </>
                ) : (
                  <>
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      className="h-7 gap-1 px-2.5 text-[11px]"
                      onClick={() => {
                        setSelect({
                          mode: true,
                          ids: new Set(machineAgents.map((a) => a.id)),
                          busy: null,
                        });
                      }}
                      data-testid="machine-agents-select"
                      disabled={machineAgents.length === 0}
                    >
                      {t(($) => $.machine.agents_select)}
                    </Button>
                    <Button
                      type="button"
                      size="xs"
                      className="h-7 gap-1 px-2.5 text-[11px]"
                      onClick={() => setShowCreateAgent(true)}
                      data-testid="machine-agents-create"
                    >
                      <Plus className="h-3 w-3" />
                      {t(($) => $.machine.agents_create)}
                    </Button>
                  </>
                )}
              </div>
            </div>
            {machineAgents.length === 0 ? (
              <p className="px-1 text-sm text-muted-foreground">
                {t(($) => $.detail.no_agents)}
              </p>
            ) : (
              <>
                {selectMode && selectedCount > 0 ? (
                  <div
                    className="mb-2 flex flex-wrap items-center justify-between gap-2 rounded-xl border bg-muted/40 px-3 py-2"
                    data-testid="machine-agents-bulk-bar"
                  >
                    <span className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                      {t(($) => $.machine.agents_selected_count, {
                        count: selectedCount,
                      })}
                    </span>
                    <div className="flex shrink-0 items-center gap-1.5">
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        className="h-7 gap-1 px-2.5 text-[11px]"
                        onClick={() => void handleBulkStop()}
                        disabled={!!bulkBusy}
                        data-testid="machine-agents-bulk-stop"
                      >
                        {bulkBusy === "stop" ? (
                          <Loader2 className="h-3 w-3 animate-spin" />
                        ) : (
                          <Square className="h-3 w-3" />
                        )}
                        {t(($) => $.machine.agents_bulk_stop)}
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        className="h-7 gap-1 px-2.5 text-[11px]"
                        onClick={() => void handleBulkRestart()}
                        disabled={!!bulkBusy}
                        data-testid="machine-agents-bulk-restart"
                      >
                        {bulkBusy === "restart" ? (
                          <Loader2 className="h-3 w-3 animate-spin" />
                        ) : (
                          <RotateCcw className="h-3 w-3" />
                        )}
                        {t(($) => $.machine.agents_bulk_restart)}
                      </Button>
                    </div>
                  </div>
                ) : null}
                <div className="overflow-hidden rounded-xl border bg-card">
                  {machineAgents.map((agent, idx) => {
                    const runtime = runtimeForAgent(agent, machine);
                    return (
                      <AgentActivityListItem
                        key={agent.id}
                        agentId={agent.id}
                        displayName={getActorName("agent", agent.id)}
                        provider={runtime?.provider}
                        runtimeLabel={providerLabel(runtime)}
                        presence={presenceMap.get(agent.id) ?? null}
                        onClick={() =>
                          selectMode
                            ? toggleAgentSelected(agent.id)
                            : openAgentPanel(agent.id)
                        }
                        layout={isMobile ? "stacked" : "inline"}
                        showChevron={isMobile && !selectMode}
                        showBorder={idx < machineAgents.length - 1}
                        selectionMode={selectMode}
                        selected={selectedAgentIds.has(agent.id)}
                      />
                    );
                  })}
                </div>
              </>
            )}
            {showCreateAgent ? (
              <CreateAgentDialog
                runtimes={allRuntimes.length > 0 ? allRuntimes : machine.runtimes}
                runtimesLoading={runtimesLoading}
                members={members}
                currentUserId={user?.id ?? null}
                defaultMachineId={machine.id}
                onClose={() => setShowCreateAgent(false)}
                onCreate={handleCreateAgent}
              />
            ) : null}
          </section>

          <MachineWorkspacesSection
            machineOnline={machine.health === "online"}
            primaryRuntimeId={primaryRuntimeId}
            canUpdate={canUpdate}
            scanned={workspacesEnabled}
            loading={workspacesLoading}
            data={workspacesData}
            deletePending={deleteWorkspace.isPending}
            onScan={scanWorkspaces}
            onDelete={(dirName) => {
              if (!primaryRuntimeId) return;
              deleteWorkspace.mutate(dirName);
            }}
          />

          <MachineDangerZone
            machine={machine}
            onDeleted={onComputerDeleted}
          />

        </div>
      </div>

      {!machine.runtimes.length && bootstrapping && (
        <div className="px-6 py-8 text-center text-sm text-muted-foreground">
          {t(($) => $.page.bootstrapping.hint)}
        </div>
      )}
      {!machine.runtimes.length && machine.pendingCloud && (
        <div className="px-6 py-8 text-center text-sm text-muted-foreground">
          {t(($) => $.create_cloud_computer.provisioning_hint)}
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

/**
 * Full Computer ID + copy. Truncated IDs without copy are noise — either show
 * the complete value with a copy control, or omit the row entirely.
 */
function ComputerIdValue({
  id,
  copyAria,
}: {
  id: string;
  copyAria: string;
}) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1400);
    return () => clearTimeout(timer);
  }, [copied]);

  return (
    <span className="inline-flex min-w-0 max-w-full items-center justify-end gap-1.5">
      <span
        className="min-w-0 break-all text-right font-mono text-xs text-muted-foreground"
        data-testid="machine-basics-computer-id"
        title={id}
      >
        {id}
      </span>
      <button
        type="button"
        data-testid="machine-basics-computer-id-copy"
        aria-label={copyAria}
        title={copyAria}
        className="inline-flex size-7 shrink-0 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => {
          void copyText(id).then((ok) => {
            if (ok) setCopied(true);
          });
        }}
      >
        {copied ? (
          <Check className="size-3.5 text-success" aria-hidden />
        ) : (
          <Copy className="size-3.5" aria-hidden />
        )}
      </button>
    </span>
  );
}

function EmptyState({ onConnectRemote }: { onConnectRemote: () => void }) {
  const { t } = useT("runtimes");
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted">
        <Monitor className="h-6 w-6 text-muted-foreground" />
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
