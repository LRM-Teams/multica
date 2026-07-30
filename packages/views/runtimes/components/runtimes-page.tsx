"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  Cloud,
  Monitor,
  Plus,
  Server,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { runtimeListOptions, runtimeKeys } from "@multica/core/runtimes/queries";
import { useWSEvent } from "@multica/core/realtime";
import { agentListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { PageHeader } from "../../layout/page-header";
import { ConnectRemoteDialog } from "./connect-remote-dialog";
import { CloudRuntimeDialog } from "./cloud-runtime-dialog";
import { formatRuntimeUpdateError } from "./update-error";
import { RuntimeRows, buildWorkloadIndex } from "./runtime-list";
import { MachineDeleteControl } from "./delete-computer-dialog";
import {
  buildRuntimeMachines,
  filterRuntimeMachines,
  headerRuntimeHealthBadge,
  runtimeMachineCounts,
  type RuntimeMachine,
  type RuntimeMachineFilter,
} from "./runtime-machines";
import {
  HealthDot,
  ProviderChip,
  RuntimeConnectivityStatus,
  RuntimeHealthStateBadge,
  useHealthLabel,
} from "./shared";
import { formatLastSeen } from "../utils";
import { useT } from "../../i18n/use-t";

const MACHINE_FILTERS: RuntimeMachineFilter[] = ["all", "online", "issues"];

interface RuntimesPageProps {
  /** Desktop-only daemon id used to mark the row for this Mac. */
  localDaemonId?: string | null;
  /** Desktop-only friendly device name for the local daemon. */
  localMachineName?: string | null;
  /** Desktop-only controls shown when the local machine is selected. */
  localMachineActions?: React.ReactNode;
  /**
   * Desktop-only signal: this host always owns a local machine, even
   * when no runtime is currently registered (daemon stopped, not yet
   * started, or runtime GC'd). When true, a placeholder local row is
   * synthesized so `localMachineActions` (the daemon Start button) is
   * always reachable. Web omits this.
   */
  hasLocalMachine?: boolean;
  /**
   * Desktop-only signal: the bundled daemon is still booting / hasn't
   * registered with the server yet. Forwarded so the empty state can show
   * a "starting" indicator instead of the static "register a runtime" hint
   * during the boot window. Web omits this.
   */
  bootstrapping?: boolean;
  /** Web SaaS-only Cloud Runtime entrypoint. Defaults off for self-hosted builds. */
  cloudRuntimeEnabled?: boolean;
}

// Re-render every 30s so derived health (recently_lost → offline transitions)
// catches up even when no underlying query data has changed.
function useNowTick(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

/**
 * LRM-745 (design gate v3, Frank-frozen): chat-style two-pane shell.
 * Desktop = fixed machine-list column (~300px, conversation-list style) +
 * detail panel side by side. Mobile = list page › drill into a detail page
 * with a back button — only one view renders at a time, so the main
 * content is always a single vertical scroller (this is what structurally
 * absorbs LRM-737 "page won't scroll on phones": no more shrink-0 sidebar
 * stacked above the detail with no bounded scroll container).
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
  const [machineFilter, setMachineFilter] =
    useState<RuntimeMachineFilter>("all");
  // Only the user's explicit pick is held in state; the effective selection
  // is derived during render (see below), so no sync effect is needed.
  const [userPickId, setUserPickId] = useState<string | null>(null);
  // Mobile drill-in: list page until a row is tapped, then the detail page
  // until Back. Desktop ignores this flag (both panes stay visible).
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const handleSelectMachine = useCallback((id: string) => {
    setUserPickId(id);
    setMobileDetailOpen(true);
  }, []);
  const handleMobileBack = useCallback(() => setMobileDetailOpen(false), []);
  const [showConnectDialog, setShowConnectDialog] = useState(false);
  const [showCloudRuntimeDialog, setShowCloudRuntimeDialog] = useState(false);
  const isMobile = useIsMobile();

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

  const machineCounts = useMemo(() => runtimeMachineCounts(machines), [machines]);

  const filteredMachines = useMemo(
    () => filterRuntimeMachines(machines, "", machineFilter),
    [machines, machineFilter],
  );

  // Derived selection: an explicit user pick wins while its machine is still
  // in the filtered list. Otherwise desktop defaults to the Local section as
  // soon as it shows up (`localDaemonId` resolves async, so a remote may have
  // been the first-paint default), falling back to the first machine. Mobile
  // has no default — the list page shows until the user drills in.
  const userPickValid =
    userPickId !== null &&
    filteredMachines.some((m) => m.id === userPickId);
  const selectedMachineId = isMobile
    ? userPickId
    : userPickValid
      ? userPickId
      : (filteredMachines.find((m) => m.section === "local")?.id ??
        filteredMachines[0]?.id ??
        null);

  const selectedMachine =
    machines.find((machine) => machine.id === selectedMachineId) ?? null;
  const desktopMachine = selectedMachine ?? filteredMachines[0] ?? null;

  if (isLoading || fetching) return <RuntimesPageSkeleton />;

  const totalCount = runtimes.length;
  // Desktop always has a synthesized local machine row, so the
  // "register a runtime" empty state would hide the Start button.
  const showEmpty = totalCount === 0 && !bootstrapping && !hasLocalMachine;

  const handleComputerDeleted = () => {
    setUserPickId(null);
    setMobileDetailOpen(false);
  };

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

  const listHeader = (
    <MachineListHeader
      machines={machines}
      onAdd={() => setShowConnectDialog(true)}
      cloudRuntimeEnabled={cloudRuntimeEnabled}
      onOpenCloudRuntime={() => setShowCloudRuntimeDialog(true)}
    />
  );

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      {isMobile ? (
        mobileDetailOpen && selectedMachine ? (
          <MachineDetail
            machine={selectedMachine}
            now={now}
            wsId={wsId}
            actions={
              selectedMachine.isCurrent ? localMachineActions : undefined
            }
            onComputerDeleted={handleComputerDeleted}
            onBack={handleMobileBack}
          />
        ) : (
          <div className="flex min-h-0 flex-1 flex-col">
            <PageHeader className="justify-between gap-2 px-4">
              <MachineListSummary machines={machines} />
              <MachineListActions
                onAdd={() => setShowConnectDialog(true)}
                cloudRuntimeEnabled={cloudRuntimeEnabled}
                onOpenCloudRuntime={() => setShowCloudRuntimeDialog(true)}
              />
            </PageHeader>
            <MachineFilterBar
              counts={machineCounts}
              filter={machineFilter}
              setFilter={setMachineFilter}
            />
            <div className="min-h-0 flex-1 overflow-y-auto p-3">
              <MachineRows
                machines={filteredMachines}
                totalMachines={machines.length}
                selectedMachineId={null}
                showChevron
                card
                onSelect={handleSelectMachine}
              />
            </div>
          </div>
        )
      ) : (
        <div className="flex min-h-0 flex-1">
          <aside className="flex min-h-0 w-[300px] shrink-0 flex-col border-r">
            {listHeader}
            <MachineFilterBar
              counts={machineCounts}
              filter={machineFilter}
              setFilter={setMachineFilter}
            />
            <div className="min-h-0 flex-1 overflow-y-auto py-1">
              <MachineRows
                machines={filteredMachines}
                totalMachines={machines.length}
                selectedMachineId={desktopMachine?.id ?? null}
                onSelect={handleSelectMachine}
              />
            </div>
          </aside>
          <MachineDetail
            machine={desktopMachine}
            now={now}
            bootstrapping={bootstrapping}
            wsId={wsId}
            actions={desktopMachine?.isCurrent ? localMachineActions : undefined}
            onComputerDeleted={handleComputerDeleted}
          />
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

// ---------------------------------------------------------------------------
// Machine list column (frozen v3): title + "N machines · M online" summary,
// ＋ add-computer entry, All / Online / Issues chips, then flat rows —
// conversation-list style, no Local/Remote/Cloud section dividers.
// ---------------------------------------------------------------------------

function MachineListSummary({ machines }: { machines: RuntimeMachine[] }) {
  const { t } = useT("runtimes");
  const onlineRuntimes = machines.reduce((sum, m) => sum + m.onlineCount, 0);
  return (
    <div className="min-w-0">
      <h1 className="text-sm font-semibold">{t(($) => $.page.title)}</h1>
      <p className="text-[11px] text-muted-foreground">
        {t(($) => $.machine.machine_count, { count: machines.length })}
        {" · "}
        {t(($) => $.machine.online_count, { count: onlineRuntimes })}
      </p>
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
        variant="ghost"
        size="icon-sm"
        onClick={onAdd}
        aria-label={t(($) => $.page.connect_remote)}
        title={t(($) => $.page.connect_remote)}
      >
        <Plus className="h-4 w-4" />
      </Button>
    </div>
  );
}

function MachineListHeader({
  machines,
  onAdd,
  cloudRuntimeEnabled,
  onOpenCloudRuntime,
}: {
  machines: RuntimeMachine[];
  onAdd: () => void;
  cloudRuntimeEnabled: boolean;
  onOpenCloudRuntime: () => void;
}) {
  return (
    <div className="flex shrink-0 items-center justify-between gap-2 px-4 pb-2 pt-4">
      <MachineListSummary machines={machines} />
      <MachineListActions
        onAdd={onAdd}
        cloudRuntimeEnabled={cloudRuntimeEnabled}
        onOpenCloudRuntime={onOpenCloudRuntime}
      />
    </div>
  );
}

function MachineFilterBar({
  counts,
  filter,
  setFilter,
}: {
  counts: { all: number; online: number; issues: number };
  filter: RuntimeMachineFilter;
  setFilter: (value: RuntimeMachineFilter) => void;
}) {
  const { t } = useT("runtimes");
  return (
    <div className="flex shrink-0 items-center gap-1.5 overflow-x-auto border-b px-4 py-2">
      {MACHINE_FILTERS.map((key) => (
        <MachineFilterChip
          key={key}
          active={filter === key}
          onClick={() => setFilter(key)}
          label={t(($) => $.machine.filters[key])}
          count={counts[key]}
          tone={key}
        />
      ))}
    </div>
  );
}

function MachineFilterChip({
  active,
  onClick,
  label,
  count,
  tone,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  count: number;
  tone: RuntimeMachineFilter;
}) {
  const dotClass =
    tone === "online"
      ? "bg-success"
      : tone === "issues"
        ? "bg-warning"
        : "bg-muted-foreground/40";
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={onClick}
      className={cn(
        "h-7 gap-1.5 px-2 text-xs",
        active
          ? "bg-accent text-accent-foreground hover:bg-accent/80"
          : "bg-background text-muted-foreground",
      )}
    >
      {tone !== "all" && <span className={cn("h-1.5 w-1.5 rounded-full", dotClass)} />}
      <span>{label}</span>
      <span className="font-mono tabular-nums text-muted-foreground/70">
        {count}
      </span>
    </Button>
  );
}

function MachineRows({
  machines,
  totalMachines,
  selectedMachineId,
  showChevron = false,
  card = false,
  onSelect,
}: {
  machines: RuntimeMachine[];
  totalMachines: number;
  selectedMachineId: string | null;
  showChevron?: boolean;
  card?: boolean;
  onSelect: (id: string) => void;
}) {
  const { t } = useT("runtimes");
  if (machines.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center px-6 py-12 text-center">
        <Server className="h-8 w-8 text-muted-foreground/40" />
        <p className="mt-3 text-sm font-medium">
          {t(($) => $.machine.no_matches_title)}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          {totalMachines > 0
            ? t(($) => $.machine.no_matches_hint)
            : t(($) => $.page.bootstrapping.hint)}
        </p>
      </div>
    );
  }
  return (
    <div
      className={cn(card && "overflow-hidden rounded-xl border bg-card")}
    >
      {machines.map((machine, idx) => (
        <MachineRow
          key={machine.id}
          machine={machine}
          active={machine.id === selectedMachineId}
          showChevron={showChevron}
          divider={card && idx < machines.length - 1}
          onClick={() => onSelect(machine.id)}
        />
      ))}
    </div>
  );
}

function MachineRow({
  machine,
  active,
  showChevron,
  divider,
  onClick,
}: {
  machine: RuntimeMachine;
  active: boolean;
  showChevron: boolean;
  divider: boolean;
  onClick: () => void;
}) {
  const { t } = useT("runtimes");
  const Icon = machine.section === "cloud" ? Cloud : Monitor;
  const updateLabel =
    machine.runtimeHealth === "update_available" ||
    machine.runtimeHealth === "ready_to_apply"
      ? t(($) => $.machine.update_available)
      : null;
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "group flex w-full min-w-0 items-center gap-3 px-3.5 py-2.5 text-left transition-colors",
        divider && "border-b",
        active ? "bg-accent" : "hover:bg-accent/50",
      )}
    >
      <span className="relative flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border bg-background">
        <Icon className="h-4 w-4 text-muted-foreground" />
        <HealthDot
          health={machine.health}
          className="absolute -bottom-0.5 -right-0.5 ring-2 ring-background"
        />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <span
            className="truncate text-sm font-medium"
            title={
              machine.daemonId
                ? `daemon ${machine.daemonId}`
                : (machine.subtitle ?? undefined)
            }
          >
            {machine.title}
          </span>
          {machine.isCurrent && (
            <span className="shrink-0 rounded bg-foreground px-1.5 py-0.5 text-[10px] font-medium text-background">
              {t(($) => $.machine.this_machine)}
            </span>
          )}
        </span>
        <MachineRowMeta machine={machine} />
      </span>
      {/* Frozen v3: update availability is incremental small text only —
          the action itself lives in the machine detail. */}
      {updateLabel && (
        <span className="shrink-0 text-[11px] font-medium text-brand">
          {updateLabel}
        </span>
      )}
      {showChevron && (
        <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground/50" />
      )}
    </button>
  );
}

