"use client";

import { useState } from "react";
import { GitBranch, Play, RefreshCw, ShieldCheck, Timer } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api, ApiError } from "@multica/core/api";
import { EMPTY_GRAPH_MEMORY_PROFILE } from "@multica/core/api/schemas";
import {
  evolutionKeys,
  graphMemoryAuditOptions,
  graphMemoryConsolidationsOptions,
  graphMemoryProfileOptions,
  graphMemoryStatusOptions,
} from "@multica/core/evolution/queries";
import type { GraphMemoryProfile, UpdateGraphMemoryProfileRequest } from "@multica/core/types";
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
  patch: { ttt_enabled: boolean; explore_agents: number },
): UpdateGraphMemoryProfileRequest {
  return {
    memory_type: profile.memory_type,
    explore_agents: patch.explore_agents,
    explore_max_rounds: profile.explore_max_rounds,
    config_version: profile.config_version,
    ttt_enabled: patch.ttt_enabled,
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
            <div className="mt-2 grid grid-cols-2 gap-2 text-xs text-muted-foreground sm:grid-cols-4">
              <span>{copy("graphVersion")}: {graph.current_version}</span>
              <span>{copy("graphStaging")}: {graph.staging_segments}</span>
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
  const [tttEnabled, setTttEnabled] = useState(profile.ttt_enabled);
  const [exploreAgents, setExploreAgents] = useState(profile.explore_agents);

  const save = useMutation({
    mutationFn: (patch: { ttt_enabled: boolean; explore_agents: number }) =>
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
          <Label htmlFor="graph-ttt-enabled" className="text-sm">{copy("graphTtt")}</Label>
          <Switch
            id="graph-ttt-enabled"
            checked={tttEnabled}
            onCheckedChange={(checked) => setTttEnabled(checked === true)}
            disabled={!isAdmin || save.isPending}
            aria-label={copy("graphTtt")}
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
            disabled={!isAdmin || !tttEnabled || save.isPending}
            aria-label={copy("graphTttConcurrency")}
            title={isAdmin ? undefined : copy("graphTttAdminOnly")}
          />
          {!tttEnabled && (
            <p className="text-xs text-muted-foreground">{copy("graphTttEffectiveK")}</p>
          )}
        </div>
        <Button
          variant="outline"
          disabled={!isAdmin || save.isPending}
          onClick={() => save.mutate({
            ttt_enabled: tttEnabled,
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
