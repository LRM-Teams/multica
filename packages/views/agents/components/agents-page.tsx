"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertCircle,
  ArrowLeft,
  ArrowUpDown,
  Bot,
  Plus,
  Search,
} from "lucide-react";
import { Virtuoso } from "react-virtuoso";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  Agent,
  AgentRuntime,
  CreateAgentRequest,
  AgentCreationDraft,
} from "@multica/core/types";
import {
  agentRunCounts30dOptions,
  agentFleetRankingsOptions,
  summarizeActivityWindow,
  useWorkspaceActivityMap,
  useWorkspacePresenceMap,
} from "@multica/core/agents";
import { useAgentsViewStore } from "@multica/core/agents/stores";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  matchesActorIdentitySearch,
  resolveActorDisplayName,
} from "@multica/core/identity";
import {
  agentListOptions,
  memberListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { runtimeListOptions } from "@multica/core/runtimes";
import {
  dashboardUsageByAgentOptions,
  dashboardAgentRunTimeOptions,
} from "@multica/core/dashboard/queries";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Input } from "@multica/ui/components/ui/input";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useNavigation } from "../../navigation";
import { PageHeader } from "../../layout/page-header";
import {
  availabilityConfig,
  availabilityOrder,
  matchesLiveAvailabilityFilter,
  type LiveAvailability,
} from "../presence";
import { CreateAgentDialog } from "./create-agent-dialog";
import { useT } from "../../i18n";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { buildRuntimeMachines } from "../../runtimes/components/runtime-machines";
import { RuntimeMachineFilterDropdown } from "./runtime-machine-filter-dropdown";
import { AgentDetailOverview, type AgentMetric } from "./agent-detail-overview";
import { ConfirmDeleteAgent } from "./confirm-delete-agent";
import { ActorAvatar } from "../../common/actor-avatar";
import { estimateCost } from "../../runtimes/utils";
import { AgentOpenDmButton } from "./agent-open-dm-button";
import { AgentHonorLevelIcon } from "./agent-honor-level-icon";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { AgentActivityStatus } from "./agent-activity-list-item";
import type { AgentPresenceDetail } from "@multica/core/agents";

// Filter axes:
//
//   View           = active vs archived dataset. Archived is low-frequency,
//                    accessed through a ghost link in the toolbar.
//   Scope          = ownership lens (All vs Mine). Layer-1 segment.
//   Runtime machine = "Which host is the agent bound to?" — dropdown
//                    filter grouped by section (Local / Remote / Cloud).
//                    Mirrors the machine grouping on the Runtimes page
//                    so a user can drill from a machine into the agents
//                    hosted on it.
//   Availability   = "Can the agent take work right now?" — 3-state chip
//                    group (online / unstable / offline) sourced from
//                    AgentAvailability. The only chip filter we keep —
//                    the previous Workload axis was dropped because its
//                    "queued / failed / cancelled" buckets became
//                    meaningless once Failed left the workload model.
type View = "active" | "archived";
type Scope = "all" | "mine";
type AvailabilityFilter = "all" | LiveAvailability;

type SortKey = "recent" | "name" | "runs" | "created" | "fleet";
const SORT_KEYS: SortKey[] = ["recent", "fleet", "name", "runs", "created"];
const SORT_LABEL_KEY: Record<SortKey, "label_recent" | "label_fleet" | "label_name" | "label_runs" | "label_created"> = {
  recent: "label_recent",
  fleet: "label_fleet",
  name: "label_name",
  runs: "label_runs",
  created: "label_created",
};

const identitySearchOptions = { extendedMatch: matchesPinyin };

export interface AgentsPageProps {
  /**
   * Desktop-only daemon id for the current host. Forwarded into
   * `buildRuntimeMachines` so the local machine renders under the
   * "Local" section (rather than "Remote") on the same host that owns
   * the daemon. Web omits this — the SaaS shell doesn't bundle a
   * daemon, so the local section never has a real candidate anyway.
   */
  localDaemonId?: string | null;
  /**
   * Desktop-only friendly device name for the local daemon. Paired
   * with `localDaemonId` for the "Local" section title; web omits.
   */
  localMachineName?: string | null;
  /**
   * Desktop-only signal that this host always owns a local machine
   * row, even when no server-side runtime is currently registered
   * (daemon stopped, not yet started, or runtime GC'd). Mirrors
   * `RuntimesPage.hasLocalMachine`. The filter dropdown uses the
   * synthesized placeholder to keep "Local" available for selection
   * in the empty window.
   */
  hasLocalMachine?: boolean;
}

