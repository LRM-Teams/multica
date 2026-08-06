"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Globe, MoreHorizontal, Trash2 } from "lucide-react";
import { toast } from "sonner";
import type {
  Agent,
  AgentRuntime,
  AgentTask,
} from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  agentListOptions,
} from "@multica/core/workspace/queries";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { deriveWorkload } from "@multica/core/agents";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import {
  deriveRuntimeHealth,
  deriveRuntimeHealthPresentation,
} from "@multica/core/runtimes";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { workloadConfig } from "../../agents/presence";
import { useNavigation } from "../../navigation";
import { ProviderLogo } from "./provider-logo";
import {
  RuntimeHealthStateBadge,
  useHealthLabel,
} from "./shared";
import { DeleteRuntimeDialog } from "./delete-runtime-dialog";
import { splitRuntimeName } from "./runtime-machines";
import { formatLastSeen } from "../utils";
import { useT } from "../../i18n";

interface RuntimeWorkload {
  agentIds: string[];
  runningCount: number;
  queuedCount: number;
}

const EMPTY_WORKLOAD: RuntimeWorkload = {
  agentIds: [],
  runningCount: 0,
  queuedCount: 0,
};

// Per-runtime workload snapshot — agent IDs serving this runtime (drives
// the row avatar; .length doubles as the agent count) plus task counts
// split by status. Built once per render off the workspace-wide
// agents / agent-task-snapshot caches; filtered locally — no extra requests.
export function buildWorkloadIndex(
  agents: Agent[],
  tasks: AgentTask[],
): Map<string, RuntimeWorkload> {
  const result = new Map<string, RuntimeWorkload>();
  const agentToRuntime = new Map<string, string>();

  for (const a of agents) {
    if (!a.runtime_id || a.archived_at) continue;
    agentToRuntime.set(a.id, a.runtime_id);
    const entry =
      result.get(a.runtime_id) ?? {
        agentIds: [],
        runningCount: 0,
        queuedCount: 0,
      };
    entry.agentIds.push(a.id);
    result.set(a.runtime_id, entry);
  }
  for (const t of tasks) {
    const rid = agentToRuntime.get(t.agent_id);
    if (!rid) continue;
    const entry = result.get(rid);
    if (!entry) continue;
    if (t.status === "running") entry.runningCount += 1;
    else if (t.status === "queued" || t.status === "dispatched")
      entry.queuedCount += 1;
  }
  return result;
}

/**
 * LRM-745 (frozen v3): runtime rows replace the old nested DataTable. One
 * row per runtime on the selected machine — bound-agent avatar (or the
 * provider logo when nothing is bound), runtime name, a single
 * workload/connectivity status, chevron into the existing runtime-detail
 * route. Per-runtime delete stays reachable via the hover kebab so the
 * redesign doesn't drop an existing capability.
 */
