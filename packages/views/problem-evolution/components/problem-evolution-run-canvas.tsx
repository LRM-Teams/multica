"use client";

import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Download, Loader2, Play, Sparkles, Snowflake, Square } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  problemEvolutionKeys,
  problemEvolutionSnapshotOptions,
} from "@multica/core/problem-evolution";
import type {
  ProblemEvolutionCandidate,
  ProblemEvolutionCandidateEdge,
} from "@multica/core/problem-evolution";
import { useT } from "../../i18n/use-t";
import { ProblemEvolutionStatusBadge } from "./problem-evolution-solve-page";

/**
 * Run detail: contract state, run controls and the candidate lanes.
 *
 * This is deliberately its own thin canvas rather than a reuse of Research's
 * graph: the two domains share no business model, and extracting Research's
 * primitives for a second consumer would be a much larger change than the
 * value it buys here.
 */
export function ProblemEvolutionRunCanvas({
  runId,
  onBack,
}: {
  runId: string;
  onBack?: () => void;
}) {
  const { t } = useT("problem-evolution");
  const workspaceId = useWorkspaceId();
  const queryClient = useQueryClient();
  const snapshotQuery = useQuery(
    problemEvolutionSnapshotOptions(workspaceId ?? "", runId),
  );

  const invalidate = () => {
    if (!workspaceId) return;
    void queryClient.invalidateQueries({
      queryKey: problemEvolutionKeys.snapshot(workspaceId, runId),
    });
    void queryClient.invalidateQueries({
      queryKey: problemEvolutionKeys.runs(workspaceId),
    });
  };

  const freezeEvaluator = useMutation({
    mutationFn: () => api.freezeProblemEvolutionEvaluator(runId),
    onSuccess: invalidate,
    onError: () => showErrorToast(t(($) => $.errors.freezeFailed)),
  });
  const proposeEvaluator = useMutation({
    mutationFn: () => api.proposeProblemEvolutionEvaluator(runId),
    onSuccess: invalidate,
    onError: () => showErrorToast(t(($) => $.errors.proposeFailed)),
  });
  const startRun = useMutation({
    mutationFn: () => api.startProblemEvolutionRun(runId),
    onSuccess: invalidate,
    onError: () => showErrorToast(t(($) => $.errors.startFailed)),
  });
  const stopRun = useMutation({
    mutationFn: () => api.stopProblemEvolutionRun(runId),
    onSuccess: invalidate,
    onError: () => showErrorToast(t(($) => $.errors.stopFailed)),
  });
  const exportRun = useMutation({
    mutationFn: () => api.exportProblemEvolutionRun(runId),
    onSuccess: (bundle) => downloadProblemEvolutionExport(runId, bundle),
    onError: () => showErrorToast(t(($) => $.errors.exportFailed)),
  });

  const snapshot = snapshotQuery.data;
  const lanes = useMemo(() => groupCandidatesByLane(snapshot?.candidates ?? []), [
    snapshot?.candidates,
  ]);
  const parentsByChild = useMemo(
    () => indexParentsByChild(snapshot?.candidates ?? [], snapshot?.edges ?? []),
    [snapshot?.candidates, snapshot?.edges],
  );

  if (snapshotQuery.isLoading || !snapshot) {
    return (
      <div className="text-muted-foreground flex h-full items-center justify-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" aria-hidden />
      </div>
    );
  }

  const run = snapshot.run;
  const contractFrozen = snapshot.evaluator?.status === "frozen";
  const startable =
    contractFrozen &&
    (run.status === "draft" || run.status === "validating_evaluator" || run.status === "ready");
  const stoppable =
    run.status === "queued" || run.status === "running" || run.status === "synthesizing";

  return (
    <div className="flex h-full flex-col">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b p-4">
        <div className="flex min-w-0 items-center gap-3">
          {onBack ? (
            <Button variant="ghost" size="sm" onClick={onBack}>
              <ArrowLeft className="size-4" aria-hidden />
              {t(($) => $.detail.back)}
            </Button>
          ) : null}
          <div className="min-w-0">
            <h1 className="truncate text-sm font-semibold">{run.title || run.id}</h1>
            <p className="text-muted-foreground text-xs">
              {t(($) => $.detail.graphVersion)}: {snapshot.graph_version}
            </p>
          </div>
          <ProblemEvolutionStatusBadge run={run} />
        </div>
        <div className="flex items-center gap-2">
          {!run.evaluator_contract_id ? (
            <Button
              size="sm"
              variant="secondary"
              disabled={proposeEvaluator.isPending}
              onClick={() => proposeEvaluator.mutate()}
            >
              <Sparkles className="size-4" aria-hidden />
              {t(($) => $.detail.proposeEvaluator)}
            </Button>
          ) : !contractFrozen ? (
            <Button
              size="sm"
              variant="secondary"
              disabled={freezeEvaluator.isPending}
              onClick={() => freezeEvaluator.mutate()}
            >
              <Snowflake className="size-4" aria-hidden />
              {t(($) => $.detail.freezeEvaluator)}
            </Button>
          ) : null}
          <Button
            size="sm"
            disabled={!startable || startRun.isPending}
            onClick={() => startRun.mutate()}
          >
            <Play className="size-4" aria-hidden />
            {t(($) => $.detail.start)}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={!stoppable || stopRun.isPending}
            onClick={() => stopRun.mutate()}
          >
            <Square className="size-4" aria-hidden />
            {t(($) => $.detail.stop)}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={exportRun.isPending}
            onClick={() => exportRun.mutate()}
          >
            <Download className="size-4" aria-hidden />
            {t(($) => $.detail.export)}
          </Button>
        </div>
      </header>

      <section className="text-muted-foreground flex flex-wrap gap-4 border-b px-4 py-2 text-xs">
        <span>
          {t(($) => $.detail.evaluatorHash)}: {run.evaluator_content_hash || "—"}
        </span>
        <span>
          {t(($) => $.detail.evolverVersion)}: {run.evolver_version || "—"}
        </span>
        <span>
          {t(($) => $.detail.searchBest)}: {run.best_score.toFixed(3)}
        </span>
        <span>
          {t(($) => $.detail.blindScore)}:{" "}
          {run.blind_score == null ? "—" : run.blind_score.toFixed(3)}
        </span>
        <span>
          {t(($) => $.detail.modelCalls)}: {run.model_call_count}
        </span>
        {run.overfit_gap != null && run.overfit_gap > 0.15 ? (
          <span className="text-amber-600">
            {t(($) => $.detail.overfitWarning)}: {run.overfit_gap.toFixed(3)}
          </span>
        ) : null}
        {run.evolver_version === "" && run.status === "completed" ? (
          <span className="text-amber-600">{t(($) => $.detail.notReplayable)}</span>
        ) : null}
      </section>

      <div className="flex-1 overflow-auto p-4">
        {snapshot.candidates.length === 0 ? (
          <p className="text-muted-foreground text-sm">{t(($) => $.detail.noCandidates)}</p>
        ) : (
          <div className="flex gap-4">
            {lanes.map(([lane, candidates]) => (
              <div key={lane} className="w-72 shrink-0 space-y-2">
                <h2 className="text-muted-foreground text-xs font-medium uppercase">
                  {lane}
                </h2>
                {candidates.map((candidate) => (
                  <CandidateCard
                    key={candidate.id}
                    candidate={candidate}
                    lineage={parentsByChild.get(candidate.id)}
                  />
                ))}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function CandidateCard({
  candidate,
  lineage,
}: {
  candidate: ProblemEvolutionCandidate;
  lineage?: CandidateLineage;
}) {
  const { t } = useT("problem-evolution");
  const score = candidate.score ?? null;
  return (
    <article className="space-y-2 rounded-lg border p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-xs font-medium">{candidate.external_ref}</span>
        <Badge variant={candidate.status === "failed" ? "destructive" : "secondary"}>
          {candidate.status}
        </Badge>
      </div>
      <p className="text-muted-foreground text-xs">
        {t(($) => $.detail.candidateOperator)}: {candidate.operator}
      </p>
      <p className="text-xs">
        {t(($) => $.detail.candidateScore)}:{" "}
        {score ? score.total.toFixed(3) : t(($) => $.detail.candidateUnscored)}
      </p>
      {score && !score.hard_gate_passed ? (
        <p className="text-xs text-amber-600">{t(($) => $.detail.hardGateFailed)}</p>
      ) : null}
      {candidate.feedback_rounds > 0 ? (
        <p className="text-muted-foreground text-xs">
          {t(($) => $.detail.repairRounds)}: {candidate.feedback_rounds}
        </p>
      ) : null}
      {candidate.failure_class ? (
        <p className="text-xs text-red-600">
          {t(($) => $.detail.failureClass)}: {candidate.failure_class}
        </p>
      ) : null}
      {lineage ? (
        <p className="text-muted-foreground text-xs">
          {t(($) => $.detail.candidateLineage)}: {lineage.relation} ←{" "}
          {lineage.parentRefs.join(", ")}
        </p>
      ) : null}
      {candidate.summary ? (
        <p className="text-muted-foreground line-clamp-3 text-xs">{candidate.summary}</p>
      ) : null}
    </article>
  );
}

export type CandidateLineage = { relation: string; parentRefs: string[] };

/**
 * Resolve lineage edges into the parent refs shown on each candidate card.
 * Edges reference candidate ids, but a card shows the evolver's own refs, which
 * is what a user matches against the run's logs.
 */
export function indexParentsByChild(
  candidates: ProblemEvolutionCandidate[],
  edges: ProblemEvolutionCandidateEdge[],
): Map<string, CandidateLineage> {
  const refById = new Map(candidates.map((candidate) => [candidate.id, candidate.external_ref]));
  const lineage = new Map<string, CandidateLineage>();
  const ordered = [...edges].sort((left, right) => left.parent_index - right.parent_index);
  for (const edge of ordered) {
    const parentRef = refById.get(edge.parent_id);
    if (!parentRef) continue;
    const existing = lineage.get(edge.child_id);
    if (existing) {
      existing.parentRefs.push(parentRef);
    } else {
      lineage.set(edge.child_id, { relation: edge.relation, parentRefs: [parentRef] });
    }
  }
  return lineage;
}

/**
 * Group candidates into lanes for display. Lane order is stable so the canvas
 * does not reshuffle columns when a new candidate arrives.
 */
export function groupCandidatesByLane(
  candidates: ProblemEvolutionCandidate[],
): Array<[string, ProblemEvolutionCandidate[]]> {
  const lanes = new Map<string, ProblemEvolutionCandidate[]>();
  for (const candidate of candidates) {
    const lane = candidate.lane || "baseline";
    const bucket = lanes.get(lane);
    if (bucket) {
      bucket.push(candidate);
    } else {
      lanes.set(lane, [candidate]);
    }
  }
  return [...lanes.entries()].sort(([left], [right]) => left.localeCompare(right));
}

/**
 * downloadProblemEvolutionExport saves the delivery bundle as a file.
 *
 * The bundle is fetched through the API client so it passes schema validation
 * before it reaches disk; a link straight to the endpoint would hand the user a
 * file the frontend never checked.
 */
export function downloadProblemEvolutionExport(runId: string, bundle: unknown) {
  const blob = new Blob([JSON.stringify(bundle, null, 2)], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `problem-evolution-${runId}.json`;
  link.click();
  URL.revokeObjectURL(url);
}