export function AgentsPage({
  localDaemonId = null,
  localMachineName = null,
  hasLocalMachine = false,
}: AgentsPageProps = {}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const qc = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);

  const {
    data: agents = [],
    isLoading,
    error: listError,
    refetch: refetchList,
  } = useQuery(agentListOptions(wsId, { includeArchived: true }));
  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(
    runtimeListOptions(wsId),
  );
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: runCountsRaw = [] } = useQuery(agentRunCounts30dOptions(wsId));
  const { data: fleetRankings = [] } = useQuery(agentFleetRankingsOptions(wsId));

  // Single source of truth for derived agent state. The hook owns the
  // 30s tick + the runtime/null/task orchestration; the page only reads
  // the resulting Maps. Replaces the 24-line useMemo presenceMap +
  // 12-line activityMap that lived here previously.
  const { byAgent: presenceMap } = useWorkspacePresenceMap(wsId);
  const { byAgent: activityMap } = useWorkspaceActivityMap(wsId);

  const [view, setView] = useState<View>("active");
  // Scope (Mine/All) is persisted per workspace so it survives list →
  // detail → back navigation. Default is "mine" on first visit.
  const scope = useAgentsViewStore((s) => s.scope);
  const setScope = useAgentsViewStore((s) => s.setScope);
  const [availabilityFilter, setAvailabilityFilter] =
    useState<AvailabilityFilter>("all");
  // `null` means "all runtimes" (the default). When set, the value is a
  // RuntimeMachine id from `buildRuntimeMachines` (the same grouping the
  // Runtimes page uses), so the user can drill from a machine on that
  // page into the agents bound to it.
  const [runtimeMachineId, setRuntimeMachineId] = useState<string | null>(null);
  const [sort, setSort] = useState<SortKey>("recent");
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [createDraft, setCreateDraft] = useState<AgentCreationDraft | null>(null);
  // When set, the Create dialog opens pre-populated with this agent's
  // config — driven by the row-level "Duplicate" action. We keep this
  // separate from `showCreate` so a stray null-template doesn't open the
  // dialog: the dialog opens iff `showCreate || duplicateTemplate`.
  const [duplicateTemplate, setDuplicateTemplate] = useState<Agent | null>(
    null,
  );
  const openBlankCreate = useCallback(() => {
    setCreateDraft(null);
    setDuplicateTemplate(null);
    setShowCreate(true);
  }, []);

  // Research / legacy: ?draft=<id> still opens draft-seeded create. Proposal
  // creation is rendered from its canonical Message, never a URL card id.
  useEffect(() => {
    const draftId = navigation.searchParams.get("draft");
    if (!draftId) return;
    let cancelled = false;
    (async () => {
      try {
        if (draftId) {
          const draft = await api.getAgentDraft(draftId);
          if (cancelled) return;
          setCreateDraft(draft);
          setDuplicateTemplate(null);
          setShowCreate(true);
        }
      } catch (err) {
        if (!cancelled) {
          showErrorToast(
            err instanceof Error
              ? err.message
              : "Failed to load Wendy draft",
          );
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [navigation.searchParams]);

  const runtimesById = useMemo(() => {
    const m = new Map<string, AgentRuntime>();
    for (const r of runtimes) m.set(r.id, r);
    return m;
  }, [runtimes]);

  const runCountsById = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of runCountsRaw) m.set(r.agent_id, r.run_count);
    return m;
  }, [runCountsRaw]);

  const fleetByAgentId = useMemo(() => {
    const m = new Map<string, (typeof fleetRankings)[number]>();
    for (const row of fleetRankings) m.set(row.agent_id, row);
    return m;
  }, [fleetRankings]);

  // Per-agent dashboard metrics (last 30d), sliced in the viewer's tz so the
  // day boundary matches the rest of the dashboard. Cost is summed across the
  // agent's per-model usage rows via the shared pricing table; success rate is
  // derived from terminal-task counts. Both feed the detail panel's stat cards.
  const tz = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone, []);
  const { data: usageByAgent = [] } = useQuery(dashboardUsageByAgentOptions(wsId, 30, null, tz));
  const { data: agentRunTime = [] } = useQuery(dashboardAgentRunTimeOptions(wsId, 30, null, tz));

  const costByAgent = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of usageByAgent) m.set(r.agent_id, (m.get(r.agent_id) ?? 0) + estimateCost(r));
    return m;
  }, [usageByAgent]);

  const successByAgent = useMemo(() => {
    const m = new Map<string, number | null>();
    for (const r of agentRunTime) {
      m.set(r.agent_id, r.task_count > 0 ? ((r.task_count - r.failed_count) / r.task_count) * 100 : null);
    }
    return m;
  }, [agentRunTime]);

  // Workspace role of the current user, used to gate row-level "manage"
  // operations (archive / cancel-tasks). Mirrors the back-end's
  // canManageAgent rule: workspace owner/admin OR the agent's owner.
  const myRole = useMemo(() => {
    if (!currentUser) return null;
    return members.find((m) => m.user_id === currentUser.id)?.role ?? null;
  }, [members, currentUser]);
  const isWorkspaceAdmin = myRole === "owner" || myRole === "admin";

  // Layer 1a — view (active / archived).
  const inView = useMemo(
    () =>
      agents.filter((a) =>
        view === "archived" ? !!a.archived_at : !a.archived_at,
      ),
    [agents, view],
  );

  // Layer 1b — visibility. Personal (visibility=private) agents owned by
  // someone else are hidden from regular members; workspace owners/admins
  // still see everything. Mirrors the assign-to-issue gate so the list
  // only ever shows agents the user could actually act on. Backend keeps
  // returning all agents, so admin tools (and the API itself) are
  // unaffected — this is a UI-only filter.
  const visibleInView = useMemo(() => {
    return inView.filter((a) =>
      canAssignAgentToIssue(a, {
        userId: currentUser?.id ?? null,
        role: myRole,
      }).allowed,
    );
  }, [inView, currentUser?.id, myRole]);

  // Layer 1c — ownership scope. Counts shown on the segment are
  // computed against the visibleInView set so the numbers always reflect
  // "what would I see if I clicked this".
  const scopeCounts = useMemo(() => {
    let mine = 0;
    if (currentUser) {
      for (const a of visibleInView) {
        if (a.owner_id === currentUser.id) mine += 1;
      }
    }
    return { all: visibleInView.length, mine };
  }, [visibleInView, currentUser]);

  const inScope = useMemo(() => {
    // Archived view ignores Mine / All — its toolbar has no scope
    // segment, so silently filtering by `scope` would hide other
    // people's archived agents without any UI to explain why.
    if (view === "archived") return visibleInView;
    if (scope === "all" || !currentUser) return visibleInView;
    return visibleInView.filter((a) => a.owner_id === currentUser.id);
  }, [visibleInView, scope, currentUser, view]);

  // Build the workspace's runtime machines (local / remote / cloud
  // groupings) the same way the Runtimes page does, so the filter
  // dropdown labels match the machines the user sees there. The
  // `now` clock only affects health rollups — we don't render health
  // chips in this list, so a snapshot from mount time is fine. We
  // also forward `localDaemonId` / `localMachineName` /
  // `hasLocalMachine` so the Local section (and the synthesized
  // placeholder on Desktop) appears here the same way it does on the
  // Runtimes page; `currentUserId` gates device-name consolidation
  // so a remote member's identically-named host doesn't get claimed
  // as the viewer's local machine.
  const [machinesNow] = useState(() => Date.now());
  const machines = useMemo(
    () =>
      buildRuntimeMachines(runtimes, {
        now: machinesNow,
        localDaemonId,
        localMachineName,
        currentUserId: currentUser?.id ?? null,
        ensureLocalMachine: hasLocalMachine,
      }),
    [runtimes, machinesNow, localDaemonId, localMachineName, currentUser?.id, hasLocalMachine],
  );

  // Reverse map: runtime_id → machine id. Lets the filter step look up
  // an agent's machine in O(1). Built off the machine grouping rather
  // than `runtimesById` so a runtime's machine identity matches the
  // dropdown labels (machines dedupe across providers by daemon).
  const runtimeIdToMachineId = useMemo(() => {
    const m = new Map<string, string>();
    for (const machine of machines) {
      for (const r of machine.runtimes) m.set(r.id, machine.id);
    }
    return m;
  }, [machines]);

  // Per-machine agent counts in `inScope` — used both for the chip
  // badges in the dropdown AND to make the runtime filter respect the
  // current scope (e.g. "Mine" only shows machines that have one of
  // my agents). Computed against `inScope` (not `visibleInView`).
  // Agents whose runtime doesn't map to a current machine
  // (e.g. bound to a GC'd runtime) are intentionally skipped here
  // — they still appear in the list when the filter is "All
  // runtimes", just not bucketed under any per-machine chip. The
  // "All runtimes" badge uses `inScope.length` directly so it stays
  // consistent with the unfiltered list.
  const agentCountByMachine = useMemo(() => {
    const counts = new Map<string, number>();
    for (const a of inScope) {
      const machineId = runtimeIdToMachineId.get(a.runtime_id);
      if (!machineId) continue;
      counts.set(machineId, (counts.get(machineId) ?? 0) + 1);
    }
    return counts;
  }, [inScope, runtimeIdToMachineId]);

  // If the selected machine is GC'd while we're on the page (daemon
  // stopped, runtime deleted), the filter would zero out the list with
  // no UI to clear it. Bounce back to "all" so the user always sees
  // something actionable.
  useEffect(() => {
    if (
      runtimeMachineId !== null &&
      !machines.some((machine) => machine.id === runtimeMachineId)
    ) {
      setRuntimeMachineId(null);
    }
  }, [runtimeMachineId, machines]);

  // Resolved title for the current machine filter — used by the
  // no-matches state so the user sees "No agents on `dev.local`" rather
  // than a bare "No agents match this filter" when the search is empty
  // but the machine filter is doing the narrowing.
  const selectedMachine = useMemo(
    () =>
      runtimeMachineId === null
        ? null
        : machines.find((machine) => machine.id === runtimeMachineId) ?? null,
    [runtimeMachineId, machines],
  );

  // Machine-scoped list: `inScope` narrowed by the selected runtime
  // machine, but NOT by the availability chip or search. The
  // availability row needs this intermediate step so its chips show
  // counts for "agents on this machine", not "agents on every machine"
  // — once a machine is selected, the chips further narrow the
  // already-machine-scoped list. The `inScope.length` total stays
  // available for the dropdown's "All runtimes" badge (the count the
  // user would see if they cleared the machine filter).
  const inScopeOnMachine = useMemo(() => {
    if (view !== "active") return inScope;
    if (runtimeMachineId === null) return inScope;
    return inScope.filter(
      (a) => runtimeIdToMachineId.get(a.runtime_id) === runtimeMachineId,
    );
  }, [inScope, view, runtimeMachineId, runtimeIdToMachineId]);

  // Final cut — availability chip + search. Starts from
  // `inScopeOnMachine` so a selected machine filter is already
  // applied; the availability chip and search refine within it.
  const filteredAgents = useMemo(() => {
    const q = search.trim().toLowerCase();
    return inScopeOnMachine.filter((a) => {
      // Availability chip filter only applies to the Active view —
      // archived agents have no presence to match against.
      if (view === "active" && availabilityFilter !== "all") {
        const detail = presenceMap.get(a.id);
        if (!matchesLiveAvailabilityFilter(detail?.availability, availabilityFilter)) {
          return false;
        }
      }
      if (q) {
        if (
          !matchesActorIdentitySearch(
            resolveActorDisplayName(a, a.name),
            a.name,
            q,
            identitySearchOptions,
          ) &&
          !(a.description ?? "").toLowerCase().includes(q)
        ) {
          return false;
        }
      }
      return true;
    });
  }, [
    inScopeOnMachine,
    view,
    availabilityFilter,
    presenceMap,
    search,
  ]);

  // Per-availability counts for the chip badges. Computed against
  // `inScopeOnMachine` (ignoring the availability filter itself) so
  // the numbers reflect "if I clicked this chip, this many agents
  // would match on the currently-selected machine" rather than
  // collapsing to 0 for the unselected chips.
  const availabilityCounts = useMemo(() => {
    // LRM-248: Online / Offline only — unstable folds into Online.
    const counts: Record<LiveAvailability, number> = {
      online: 0,
      offline: 0,
    };
    for (const a of inScopeOnMachine) {
      const detail = presenceMap.get(a.id);
      if (!detail) continue;
      if (matchesLiveAvailabilityFilter(detail.availability, "online")) {
        counts.online += 1;
      } else if (matchesLiveAvailabilityFilter(detail.availability, "offline")) {
        counts.offline += 1;
      }
    }
    return counts;
  }, [inScopeOnMachine, presenceMap]);

  const sortedAgents = useMemo(() => {
    const xs = [...filteredAgents];
    switch (sort) {
      case "name":
        xs.sort((a, b) =>
          resolveActorDisplayName(a, a.name).localeCompare(
            resolveActorDisplayName(b, b.name),
          ),
        );
        break;
      case "runs":
        xs.sort(
          (a, b) =>
            (runCountsById.get(b.id) ?? 0) - (runCountsById.get(a.id) ?? 0),
        );
        break;
      case "fleet":
        xs.sort((a, b) => {
          const af = fleetByAgentId.get(a.id);
          const bf = fleetByAgentId.get(b.id);
          const as = af?.fleet_score ?? -1;
          const bs = bf?.fleet_score ?? -1;
          if (as !== bs) return bs - as;
          return (af?.fleet_rank ?? 999) - (bf?.fleet_rank ?? 999);
        });
        break;
      case "created":
        xs.sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
        break;
      case "recent":
      default:
        // "Recent activity" prioritises 7d total completions (the same
        // window the row's sparkline shows), then 30d run count, then
        // created_at. We don't have a precise last-touched timestamp on
        // Agent today; this approximates it closely without a new column.
        xs.sort((a, b) => {
          const aSum = summarizeActivityWindow(
            activityMap.get(a.id),
            7,
          ).totalRuns;
          const bSum = summarizeActivityWindow(
            activityMap.get(b.id),
            7,
          ).totalRuns;
          if (aSum !== bSum) return bSum - aSum;
          const aRuns = runCountsById.get(a.id) ?? 0;
          const bRuns = runCountsById.get(b.id) ?? 0;
          if (aRuns !== bRuns) return bRuns - aRuns;
          return +new Date(b.created_at) - +new Date(a.created_at);
        });
        break;
    }
    return xs;
  }, [filteredAgents, sort, runCountsById, activityMap, fleetByAgentId]);

  // Master-detail selection. Falls back to the first visible agent whenever
  // the chosen one drops out of the filtered list (filter/search change), so
  // the detail pane is never blank while agents exist.
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [confirmDeleteSelected, setConfirmDeleteSelected] = useState(false);
  const [deletingSelected, setDeletingSelected] = useState(false);
  const selectedAgent = useMemo(() => {
    if (selectedId) {
      const found = sortedAgents.find((a) => a.id === selectedId);
      if (found) return found;
    }
    return sortedAgents[0] ?? null;
  }, [selectedId, sortedAgents]);

  const selectedMetric: AgentMetric | null = useMemo(() => {
    if (!selectedAgent) return null;
    return {
      runCount: runCountsById.get(selectedAgent.id) ?? 0,
      successRate: successByAgent.get(selectedAgent.id) ?? null,
      cost: costByAgent.has(selectedAgent.id) ? costByAgent.get(selectedAgent.id)! : null,
    };
  }, [selectedAgent, runCountsById, successByAgent, costByAgent]);

  const selectedCanManage = useMemo(() => {
    if (!selectedAgent) return false;
    const isOwner = !!currentUser?.id && selectedAgent.owner_id === currentUser.id;
    return isWorkspaceAdmin || isOwner;
  }, [selectedAgent, currentUser?.id, isWorkspaceAdmin]);

  // LRM-865: never call archiveAgent from the overview Delete button —
  // open ConfirmDeleteAgent first; only confirm submits.
  const handleArchiveSelected = useCallback(async () => {
    if (!selectedAgent) return;
    setDeletingSelected(true);
    try {
      await api.archiveAgent(selectedAgent.id);
      toast.success(t(($) => $.dashboard.delete_success));
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      setConfirmDeleteSelected(false);
    } catch (err) {
      showErrorToast(
        err instanceof Error && err.message ? err.message : t(($) => $.dashboard.delete_failed),
      );
    } finally {
      setDeletingSelected(false);
    }
  }, [selectedAgent, qc, wsId, t]);

  const archivedCount = useMemo(
    () => agents.filter((a) => !!a.archived_at).length,
    [agents],
  );

  const totalActiveCount = useMemo(
    () => agents.filter((a) => !a.archived_at).length,
    [agents],
  );

  // Auto-bounce out of Archived if the population empties (e.g. user
  // restored the last archived agent from another surface).
  useEffect(() => {
    if (view === "archived" && archivedCount === 0) setView("active");
  }, [view, archivedCount]);

  const handleCreate = async (data: CreateAgentRequest): Promise<Agent> => {
    const agent = await api.createAgent(data);
    // Skill follow-up is now owned by the dialog (it reads the user's
    // form selection, which already includes the duplicate source's
    // skills as a default when applicable). The dialog will call
    // setAgentSkills after we return; we just have to surface the
    // created agent so it can.
    qc.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (current = []) => {
      const exists = current.some((a) => a.id === agent.id);
      return exists
        ? current.map((a) => (a.id === agent.id ? agent : a))
        : [...current, agent];
    });
    setShowCreate(false);
    setCreateDraft(null);
    setDuplicateTemplate(null);
    navigation.push(paths.agentDetail(agent.id));
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    return agent;
  };

  // ---- Loading ----
  if (isLoading) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <PageHeaderBar
          totalCount={0}
          onCreate={openBlankCreate}
        />
        <div className="flex flex-1 min-h-0 flex-col gap-4 p-6">
          <div className="flex flex-1 min-h-0 flex-col overflow-hidden rounded-lg border">
            <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
              <Skeleton className="h-7 w-32 rounded-md" />
              <Skeleton className="h-7 w-32 rounded-md" />
            </div>
            <div className="flex h-11 shrink-0 items-center gap-2 border-b px-4">
              <Skeleton className="h-6 w-16 rounded-full" />
              <Skeleton className="h-6 w-24 rounded-full" />
              <Skeleton className="h-6 w-20 rounded-full" />
            </div>
            <div className="space-y-2 p-4">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-14 w-full rounded-md" />
              ))}
            </div>
          </div>
        </div>
      </div>
    );
  }

  // ---- List request error ----
  if (listError) {
    return (
      <ListError
        onCreate={openBlankCreate}
        listError={listError}
        onRetry={refetchList}
      />
    );
  }

  const showEmpty = totalActiveCount === 0 && archivedCount === 0;

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeaderBar
        totalCount={totalActiveCount}
        onCreate={openBlankCreate}
      />

      {showEmpty ? (
        <div className="flex flex-1 items-center justify-center">
          <EmptyState onCreate={openBlankCreate} />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1">
          {/* Left rail: header + compact filter toolbar + agent list */}
          <div className="flex w-[340px] shrink-0 flex-col border-r">
            <div className="flex h-12 shrink-0 items-center gap-2 px-4">
              {view === "archived" ? (
                <button
                  type="button"
                  onClick={() => setView("active")}
                  className="inline-flex items-center gap-1 text-sm font-semibold transition-colors hover:text-foreground"
                >
                  <ArrowLeft className="h-3.5 w-3.5" />
                  {t(($) => $.archived.title)}
                </button>
              ) : (
                <h2 className="text-sm font-semibold">{t(($) => $.dashboard.all_agents)}</h2>
              )}
              <span className="font-mono text-xs tabular-nums text-muted-foreground/60">
                {view === "archived" ? archivedCount : inScope.length}
              </span>
              {view === "active" && archivedCount > 0 && (
                <button
                  type="button"
                  onClick={() => setView("archived")}
                  className="ml-auto text-xs text-muted-foreground transition-colors hover:text-foreground"
                >
                  {t(($) => $.page.show_archived, { count: archivedCount })}
                </button>
              )}
            </div>

            <div className="flex flex-col gap-2 border-y px-3 py-2">
              <div className="relative">
                <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder={t(($) => $.page.search_placeholder)}
                  className="h-8 w-full pl-8 text-sm"
                />
              </div>
              {view === "active" && (
                <div className="flex flex-wrap items-center gap-1.5">
                  <ScopeSegment scope={scope} setScope={setScope} counts={scopeCounts} />
                  <div className="ml-auto flex items-center gap-1">
                    <RuntimeMachineFilterDropdown
                      machines={machines}
                      value={runtimeMachineId}
                      onChange={setRuntimeMachineId}
                      agentCountByMachine={agentCountByMachine}
                      totalAgentCount={inScope.length}
                    />
                    <SortDropdown sort={sort} setSort={setSort} />
                  </div>
                </div>
              )}
              {view === "archived" && (
                <div className="flex justify-end">
                  <SortDropdown sort={sort} setSort={setSort} />
                </div>
              )}
              {view === "active" && (
                <div className="flex flex-wrap items-center gap-1.5">
                  <AvailabilityChip
                    active={availabilityFilter === "all"}
                    onClick={() => setAvailabilityFilter("all")}
                    label={t(($) => $.availability.all)}
                    count={inScopeOnMachine.length}
                  />
                  {availabilityOrder.map((a) => (
                    <AvailabilityChip
                      key={a}
                      active={availabilityFilter === a}
                      onClick={() => setAvailabilityFilter(a)}
                      label={t(($) => $.availability[a])}
                      count={availabilityCounts[a]}
                      dotClass={availabilityConfig[a].dotClass}
                    />
                  ))}
                </div>
              )}
            </div>

            <div className="min-h-0 flex-1 py-1">
              {sortedAgents.length === 0 ? (
                <NoMatches
                  view={view}
                  search={search}
                  scope={scope}
                  runtimeMachineTitle={selectedMachine?.title ?? null}
                />
              ) : (
                // LRM-1264: window the rail — large workspaces keep ~150 agent
                // rows off-DOM without changing row chrome.
                <Virtuoso
                  className="h-full"
                  data={sortedAgents}
                  increaseViewportBy={{ top: 200, bottom: 200 }}
                  itemContent={(_, agent) => (
                    <AgentRailRow
                      agent={agent}
                      fleet={fleetByAgentId.get(agent.id)}
                      presence={presenceMap.get(agent.id)}
                      selected={selectedAgent?.id === agent.id}
                      onClick={() => setSelectedId(agent.id)}
                    />
                  )}
                />
              )}
            </div>
          </div>

          {/* Detail */}
          {selectedAgent && selectedMetric ? (
            <AgentDetailOverview
              key={selectedAgent.id}
              agent={selectedAgent}
              runtime={runtimesById.get(selectedAgent.runtime_id) ?? null}
              metric={selectedMetric}
              fleet={fleetByAgentId.get(selectedAgent.id)}
              canManage={selectedCanManage}
              onHonor={() =>
                navigation.push(
                  `${paths.agentDetail(selectedAgent.id)}?tab=honor`,
                )
              }
              onEdit={() => navigation.push(paths.agentDetail(selectedAgent.id))}
              onDelete={() => setConfirmDeleteSelected(true)}
            />
          ) : (
            <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
              {t(($) => $.dashboard.empty_select)}
            </div>
          )}
        </div>
      )}

      {selectedAgent ? (
        <ConfirmDeleteAgent
          open={confirmDeleteSelected}
          displayName={resolveActorDisplayName(selectedAgent, selectedAgent.id)}
          pending={deletingSelected}
          onConfirm={() => void handleArchiveSelected()}
          onOpenChange={setConfirmDeleteSelected}
        />
      ) : null}

      {showCreate && (
        <CreateAgentDialog
          runtimes={runtimes}
          runtimesLoading={runtimesLoading}
          members={members}
          currentUserId={currentUser?.id ?? null}
          template={duplicateTemplate}
          draft={createDraft}
          onClose={() => {
            setShowCreate(false);
            setCreateDraft(null);
            setDuplicateTemplate(null);
          }}
          onCreate={handleCreate}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Page header — icon + title + count + create CTA. Unchanged.
// ---------------------------------------------------------------------------

function PageHeaderBar({
  totalCount,
  onCreate,
}: {
  totalCount: number;
  onCreate: () => void;
}) {
  const { t } = useT("agents");
  return (
    <PageHeader className="justify-between px-5">
      <div className="flex items-center gap-2">
        <Bot className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
        {totalCount > 0 && (
          <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
            {totalCount}
          </span>
        )}
        {/* Tagline next to the title — mirrors Runtimes / Skills. */}
        <p className="ml-2 hidden text-xs text-muted-foreground md:block">
          {t(($) => $.page.tagline)}{" "}
          <a
            href="https://leagent.me/docs/agents"
            target="_blank"
            rel="noopener noreferrer"
            className="underline decoration-muted-foreground/30 underline-offset-4 transition-colors hover:text-foreground"
          >
            {t(($) => $.page.learn_more)}
          </a>
        </p>
      </div>
      <div className="flex items-center gap-2">
        <Button type="button" size="sm" onClick={onCreate}>
          <Plus className="h-3 w-3" />
          {t(($) => $.page.new_agent)}
        </Button>
      </div>
    </PageHeader>
  );
}

function ListError({
  onCreate,
  listError,
  onRetry,
}: {
  onCreate: () => void;
  listError: unknown;
  onRetry: () => void;
}) {
  const { t } = useT("agents");
  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeaderBar
        totalCount={0}
        onCreate={onCreate}
      />
      <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
        <AlertCircle className="h-8 w-8 text-destructive" />
        <div>
          <p className="text-sm font-medium">{t(($) => $.page.list_load_failed)}</p>
          <p className="mt-1 text-xs text-muted-foreground">
            {listError instanceof Error
              ? listError.message
              : t(($) => $.page.list_load_failed_default)}
          </p>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onRetry}
        >
          {t(($) => $.page.try_again)}
        </Button>
      </div>
    </div>
  );
}

