"use client";

import { useMemo, useState } from "react";
import { RuntimeConfigFields } from "../../agents/components/runtime-config-fields";
import { useRuntimeConfigSelection } from "../../agents/components/use-runtime-config-selection";
import { Activity, GitBranch, Play, RefreshCw, ShieldCheck, Timer } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api, ApiError } from "@multica/core/api";
import { EMPTY_GRAPH_MEMORY_PROFILE } from "@multica/core/api/schemas";
import type { TrainingPolicyPatch } from "@multica/core/api/schemas";
import {
  evolutionKeys,
  graphMemoryAuditOptions,
  graphMemoryConsolidationsOptions,
  graphMemoryProfileOptions,
  graphMemoryStatusOptions,
} from "@multica/core/evolution/queries";
import type {
  GraphMemoryMode,
  GraphMemoryProfile,
  MemberWithUser,
  RuntimeDevice,
  UpdateGraphMemoryProfileRequest,
} from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Switch } from "@multica/ui/components/ui/switch";
import { cn } from "@multica/ui/lib/utils";
import { useEvolutionCopy } from "./evolution-center-page";

const TTT_CONCURRENCY_MIN = 1;
const TTT_CONCURRENCY_MAX = 64;

function clampExploreAgents(value: number): number {
  if (!Number.isFinite(value)) return TTT_CONCURRENCY_MIN;
  return Math.min(TTT_CONCURRENCY_MAX, Math.max(TTT_CONCURRENCY_MIN, Math.trunc(value)));
}

function graphMemoryTttUpdatePayload(
  profile: GraphMemoryProfile,
  patch: { recall_ttt_enabled: boolean; consolidation_ttt_enabled: boolean; explore_agents: number },
): UpdateGraphMemoryProfileRequest {
  return {
    memory_type: profile.memory_type,
    explore_agents: patch.explore_agents,
    explore_max_rounds: profile.explore_max_rounds,
    config_version: profile.config_version,
    ttt_enabled: patch.recall_ttt_enabled,
    recall_ttt_enabled: patch.recall_ttt_enabled,
    consolidation_ttt_enabled: patch.consolidation_ttt_enabled,
    explore_nodes_per_expansion: profile.explore_nodes_per_expansion,
    max_hierarchy_fanout: profile.max_hierarchy_fanout,
    max_relation_edges_per_node: profile.max_relation_edges_per_node,
    dive_max_rounds: profile.dive_max_rounds,
    dive_max_viewed_nodes: profile.dive_max_viewed_nodes,
    dive_max_source_files: profile.dive_max_source_files,
    dive_timeout_seconds: profile.dive_timeout_seconds,
    w_round: profile.w_round,
    source_max_file_bytes: profile.source_max_file_bytes,
    source_max_total_bytes: profile.source_max_total_bytes,
    source_max_pdf_pages: profile.source_max_pdf_pages,
    source_max_av_seconds: profile.source_max_av_seconds,
    source_max_image_megapixels: profile.source_max_image_megapixels,
    dive_model: profile.dive_model,
    dive_provider: profile.dive_provider,
  };
}

export function LegacyCurationNotApplicableCard() {
  const copy = useEvolutionCopy();
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldCheck className="h-4 w-4 text-muted-foreground" />
          {copy("legacyCurationNotApplicable")}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <p className="text-sm text-muted-foreground">{copy("legacyCurationNotApplicableHint")}</p>
      </CardContent>
    </Card>
  );
}