export function RuntimeRows({
  runtimes,
  now,
}: {
  runtimes: AgentRuntime[];
  now: number;
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const slug = useWorkspaceSlug();
  const navigation = useNavigation();
  const user = useAuthStore((s) => s.user);

  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const workloadIndex = useMemo(
    () => buildWorkloadIndex(agents, snapshot),
    [agents, snapshot],
  );

  if (runtimes.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center px-6 py-12 text-center">
        <p className="text-sm text-muted-foreground">
          {t(($) => $.machine.no_runtimes_hint)}
        </p>
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-xl border bg-card">
      {runtimes.map((runtime, idx) => (
        <RuntimeRow
          key={runtime.id}
          runtime={runtime}
          workload={workloadIndex.get(runtime.id) ?? EMPTY_WORKLOAD}
          canDelete={!!user && runtime.owner_id === user.id}
          wsId={wsId}
          now={now}
          last={idx === runtimes.length - 1}
          onOpen={() => {
            if (!slug) return;
            navigation.push(paths.workspace(slug).computers());
          }}
        />
      ))}
    </div>
  );
}

function RuntimeRow({
  runtime,
  workload,
  canDelete,
  wsId,
  now,
  last,
  onOpen,
}: {
  runtime: AgentRuntime;
  workload: RuntimeWorkload;
  canDelete: boolean;
  wsId: string;
  now: number;
  last: boolean;
  onOpen: () => void;
}) {
  const { base: baseName } = splitRuntimeName(runtime.name);
  const primaryAgentId = workload.agentIds[0] ?? null;

  return (
    <div
      className={cn(
        "group flex w-full min-w-0 items-center gap-3 px-4 py-3",
        !last && "border-b",
      )}
    >
      <button
        type="button"
        onClick={onOpen}
        className="flex min-w-0 flex-1 items-center gap-3 text-left transition-colors"
      >
        {primaryAgentId ? (
          <ActorAvatar
            actorType="agent"
            actorId={primaryAgentId}
            size={24}
            profileLink={false}
          />
        ) : (
          <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border bg-background">
            <ProviderLogo provider={runtime.provider} className="h-4 w-4" />
          </span>
        )}
        <span className="flex min-w-0 flex-1 items-center gap-1.5">
          <span className="truncate text-sm font-medium">{baseName}</span>
          <VisibilityBadge runtime={runtime} />
        </span>
        <RuntimeRowStatus runtime={runtime} workload={workload} now={now} />
        <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground/50" />
      </button>
      {canDelete && (
        <div
          className="shrink-0 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100"
          onClick={(e) => e.stopPropagation()}
        >
          <RowMenu runtime={runtime} wsId={wsId} />
        </div>
      )}
    </div>
  );
}

// Single status slot on the row's right edge (frozen v3): an incremental
// update badge when the runtime has one; otherwise workload wins while
// busy ("Working"), connectivity carries offline states, and an online
// idle runtime reads "Idle" — matching the mock's 运行中 / 空闲 rows.
function RuntimeRowStatus({
  runtime,
  workload,
  now,
}: {
  runtime: AgentRuntime;
  workload: RuntimeWorkload;
  now: number;
}) {
  const { t: tAgents } = useT("agents");
  const labelOf = useHealthLabel();
  const updateHealth = deriveRuntimeHealthPresentation(runtime);
  if (updateHealth !== "ok") {
    return <RuntimeHealthStateBadge health={updateHealth} />;
  }
  const health = deriveRuntimeHealth(runtime, now);
  if (health !== "online") {
    const lastSeen = formatLastSeen(runtime.last_seen_at);
    return (
      <span className="inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
        <span
          className={cn(
            "h-1.5 w-1.5 rounded-full",
            health === "recently_lost"
              ? "bg-warning"
              : health === "about_to_gc"
                ? "bg-destructive"
                : "bg-muted-foreground/40",
          )}
        />
        <span className="truncate">
          {labelOf(health)}
          {runtime.last_seen_at && (
            <span className="text-muted-foreground/70"> · {lastSeen}</span>
          )}
        </span>
      </span>
    );
  }
  const workloadState = deriveWorkload({
    runningCount: workload.runningCount,
    queuedCount: workload.queuedCount,
  });
  const wl = workloadConfig[workloadState];
  return (
    <span className="inline-flex shrink-0 items-center gap-1 text-xs">
      {workloadState !== "idle" && (
        <wl.icon
          className={cn(
            "h-3 w-3",
            wl.textClass,
            workloadState === "working" && "animate-spin",
          )}
        />
      )}
      <span className={wl.textClass}>
        {tAgents(($) => $.workload[workloadState])}
      </span>
    </span>
  );
}

// Only public is worth a badge — private is the default and rendering a
// `Public` chip on every row turns the list into noise.
function VisibilityBadge({ runtime }: { runtime: AgentRuntime }) {
  const { t } = useT("runtimes");
  if (runtime.visibility !== "public") return null;
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span className="inline-flex shrink-0 items-center gap-0.5 rounded bg-brand/10 px-1 text-[10px] font-medium text-brand">
            <Globe className="h-2.5 w-2.5" />
            {t(($) => $.detail.visibility_label.public)}
          </span>
        }
      />
      <TooltipContent>{t(($) => $.detail.visibility_hint.public)}</TooltipContent>
    </Tooltip>
  );
}

function RowMenu({
  runtime,
  wsId,
}: {
  runtime: AgentRuntime;
  wsId: string;
}) {
  const { t } = useT("runtimes");
  const [deleteOpen, setDeleteOpen] = useState(false);
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t(($) => $.list.row_actions_aria)}
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => e.stopPropagation()}
            />
          }
        >
          <MoreHorizontal className="h-4 w-4 text-muted-foreground" />
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="w-40"
          onClick={(e) => e.stopPropagation()}
        >
          <DropdownMenuItem
            variant="destructive"
            onClick={() => setDeleteOpen(true)}
            title={t(($) => $.list.delete_permission_hint)}
          >
            <Trash2 className="h-3.5 w-3.5" />
            {t(($) => $.list.delete_action)}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <DeleteRuntimeDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        runtime={runtime}
        wsId={wsId}
        onDeleted={() => {
          setDeleteOpen(false);
          toast.success(t(($) => $.detail.toast_deleted));
        }}
      />
    </>
  );
}