function ScopeSegment({
  scope,
  setScope,
  counts,
}: {
  scope: Scope;
  setScope: (v: Scope) => void;
  counts: { all: number; mine: number };
}) {
  const { t } = useT("agents");
  return (
    <div className="flex items-center gap-0.5 rounded-md bg-muted p-0.5">
      <ScopeButton
        active={scope === "mine"}
        label={t(($) => $.scope.mine)}
        count={counts.mine}
        onClick={() => setScope("mine")}
      />
      <ScopeButton
        active={scope === "all"}
        label={t(($) => $.scope.all)}
        count={counts.all}
        onClick={() => setScope("all")}
      />
    </div>
  );
}

function ScopeButton({
  active,
  label,
  count,
  onClick,
}: {
  active: boolean;
  label: string;
  count: number;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors ${
        active
          ? "bg-background text-foreground shadow-sm"
          : "text-muted-foreground hover:text-foreground"
      }`}
    >
      <span>{label}</span>
      <span
        className={`font-mono tabular-nums ${
          active ? "text-muted-foreground/80" : "text-muted-foreground/50"
        }`}
      >
        {count}
      </span>
    </button>
  );
}

function SortDropdown({
  sort,
  setSort,
}: {
  sort: SortKey;
  setSort: (v: SortKey) => void;
}) {
  const { t } = useT("agents");
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className="h-8 gap-1.5 text-xs text-muted-foreground hover:text-foreground"
          />
        }
      >
        <ArrowUpDown className="h-3 w-3" />
        {t(($) => $.sort[SORT_LABEL_KEY[sort]])}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-auto">
        {SORT_KEYS.map((k) => (
          <DropdownMenuItem
            key={k}
            onClick={() => setSort(k)}
            className="text-xs"
          >
            {t(($) => $.sort[SORT_LABEL_KEY[k]])}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// ---------------------------------------------------------------------------
// Availability chip — All / Online / Unstable / Offline. Rendered inline in
// the rail's compact toolbar.
// ---------------------------------------------------------------------------

function AvailabilityChip({
  active,
  onClick,
  label,
  count,
  dotClass,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  count: number;
  dotClass?: string;
}) {
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={onClick}
      className={
        active
          ? "bg-accent text-accent-foreground hover:bg-accent/80"
          : "text-muted-foreground"
      }
    >
      {dotClass && <span className={`h-1.5 w-1.5 rounded-full ${dotClass}`} />}
      <span>{label}</span>
      <span className="font-mono tabular-nums text-muted-foreground/70">
        {count}
      </span>
    </Button>
  );
}

// ---------------------------------------------------------------------------
// Agent rail row — avatar + name + role subtitle + status pill. Selected row
// gets a left accent border (master-detail).
// ---------------------------------------------------------------------------

function AgentRailRow({
  agent,
  fleet,
  presence,
  selected,
  onClick,
}: {
  agent: Agent;
  fleet?: import("@multica/core/types/agent-fleet").AgentFleetRank;
  presence?: AgentPresenceDetail;
  selected: boolean;
  onClick: () => void;
}) {
	void presence;
  const { t } = useT("agents");
  const displayName = resolveActorDisplayName(agent, agent.name);
  const isArchived = !!agent.archived_at;
  return (
    // Outer div nests select (avatar+copy) and an explicit DM "小框" so
    // private chat is discoverable — avatar alone was not a clear entry
    // (LRM-1216). Profile-card Message (LRM-283) stays on its own track.
    <div
      className={cn(
        "flex w-full items-stretch border-l-2 transition-colors",
        selected
          ? "border-primary bg-accent"
          : "border-transparent hover:bg-accent/50",
      )}
    >
      <button
        type="button"
        onClick={onClick}
        className="flex min-w-0 flex-1 items-center gap-3 py-2.5 pl-3 pr-1.5 text-left"
      >
        <ActorAvatar
          actorType="agent"
          actorId={agent.id}
          size={32}
          fleetRank={fleet && !isArchived ? fleet.fleet_rank : undefined}
          showStatusDot={!isArchived}
          profileLink={false}
          className={isArchived ? "opacity-50 grayscale" : undefined}
        />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-1.5">
            <p
              className={cn(
                "truncate text-sm font-medium",
                isArchived ? "text-muted-foreground" : "text-foreground",
              )}
            >
              {displayName}
            </p>
            {agent.honor_level ? (
              <AgentHonorLevelIcon
                level={agent.honor_level}
                title={t(($) => $.honor_agent.level_value, {
                  level: agent.honor_level,
                })}
                className={cn(
                  "size-6 drop-shadow-sm",
                  isArchived && "opacity-50 grayscale",
                )}
              />
            ) : null}
          </div>
          <p className="truncate text-xs text-muted-foreground">
            {agent.description?.trim() || "—"}
          </p>
        </div>
        {!isArchived ? (
          <AgentActivityStatus
            agentId={agent.id}
            alignEnd
            className="max-w-[36%]"
          />
        ) : null}
      </button>
      {!isArchived ? (
        <div className="flex shrink-0 items-center pr-2">
          <AgentOpenDmButton agentId={agent.id} variant="icon" />
        </div>
      ) : null}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Empty / no-matches states
// ---------------------------------------------------------------------------

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useT("agents");
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted">
        <Bot className="h-6 w-6 text-muted-foreground" />
      </div>
      <h2 className="mt-4 text-base font-semibold">{t(($) => $.empty.title)}</h2>
      <p className="mt-1 max-w-md text-sm text-muted-foreground">
        {t(($) => $.empty.description)}
      </p>
      <Button type="button" onClick={onCreate} size="sm" className="mt-5">
        <Plus className="h-3 w-3" />
        {t(($) => $.page.new_agent)}
      </Button>
    </div>
  );
}

function NoMatches({
  view,
  search,
  scope,
  runtimeMachineTitle,
}: {
  view: View;
  search: string;
  scope: Scope;
  runtimeMachineTitle: string | null;
}) {
  const { t } = useT("agents");
  const hasSearch = search.length > 0;
  const hasFilter = scope === "mine";
  const hasRuntimeFilter = runtimeMachineTitle !== null;

  let body: string;
  if (view === "archived") {
    body = hasSearch
      ? t(($) => $.no_matches.search_archived, { query: search })
      : t(($) => $.no_matches.no_archived);
  } else if (hasSearch && hasRuntimeFilter) {
    body = t(($) => $.no_matches.search_runtime_filtered, {
      query: search,
      machine: runtimeMachineTitle,
    });
  } else if (hasSearch) {
    body = hasFilter
      ? t(($) => $.no_matches.search_active_filtered, { query: search })
      : t(($) => $.no_matches.search_active, { query: search });
  } else if (hasRuntimeFilter) {
    body = t(($) => $.no_matches.runtime_filtered, {
      machine: runtimeMachineTitle,
    });
  } else {
    body = t(($) => $.no_matches.no_filter_match);
  }

  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 px-4 py-16 text-center text-muted-foreground">
      <Search className="h-8 w-8 text-muted-foreground/40" />
      <p className="text-sm">{t(($) => $.no_matches.title)}</p>
      <p className="max-w-xs text-xs">{body}</p>
    </div>
  );
}