export function GraphMemoryStatusCard({ wsId }: { wsId: string }) {
  const copy = useEvolutionCopy();
  const { data: status, isLoading } = useQuery(graphMemoryStatusOptions(wsId));
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-brand" />{copy("graphStatus")}
        </CardTitle>
        <p className="text-sm text-muted-foreground">
          {status?.empty_start ? copy("graphEmptyStart") : copy("graphStatusHint")}
        </p>
      </CardHeader>
      <CardContent className="space-y-3">
        {isLoading && <Skeleton className="h-24 rounded-2xl" />}
        {(status?.graphs ?? []).map((graph) => (
          <div key={`${graph.kind}:${graph.owner_id}`} className="rounded-2xl border bg-muted/20 p-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="secondary">{graph.kind}</Badge>
              <span className="font-mono text-xs text-muted-foreground">{graph.owner_id}</span>
              {graph.consolidation_backoff && <Badge variant="outline">{copy("graphBackoff")}</Badge>}
            </div>
            <div className="mt-2 grid grid-cols-2 gap-2 text-xs text-muted-foreground sm:grid-cols-5">
              <span>{copy("graphVersion")}: {graph.current_version}</span>
              <span>{copy("graphStaging")}: {graph.staging_segments}</span>
              <span>{copy("graphNodes")}: {graph.node_count}</span>
              <span>{copy("graphRecall24h")}: {graph.recall_queries_24h}</span>
              <span>{copy("graphHitRate")}: {Math.round(graph.recall_hit_rate_24h * 100)}%</span>
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

export function GraphMemoryAuditCard({ wsId }: { wsId: string }) {
  const copy = useEvolutionCopy();
  const { data: audit } = useQuery(graphMemoryAuditOptions(wsId));
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <RefreshCw className="h-4 w-4 text-brand" />{copy("graphAudit")}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-3 gap-2 text-sm">
          <span>{copy("graphQueries24h")}: {audit?.queries_24h ?? 0}</span>
          <span>{copy("graphJudged24h")}: {audit?.judged_queries_24h ?? 0}</span>
          <span>{copy("graphRegressions")}: {audit?.regressions_total ?? 0}</span>
        </div>
      </CardContent>
    </Card>
  );
}

function isGraphMemoryParseFallback(profile: GraphMemoryProfile): boolean {
  // parseWithFallback returns EMPTY_GRAPH_MEMORY_PROFILE when schema
  // validation fails (spec §16). Empty workspace_id is the sentinel —
  // a real profile always carries the requesting workspace.
  return (
    profile.workspace_id === EMPTY_GRAPH_MEMORY_PROFILE.workspace_id &&
    profile.updated_at === EMPTY_GRAPH_MEMORY_PROFILE.updated_at
  );
}

export function GraphMemoryAgentModeCard({
  wsId,
  isAdmin,
  runtimes,
  runtimesLoading,
  members,
  currentUserId,
}: {
  wsId: string;
  isAdmin: boolean;
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  members: MemberWithUser[];
  currentUserId: string | null;
}) {
  const { data: profile } = useQuery(graphMemoryProfileOptions(wsId));

  if (!profile || profile.memory_type !== "graph" || isGraphMemoryParseFallback(profile)) return null;

  return (
    <GraphMemoryAgentModeForm
      key={`${profile.config_version}:${profile.memory_agent_runtime_id}:${profile.memory_agent_model}:${profile.memory_agent_thinking}`}
      wsId={wsId}
      isAdmin={isAdmin}
      profile={profile}
      runtimes={runtimes}
      runtimesLoading={runtimesLoading}
      members={members}
      currentUserId={currentUserId}
    />
  );
}

function GraphMemoryAgentModeForm({
  wsId,
  isAdmin,
  profile,
  runtimes,
  runtimesLoading,
  members,
  currentUserId,
}: {
  wsId: string;
  isAdmin: boolean;
  profile: GraphMemoryProfile;
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  members: MemberWithUser[];
  currentUserId: string | null;
}) {
  const copy = useEvolutionCopy();
  const queryClient = useQueryClient();
  const [mode, setMode] = useState<GraphMemoryMode>(profile.graph_memory_mode);
  const [tokensPerHour, setTokensPerHour] = useState(profile.memory_agent_max_tokens_per_hour);
  // The managed Graph Memory Agent currently runs on Pi. Keep the shared
  // Computer → Runtime → Model picker, but do not offer providers that the
  // control plane would reject when it reconciles channel agents.
  const eligibleRuntimes = useMemo(
    () => runtimes.filter((runtime) => runtime.provider === "pi"),
    [runtimes],
  );
  const selection = useRuntimeConfigSelection({
    runtimes: eligibleRuntimes,
    currentUserId,
    initialRuntimeId: profile.memory_agent_runtime_id,
    initialModel: profile.memory_agent_model,
    initialThinkingLevel: profile.memory_agent_thinking,
    autoSeedMachine: true,
  });

  const save = useMutation({
    mutationFn: () => api.updateGraphMemoryProfile(wsId, {
      memory_type: profile.memory_type,
      explore_agents: profile.explore_agents,
      explore_max_rounds: profile.explore_max_rounds,
      config_version: profile.config_version,
      graph_memory_mode: mode,
      memory_agent_runtime_id: selection.runtimeId.trim(),
      memory_agent_model: selection.model.trim(),
      memory_agent_thinking: selection.thinkingLevel.trim(),
      memory_agent_max_tokens_per_hour: Math.max(1000, Math.trunc(tokensPerHour)),
    }),
    onSuccess: async () => {
      toast.success(copy("graphAgentModeSaved"));
      await queryClient.invalidateQueries({ queryKey: evolutionKeys.graphMemoryProfile(wsId) });
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        void queryClient.invalidateQueries({ queryKey: evolutionKeys.graphMemoryProfile(wsId) });
        showErrorToast(copy("graphTttConflict"));
        return;
      }
      showErrorToast(error instanceof Error ? error.message : copy("graphAgentMode"));
    },
  });

  const configDisabled = !isAdmin || mode !== "agent" || save.isPending;

  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle>{copy("graphAgentMode")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("graphAgentModeHint")}</p>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-2 gap-2">
          <Button type="button" variant={mode === "agent" ? "default" : "outline"} disabled={!isAdmin || save.isPending} onClick={() => setMode("agent")}>{copy("graphAgentModeAgent")}</Button>
          <Button type="button" variant={mode === "inject" ? "default" : "outline"} disabled={!isAdmin || save.isPending} onClick={() => setMode("inject")}>{copy("graphAgentModeInject")}</Button>
        </div>
        <RuntimeConfigFields
          runtimes={eligibleRuntimes}
          runtimesLoading={runtimesLoading}
          members={members}
          currentUserId={currentUserId}
          machineId={selection.machineId}
          onMachineSelect={selection.selectMachine}
          machineRuntimes={selection.machineRuntimes}
          runtimeId={selection.runtimeId}
          onRuntimeSelect={selection.selectRuntime}
          model={selection.model}
          onModelChange={selection.selectModel}
          thinkingLevel={selection.thinkingLevel}
          onThinkingChange={selection.selectThinking}
          disabled={configDisabled}
        />
        <div className="space-y-1">
          <Label htmlFor="graph-memory-agent-token-limit">{copy("graphAgentTokensPerHour")}</Label>
          <Input id="graph-memory-agent-token-limit" type="number" min={1000} max={10000000} value={tokensPerHour} onChange={(event) => setTokensPerHour(Number.parseInt(event.target.value, 10) || 1000)} disabled={configDisabled} />
        </div>
        <Button variant="outline" disabled={!isAdmin || save.isPending} onClick={() => save.mutate()}>{copy("graphAgentModeSave")}</Button>
      </CardContent>
    </Card>
  );
}

export function GraphMemoryTttCard({ wsId, isAdmin }: { wsId: string; isAdmin: boolean }) {
  const copy = useEvolutionCopy();
  const { data: profile, refetch } = useQuery(graphMemoryProfileOptions(wsId));

  if (!profile) {
    return null;
  }

  if (isGraphMemoryParseFallback(profile)) {
    return (
      <Card className="bg-background/85 backdrop-blur">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Timer className="h-4 w-4 text-brand" />
            {copy("graphTtt")}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm text-destructive">{copy("graphTttParseError")}</p>
          <Button variant="outline" onClick={() => void refetch()}>
            {copy("graphTttRetry")}
          </Button>
        </CardContent>
      </Card>
    );
  }

  if (profile.memory_type !== "graph") {
    return null;
  }

  return (
    <GraphMemoryTttEditor
      key={profile.config_version}
      wsId={wsId}
      isAdmin={isAdmin}
      profile={profile}
    />
  );
}

function GraphMemoryTttEditor({
  wsId,
  isAdmin,
  profile,
}: {
  wsId: string;
  isAdmin: boolean;
  profile: GraphMemoryProfile;
}) {
  const copy = useEvolutionCopy();
  const queryClient = useQueryClient();
  const [recallTttEnabled, setRecallTttEnabled] = useState(profile.recall_ttt_enabled);
  const [consolidationTttEnabled, setConsolidationTttEnabled] = useState(profile.consolidation_ttt_enabled);
  const [exploreAgents, setExploreAgents] = useState(profile.explore_agents);

  const save = useMutation({
    mutationFn: (patch: { recall_ttt_enabled: boolean; consolidation_ttt_enabled: boolean; explore_agents: number }) =>
      api.updateGraphMemoryProfile(wsId, graphMemoryTttUpdatePayload(profile, patch)),
    onSuccess: async () => {
      toast.success(copy("graphTttSaved"));
      await queryClient.invalidateQueries({ queryKey: evolutionKeys.graphMemoryProfile(wsId) });
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        void queryClient.invalidateQueries({ queryKey: evolutionKeys.graphMemoryProfile(wsId) });
        showErrorToast(copy("graphTttConflict"));
        return;
      }
      showErrorToast(error instanceof Error ? error.message : copy("graphTtt"));
    },
  });

  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Timer className="h-4 w-4 text-brand" />
          {copy("graphTtt")}
        </CardTitle>
        <p className="text-sm text-muted-foreground">{copy("graphTttHint")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex items-center justify-between gap-3">
          <Label htmlFor="graph-recall-ttt-enabled" className="text-sm">{copy("graphRecallTtt")}</Label>
          <Switch
            id="graph-recall-ttt-enabled"
            checked={recallTttEnabled}
            onCheckedChange={(checked) => setRecallTttEnabled(checked === true)}
            disabled={!isAdmin || profile.graph_memory_mode === "agent" || save.isPending}
            aria-label={copy("graphRecallTtt")}
            title={profile.graph_memory_mode === "agent" ? copy("graphRecallTttAgentDisabled") : isAdmin ? undefined : copy("graphTttAdminOnly")}
          />
        </div>
        <div className="flex items-center justify-between gap-3">
          <Label htmlFor="graph-consolidation-ttt-enabled" className="text-sm">{copy("graphConsolidationTtt")}</Label>
          <Switch
            id="graph-consolidation-ttt-enabled"
            checked={consolidationTttEnabled}
            onCheckedChange={(checked) => setConsolidationTttEnabled(checked === true)}
            disabled={!isAdmin || save.isPending}
            aria-label={copy("graphConsolidationTtt")}
            title={isAdmin ? undefined : copy("graphTttAdminOnly")}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="graph-ttt-concurrency">{copy("graphTttConcurrency")}</Label>
          <Input
            id="graph-ttt-concurrency"
            type="number"
            min={TTT_CONCURRENCY_MIN}
            max={TTT_CONCURRENCY_MAX}
            step={1}
            value={exploreAgents}
            onChange={(event) => {
              const next = Number.parseInt(event.target.value, 10);
              if (!Number.isFinite(next)) return;
              setExploreAgents(Math.trunc(next));
            }}
            disabled={!isAdmin || !recallTttEnabled || profile.graph_memory_mode === "agent" || save.isPending}
            aria-label={copy("graphTttConcurrency")}
            title={isAdmin ? undefined : copy("graphTttAdminOnly")}
          />
          {!recallTttEnabled && (
            <p className="text-xs text-muted-foreground">{copy("graphTttEffectiveK")}</p>
          )}
        </div>
        <Button
          variant="outline"
          disabled={!isAdmin || save.isPending}
          onClick={() => save.mutate({
            recall_ttt_enabled: recallTttEnabled,
            consolidation_ttt_enabled: consolidationTttEnabled,
            explore_agents: clampExploreAgents(exploreAgents),
          })}
        >
          {copy("graphTttSave")}
        </Button>
      </CardContent>
    </Card>
  );
}

