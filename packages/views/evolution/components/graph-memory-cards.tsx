"use client";

import { GitBranch, Play, RefreshCw, ShieldCheck } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import {
  evolutionKeys,
  graphMemoryAuditOptions,
  graphMemoryConsolidationsOptions,
  graphMemoryStatusOptions,
} from "@multica/core/evolution/queries";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useEvolutionCopy } from "./evolution-center-page";

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