// Spec line under the machine name (frozen v3): "在线 · N 运行时 · M 运行中"
// while reachable — "M 运行中" only when something is actually busy,
// otherwise 空闲; unreachable machines show the connectivity label plus
// last-seen instead of runtime counts.
function MachineRowMeta({ machine }: { machine: RuntimeMachine }) {
  const { t } = useT("runtimes");
  const labelOf = useHealthLabel();
  const busyCount = machine.runningCount + machine.queuedCount;

  if (machine.health !== "online") {
    return (
      <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
        <HealthDot health={machine.health} />
        <span className="truncate">
          {labelOf(machine.health)}
          {machine.lastSeenAt && (
            <span className="text-muted-foreground/70">
              {" · "}
              {formatLastSeen(machine.lastSeenAt)}
            </span>
          )}
        </span>
      </span>
    );
  }

  return (
    <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
      <HealthDot health={machine.health} />
      <span>{labelOf(machine.health)}</span>
      {machine.runtimes.length > 0 && (
        <>
          <span className="text-muted-foreground/40">·</span>
          <span className="shrink-0">
            {t(($) => $.machine.runtime_count, {
              count: machine.runtimes.length,
            })}
          </span>
          <span className="text-muted-foreground/40">·</span>
          {busyCount > 0 ? (
            <span className="shrink-0 font-medium text-primary">
              {t(($) => $.machine.running_count, { count: busyCount })}
            </span>
          ) : (
            <span className="shrink-0">{t(($) => $.machine.idle)}</span>
          )}
        </>
      )}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Machine detail panel (frozen v3): header summary → Runtimes section
// (rows drill into each runtime's existing detail route) → Basic info card
// → bottom red "Delete this computer" row (same confirm flow as before).
// ---------------------------------------------------------------------------

function MachineDetail({
  machine,
  now,
  bootstrapping,
  actions,
  wsId,
  onComputerDeleted,
  onBack,
}: {
  machine: RuntimeMachine | null;
  now: number;
  bootstrapping?: boolean;
  actions?: React.ReactNode;
  wsId: string;
  onComputerDeleted?: () => void;
  /** Mobile only: renders the back bar and turns the panel into a page. */
  onBack?: () => void;
}) {
  const { t } = useT("runtimes");

  if (!machine) {
    return (
      <main className="flex min-h-0 min-w-0 flex-1 flex-col items-center justify-center px-6 text-center">
        {bootstrapping ? (
          <>
            <Server className="h-8 w-8 animate-pulse text-muted-foreground/40" />
            <p className="mt-3 text-sm text-muted-foreground">
              {t(($) => $.page.bootstrapping.title)}
            </p>
            <p className="mt-1 max-w-xs text-xs text-muted-foreground/70">
              {t(($) => $.page.bootstrapping.hint)}
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
  }

  const Icon = machine.section === "cloud" ? Cloud : Monitor;
  const busyCount = machine.runningCount + machine.queuedCount;
  const updateIssue = machine.updateError
    ? formatRuntimeUpdateError({
        rawError: machine.updateError,
        currentVersion: machine.cliVersion,
        targetVersion: machine.updateTargetVersion,
        t,
      })
    : "";
  // LRM-624 / Plan A: the secondary runtimeHealth badge is reserved for
  // *incremental* update info only. When runtimeHealth is offline and
  // connectivity is already offline-ish, it is suppressed (no duplicate
  // "Offline" next to the single connectivity dot above).
  const headerBadge = headerRuntimeHealthBadge(
    machine.runtimeHealth,
    machine.health,
  );

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col">
      {onBack && (
        <PageHeader className="gap-1 px-2">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={onBack}
            aria-label={t(($) => $.machine.back_to_list)}
          >
            <ChevronLeft className="h-5 w-5" />
          </Button>
          <span className="truncate text-sm font-medium">{machine.title}</span>
        </PageHeader>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[760px] flex-col gap-5 px-4 py-5 md:px-6">
          {/* Header summary */}
          <div className="flex items-start gap-3">
            <span className="relative flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border bg-background">
              <Icon className="h-4 w-4 text-muted-foreground" />
              <HealthDot
                health={machine.health}
                className="absolute -bottom-0.5 -right-0.5 ring-2 ring-background"
              />
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <h2 className="truncate text-lg font-semibold tracking-tight">
                  {machine.title}
                </h2>
                {machine.isCurrent && (
                  <span className="shrink-0 rounded bg-foreground px-1.5 py-0.5 text-[10px] font-medium text-background">
                    {t(($) => $.machine.this_machine)}
                  </span>
                )}
                {headerBadge ? (
                  <RuntimeHealthStateBadge health={headerBadge} />
                ) : null}
              </div>
              <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                {/* LRM-624 / Plan A: single connectivity dot+label —
                    connectivity is expressed exactly once here. */}
                <RuntimeConnectivityStatus health={machine.health} />
                {machine.health !== "online" && machine.lastSeenAt ? (
                  <span className="text-muted-foreground/70">
                    · {formatLastSeen(machine.lastSeenAt)}
                  </span>
                ) : null}
                {machine.health === "online" && machine.runtimes.length > 0 && (
                  <>
                    <span className="text-muted-foreground/40">·</span>
                    <span>
                      {t(($) => $.machine.runtime_count, {
                        count: machine.runtimes.length,
                      })}
                    </span>
                    <span className="text-muted-foreground/40">·</span>
                    {busyCount > 0 ? (
                      <span className="font-medium text-primary">
                        {t(($) => $.machine.running_count, { count: busyCount })}
                      </span>
                    ) : (
                      <span>{t(($) => $.machine.idle)}</span>
                    )}
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

          {/* Runtimes on this machine */}
          <section>
            <SectionTitle>{t(($) => $.machine.runtimes_section)}</SectionTitle>
            <RuntimeRows runtimes={machine.runtimes} now={now} />
          </section>

          {/* Basic info — machine name is read-only: the runtime PATCH
              endpoint only accepts `visibility`, and the display name is
              reported by the daemon itself (frozen v3 note: ✎ dropped
              because BE cannot persist a rename). */}
          <section>
            <SectionTitle>{t(($) => $.machine.info_section)}</SectionTitle>
            <div className="overflow-hidden rounded-xl border bg-card">
              <InfoRow label={t(($) => $.machine.info_name)}>
                <span className="truncate text-sm font-medium">
                  {machine.title}
                </span>
              </InfoRow>
              {machine.cliVersion && (
                <InfoRow label={t(($) => $.machine.info_cli)}>
                  <span className="truncate font-mono text-xs text-muted-foreground">
                    {machine.cliVersion}
                  </span>
                </InfoRow>
              )}
              {machine.daemonId && (
                <InfoRow label={t(($) => $.machine.info_daemon_id)}>
                  <span
                    className="truncate font-mono text-xs text-muted-foreground"
                    title={machine.daemonId}
                  >
                    {machine.daemonId}
                  </span>
                </InfoRow>
              )}
              {machine.providerNames.length > 0 && (
                <InfoRow label={t(($) => $.machine.info_providers)}>
                  <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                    {machine.providerNames.map((provider) => (
                      <ProviderChip key={provider} provider={provider} />
                    ))}
                  </span>
                </InfoRow>
              )}
            </div>
          </section>

          {/* Danger zone — same one-click computer delete + confirm flow,
              relocated from the header to the bottom red row per frozen v3. */}
          <MachineDeleteControl
            machine={machine}
            wsId={wsId}
            onDeleted={onComputerDeleted}
            layout="row"
          />
        </div>
      </div>
    </main>
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
    <div className="flex items-center gap-3 border-b px-4 py-3 last:border-b-0">
      <span className="w-28 shrink-0 text-xs text-muted-foreground">
        {label}
      </span>
      <span className="flex min-w-0 flex-1 items-center">{children}</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Empty state — shown when zero runtimes have ever registered in this
// workspace.
// ---------------------------------------------------------------------------

function EmptyState({ onConnectRemote }: { onConnectRemote: () => void }) {
  const { t } = useT("runtimes");
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted">
        <Server className="h-6 w-6 text-muted-foreground" />
      </div>
      <h2 className="mt-4 text-base font-semibold text-foreground">{t(($) => $.page.empty.title)}</h2>
      <p className="mt-1 max-w-md text-sm text-muted-foreground">
        {t(($) => $.page.empty.hint)}
      </p>
      <Button
        type="button"
        size="sm"
        onClick={onConnectRemote}
        className="mt-5"
      >
        <Plus className="h-3 w-3" />
        {t(($) => $.page.connect_remote)}
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Loading skeleton — laid out like the new shell: list column on the left
// (full-width on mobile, where the detail pane only exists after drill-in),
// detail panel beside it on desktop.
// ---------------------------------------------------------------------------

function RuntimesPageSkeleton() {
  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-h-0 w-full shrink-0 flex-col border-r md:w-[300px]">
        <div className="px-4 pb-2 pt-4">
          <Skeleton className="h-4 w-20" />
          <Skeleton className="mt-1.5 h-3 w-32" />
        </div>
        <div className="flex gap-2 border-b px-4 py-2">
          <Skeleton className="h-7 w-16 rounded-md" />
          <Skeleton className="h-7 w-20 rounded-md" />
          <Skeleton className="h-7 w-20 rounded-md" />
        </div>
        <div className="space-y-1 py-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3 px-3.5 py-2.5">
              <Skeleton className="h-9 w-9 rounded-lg" />
              <div className="flex-1">
                <Skeleton className="h-4 w-28" />
                <Skeleton className="mt-1.5 h-3 w-36" />
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className="hidden min-w-0 flex-1 flex-col md:flex">
        <div className="px-6 py-5">
          <div className="flex items-center gap-3">
            <Skeleton className="h-10 w-10 rounded-lg" />
            <div>
              <Skeleton className="h-5 w-44" />
              <Skeleton className="mt-2 h-3 w-56" />
            </div>
          </div>
          <Skeleton className="mt-6 h-3 w-16" />
          <div className="mt-2 space-y-0 rounded-xl border">
            {Array.from({ length: 3 }).map((_, i) => (
              <div
                key={i}
                className="flex items-center gap-3 border-b px-4 py-3 last:border-b-0"
              >
                <Skeleton className="h-6 w-6 rounded-full" />
                <Skeleton className="h-4 w-32" />
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

export default RuntimesPage;
export type { RuntimesPageProps };