export function GraphMemoryConsolidationCard({ wsId, isAdmin }: { wsId: string; isAdmin: boolean }) {
  const copy = useEvolutionCopy();
  const queryClient = useQueryClient();
  const { data: runs } = useQuery(graphMemoryConsolidationsOptions(wsId));
  const start = useMutation({
    mutationFn: () => api.startGraphMemoryConsolidation(wsId),
    onSuccess: async () => {
      toast.success(copy("graphConsolidationQueued"));
      await queryClient.invalidateQueries({ queryKey: evolutionKeys.graphMemoryConsolidations(wsId) });
    },
    onError: (error) => showErrorToast(error instanceof Error ? error.message : copy("graphConsolidation")),
  });
  const latest = runs?.[0];
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Play className="h-4 w-4 text-brand" />{copy("graphConsolidation")}
        </CardTitle>
        <p className="text-sm text-muted-foreground">{copy("graphConsolidationHint")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        <Button
          variant="outline"
          className={cn("gap-2")}
          disabled={!isAdmin || start.isPending}
          onClick={() => start.mutate()}
        >
          <Play className="h-4 w-4" />{copy("graphRunConsolidation")}
        </Button>
        {latest && (
          <div className="text-xs text-muted-foreground">
            {copy("graphLastRun")}: {latest.status}
            {latest.error ? ` — ${latest.error}` : ""}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// Training governance (plan Task 18 / spec §14.1): owner-only card. The
// tenant grant needs an explicit CAS acknowledgement before training runs;
// the switches are the global kill switches surfaced through the same
// governance endpoint. 409s render an inline conflict state and refetch.
export function TrainingGovernanceCard({ wsId, isAdmin }: { wsId: string; isAdmin: boolean }) {
  const copy = useEvolutionCopy();
  const queryClient = useQueryClient();
  const key = ["evolution", wsId, "training-governance"] as const;
  const { data } = useQuery({
    queryKey: key,
    queryFn: () => api.getTrainingGovernance(wsId),
    enabled: isAdmin && Boolean(wsId),
    retry: false,
  });
  const [conflict, setConflict] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: key });
  const onMutationError = (fallback: string) => (error: Error) => {
    if (error instanceof ApiError && error.status === 409) {
      setConflict(true);
      void refresh();
      return;
    }
    showErrorToast(error instanceof Error ? error.message : fallback);
  };

  const ack = useMutation({
    mutationFn: () => api.updateTrainingGrant(wsId, {
      purpose: "tenant",
      action: "ack",
      expected_version: data?.grant.tenant_policy_version ?? 0,
    }),
    onSuccess: async () => {
      setConflict(false);
      toast.success(copy("graphTrainingAckSaved"));
      await refresh();
    },
    onError: onMutationError(copy("graphTrainingGovernance")),
  });
  const revoke = useMutation({
    mutationFn: () => api.updateTrainingGrant(wsId, {
      purpose: "tenant",
      action: "revoke",
      expected_version: data?.grant.tenant_policy_version ?? 0,
    }),
    onSuccess: async () => {
      setConflict(false);
      toast.success(copy("graphTrainingRevoked"));
      await refresh();
    },
    onError: onMutationError(copy("graphTrainingGovernance")),
  });
  const policy = useMutation({
    mutationFn: (patch: TrainingPolicyPatch) => api.updateTrainingPolicy(wsId, patch),
    onSuccess: async () => {
      setConflict(false);
      toast.success(copy("graphTrainingPolicySaved"));
      await refresh();
    },
    onError: onMutationError(copy("graphTrainingGovernance")),
  });

  if (!isAdmin) return null;

  const grantStatus = (status: string): string => {
    switch (status) {
      case "pending_owner_ack": return copy("graphTrainingStatusPendingOwnerAck");
      case "active": return copy("graphTrainingStatusActive");
      case "revoked": return copy("graphTrainingStatusRevoked");
      default: return copy("graphTrainingStatusDisabled");
    }
  };

  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldCheck className="h-4 w-4 text-brand" />{copy("graphTrainingGovernance")}
        </CardTitle>
        <p className="text-sm text-muted-foreground">{copy("graphTrainingGovernanceHint")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        {data ? (
          <>
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <Badge variant={data.grant.tenant_status === "active" ? "secondary" : "outline"}>
                {grantStatus(data.grant.tenant_status)}
              </Badge>
              <span className="text-xs text-muted-foreground">
                {copy("graphTrainingPolicyVersion").replace("{version}", String(data.grant.tenant_policy_version))}
              </span>
              {data.grant.tenant_status === "pending_owner_ack" ? (
                <Button size="sm" variant="outline" disabled={ack.isPending} onClick={() => ack.mutate()}>
                  {copy("graphTrainingAck")}
                </Button>
              ) : null}
              {data.grant.tenant_status === "active" || data.grant.tenant_status === "pending_owner_ack" ? (
                <Button size="sm" variant="ghost" disabled={revoke.isPending} onClick={() => revoke.mutate()}>
                  {copy("graphTrainingRevoke")}
                </Button>
              ) : null}
            </div>
            <div className="flex items-center justify-between gap-3">
              <Label htmlFor="training-selection-enabled" className="text-sm">{copy("graphTrainingSelection")}</Label>
              <Switch
                id="training-selection-enabled"
                checked={data.policy.selection_enabled}
                onCheckedChange={(checked) => policy.mutate({ selection_enabled: checked === true })}
                disabled={policy.isPending}
                aria-label={copy("graphTrainingSelection")}
                title={copy("graphTrainingAdminOnly")}
              />
            </div>
            <div className="flex items-center justify-between gap-3">
              <Label htmlFor="training-execution-enabled" className="text-sm">{copy("graphTrainingExecution")}</Label>
              <Switch
                id="training-execution-enabled"
                checked={data.policy.execution_enabled}
                onCheckedChange={(checked) => policy.mutate({ execution_enabled: checked === true })}
                disabled={policy.isPending}
                aria-label={copy("graphTrainingExecution")}
                title={copy("graphTrainingAdminOnly")}
              />
            </div>
          </>
        ) : (
          <Skeleton className="h-16 rounded-2xl" />
        )}
        {conflict ? <p role="alert" className="text-xs text-destructive">{copy("graphTrainingConflict")}</p> : null}
      </CardContent>
    </Card>
  );
}

// Memory retention (plan Task 17 / spec §13): owner-only card. Values can be
// shortened at any time; a 422 means the edit exceeds the platform caps and a
// 409 means the policy version moved on — both render inline and refetch.
export function RetentionCard({ wsId, isAdmin }: { wsId: string; isAdmin: boolean }) {
  const copy = useEvolutionCopy();
  const queryClient = useQueryClient();
  const key = ["evolution", wsId, "memory-retention"] as const;
  const { data } = useQuery({
    queryKey: key,
    queryFn: () => api.getMemoryRetention(wsId),
    enabled: isAdmin && Boolean(wsId),
    retry: false,
  });
  const [draft, setDraft] = useState<{ trajectory: string; archive: string; trace: string } | null>(null);
  const [conflict, setConflict] = useState(false);
  const [capError, setCapError] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: key });

  const save = useMutation({
    mutationFn: () => api.updateMemoryRetention(wsId, {
      trajectory_hot_days: retentionDays(draft?.trajectory, data?.policy.trajectory_hot_days),
      archive_days: retentionDays(draft?.archive, data?.policy.archive_days),
      trace_hot_days: retentionDays(draft?.trace, data?.policy.trace_hot_days),
      expected_version: data?.policy.version ?? 0,
    }),
    onSuccess: async () => {
      setConflict(false);
      setCapError(false);
      setDraft(null);
      toast.success(copy("graphRetentionSaved"));
      await refresh();
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true);
        setCapError(false);
        setDraft(null);
        void refresh();
        return;
      }
      if (error instanceof ApiError && error.status === 422) {
        setCapError(true);
        setConflict(false);
        return;
      }
      showErrorToast(error instanceof Error ? error.message : copy("graphRetention"));
    },
  });

  if (!isAdmin) return null;

  if (!data) {
    return (
      <Card className="bg-background/85 backdrop-blur">
        <CardHeader><CardTitle>{copy("graphRetention")}</CardTitle></CardHeader>
        <CardContent><Skeleton className="h-24 rounded-2xl" /></CardContent>
      </Card>
    );
  }

  const trajectoryHot = draft?.trajectory ?? String(data.policy.trajectory_hot_days);
  const archiveDays = draft?.archive ?? String(data.policy.archive_days);
  const traceHot = draft?.trace ?? String(data.policy.trace_hot_days);

  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle>{copy("graphRetention")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("graphRetentionHint")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
          <span>{copy("graphRetentionVersion")}: {data.policy.version}</span>
          <span>{copy("graphRetentionCaps")}: {data.caps.trajectory_hot_days} / {data.caps.archive_days} / {data.caps.trace_hot_days}</span>
        </div>
        <div className="grid gap-2 sm:grid-cols-3">
          <div className="space-y-1">
            <Label htmlFor="graph-retention-trajectory">{copy("graphRetentionTrajectoryHot")}</Label>
            <Input
              id="graph-retention-trajectory"
              type="number"
              min={1}
              value={trajectoryHot}
              onChange={(event) => setDraft({ trajectory: event.target.value, archive: archiveDays, trace: traceHot })}
              disabled={save.isPending}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="graph-retention-archive">{copy("graphRetentionArchive")}</Label>
            <Input
              id="graph-retention-archive"
              type="number"
              min={1}
              value={archiveDays}
              onChange={(event) => setDraft({ trajectory: trajectoryHot, archive: event.target.value, trace: traceHot })}
              disabled={save.isPending}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="graph-retention-trace">{copy("graphRetentionTraceHot")}</Label>
            <Input
              id="graph-retention-trace"
              type="number"
              min={1}
              value={traceHot}
              onChange={(event) => setDraft({ trajectory: trajectoryHot, archive: archiveDays, trace: event.target.value })}
              disabled={save.isPending}
            />
          </div>
        </div>
        <Button variant="outline" disabled={save.isPending} onClick={() => save.mutate()}>
          {copy("graphRetentionSave")}
        </Button>
        {conflict ? <p role="alert" className="text-xs text-destructive">{copy("graphRetentionConflict")}</p> : null}
        {capError ? <p role="alert" className="text-xs text-destructive">{copy("graphRetentionCapError")}</p> : null}
      </CardContent>
    </Card>
  );
}

function retentionDays(draft: string | undefined, fallback: number | undefined): number {
  const parsed = Number.parseInt(draft ?? "", 10);
  if (!Number.isFinite(parsed) || parsed < 0) return fallback ?? 0;
  return Math.trunc(parsed);
}

// Workspace pipeline health (spec §15): status-side backlog/backoff plus the
// audit ledger's anonymous failure counters — the migration/DLQ health view
// available without new endpoints (Task 21 adds the dedicated metrics).
export function MemoryHealthCard({ wsId }: { wsId: string }) {
  const copy = useEvolutionCopy();
  const { data: status } = useQuery(graphMemoryStatusOptions(wsId));
  const { data: audit } = useQuery(graphMemoryAuditOptions(wsId));

  const stagingBacklog = (status?.graphs ?? []).reduce((total, graph) => total + graph.staging_segments, 0);
  const backoff = (status?.graphs ?? []).some((graph) => graph.consolidation_backoff);
  const recallErrors = Object.entries(audit?.ledger?.recalls_by_error_kind ?? {})
    .filter(([, count]) => count > 0);
  const outboxFailed = audit?.ledger?.reward_outbox_by_status.failed ?? 0;
  const diveFailed = audit?.ledger?.dive_jobs_by_status.failed ?? 0;
  const healthy = !backoff && stagingBacklog === 0 && recallErrors.length === 0 && outboxFailed === 0 && diveFailed === 0;

  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-brand" />{copy("graphHealth")}
        </CardTitle>
        <p className="text-sm text-muted-foreground">{copy("graphHealthHint")}</p>
      </CardHeader>
      <CardContent className="space-y-2">
        {backoff ? <Badge variant="outline">{copy("graphHealthBackoff")}</Badge> : null}
        <div className="text-xs text-muted-foreground">
          <span>{copy("graphHealthStaging")}: {stagingBacklog}</span>
        </div>
        {recallErrors.length > 0 ? (
          <div className="space-y-1 text-xs text-muted-foreground">
            <p className="font-medium text-foreground">{copy("graphHealthRecallErrors")}</p>
            {recallErrors.map(([kind, count]) => (
              <p key={kind} className="font-mono">{kind}: {count}</p>
            ))}
          </div>
        ) : null}
        <div className="text-xs text-muted-foreground">
          <span>{copy("graphHealthOutboxFailed")}: {outboxFailed}</span>
        </div>
        <div className="text-xs text-muted-foreground">
          <span>{copy("graphHealthDiveFailed")}: {diveFailed}</span>
        </div>
        {healthy ? <p className="text-xs text-muted-foreground">{copy("graphHealthClean")}</p> : null}
      </CardContent>
    </Card>
  );
}
